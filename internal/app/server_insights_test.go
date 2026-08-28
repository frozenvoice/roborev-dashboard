package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/insights"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type fakeInsightsService struct {
	*fakeService
	launches         int
	launchContextErr error
}

func (f *fakeInsightsService) InsightsPreflight(context.Context, insights.Request) (insights.Preflight, error) {
	return insights.Preflight{Command: insights.Command{Executable: "roborev", Args: []string{"insights", "--repo", `E:\repo`, "--since", "30d", "--json"}, Dir: `E:\repo`}, Binding: "1:roborev", CLIVersion: "v0.64.0", DaemonVersion: "v0.64.0"}, nil
}
func (f *fakeInsightsService) Insights(ctx context.Context, _ insights.Request, _ insights.Preflight) (insights.LaunchResult, error) {
	f.launches++
	f.launchContextErr = ctx.Err()
	return insights.LaunchResult{Receipt: &insights.Receipt{JobID: 42, Agent: "codex", Since: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}}, nil
}

func TestInsightsResultErrorsDoNotExposeDaemonDetails(t *testing.T) {
	service := &fakeInsightsService{fakeService: &fakeService{
		repoPath: `E:\repo`,
		showErr:  errors.New(`daemon private path C:\secret\prompt-marker`),
		panelMetadata: map[int64]roborev.JobMetadata{
			42: {ID: 42, RepoPath: `E:\repo`, JobType: "insights", Status: "done"},
		},
	}}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/insights/42/result?repositoryIdentity="+identity, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway || strings.Contains(recorder.Body.String(), "prompt-marker") || !strings.Contains(recorder.Body.String(), "Insights result could not be loaded") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestInsightsPreviewConfirmAndGenericRerunFailClosed(t *testing.T) {
	service := &fakeInsightsService{fakeService: &fakeService{repoPath: `E:\repo`, panelMetadata: map[int64]roborev.JobMetadata{42: {ID: 42, RepoPath: `E:\repo`, JobType: "insights", Status: "done"}}}}
	// Rebuild with the optional adapter; setupServer accepts the base fake for
	// most tests and this explicit construction keeps the test's scope local.
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	previewBodyBytes, _ := json.Marshal(map[string]any{"repositoryIdentity": identity, "window": "30d", "branchScope": "all"})
	previewBody := string(previewBodyBytes)
	request := httptest.NewRequest(http.MethodPost, "/api/insights/preview", strings.NewReader(previewBody))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var preview insights.Preview
	if err := json.Unmarshal(recorder.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	confirm := httptest.NewRequest(http.MethodPost, "/api/insights", strings.NewReader(`{"token":"`+preview.Token+`"}`))
	confirm.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, confirm)
	if recorder.Code != http.StatusAccepted || service.launches != 1 {
		t.Fatalf("confirm status=%d launches=%d body=%s", recorder.Code, service.launches, recorder.Body.String())
	}
	confirm = httptest.NewRequest(http.MethodPost, "/api/insights", strings.NewReader(`{"token":"`+preview.Token+`"}`))
	confirm.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, confirm)
	if recorder.Code == http.StatusAccepted || service.launches != 1 {
		t.Fatalf("double confirm status=%d launches=%d", recorder.Code, service.launches)
	}

	rerun := httptest.NewRequest(http.MethodPost, "/api/reviews/42/rerun", nil)
	rerun.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, rerun)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("generic Insights rerun status=%d", recorder.Code)
	}
}

func TestInsightsAcceptedLaunchDoesNotInheritBrowserCancellation(t *testing.T) {
	service := &fakeInsightsService{fakeService: &fakeService{repoPath: `E:\repo`}}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	previewBody, _ := json.Marshal(map[string]any{"repositoryIdentity": identity, "window": "30d", "branchScope": "all"})
	preview := httptest.NewRequest(http.MethodPost, "/api/insights/preview", strings.NewReader(string(previewBody)))
	preview.Header.Set("X-CSRF-Token", server.csrfToken)
	previewRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(previewRecorder, preview)
	if previewRecorder.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewRecorder.Code, previewRecorder.Body.String())
	}
	var decoded insights.Preview
	if err := json.Unmarshal(previewRecorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	requestCtx, cancel := context.WithCancel(context.Background())
	confirm := httptest.NewRequest(http.MethodPost, "/api/insights", strings.NewReader(`{"token":"`+decoded.Token+`"}`)).WithContext(requestCtx)
	confirm.Header.Set("X-CSRF-Token", server.csrfToken)
	cancel()
	confirmRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(confirmRecorder, confirm)
	if confirmRecorder.Code != http.StatusAccepted || service.launches != 1 || service.launchContextErr != nil {
		t.Fatalf("confirm status=%d launches=%d contextErr=%v body=%s", confirmRecorder.Code, service.launches, service.launchContextErr, confirmRecorder.Body.String())
	}
}

func TestInsightsGlobalGateBlocksDaemonEnqueue(t *testing.T) {
	service := &fakeInsightsService{fakeService: &fakeService{repoPath: `E:\repo`}}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	previewBody, _ := json.Marshal(map[string]any{"repositoryIdentity": identity, "window": "30d", "branchScope": "all"})
	preview := httptest.NewRequest(http.MethodPost, "/api/insights/preview", strings.NewReader(string(previewBody)))
	preview.Header.Set("X-CSRF-Token", server.csrfToken)
	previewRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(previewRecorder, preview)
	if previewRecorder.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewRecorder.Code, previewRecorder.Body.String())
	}
	var decoded insights.Preview
	if err := json.Unmarshal(previewRecorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	release, err := server.Gate.AcquireFix()
	if err != nil {
		t.Fatal(err)
	}
	confirm := httptest.NewRequest(http.MethodPost, "/api/insights", strings.NewReader(`{"token":"`+decoded.Token+`"}`))
	confirm.Header.Set("X-CSRF-Token", server.csrfToken)
	confirmRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(confirmRecorder, confirm)
	release()
	if confirmRecorder.Code != http.StatusConflict || service.launches != 0 {
		t.Fatalf("busy confirm status=%d launches=%d body=%s", confirmRecorder.Code, service.launches, confirmRecorder.Body.String())
	}
}

func TestInsightsMutationsRejectOriginCSRFAndMalformedBodies(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		body       string
		origin     string
		csrf       string
		wantStatus int
	}{
		{name: "preview foreign origin", path: "/api/insights/preview", body: `{}`,
			origin: "https://evil.example", csrf: "valid", wantStatus: http.StatusForbidden},
		{name: "preview missing csrf", path: "/api/insights/preview", body: `{}`,
			origin: "", csrf: "", wantStatus: http.StatusForbidden},
		{name: "preview bad csrf", path: "/api/insights/preview", body: `{}`,
			origin: "", csrf: "invalid", wantStatus: http.StatusForbidden},
		{name: "preview malformed body", path: "/api/insights/preview", body: `{`,
			origin: "", csrf: "valid", wantStatus: http.StatusBadRequest},
		{name: "confirm malformed body", path: "/api/insights", body: `{"token":`,
			origin: "", csrf: "valid", wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeInsightsService{fakeService: &fakeService{repoPath: `E:\repo`}}
			registry := NewRegistry(fakeInspector{}, "")
			if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
				t.Fatal(err)
			}
			server, err := NewServer(service, registry, "")
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			switch test.csrf {
			case "valid":
				request.Header.Set("X-CSRF-Token", server.csrfToken)
			case "invalid":
				request.Header.Set("X-CSRF-Token", "bad-token")
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if service.launches != 0 {
				t.Fatalf("rejected mutation launched %d Insights jobs", service.launches)
			}
			if test.path == "/api/insights/preview" && recorder.Code == http.StatusOK {
				t.Fatal("rejected preview unexpectedly returned a success")
			}
		})
	}
}

func TestInsightsConfirmMalformedBodyDoesNotConsumeValidPreviewOrLaunch(t *testing.T) {
	service := &fakeInsightsService{fakeService: &fakeService{repoPath: `E:\repo`}}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	previewBodyBytes, _ := json.Marshal(map[string]any{"repositoryIdentity": identity, "window": "30d", "branchScope": "all"})
	preview := httptest.NewRequest(http.MethodPost, "/api/insights/preview", strings.NewReader(string(previewBodyBytes)))
	preview.Header.Set("X-CSRF-Token", server.csrfToken)
	previewRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(previewRecorder, preview)
	if previewRecorder.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewRecorder.Code, previewRecorder.Body.String())
	}
	var decoded insights.Preview
	if err := json.Unmarshal(previewRecorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	malformed := httptest.NewRequest(http.MethodPost, "/api/insights", strings.NewReader(`{"token":"`+decoded.Token+`"} {}`))
	malformed.Header.Set("X-CSRF-Token", server.csrfToken)
	malformedRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(malformedRecorder, malformed)
	if malformedRecorder.Code != http.StatusBadRequest || service.launches != 0 {
		t.Fatalf("malformed confirm status=%d launches=%d body=%s", malformedRecorder.Code, service.launches, malformedRecorder.Body.String())
	}
	valid := httptest.NewRequest(http.MethodPost, "/api/insights", strings.NewReader(`{"token":"`+decoded.Token+`"}`))
	valid.Header.Set("X-CSRF-Token", server.csrfToken)
	validRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(validRecorder, valid)
	if validRecorder.Code != http.StatusAccepted || service.launches != 1 {
		t.Fatalf("valid confirm after malformed status=%d launches=%d body=%s", validRecorder.Code, service.launches, validRecorder.Body.String())
	}
}

func TestInsightsStatusAndResultAuthorizeRepositoryAndJobType(t *testing.T) {
	service := &fakeInsightsService{fakeService: &fakeService{
		repoPath: `E:\repo`,
		showRaw:  json.RawMessage(`{"id":1,"job_id":42,"output":"# recommendations","agent":"test"}`),
		panelMetadata: map[int64]roborev.JobMetadata{
			42: {ID: 42, RepoPath: `E:\repo`, JobType: "insights", Status: "done"},
			43: {ID: 43, RepoPath: `E:\repo`, JobType: "review", Status: "done"},
			44: {ID: 44, RepoPath: `E:\other`, JobType: "insights", Status: "done"},
			45: {ID: 45, RepoPath: `E:\repo`, JobType: "insights", Status: "queued"},
			46: {ID: 46, RepoPath: `E:\repo`, JobType: "insights", Status: "running"},
			47: {ID: 47, RepoPath: `E:\repo`, JobType: "insights", Status: "failed"},
			48: {ID: 48, RepoPath: `E:\repo`, JobType: "insights", Status: "canceled"},
		},
	}}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		request.Header.Set("Sec-Fetch-Mode", "cors")
		request.Header.Set("Sec-Fetch-Dest", "empty")
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		return recorder
	}
	if recorder := read("/api/insights/42?repositoryIdentity=" + identity); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"done"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := read("/api/insights/42/result?repositoryIdentity=" + identity); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "# recommendations") {
		t.Fatalf("result=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := read("/api/insights/43?repositoryIdentity=" + identity); recorder.Code != http.StatusNotFound {
		t.Fatalf("non-Insights status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	foreign := read("/api/insights/44?repositoryIdentity=" + identity)
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("foreign status=%d body=%s", foreign.Code, foreign.Body.String())
	}
	for id, want := range map[int]string{45: "queued", 46: "running", 47: "failed", 48: "canceled"} {
		recorder := read("/api/insights/" + strconv.Itoa(id) + "?repositoryIdentity=" + identity)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"`+want+`"`) {
			t.Fatalf("status id=%d code=%d want=%s body=%s", id, recorder.Code, want, recorder.Body.String())
		}
	}
	for _, query := range []string{
		"",
		"repositoryIdentity=",
		"repositoryIdentity=" + identity + "&extra=1",
		"repositoryIdentity=" + identity + "&repositoryIdentity=" + identity,
	} {
		recorder := read("/api/insights/42?" + query)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("query %q status=%d body=%s", query, recorder.Code, recorder.Body.String())
		}
	}
}

func TestDecodeInsightsMarkdownUsesTopLevelJobID(t *testing.T) {
	for _, test := range []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{
			name: "upstream show shape",
			raw:  `{"id":1,"job_id":42,"output":"# recommendations","agent":"test"}`,
			want: "# recommendations",
		},
		{
			name:    "wrong job id",
			raw:     `{"id":42,"job_id":43,"output":"# recommendations","agent":"test"}`,
			wantErr: true,
		},
		{
			name:    "missing job id",
			raw:     `{"id":42,"output":"# recommendations","agent":"test"}`,
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := decodeInsightsMarkdown([]byte(test.raw), 42)
			if test.wantErr {
				if err == nil {
					t.Fatalf("accepted invalid result: %s", test.raw)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("markdown=%q err=%v want=%q", got, err, test.want)
			}
		})
	}
}
