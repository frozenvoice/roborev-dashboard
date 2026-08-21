package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

type taskFakeService struct {
	*fakeService
	preflight      roborev.TaskPreflight
	receipt        roborev.TaskReceipt
	preflightCalls int
	taskCalls      int
}

func (f *taskFakeService) TaskPreflight(context.Context) (roborev.TaskPreflight, error) {
	f.preflightCalls++
	return f.preflight, nil
}

func (f *taskFakeService) RunTask(_ context.Context, request roborev.TaskRequest) (roborev.TaskReceipt, error) {
	f.taskCalls++
	if request.Agent != "codex" || request.Task == "" || request.ExpectedBindingIdentity != f.preflight.BindingIdentity {
		return roborev.TaskReceipt{}, nil
	}
	return f.receipt, nil
}

func setupTaskServer(t *testing.T) (*Server, *taskFakeService) {
	t.Helper()
	service := &taskFakeService{fakeService: &fakeService{}, preflight: roborev.TaskPreflight{Binary: `C:\roborev.exe`, Version: "v0.64.0", DaemonVersion: "v0.64.0", BindingIdentity: "1:binary", Safety: roborev.TaskSafety{Known: true}}, receipt: roborev.TaskReceipt{JobID: 7, JobUUID: "00000000-0000-0000-0000-000000000007", GitRef: "run", Status: "queued"}}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	return server, service
}

func TestTaskPreviewStrictBrowserAuthRunsNoPreflightBeforeAuth(t *testing.T) {
	server, service := setupTaskServer(t)
	query := url.Values{"repositoryIdentity": []string{`E:\repo`}, "task": []string{"inspect"}}
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/preview?"+query.Encode(), nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d", recorder.Code)
	}
	if service.taskCalls != 0 {
		t.Fatal("task adapter was called before browser authorization")
	}
	if service.preflightCalls != 0 {
		t.Fatal("preflight ran before browser authorization")
	}
}

func TestTaskPreviewStrictBrowserAuthMatrix(t *testing.T) {
	tests := []struct {
		name       string
		origin     []string
		fetchSite  []string
		fetchMode  string
		fetchDest  string
		wantStatus int
	}{
		{name: "exact origin", origin: []string{"http://example.com"}, wantStatus: http.StatusOK},
		{name: "native fetch metadata", fetchSite: []string{"same-origin"}, fetchMode: "cors", fetchDest: "empty", wantStatus: http.StatusOK},
		{name: "foreign origin", origin: []string{"https://evil.example"}, wantStatus: http.StatusForbidden},
		{name: "null origin", origin: []string{"null"}, wantStatus: http.StatusForbidden},
		{name: "duplicate origin", origin: []string{"http://example.com", "http://example.com"}, wantStatus: http.StatusForbidden},
		{name: "missing metadata", wantStatus: http.StatusForbidden},
		{name: "duplicate metadata", fetchSite: []string{"same-origin", "same-origin"}, fetchMode: "cors", fetchDest: "empty", wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, service := setupTaskServer(t)
			query := url.Values{"repositoryIdentity": []string{`e:\repo`}, "task": []string{"inspect"}}
			req := httptest.NewRequest(http.MethodGet, "/api/tasks/preview?"+query.Encode(), nil)
			req.Host = "example.com"
			for _, value := range test.origin {
				req.Header.Add("Origin", value)
			}
			for _, value := range test.fetchSite {
				req.Header.Add("Sec-Fetch-Site", value)
			}
			if test.fetchMode != "" {
				req.Header.Set("Sec-Fetch-Mode", test.fetchMode)
			}
			if test.fetchDest != "" {
				req.Header.Set("Sec-Fetch-Dest", test.fetchDest)
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			if test.wantStatus != http.StatusOK && service.preflightCalls != 0 {
				t.Fatalf("preflight calls = %d for unauthorized request", service.preflightCalls)
			}
		})
	}
}

func TestTaskPreviewAndStartRequireAuthAndUseExactTask(t *testing.T) {
	server, service := setupTaskServer(t)
	taskText := "한글\nquotes ' \" & | $(echo no)"
	query := url.Values{"repositoryIdentity": []string{`e:\repo`}, "task": []string{taskText}}
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/preview?"+query.Encode(), nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var preview struct {
		PreviewToken string `json:"previewToken"`
		Command      struct {
			Args  []string `json:"args"`
			Stdin string   `json:"stdin"`
		} `json:"command"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.PreviewToken == "" || preview.Command.Stdin != taskText || strings.Join(preview.Command.Args, " ") != "run --agent codex --json" {
		t.Fatalf("preview = %#v", preview)
	}

	body, _ := json.Marshal(map[string]string{"repositoryIdentity": `e:\repo`, "task": taskText, "previewToken": preview.PreviewToken})
	post := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(string(body)))
	post.Host = "example.com"
	post.Header.Set("Origin", "http://example.com")
	post.Header.Set("X-CSRF-Token", server.csrfToken)
	postRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(postRecorder, post)
	if postRecorder.Code != http.StatusAccepted {
		t.Fatalf("start status = %d body=%s", postRecorder.Code, postRecorder.Body.String())
	}
	if service.taskCalls != 1 {
		t.Fatalf("task calls = %d", service.taskCalls)
	}
	if service.preflightCalls != 2 {
		t.Fatalf("preflight calls = %d", service.preflightCalls)
	}
}

func TestTaskStartRejectsMissingCSRFBeforeAdapter(t *testing.T) {
	server, service := setupTaskServer(t)
	body := `{"repositoryIdentity":"e:\\repo","task":"inspect","previewToken":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(body))
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden || service.taskCalls != 0 || service.preflightCalls != 0 {
		t.Fatalf("status=%d taskCalls=%d preflightCalls=%d", recorder.Code, service.taskCalls, service.preflightCalls)
	}
}

func TestTaskStartRejectsForeignOriginBeforeAdapter(t *testing.T) {
	server, service := setupTaskServer(t)
	body := `{"repositoryIdentity":"e:\\repo","task":"inspect","previewToken":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(body))
	req.Host = "example.com"
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden || service.taskCalls != 0 || service.preflightCalls != 0 {
		t.Fatalf("status=%d taskCalls=%d preflightCalls=%d", recorder.Code, service.taskCalls, service.preflightCalls)
	}
}
