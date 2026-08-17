package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type fakeInspector struct{}

func (fakeInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	return gitx.Repository{Name: "repo", Path: path, Branch: "main", Head: "abcdef123456", HeadShort: "abcdef12"}, nil
}

type mutableInspector struct {
	repository gitx.Repository
}

func (m *mutableInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	repo := m.repository
	repo.Path = path
	return repo, nil
}

type fakeService struct {
	commentErr        error
	closeCalls        int
	repoPath          string
	cancelCalls       int
	installationCalls int
	panelRaw          json.RawMessage
	reviewRequest     roborev.ReviewRequest
	reviewReceipt     json.RawMessage
}

func (f *fakeService) Installation(context.Context) roborev.Installation {
	f.installationCalls++
	return roborev.Installation{Installed: true, Version: "v0.64.0", Status: json.RawMessage(`{"running":true,"known":true}`)}
}
func (f *fakeService) List(context.Context, string, int) (json.RawMessage, error) {
	return json.RawMessage(`[]`), nil
}
func (f *fakeService) JobMetadata(_ context.Context, id int64) (roborev.JobMetadata, error) {
	path := f.repoPath
	if path == "" {
		path = `E:\repo`
	}
	return roborev.JobMetadata{ID: id, RepoPath: path}, nil
}
func (f *fakeService) Show(context.Context, int64) (json.RawMessage, error) {
	return json.RawMessage(`{"id":42}`), nil
}
func (f *fakeService) PanelMembers(context.Context, string) (json.RawMessage, error) {
	if f.panelRaw != nil {
		return f.panelRaw, nil
	}
	return json.RawMessage(`{"jobs":[]}`), nil
}
func (f *fakeService) Prompt(context.Context, int64) (string, error) { return "prompt", nil }
func (f *fakeService) Log(context.Context, int64) (string, error)    { return "log", nil }
func (f *fakeService) Review(_ context.Context, request roborev.ReviewRequest) (json.RawMessage, error) {
	f.reviewRequest = request
	if f.reviewReceipt != nil {
		return f.reviewReceipt, nil
	}
	return json.RawMessage(`{"id":42}`), nil
}
func (f *fakeService) Rerun(context.Context, int64) error           { return nil }
func (f *fakeService) Comment(context.Context, int64, string) error { return f.commentErr }
func (f *fakeService) Close(context.Context, int64, bool) error     { f.closeCalls++; return nil }
func (f *fakeService) Cancel(context.Context, int64) error          { f.cancelCalls++; return nil }
func (f *fakeService) StartDaemon(context.Context) error            { return nil }

func setupServer(t *testing.T, service *fakeService) *Server {
	t.Helper()
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestCommentCloseDoesNotCloseWhenCommentFails(t *testing.T) {
	service := &fakeService{commentErr: errors.New("comment rejected")}
	server := setupServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/api/reviews/42/comment-close", strings.NewReader(`{"comment":"details"}`))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", recorder.Code)
	}
	if service.closeCalls != 0 {
		t.Fatalf("close called after failed comment: %d", service.closeCalls)
	}
}

func TestMutationRequiresCSRF(t *testing.T) {
	server := setupServer(t, &fakeService{})
	request := httptest.NewRequest(http.MethodPost, "/api/reviews/42/cancel", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", recorder.Code)
	}
}

func TestForeignRepositoryJobIsRejected(t *testing.T) {
	service := &fakeService{repoPath: `E:\foreign-repo`}
	server := setupServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/api/reviews/42/cancel", nil)
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if service.cancelCalls != 0 {
		t.Fatal("foreign job was canceled")
	}
}

func TestRuntimeHealthDoesNotBootstrapRepositories(t *testing.T) {
	service := &fakeService{}
	server := setupServer(t, service)
	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if service.installationCalls != 1 {
		t.Fatalf("expected one lightweight installation check, got %d", service.installationCalls)
	}
	if !strings.Contains(recorder.Body.String(), `"version":"v0.64.0"`) {
		t.Fatalf("runtime response missing version: %s", recorder.Body.String())
	}
}

func TestPanelMembersEnforcesRepositoryBoundary(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{"registered", `{"jobs":[{"id":1,"repo_path":"E:\\repo"}]}`, http.StatusOK},
		{"foreign", `{"jobs":[{"id":1,"repo_path":"E:\\foreign"}]}`, http.StatusForbidden},
		{"empty", `{"jobs":[]}`, http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := setupServer(t, &fakeService{panelRaw: json.RawMessage(test.raw)})
			request := httptest.NewRequest(http.MethodGet, "/api/panels/986d3bfa-05da-4621-aeca-66f493a19ea3/members", nil)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("expected %d, got %d: %s", test.want, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestCreateReviewReturnsFreshPinnedTargetAndReceipt(t *testing.T) {
	inspector := &mutableInspector{repository: gitx.Repository{Name: "repo", Branch: "main", Head: "old-bootstrap-sha", HeadShort: "oldboots"}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	inspector.repository = gitx.Repository{Name: "repo", Branch: "feature/fresh", Head: "fresh-pinned-sha", HeadShort: "freshpin", Dirty: true}
	service := &fakeService{reviewReceipt: json.RawMessage(`{"job_id":50,"status":"queued"}`)}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/reviews", strings.NewReader(`{"repoPath":"E:\\repo","panel":"dashboard_cross"}`))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if service.reviewRequest.GitRef != "fresh-pinned-sha" || service.reviewRequest.Branch != "feature/fresh" {
		t.Fatalf("enqueue did not use fresh inspection: %#v", service.reviewRequest)
	}
	var response struct {
		Receipt struct {
			JobID int64 `json:"job_id"`
		} `json:"receipt"`
		Target gitx.Repository `json:"target"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Receipt.JobID != 50 || response.Target.Head != "fresh-pinned-sha" || response.Target.Branch != "feature/fresh" || !response.Target.Dirty {
		t.Fatalf("unexpected review metadata: %#v", response)
	}
}

func TestBootstrapDoesNotAdvertiseFictionalReviewCLICommand(t *testing.T) {
	server := setupServer(t, &fakeService{})
	request := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "reviewHeadCommand") {
		t.Fatalf("bootstrap still contains command preview: %s", recorder.Body.String())
	}
}
