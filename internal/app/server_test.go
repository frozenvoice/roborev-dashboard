package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type fakeInspector struct{}

func (fakeInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	return gitx.Repository{Name: "repo", Path: path, Branch: "main", Head: "abcdef123456", HeadShort: "abcdef12"}, nil
}

type mutableInspector struct {
	repository gitx.Repository
	err        error
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
	if m.err != nil {
		return gitx.Repository{}, m.err
	}
	repo := m.repository
	if repo.Path == "" {
		repo.Path = path
	}
	return repo, nil
}

type fakeService struct {
	commentErr            error
	cancelErr             error
	closeCalls            int
	closeErr              error
	repoPath              string
	cancelCalls           int
	installationCalls     int
	installationValue     *roborev.Installation
	fixCalls              int
	panelRaw              json.RawMessage
	panelMembersErr       error
	reviewRequest         roborev.ReviewRequest
	reviewReceipt         json.RawMessage
	reviewErr             error
	reviewCalls           int
	rerunErr              error
	showRaw               json.RawMessage
	showCalls             int
	showErr               error
	listErr               error
	promptCalls           int
	promptValue           string
	promptErr             error
	logCalls              int
	logValue              string
	logErr                error
	panelMetadata         map[int64]roborev.JobMetadata
	metadataID            int64
	metadataErr           error
	metadataCalls         int
	authorizedPromptCalls int
	authorizedLogCalls    int
	authorizedPaths       []string
	panelMembersCalls     int
	rerunCalls            int
}

func (f *fakeService) Installation(context.Context) roborev.Installation {
	f.installationCalls++
	if f.installationValue != nil {
		return *f.installationValue
	}
	return roborev.Installation{Installed: true, Version: "v0.64.0", Capabilities: roborev.Capabilities{ReviewEnqueueAPI: true, FrozenRerunAPI: true, JobListAPI: true, JobOutputStatusAPI: true, PanelReview: true, FixCommand: true}, Status: json.RawMessage(`{"running":true,"known":true}`)}
}
func (f *fakeService) List(context.Context, string, int) (json.RawMessage, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
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
	f.metadataCalls++
	if f.metadataErr != nil {
		return roborev.JobMetadata{}, f.metadataErr
	}
	if f.panelMetadata != nil {
		metadata, ok := f.panelMetadata[id]
		if !ok {
			return roborev.JobMetadata{}, errors.New("job not found")
		}
		return metadata, nil
	}
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
	if f.showErr != nil {
		return nil, f.showErr
	}
	if f.showRaw != nil {
		return f.showRaw, nil
	}
	return json.RawMessage(`{"id":42}`), nil
}
func (f *fakeService) PanelMembers(context.Context, string) (json.RawMessage, error) {
	f.panelMembersCalls++
	if f.panelMembersErr != nil {
		return nil, f.panelMembersErr
	}
	if f.panelRaw != nil {
		return f.panelRaw, nil
	}
	return json.RawMessage(`{"jobs":[]}`), nil
}
func (f *fakeService) Prompt(context.Context, int64) (string, error) {
	f.promptCalls++
	if f.promptErr != nil {
		return "", f.promptErr
	}
	if f.promptValue != "" {
		return f.promptValue, nil
	}
	return "prompt", nil
}
func (f *fakeService) Log(context.Context, int64) (string, error) {
	f.logCalls++
	if f.logErr != nil {
		return "", f.logErr
	}
	if f.logValue != "" {
		return f.logValue, nil
	}
	return "log", nil
}

// AuthorizedPrompt/AuthorizedLog model the production narrow lazy-content
// contract used by the handlers.  The fake deliberately keeps the ownership
// check separate from Prompt/Log so tests can prove foreign requests never
// reach the content counters.
func (f *fakeService) authorizeLazy(id int64, registeredPaths []string) error {
	metadata, err := f.JobMetadata(context.Background(), id)
	if err != nil {
		return err
	}
	if metadata.ID != id {
		return roborev.ErrLazyContentMalformed
	}
	for _, path := range registeredPaths {
		if gitx.SameRepositoryPath(metadata.RepoPath, path) {
			return nil
		}
	}
	return roborev.ErrLazyContentForbidden
}

func (f *fakeService) AuthorizedPrompt(_ context.Context, id int64, registeredPaths []string) (string, error) {
	f.authorizedPromptCalls++
	f.authorizedPaths = append([]string(nil), registeredPaths...)
	if err := f.authorizeLazy(id, registeredPaths); err != nil {
		return "", err
	}
	return f.Prompt(context.Background(), id)
}

func (f *fakeService) AuthorizedLog(_ context.Context, id int64, registeredPaths []string) (string, error) {
	f.authorizedLogCalls++
	f.authorizedPaths = append([]string(nil), registeredPaths...)
	if err := f.authorizeLazy(id, registeredPaths); err != nil {
		return "", err
	}
	return f.Log(context.Background(), id)
}
func (f *fakeService) Review(_ context.Context, request roborev.ReviewRequest) (json.RawMessage, error) {
	f.reviewCalls++
	f.reviewRequest = request
	if f.reviewErr != nil {
		return nil, f.reviewErr
	}
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
func (f *fakeService) Fix(context.Context, roborev.FixRequest) error { f.fixCalls++; return nil }
func (f *fakeService) Rerun(context.Context, int64) error            { f.rerunCalls++; return f.rerunErr }
func (f *fakeService) Comment(context.Context, int64, string) error  { return f.commentErr }
func (f *fakeService) Close(context.Context, int64, bool) error      { f.closeCalls++; return f.closeErr }
func (f *fakeService) Cancel(context.Context, int64) error           { f.cancelCalls++; return f.cancelErr }
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

func TestShowReviewScopedRepositoryIdentityAuthorization(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	for _, path := range []string{`E:\repo-a`, `E:\repo-b`} {
		if _, err := registry.Add(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	identityA, err := registry.Identity(`E:\repo-a`)
	if err != nil {
		t.Fatal(err)
	}
	service := &fakeService{
		showRaw:       json.RawMessage(`{"job_id":42,"review":{"output":"ok"}}`),
		panelMetadata: map[int64]roborev.JobMetadata{42: {ID: 42, RepoPath: `E:\repo-a`}},
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/reviews/42?repositoryIdentity="+url.QueryEscape(identityA), nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != string(service.showRaw) || service.showCalls != 1 {
		t.Fatalf("allowed scoped request = %d calls=%d body=%s", recorder.Code, service.showCalls, recorder.Body.String())
	}

	service.showCalls = 0
	service.panelMetadata[42] = roborev.JobMetadata{ID: 42, RepoPath: `E:\repo-b`}
	request = httptest.NewRequest(http.MethodGet, "/api/reviews/42?repositoryIdentity="+url.QueryEscape(identityA), nil)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || service.showCalls != 0 {
		t.Fatalf("foreign scoped job = %d calls=%d body=%s", recorder.Code, service.showCalls, recorder.Body.String())
	}
}

func TestShowReviewScopedRepositoryIdentityQueryValidation(t *testing.T) {
	server := setupServer(t, &fakeService{})
	for _, query := range []string{
		"?repositoryIdentity=",
		"?repositoryIdentity=%20",
		"?repositoryIdentity=E%3A%5Crepo&repositoryIdentity=E%3A%5Crepo",
		"?repositoryIdentity=E%3A%5Crepo&extra=value",
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/reviews/42"+query, nil)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("query %q status = %d, want %d: %s", query, recorder.Code, http.StatusBadRequest, recorder.Body.String())
		}
	}
}

func TestShowReviewWithoutRepositoryIdentityPreservesGenericAuthorization(t *testing.T) {
	service := &fakeService{showRaw: json.RawMessage(`{"id":42}`), panelMetadata: map[int64]roborev.JobMetadata{42: {ID: 42, RepoPath: `E:\repo`}}}
	server := setupServer(t, service)
	request := httptest.NewRequest(http.MethodGet, "/api/reviews/42", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.showCalls != 1 || recorder.Body.String() != string(service.showRaw) {
		t.Fatalf("generic request = %d calls=%d body=%s", recorder.Code, service.showCalls, recorder.Body.String())
	}
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

func TestBrowserErrorsDoNotExposeExternalDiagnostics(t *testing.T) {
	const (
		privatePath   = `C:\Users\private\repositories\secret-project`
		privateMarker = "PRIVATE_EXTERNAL_DIAGNOSTIC_7f3c"
		run           = "986d3bfa-05da-4621-aeca-66f493a19ea3"
	)
	privateErr := errors.New(privatePath + ": " + privateMarker)
	type testCase struct {
		name      string
		configure func(*fakeService)
		request   func(*testing.T, *Server) *http.Request
		status    int
		phase     string
	}
	requestWithCSRF := func(method, path, body string) func(*testing.T, *Server) *http.Request {
		return func(_ *testing.T, server *Server) *http.Request {
			request := httptest.NewRequest(method, path, strings.NewReader(body))
			request.Header.Set("X-CSRF-Token", server.csrfToken)
			return request
		}
	}
	cases := []testCase{
		{
			name:      "list",
			configure: func(service *fakeService) { service.listErr = privateErr },
			request:   requestWithCSRF(http.MethodGet, "/api/reviews?repo="+url.QueryEscape(`E:\repo`), ""),
			status:    http.StatusBadGateway, phase: "list",
		},
		{
			name:      "detail",
			configure: func(service *fakeService) { service.showErr = privateErr },
			request:   requestWithCSRF(http.MethodGet, "/api/reviews/42", ""),
			status:    http.StatusBadGateway, phase: "detail",
		},
		{
			name:      "create",
			configure: func(service *fakeService) { service.reviewErr = privateErr },
			request:   requestWithCSRF(http.MethodPost, "/api/reviews", `{"repoPath":"E:\\repo","panel":"none"}`),
			status:    http.StatusBadGateway, phase: "enqueue",
		},
		{
			name:      "rerun",
			configure: func(service *fakeService) { service.rerunErr = privateErr },
			request:   requestWithCSRF(http.MethodPost, "/api/reviews/42/rerun", ""),
			status:    http.StatusBadGateway, phase: "rerun",
		},
		{
			name:      "comment",
			configure: func(service *fakeService) { service.commentErr = privateErr },
			request:   requestWithCSRF(http.MethodPost, "/api/reviews/42/comment", `{"comment":"details"}`),
			status:    http.StatusBadGateway, phase: "comment",
		},
		{
			name:      "comment-close",
			configure: func(service *fakeService) { service.closeErr = privateErr },
			request:   requestWithCSRF(http.MethodPost, "/api/reviews/42/comment-close", `{"comment":"details"}`),
			status:    http.StatusBadGateway, phase: "close",
		},
		{
			name:      "close",
			configure: func(service *fakeService) { service.closeErr = privateErr },
			request:   requestWithCSRF(http.MethodPost, "/api/reviews/42/close", ""),
			status:    http.StatusBadGateway, phase: "close",
		},
		{
			name:      "cancel",
			configure: func(service *fakeService) { service.cancelErr = privateErr },
			request:   requestWithCSRF(http.MethodPost, "/api/reviews/42/cancel", ""),
			status:    http.StatusBadGateway, phase: "cancel",
		},
		{
			name:      "panel-members",
			configure: func(service *fakeService) { service.panelMembersErr = privateErr },
			request: func(t *testing.T, server *Server) *http.Request {
				identity, err := server.Registry.Identity(`E:\repo`)
				if err != nil {
					t.Fatal(err)
				}
				path := "/api/panels/" + run + "/members?parentJobId=42&repositoryIdentity=" + url.QueryEscape(identity)
				return strictBrowserReadRequest(http.MethodGet, path)
			},
			status: http.StatusBadGateway, phase: "panel-members",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeService{}
			test.configure(service)
			server := setupServer(t, service)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, test.request(t, server))
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.status, recorder.Body.String())
			}
			var response map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
			if response["phase"] != test.phase {
				t.Fatalf("phase = %v, want %q: %s", response["phase"], test.phase, recorder.Body.String())
			}
			body := recorder.Body.String()
			if strings.Contains(body, privateMarker) || strings.Contains(body, privatePath) {
				t.Fatalf("external diagnostic leaked: %s", body)
			}
		})
	}
}

func TestGitInspectionErrorsDoNotExposeExternalDiagnostics(t *testing.T) {
	const (
		root          = `E:\repo`
		privatePath   = `C:\Users\private\repositories\secret-project`
		privateMarker = "PRIVATE_GIT_INSPECTION_DIAGNOSTIC_4a2e"
	)
	inspector := &mutableInspector{repository: gitx.Repository{Name: "repo", Path: root, Branch: "main", Head: "abcdef123456", HeadShort: "abcdef12"}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	inspector.err = errors.New(privatePath + ": " + privateMarker)
	service := &fakeService{}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	server.Panels = fakePanelResolver{catalog: testPanelCatalog()}
	request := httptest.NewRequest(http.MethodPost, "/api/reviews", strings.NewReader(`{"repoPath":"E:\\repo","panel":"none"}`))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["phase"] != "repository" {
		t.Fatalf("phase = %v, want repository: %s", response["phase"], recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), privateMarker) || strings.Contains(recorder.Body.String(), privatePath) {
		t.Fatalf("Git diagnostic leaked: %s", recorder.Body.String())
	}
	if service.reviewCalls != 0 {
		t.Fatalf("Review called after failed Git inspection: %d", service.reviewCalls)
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

func TestSelectRepositoryMapsCanceledQueuedSelectionToRequestTimeout(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo-a`); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(context.Background(), `E:\repo-b`); err != nil {
		t.Fatal(err)
	}
	if err := registry.Select(`E:\repo-a`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(&fakeService{}, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := registry.acquireSelected(gitx.RepositoryKey(`E:\repo-a`))
	if err != nil {
		t.Fatal(err)
	}
	requestContext, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/api/repos/select", strings.NewReader(`{"repoPath":"E:\\repo-b"}`)).WithContext(requestContext)
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		response <- recorder
	}()
	waitForPendingSelectionWriter(t, registry)
	cancel()
	lease.Release()
	select {
	case recorder := <-response:
		if recorder.Code != http.StatusRequestTimeout {
			t.Fatalf("canceled selection response=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["phase"] != "repository" {
			t.Fatalf("canceled selection phase=%v, want repository", body["phase"])
		}
	case <-time.After(time.Second):
		t.Fatal("canceled selection handler did not return")
	}
	if got := registry.Selected(); got != `E:\repo-a` {
		t.Fatalf("selected repository changed after canceled handler: %q", got)
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

func TestRuntimeAndBootstrapProjectInstallationWithoutPrivateFields(t *testing.T) {
	const privateMarker = "INSTALLATION_PRIVATE_MARKER"
	installation := roborev.Installation{
		Installed:   true,
		Binary:      privateMarker,
		Version:     "v0.64.0",
		Status:      json.RawMessage(`{"running":true,"known":true,"health":{"recent_errors":["` + privateMarker + `"]}}`),
		Error:       privateMarker,
		FailureCode: privateMarker,
		Capabilities: roborev.Capabilities{
			ReviewEnqueueAPI:  true,
			JobListAPI:        true,
			SummaryStats:      true,
			CostStats:         true,
			GuidelineInsights: true,
		},
	}
	server := setupServer(t, &fakeService{installationValue: &installation})

	for _, path := range []string{"/api/runtime", "/api/bootstrap"} {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d: %s", path, recorder.Code, recorder.Body.String())
		}
		body := recorder.Body.String()
		if strings.Contains(body, privateMarker) || strings.Contains(body, `"binary"`) || strings.Contains(body, `"error"`) {
			t.Fatalf("%s leaked private installation fields: %s", path, body)
		}
		if !strings.Contains(body, `"installed":true`) || !strings.Contains(body, `"version":"v0.64.0"`) || !strings.Contains(body, `"running":true`) {
			t.Fatalf("%s lost required installation projection: %s", path, body)
		}
	}
}

func TestPanelRefineListReturnsAuthorizedEnvelopeAndOptions(t *testing.T) {
	server := setupServer(t, &fakeService{})
	request := strictBrowserReadRequest(http.MethodGet, "/api/panel-refines?repo=E%3A%5Crepo")
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
	request := strictBrowserReadRequest(http.MethodGet, "/api/panel-refines?repo=E%3A%5Cforeign")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPanelRefineListRequiresRepositoryQuery(t *testing.T) {
	server := setupServer(t, &fakeService{})
	request := strictBrowserReadRequest(http.MethodGet, "/api/panel-refines")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPanelMembersAcceptsRoboRevV064FullRunAndEnforcesRepositoryBoundary(t *testing.T) {
	run := "986d3bfa-05da-4621-aeca-66f493a19ea3"
	parent := panelTestJob(42, `E:\repo`, "dashboard_cross", run, "synthesis")
	member := panelTestJob(43, `E:\repo`, "dashboard_cross", run, "member")
	service := &fakeService{panelRaw: panelJobsJSON(parent, member)}
	server := setupServer(t, service)
	identity, err := server.Registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	query := "?parentJobId=42&repositoryIdentity=" + url.QueryEscape(identity)
	request := strictBrowserReadRequest(http.MethodGet, "/api/panels/"+run+"/members"+query)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "repo_path") || !strings.Contains(recorder.Body.String(), `"panel_role":"member"`) {
		t.Fatalf("registered panel members = %d: %s", recorder.Code, recorder.Body.String())
	}
	if service.panelMembersCalls != 1 || service.metadataCalls != 0 {
		t.Fatalf("panel member list calls = PanelMembers:%d JobMetadata:%d, want 1/0", service.panelMembersCalls, service.metadataCalls)
	}

	service.panelRaw = json.RawMessage(`{"jobs":[{"id":42,"repo_path":"E:\\foreign","panel_name":"dashboard_cross","panel_run_uuid":"` + run + `","panel_role":"synthesis","job_type":"synthesis"},{"id":43,"repo_path":"E:\\foreign","panel_name":"dashboard_cross","panel_run_uuid":"` + run + `","panel_role":"member","job_type":"review"}]}`)
	request = strictBrowserReadRequest(http.MethodGet, "/api/panels/"+run+"/members"+query)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("unregistered panel members status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPanelMembersAcceptsBackendValidatedAlternateWindowsPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path identity contract")
	}
	run := "986d3bfa-05da-4621-aeca-66f493a19ea3"
	// RoboRev can spell the same registered checkout with different case,
	// separators, and dot segments. The backend's gitx identity is authoritative.
	alternatePath := `e:/REPO/.`
	parent := panelTestJob(42, alternatePath, "dashboard_cross", run, "synthesis")
	member := panelTestJob(43, alternatePath, "dashboard_cross", run, "member")
	service := &fakeService{panelRaw: panelJobsJSON(parent, member)}
	server := setupServer(t, service)
	identity, err := server.Registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	request := strictBrowserReadRequest(http.MethodGet, "/api/panels/"+run+"/members?parentJobId=42&repositoryIdentity="+url.QueryEscape(identity))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("alternate Windows repository spelling status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func panelTestJob(id int64, repo, panel, run, role string) roborev.JobMetadata {
	jobType := "review"
	if role == "synthesis" {
		jobType = "synthesis"
	}
	return panelTestJobWithType(id, repo, panel, run, role, jobType)
}

func panelTestJobWithType(id int64, repo, panel, run, role, jobType string) roborev.JobMetadata {
	return roborev.JobMetadata{ID: id, RepoPath: repo, PanelName: panel, PanelRunUUID: run, PanelRole: role, JobType: jobType}
}

func panelJobsJSON(jobs ...roborev.JobMetadata) json.RawMessage {
	data, _ := json.Marshal(struct {
		Jobs []roborev.JobMetadata `json:"jobs"`
	}{Jobs: jobs})
	return data
}

func TestPanelMembersRequiresExactIdentityQueryAndParent(t *testing.T) {
	run := "986d3bfa-05da-4621-aeca-66f493a19ea3"
	parent := panelTestJob(42, `E:\repo`, "dashboard_cross", run, "synthesis")
	member := panelTestJob(43, `E:\repo`, "dashboard_cross", run, "member")
	service := &fakeService{panelRaw: panelJobsJSON(parent, member)}
	server := setupServer(t, service)
	identity, err := server.Registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/panels/" + run + "/members"
	for _, query := range []string{"", "?parentJobId=42", "?repositoryIdentity=" + url.QueryEscape(identity), "?parentJobId=42&repositoryIdentity=" + url.QueryEscape(identity) + "&extra=x", "?parentJobId=0&repositoryIdentity=" + url.QueryEscape(identity), "?parentJobId=42&parentJobId=43&repositoryIdentity=" + url.QueryEscape(identity)} {
		request := strictBrowserReadRequest(http.MethodGet, base+query)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("query %q status = %d: %s", query, recorder.Code, recorder.Body.String())
		}
	}
	for _, invalidRun := range []string{"not-a-uuid", "986d3bfa-05da-4621-aeca-66f493a19e3"} {
		request := strictBrowserReadRequest(http.MethodGet, "/api/panels/"+invalidRun+"/members?parentJobId=42&repositoryIdentity="+url.QueryEscape(identity))
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid run %q status = %d: %s", invalidRun, recorder.Code, recorder.Body.String())
		}
	}

	request := strictBrowserReadRequest(http.MethodGet, base+"?parentJobId=42&repositoryIdentity="+url.QueryEscape(identity))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("valid identity query status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPanelMembersRejectsSemanticAndMalformedUpstreamRows(t *testing.T) {
	run := "986d3bfa-05da-4621-aeca-66f493a19ea3"
	parent := panelTestJob(42, `E:\repo`, "dashboard_cross", run, "synthesis")
	member := panelTestJob(43, `E:\repo`, "dashboard_cross", run, "member")
	server := setupServer(t, &fakeService{})
	identity, err := server.Registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	query := "?parentJobId=42&repositoryIdentity=" + url.QueryEscape(identity)
	cases := []struct {
		name string
		raw  json.RawMessage
		want int
	}{
		{name: "malformed JSON", raw: json.RawMessage(`{"jobs":`), want: http.StatusBadGateway},
		{name: "missing jobs", raw: json.RawMessage(`{"other":[]}`), want: http.StatusBadGateway},
		{name: "invalid ID", raw: panelJobsJSON(panelTestJob(0, `E:\repo`, "dashboard_cross", run, "synthesis"), member), want: http.StatusConflict},
		{name: "duplicate ID", raw: panelJobsJSON(parent, parent), want: http.StatusConflict},
		{name: "wrong role", raw: panelJobsJSON(parent, panelTestJob(43, `E:\repo`, "dashboard_cross", run, "coordinator")), want: http.StatusConflict},
		{name: "missing run", raw: panelJobsJSON(parent, panelTestJob(43, `E:\repo`, "dashboard_cross", "", "member")), want: http.StatusConflict},
		{name: "wrong run", raw: panelJobsJSON(parent, panelTestJob(43, `E:\repo`, "dashboard_cross", "00000000-0000-0000-0000-000000000001", "member")), want: http.StatusConflict},
		{name: "wrong panel", raw: panelJobsJSON(parent, panelTestJob(43, `E:\repo`, "other", run, "member")), want: http.StatusConflict},
		{name: "missing synthesis", raw: panelJobsJSON(member), want: http.StatusConflict},
		{name: "duplicate synthesis", raw: panelJobsJSON(parent, panelTestJob(44, `E:\repo`, "dashboard_cross", run, "synthesis"), member), want: http.StatusConflict},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service := server.RoboRev.(*fakeService)
			service.panelRaw = test.raw
			request := strictBrowserReadRequest(http.MethodGet, "/api/panels/"+run+"/members"+query)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestPanelMembersRejectsParentAndRegisteredRepositoryConflicts(t *testing.T) {
	run := "986d3bfa-05da-4621-aeca-66f493a19ea3"
	registry := NewRegistry(fakeInspector{}, "")
	for _, repo := range []string{`E:\repo`, `E:\other`} {
		if _, err := registry.Add(context.Background(), repo); err != nil {
			t.Fatal(err)
		}
	}
	parent := panelTestJob(42, `E:\repo`, "dashboard_cross", run, "synthesis")
	member := panelTestJob(43, `E:\repo`, "dashboard_cross", run, "member")
	service := &fakeService{panelRaw: panelJobsJSON(parent, member)}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	requestURL := "/api/panels/" + run + "/members?parentJobId=42&repositoryIdentity=" + url.QueryEscape(identity)
	cases := []struct {
		name string
		raw  json.RawMessage
		want int
	}{
		{name: "synthesis ID mismatch", raw: panelJobsJSON(panelTestJob(99, `E:\repo`, "dashboard_cross", run, "synthesis"), member), want: http.StatusConflict},
		{name: "synthesis role missing", raw: panelJobsJSON(panelTestJob(42, `E:\repo`, "dashboard_cross", run, "member"), member), want: http.StatusConflict},
		{name: "synthesis type mismatch", raw: panelJobsJSON(panelTestJobWithType(42, `E:\repo`, "dashboard_cross", run, "synthesis", "review"), member), want: http.StatusConflict},
		{name: "synthesis type missing", raw: panelJobsJSON(panelTestJobWithType(42, `E:\repo`, "dashboard_cross", run, "synthesis", ""), member), want: http.StatusConflict},
		{name: "synthesis run mismatch", raw: panelJobsJSON(panelTestJob(42, `E:\repo`, "dashboard_cross", "00000000-0000-0000-0000-000000000001", "synthesis"), member), want: http.StatusConflict},
		{name: "synthesis panel mismatch", raw: panelJobsJSON(panelTestJob(42, `E:\repo`, "other", run, "synthesis"), member), want: http.StatusConflict},
		{name: "registered repository mismatch", raw: panelJobsJSON(panelTestJob(42, `E:\other`, "dashboard_cross", run, "synthesis"), member), want: http.StatusConflict},
		{name: "member registered repository mismatch", raw: panelJobsJSON(parent, panelTestJob(43, `E:\other`, "dashboard_cross", run, "member")), want: http.StatusConflict},
		{name: "member unregistered repository", raw: panelJobsJSON(parent, panelTestJob(43, `E:\foreign`, "dashboard_cross", run, "member")), want: http.StatusForbidden},
		{name: "raw synthesis type mismatch", raw: panelJobsJSON(panelTestJobWithType(42, `E:\repo`, "dashboard_cross", run, "synthesis", "review"), member), want: http.StatusConflict},
		{name: "raw synthesis type missing", raw: panelJobsJSON(panelTestJobWithType(42, `E:\repo`, "dashboard_cross", run, "synthesis", ""), member), want: http.StatusConflict},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service.panelRaw = test.raw
			request := strictBrowserReadRequest(http.MethodGet, requestURL)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestPanelMemberDetailValidatesListBeforeShowAndShowIdentity(t *testing.T) {
	run := "986d3bfa-05da-4621-aeca-66f493a19ea3"
	parent := panelTestJob(42, `E:\repo`, "dashboard_cross", run, "synthesis")
	member := panelTestJob(43, `E:\repo`, "dashboard_cross", run, "member")
	show, _ := json.Marshal(map[string]any{"job_id": int64(43), "job": map[string]any{"id": int64(43), "repo_path": `E:\repo`, "panel_name": "dashboard_cross", "panel_run_uuid": run, "panel_role": "member"}, "review": map[string]any{"output": "heavy"}})
	service := &fakeService{panelRaw: panelJobsJSON(parent, member), showRaw: show}
	server := setupServer(t, service)
	identity, err := server.Registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	query := "?parentJobId=42&repositoryIdentity=" + url.QueryEscape(identity)
	request := strictBrowserReadRequest(http.MethodGet, "/api/panels/"+run+"/members/43"+query)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "repo_path") || !strings.Contains(recorder.Body.String(), `"output":"heavy"`) || service.showCalls != 1 || service.panelMembersCalls != 1 || service.metadataCalls != 0 {
		t.Fatalf("detail = %d PanelMembers=%d Show=%d JobMetadata=%d body=%s", recorder.Code, service.panelMembersCalls, service.showCalls, service.metadataCalls, recorder.Body.String())
	}

	service.showCalls = 0
	request = strictBrowserReadRequest(http.MethodGet, "/api/panels/"+run+"/members/999"+query)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound || service.showCalls != 0 {
		t.Fatalf("unknown member = %d calls=%d", recorder.Code, service.showCalls)
	}

	for _, field := range []string{"job_id", "nested_job_id", "repo_path", "panel_run_uuid", "panel_role"} {
		service.showCalls = 0
		var payload map[string]any
		if err := json.Unmarshal(show, &payload); err != nil {
			t.Fatal(err)
		}
		job := payload["job"].(map[string]any)
		switch field {
		case "job_id":
			payload["job_id"] = float64(44)
		case "nested_job_id":
			job["id"] = float64(44)
		case "repo_path":
			job[field] = `E:\other`
		case "panel_run_uuid":
			job[field] = "00000000-0000-0000-0000-000000000001"
		case "panel_role":
			job[field] = "synthesis"
		}
		service.showRaw, _ = json.Marshal(payload)
		request = strictBrowserReadRequest(http.MethodGet, "/api/panels/"+run+"/members/43"+query)
		recorder = httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusConflict || service.showCalls != 1 {
			t.Fatalf("show %s mismatch = %d calls=%d body=%s", field, recorder.Code, service.showCalls, recorder.Body.String())
		}
	}
	service.showCalls = 0
	service.showRaw = json.RawMessage(`not-json`)
	request = strictBrowserReadRequest(http.MethodGet, "/api/panels/"+run+"/members/43"+query)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway || service.showCalls != 1 {
		t.Fatalf("malformed show = %d calls=%d body=%s", recorder.Code, service.showCalls, recorder.Body.String())
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

func TestBootstrapKeepsReachableUnhealthyDaemonRunning(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"healthy":false}`))
	}))
	defer health.Close()
	client, err := roborev.NewClient("roborev", setupRunner{}, health.URL)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(client, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		RoboRev struct {
			Status struct {
				Running bool `json:"running"`
			} `json:"status"`
		} `json:"roborev"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.RoboRev.Status.Running {
		t.Fatalf("reachable unhealthy daemon was advertised as stopped: %s", recorder.Body.String())
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
