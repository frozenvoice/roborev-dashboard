package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

type listLimitService struct {
	fakeService
	mu     sync.Mutex
	limits []int
}

func (f *listLimitService) List(_ context.Context, _ string, limit int) (json.RawMessage, error) {
	f.mu.Lock()
	f.limits = append(f.limits, limit)
	f.mu.Unlock()
	return json.RawMessage(`[]`), nil
}

// The Tasks history view raises the bounded list limit once per repository;
// ordinary polling keeps the default and invalid limits fail closed.
func TestListReviewsAcceptsBoundedLimitOnly(t *testing.T) {
	service := &listLimitService{}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		query      string
		want       int
		wantStatus int
	}{
		{query: "", want: 100, wantStatus: http.StatusOK},
		{query: "&limit=200", want: 200, wantStatus: http.StatusOK},
		{query: "&limit=1", want: 1, wantStatus: http.StatusOK},
		{query: "&limit=0", wantStatus: http.StatusBadRequest},
		{query: "&limit=201", wantStatus: http.StatusBadRequest},
		{query: "&limit=abc", wantStatus: http.StatusBadRequest},
		{query: "&limit=2&limit=3", wantStatus: http.StatusBadRequest},
	}
	for _, testCase := range cases {
		request := httptest.NewRequest(http.MethodGet, "/api/reviews?repo="+url.QueryEscape(`E:\repo`)+testCase.query, nil)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != testCase.wantStatus {
			t.Fatalf("%s: status = %d body=%s", testCase.query, recorder.Code, recorder.Body.String())
		}
		if testCase.wantStatus != http.StatusOK {
			continue
		}
		service.mu.Lock()
		got := service.limits[len(service.limits)-1]
		service.mu.Unlock()
		if got != testCase.want {
			t.Fatalf("%s: limit = %d, want %d", testCase.query, got, testCase.want)
		}
	}
}

// taskCountingService proves that the disabled endpoints never reach the
// RoboRev adapter or registry inspection machinery.
type taskCountingService struct {
	fakeService
	rerunCalls int
}

func (f *taskCountingService) Rerun(context.Context, int64) error {
	f.rerunCalls++
	return nil
}

func setupTaskServer(t *testing.T) (*Server, *taskCountingService) {
	t.Helper()
	service := &taskCountingService{fakeService: fakeService{}}
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

func assertTaskExecutionDisabled(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "task-execution-disabled") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "machine-readable contract") || strings.Contains(recorder.Body.String(), "read-only guaranteed") {
		t.Fatalf("disabled explanation = %s", recorder.Body.String())
	}
}

func TestTaskPreviewFollowsMutationAuthConvention(t *testing.T) {
	tests := []struct {
		name       string
		origin     string
		csrf       bool
		wantStatus int
		wantPhase  string
	}{
		{name: "foreign origin", origin: "https://evil.example", csrf: true, wantStatus: http.StatusForbidden, wantPhase: "origin"},
		{name: "missing csrf", origin: "http://example.com", wantStatus: http.StatusForbidden, wantPhase: "csrf"},
		{name: "authorized", origin: "http://example.com", csrf: true, wantStatus: http.StatusNotImplemented, wantPhase: "task-execution-disabled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, service := setupTaskServer(t)
			req := httptest.NewRequest(http.MethodPost, "/api/tasks/preview", strings.NewReader(`{"repositoryIdentity":"e:\\repo","task":"inspect"}`))
			req.Host = "example.com"
			if test.origin != "" {
				req.Header.Set("Origin", test.origin)
			}
			if test.csrf {
				req.Header.Set("X-CSRF-Token", server.csrfToken)
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), test.wantPhase) {
				t.Fatalf("body = %s", recorder.Body.String())
			}
			if service.installationCalls != 0 || service.metadataCalls != 0 {
				t.Fatalf("service was consulted by disabled preview: installation=%d metadata=%d", service.installationCalls, service.metadataCalls)
			}
		})
	}
}

func TestTaskStartWithoutCSRFIsRejectedBeforeDisabledResponse(t *testing.T) {
	server, service := setupTaskServer(t)
	body := `{"repositoryIdentity":"e:\\repo","task":"inspect","previewToken":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(body))
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.installationCalls != 0 || service.metadataCalls != 0 {
		t.Fatal("mutation authorization must fail before any adapter work")
	}
}

func TestTaskStartWithValidMutationAuthIsStablyDisabled(t *testing.T) {
	server, service := setupTaskServer(t)
	body := `{"repositoryIdentity":"e:\\repo","task":"inspect","previewToken":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(body))
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	assertTaskExecutionDisabled(t, recorder)
	if service.installationCalls != 0 || service.metadataCalls != 0 {
		t.Fatalf("disabled start reached RoboRev: installation=%d metadata=%d", service.installationCalls, service.metadataCalls)
	}
}

func TestTaskPreviewGETIsNotAllowed(t *testing.T) {
	server, _ := setupTaskServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/preview", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestGenericRerunRejectsTaskJobsBeforeRoboRevRerun(t *testing.T) {
	server, service := setupTaskServer(t)
	service.fakeService.panelMetadata = map[int64]roborev.JobMetadata{
		42: {ID: 42, RepoPath: `E:\repo`, GitRef: "run", Branch: "main", JobType: "task", Status: "queued"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/reviews/42/rerun", nil)
	req.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.rerunCalls != 0 {
		t.Fatalf("Rerun calls = %d", service.rerunCalls)
	}
	if !strings.Contains(recorder.Body.String(), "task-rerun-unsupported") || !strings.Contains(recorder.Body.String(), "Tasks v1 does not support rerun") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

// Repository ownership is authorized before job-type policy: an unregistered
// repository's task job must not reveal its type or rerun policy.
func TestGenericRerunAuthorizesRepositoryBeforeTaskPolicy(t *testing.T) {
	server, service := setupTaskServer(t)
	service.fakeService.panelMetadata = map[int64]roborev.JobMetadata{
		43: {ID: 43, RepoPath: `D:\unregistered`, GitRef: "run", Branch: "main", JobType: "task", Status: "done"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/reviews/43/rerun", nil)
	req.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"repository"`) || strings.Contains(recorder.Body.String(), "task-rerun-unsupported") || strings.Contains(recorder.Body.String(), "task") {
		t.Fatalf("task policy leaked before ownership authorization: %s", recorder.Body.String())
	}
	if service.rerunCalls != 0 {
		t.Fatalf("Rerun calls = %d", service.rerunCalls)
	}
}

func TestGenericRerunRejectsNonReviewLikeJobTypes(t *testing.T) {
	for _, jobType := range []string{"fix", "compact", "insights", "classify", "unknown", "REVIEW"} {
		t.Run(jobType, func(t *testing.T) {
			server, service := setupTaskServer(t)
			service.fakeService.panelMetadata = map[int64]roborev.JobMetadata{
				42: {ID: 42, RepoPath: `E:\repo`, GitRef: "abcdef", Branch: "main", JobType: jobType, Status: "done"},
			}
			req := httptest.NewRequest(http.MethodPost, "/api/reviews/42/rerun", nil)
			req.Header.Set("X-CSRF-Token", server.csrfToken)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)
			if recorder.Code != http.StatusConflict || service.rerunCalls != 0 {
				t.Fatalf("job type %q status=%d reruns=%d body=%s", jobType, recorder.Code, service.rerunCalls, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), "unsupported") {
				t.Fatalf("unsupported policy missing: %s", recorder.Body.String())
			}
		})
	}
}

func TestGenericRerunPreservesHistoricalReviewLikeTypes(t *testing.T) {
	for _, jobType := range []string{"", " ", "review", "range", "dirty", "synthesis"} {
		t.Run(strings.TrimSpace(jobType), func(t *testing.T) {
			server, service := setupTaskServer(t)
			service.fakeService.panelMetadata = map[int64]roborev.JobMetadata{
				42: {ID: 42, RepoPath: `E:\repo`, GitRef: "abcdef", Branch: "main", JobType: jobType, Status: "done"},
			}
			req := httptest.NewRequest(http.MethodPost, "/api/reviews/42/rerun", nil)
			req.Header.Set("X-CSRF-Token", server.csrfToken)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)
			if recorder.Code != http.StatusCreated || service.rerunCalls != 1 {
				t.Fatalf("job type %q status=%d reruns=%d body=%s", jobType, recorder.Code, service.rerunCalls, recorder.Body.String())
			}
		})
	}
}
