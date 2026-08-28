package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/refine"
	"github.com/froze/roborev-dashboard/internal/runtimecontrol"
	"github.com/froze/roborev-dashboard/internal/setup"
)

func strictBrowserReadRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	return request
}

func TestHandlerSetsClickjackingAndStrictResourcePolicyHeaders(t *testing.T) {
	server := setupServer(t, &fakeService{})
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("root status = %d", recorder.Code)
	}
	if recorder.Header().Get("X-Content-Type-Options") != "nosniff" || recorder.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("existing security headers changed: %#v", recorder.Header())
	}
	if recorder.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("X-Frame-Options = %q", recorder.Header().Get("X-Frame-Options"))
	}
	csp := recorder.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"frame-ancestors 'none'", "object-src 'none'", "base-uri 'none'", "form-action 'self'"} {
		if !strings.Contains(csp, directive) {
			t.Fatalf("CSP missing %q: %s", directive, csp)
		}
	}
}

func TestUnknownAPIRoutesDoNotFallThroughToSPA(t *testing.T) {
	server := setupServer(t, &fakeService{})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, path := range []string{"/api", "/api/does-not-exist", "/api/%64oes-not-exist"} {
			t.Run(method+path, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(method, path, nil)
				server.Handler().ServeHTTP(recorder, request)
				if recorder.Code != http.StatusNotFound {
					t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
				}
				if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
					t.Fatalf("content type = %q", recorder.Header().Get("Content-Type"))
				}
				if strings.Contains(recorder.Body.String(), path) || strings.Contains(recorder.Body.String(), "<!doctype html") {
					t.Fatalf("API 404 leaked path or SPA body: %s", recorder.Body.String())
				}
			})
		}
	}
	root := httptest.NewRecorder()
	server.Handler().ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/", nil))
	if root.Code != http.StatusOK || !strings.HasPrefix(root.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("root no longer serves SPA: %d %q", root.Code, root.Header().Get("Content-Type"))
	}
}

func TestStrictAgentReadRejectsBeforeDownstreamWork(t *testing.T) {
	server, checker := agentTestServer(t)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if recorder.Code != http.StatusForbidden || checker.calls != 0 {
		t.Fatalf("unauthorized agents read = %d calls=%d body=%s", recorder.Code, checker.calls, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	request := strictBrowserReadRequest(http.MethodGet, "/api/agents?unexpected=1")
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || checker.calls != 0 {
		t.Fatalf("queryful agents read = %d calls=%d body=%s", recorder.Code, checker.calls, recorder.Body.String())
	}
}

func TestPanelMembersStrictReadAuthRunsBeforeQueryOrService(t *testing.T) {
	run := "986d3bfa-05da-4621-aeca-66f493a19ea3"
	service := &fakeService{panelRaw: panelJobsJSON(
		panelTestJob(42, `E:\repo`, "dashboard_cross", run, "synthesis"),
		panelTestJob(43, `E:\repo`, "dashboard_cross", run, "member"),
	), showRaw: json.RawMessage(`{"job_id":43,"job":{"id":43,"repo_path":"E:\\repo","panel_run_uuid":"986d3bfa-05da-4621-aeca-66f493a19ea3","panel_role":"member"},"review":{"output":"safe"}}`)}
	server := setupServer(t, service)
	identity, err := server.Registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	query := "?parentJobId=42&repositoryIdentity=" + url.QueryEscape(identity)
	cases := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{name: "missing headers", mutate: func(_ *http.Request) {}},
		{name: "foreign origin", mutate: func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }},
		{name: "malformed origin", mutate: func(r *http.Request) { r.Header.Set("Origin", "http://example.com/path") }},
		{name: "duplicate origin", mutate: func(r *http.Request) {
			r.Header.Set("Origin", "http://example.com")
			r.Header.Add("Origin", "http://example.com")
		}},
		{name: "missing fetch site", mutate: func(r *http.Request) { r.Header.Del("Sec-Fetch-Site") }},
		{name: "malformed fetch site", mutate: func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
		{name: "duplicate fetch site", mutate: func(r *http.Request) { r.Header.Add("Sec-Fetch-Site", "same-origin") }},
		{name: "duplicate fetch mode", mutate: func(r *http.Request) { r.Header.Add("Sec-Fetch-Mode", "cors") }},
		{name: "duplicate fetch dest", mutate: func(r *http.Request) { r.Header.Add("Sec-Fetch-Dest", "empty") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service.panelMembersCalls, service.showCalls = 0, 0
			listRequest := httptest.NewRequest(http.MethodGet, "/api/panels/"+run+"/members"+query, nil)
			if test.name != "missing headers" {
				listRequest.Header.Set("Sec-Fetch-Site", "same-origin")
				listRequest.Header.Set("Sec-Fetch-Mode", "cors")
				listRequest.Header.Set("Sec-Fetch-Dest", "empty")
			}
			test.mutate(listRequest)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, listRequest)
			if recorder.Code != http.StatusForbidden || service.panelMembersCalls != 0 || service.showCalls != 0 {
				t.Fatalf("list status=%d panel=%d show=%d body=%s", recorder.Code, service.panelMembersCalls, service.showCalls, recorder.Body.String())
			}

			detailRequest := httptest.NewRequest(http.MethodGet, "/api/panels/"+run+"/members/43"+query, nil)
			if test.name != "missing headers" {
				detailRequest.Header.Set("Sec-Fetch-Site", "same-origin")
				detailRequest.Header.Set("Sec-Fetch-Mode", "cors")
				detailRequest.Header.Set("Sec-Fetch-Dest", "empty")
			}
			test.mutate(detailRequest)
			recorder = httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, detailRequest)
			if recorder.Code != http.StatusForbidden || service.panelMembersCalls != 0 || service.showCalls != 0 {
				t.Fatalf("detail status=%d panel=%d show=%d body=%s", recorder.Code, service.panelMembersCalls, service.showCalls, recorder.Body.String())
			}
		})
	}
}

func TestRuntimeAndPanelRefineBrowserDTOsStripPrivateExecutionDetails(t *testing.T) {
	private := `C:\Users\private\secret-project`
	run := &refine.PanelRefineRun{
		ID: "run-1", RepositoryIdentity: "opaque-repository-alias", RepositoryPath: private,
		Branch: "main", IterationPanel: "dashboard_cross", FixAgent: "codex",
		Phase: refine.PhaseFailed, StartingHead: "abc", CurrentHead: "def",
		Error: "PRIVATE_PANEL_RAW_ERROR", Command: refine.CommandTemplate{Executable: `C:\fake\roborev.exe`, Args: []string{private, "PRIVATE_ARG"}},
		Iterations:  []refine.PanelRefineIteration{{Number: 1, Error: "PRIVATE_ITERATION_ERROR", Panel: "dashboard_cross"}},
		CancelState: &refine.CancelRepositoryState{Available: false, Error: "PRIVATE_CANCEL_ERROR"},
	}
	raw, err := json.Marshal(panelRefineRunDTOFrom(run))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, marker := range []string{private, "PRIVATE_PANEL_RAW_ERROR", "PRIVATE_ITERATION_ERROR", "PRIVATE_CANCEL_ERROR", "roborev.exe", "PRIVATE_ARG"} {
		if strings.Contains(body, marker) {
			t.Fatalf("panel DTO leaked %q: %s", marker, body)
		}
	}
	if !strings.Contains(body, `"executable":"roborev"`) || !strings.Contains(body, `"errorCode":"panel_refine_error"`) || !strings.Contains(body, `"errorCode":"repository_state_unavailable"`) {
		t.Fatalf("panel DTO lost safe projection: %s", body)
	}
	if run.RepositoryPath != private || run.Command.Executable == "roborev" || run.Error == "" {
		t.Fatal("internal manager state was unexpectedly changed")
	}

	server := setupServer(t, &fakeService{})
	state := server.runtimeControlDTO(runtimecontrol.State{Error: "C:\\private\\runtime-error", DisabledReason: "C:\\private\\disabled"})
	runtimeJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	runtimeBody := string(runtimeJSON)
	if strings.Contains(runtimeBody, "private") || !strings.Contains(runtimeBody, `"executable":"roborev"`) || !strings.Contains(runtimeBody, "Runtime control") {
		t.Fatalf("runtime DTO leaked or lost safe projection: %s", runtimeBody)
	}
}

func TestRuntimeControlBlocksWhileFixLeaseIsHeld(t *testing.T) {
	server := setupServer(t, &fakeService{})
	release, err := server.Gate.AcquireFix()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	state := server.runtimeControlDTO(runtimecontrol.State{DaemonKnown: true, DaemonRunning: true, QueueKnown: true})
	if state.CanStart || state.CanStop || state.CanRestart || state.CanPause || state.CanResume || state.DisabledReason != "A Fix operation is active." {
		t.Fatalf("runtime controls remained enabled during fix lease: %#v", state)
	}
}

func TestRuntimeControlErrorProjectionIsPathFree(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeRuntimeControlError(recorder, errors.New(`C:\private\runtime-failure`), runtimeControlDTO{})
	if recorder.Code != http.StatusBadGateway || strings.Contains(recorder.Body.String(), "private") || strings.Contains(recorder.Body.String(), "runtime-failure") {
		t.Fatalf("raw runtime error escaped: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestSetupStateProjectionStripsPrivateErrorDetails(t *testing.T) {
	state := projectSetupState(setup.State{Phase: setup.PhaseFailed, Status: setup.PhaseFailed, FailureCode: "install_failed", Error: `PRIVATE_SETUP_STDERR C:\Users\private\roborev.exe`})
	if strings.Contains(state.Error, "PRIVATE_SETUP_STDERR") || strings.Contains(state.Error, `C:\Users\private`) {
		t.Fatalf("setup error leaked private detail: %q", state.Error)
	}
	if state.Error != "RoboRev setup is unavailable" {
		t.Fatalf("unexpected setup safe error: %q", state.Error)
	}
}
