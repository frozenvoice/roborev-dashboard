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

type fakePanelResolver struct {
	catalog roborev.PanelCatalog
	err     error
}

func (f fakePanelResolver) Resolve(context.Context, string) (roborev.PanelCatalog, error) {
	return f.catalog, f.err
}

func testPanelCatalog() roborev.PanelCatalog {
	return roborev.PanelCatalog{Panels: []roborev.PanelInfo{
		{Name: roborev.PanelNone, Kind: "single", Members: []string{}, Origin: "builtin", Valid: true},
		{Name: "dashboard_cross", Kind: "panel", Members: []string{"reviewer"}, Origin: "local", Valid: true},
	}}
}

func (m *mutableInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	repo := m.repository
	if repo.Path == "" {
		repo.Path = path
	}
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
	reviewCalls       int
	showRaw           json.RawMessage
	showCalls         int
	metadataID        int64
}

func (f *fakeService) Installation(context.Context) roborev.Installation {
	f.installationCalls++
	return roborev.Installation{Installed: true, Version: "v0.64.0", Capabilities: roborev.Capabilities{ReviewEnqueueAPI: true, FrozenRerunAPI: true, JobListAPI: true, JobOutputStatusAPI: true, PanelReview: true, FixCommand: true}, Status: json.RawMessage(`{"running":true,"known":true}`)}
}
func (f *fakeService) List(context.Context, string, int) (json.RawMessage, error) {
	return json.RawMessage(`[]`), nil
}
func (f *fakeService) SynthesisStatus(_ context.Context, id int64) (roborev.SynthesisStatus, error) {
	path := f.repoPath
	if path == "" {
		path = `E:\repo`
	}
	return roborev.SynthesisStatus{JobMetadata: roborev.JobMetadata{ID: id, RepoPath: path, GitRef: "abcdef123456", Branch: "main", PanelName: "dashboard_cross", PanelRunUUID: "00000000-0000-0000-0000-000000000001", PanelRole: "synthesis", JobType: "synthesis", Status: "done", Verdict: "P"}}, nil
}
func (f *fakeService) JobOutputStatus(_ context.Context, id int64) (roborev.JobOutputStatus, error) {
	return roborev.JobOutputStatus{JobID: id, Status: "done"}, nil
}
func (f *fakeService) TerminalJobMetadata(_ context.Context, id int64) (roborev.JobMetadata, error) {
	path := f.repoPath
	if path == "" {
		path = `E:\repo`
	}
	return roborev.JobMetadata{ID: id, RepoPath: path, GitRef: "abcdef123456", Branch: "main", PanelName: "dashboard_cross", PanelRunUUID: "00000000-0000-0000-0000-000000000001", PanelRole: "synthesis", JobType: "synthesis", Status: "done", Verdict: "P"}, nil
}
func (f *fakeService) JobMetadata(_ context.Context, id int64) (roborev.JobMetadata, error) {
	path := f.repoPath
	if path == "" {
		path = `E:\repo`
	}
	metadataID := id
	if f.metadataID != 0 {
		metadataID = f.metadataID
	}
	return roborev.JobMetadata{ID: metadataID, RepoPath: path}, nil
}
func (f *fakeService) Show(context.Context, int64) (json.RawMessage, error) {
	f.showCalls++
	if f.showRaw != nil {
		return f.showRaw, nil
	}
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
	f.reviewCalls++
	f.reviewRequest = request
	if f.reviewReceipt != nil {
		return f.reviewReceipt, nil
	}
	data, _ := json.Marshal(map[string]any{"id": 42, "repo_path": request.RepoPath, "git_ref": request.GitRef, "branch": request.Branch, "panel_name": request.Panel, "panel_run_uuid": "00000000-0000-0000-0000-000000000042", "panel_role": "synthesis", "job_type": "synthesis"})
	return data, nil
}
func (f *fakeService) ReviewPanel(ctx context.Context, request roborev.ReviewRequest) (roborev.PanelEnqueueResponse, error) {
	raw, err := f.Review(ctx, request)
	if err != nil {
		return roborev.PanelEnqueueResponse{}, err
	}
	return roborev.DecodePanelEnqueueResponse(raw, request)
}
func (f *fakeService) Fix(context.Context, roborev.FixRequest) error { return nil }
func (f *fakeService) Rerun(context.Context, int64) error            { return nil }
func (f *fakeService) Comment(context.Context, int64, string) error  { return f.commentErr }
func (f *fakeService) Close(context.Context, int64, bool) error      { f.closeCalls++; return nil }
func (f *fakeService) Cancel(context.Context, int64) error           { f.cancelCalls++; return nil }
func (f *fakeService) StartDaemon(context.Context) error             { return nil }

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
	server.Panels = fakePanelResolver{catalog: testPanelCatalog()}
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

func TestPanelRefineListReturnsAuthorizedEnvelopeAndOptions(t *testing.T) {
	server := setupServer(t, &fakeService{})
	request := httptest.NewRequest(http.MethodGet, "/api/panel-refines?repo=E%3A%5Crepo", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Runs    []any `json:"runs"`
		Options struct {
			Executable       string   `json:"executable"`
			Args             []string `json:"args"`
			AllowedFixAgents []string `json:"allowedFixAgents"`
			DefaultFixAgent  string   `json:"defaultFixAgent"`
		} `json:"options"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Options.Executable != "roborev" || response.Options.DefaultFixAgent != "codex" || len(response.Options.Args) != 5 || len(response.Options.AllowedFixAgents) != 1 {
		t.Fatalf("unexpected Panel Refine options: %#v", response.Options)
	}
}

func TestPanelRefineListRejectsUnregisteredRepository(t *testing.T) {
	server := setupServer(t, &fakeService{})
	request := httptest.NewRequest(http.MethodGet, "/api/panel-refines?repo=E%3A%5Cforeign", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPanelRefineListRequiresRepositoryQuery(t *testing.T) {
	server := setupServer(t, &fakeService{})
	request := httptest.NewRequest(http.MethodGet, "/api/panel-refines", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
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
	server.Panels = fakePanelResolver{catalog: testPanelCatalog()}
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
		Target RepositoryDTO `json:"target"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Receipt.JobID != 50 || response.Target.Head != "fresh-pinned-sha" || response.Target.Branch != "feature/fresh" || !response.Target.Dirty || response.Target.Identity != gitx.RepositoryKey(`E:\repo`) {
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

func TestCreateReviewRejectsCanonicalRootDriftBeforeEnqueue(t *testing.T) {
	inspector := &mutableInspector{repository: gitx.Repository{Path: `E:\registered`, Head: "initial"}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), `E:\registered`); err != nil {
		t.Fatal(err)
	}
	inspector.repository = gitx.Repository{Path: `E:\replacement`, Head: "replacement-head"}
	service := &fakeService{}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/reviews", strings.NewReader(`{"repoPath":"E:\\registered","panel":"dashboard_cross"}`))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if service.reviewCalls != 0 {
		t.Fatalf("drifted repository was enqueued %d times", service.reviewCalls)
	}
}

func TestBootstrapRepositoryIdentitiesCorrelateWithSelection(t *testing.T) {
	server := setupServer(t, &fakeService{})
	request := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Repositories               []RepositoryDTO `json:"repositories"`
		SelectedRepository         string          `json:"selectedRepository"`
		SelectedRepositoryIdentity string          `json:"selectedRepositoryIdentity"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Repositories) != 1 {
		t.Fatalf("repositories = %#v", response.Repositories)
	}
	if response.Repositories[0].Path != response.SelectedRepository || response.Repositories[0].Identity != response.SelectedRepositoryIdentity || response.SelectedRepositoryIdentity != gitx.RepositoryKey(`E:\repo`) {
		t.Fatalf("identity correlation failed: %#v", response)
	}
}

func TestBootstrapOmitsRepositoryWhoseCanonicalRootDrifted(t *testing.T) {
	inspector := &mutableInspector{repository: gitx.Repository{Path: `E:\registered`, Head: "initial"}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), `E:\registered`); err != nil {
		t.Fatal(err)
	}
	inspector.repository = gitx.Repository{Path: `E:\replacement`, Head: "replacement"}
	server, err := NewServer(&fakeService{}, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Repositories []RepositoryDTO `json:"repositories"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Repositories) != 0 {
		t.Fatalf("drifted repository was exposed by bootstrap: %#v", response.Repositories)
	}
}

func TestAddRepositoryResponseCarriesRegistryIdentity(t *testing.T) {
	server := setupServer(t, &fakeService{})
	request := httptest.NewRequest(http.MethodPost, "/api/repos", strings.NewReader(`{"repoPath":"E:\\new repo"}`))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response RepositoryDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Path != `E:\new repo` || response.Identity != gitx.RepositoryKey(response.Path) {
		t.Fatalf("unexpected repository DTO: %#v", response)
	}
}
