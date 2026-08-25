package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type reviewExportInspector struct {
	repository gitx.Repository
	err        error
	calls      int
}

func (i *reviewExportInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	i.calls++
	if i.err != nil {
		return gitx.Repository{}, i.err
	}
	repository := i.repository
	if repository.Path == "" {
		repository.Path = path
	}
	return repository, nil
}

func reviewMetadataExportServer(t *testing.T, service roborev.Service, inspector *reviewExportInspector, root string) (*Server, string) {
	t.Helper()
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(root)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	// Registry.Add performs the initial inspection. Only requests below count
	// toward the authorization/fresh-root ordering assertions.
	inspector.calls = 0
	return server, identity
}

func reviewMetadataExportRequest(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	return request
}

func TestReviewMetadataExportRejectsBeforeRegistryOrUpstreamSideEffects(t *testing.T) {
	root := `E:\selected root`
	inspector := &reviewExportInspector{repository: gitx.Repository{Name: "selected", Path: root}}
	service := &fakeService{}
	server, identity := reviewMetadataExportServer(t, service, inspector, root)
	encodedIdentity := url.QueryEscape(identity)

	tests := []struct {
		name    string
		target  string
		headers bool
		want    int
	}{
		{name: "missing browser auth", target: "/api/exports/reviews?repositoryIdentity=" + encodedIdentity, want: http.StatusForbidden},
		{name: "missing identity", target: "/api/exports/reviews", headers: true, want: http.StatusBadRequest},
		{name: "blank identity", target: "/api/exports/reviews?repositoryIdentity=+", headers: true, want: http.StatusBadRequest},
		{name: "extra query", target: "/api/exports/reviews?repositoryIdentity=" + encodedIdentity + "&extra=1", headers: true, want: http.StatusBadRequest},
		{name: "duplicate identity", target: "/api/exports/reviews?repositoryIdentity=" + encodedIdentity + "&repositoryIdentity=other", headers: true, want: http.StatusBadRequest},
		{name: "malformed query", target: "/api/exports/reviews?repositoryIdentity=%", headers: true, want: http.StatusBadRequest},
		{name: "foreign identity", target: "/api/exports/reviews?repositoryIdentity=foreign", headers: true, want: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			beforeInspect := inspector.calls
			request := reviewMetadataExportRequest(test.target)
			if !test.headers {
				request.Header = nil
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
			if inspector.calls != beforeInspect {
				t.Fatalf("registry inspected before rejection: %d -> %d", beforeInspect, inspector.calls)
			}
			if strings.Contains(recorder.Header().Get("Content-Disposition"), "attachment") {
				t.Fatal("rejected request produced an attachment")
			}
		})
	}
}

func TestReviewMetadataExportFreshInspectionDriftFailsClosedWithoutUpstream(t *testing.T) {
	root := `E:\selected root`
	inspector := &reviewExportInspector{repository: gitx.Repository{Name: "selected", Path: root}}
	server, identity := reviewMetadataExportServer(t, &fakeService{}, inspector, root)
	// Make only the request-time fresh inspection fail.
	inspector.err = errors.New("root drift")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, reviewMetadataExportRequest("/api/exports/reviews?repositoryIdentity="+url.QueryEscape(identity)))
	if recorder.Code != http.StatusConflict || inspector.calls != 1 {
		t.Fatalf("status=%d inspect calls=%d body=%s", recorder.Code, inspector.calls, recorder.Body.String())
	}
	if strings.Contains(recorder.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("fresh inspection failure produced an attachment")
	}
}

type reviewExportCountingRunner struct {
	calls int32
}

func (r *reviewExportCountingRunner) Run(context.Context, process.Command) (process.Result, error) {
	atomic.AddInt32(&r.calls, 1)
	return process.Result{}, errors.New("unexpected process call")
}

type reviewExportRaceDaemon struct {
	server       *httptest.Server
	reposCalls   int32
	exportCalls  int32
	fallbackName bool
}

func newReviewExportRaceDaemon(fallbackName bool) *reviewExportRaceDaemon {
	daemon := &reviewExportRaceDaemon{fallbackName: fallbackName}
	daemon.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/repos":
			count := atomic.AddInt32(&daemon.reposCalls, 1)
			if daemon.fallbackName {
				if count == 1 {
					_, _ = io.WriteString(w, `{"repos":[{"name":"project-x","root_path":"E:/selected","identity":""}]}`)
				} else {
					_, _ = io.WriteString(w, `{"repos":[{"name":"project-x","root_path":"E:/selected","identity":""},{"name":"project-x","root_path":"E:/foreign","identity":""}]}`)
				}
				return
			}
			if count == 1 {
				_, _ = io.WriteString(w, `{"repos":[{"name":"selected","root_path":"E:/selected","identity":"shared-id"}]}`)
			} else {
				_, _ = io.WriteString(w, `{"repos":[{"name":"selected","root_path":"E:/selected","identity":"shared-id"},{"name":"foreign","root_path":"E:/foreign","identity":"shared-id"}]}`)
			}
		case "/api/export/reviews":
			atomic.AddInt32(&daemon.exportCalls, 1)
			if daemon.fallbackName {
				_, _ = io.WriteString(w, `{"reviews":[{"review_id":1,"repo":"project-x"},{"review_id":2,"repo":"project-x"}]}`)
			} else {
				_, _ = io.WriteString(w, `{"reviews":[{"review_id":1,"repo":"shared-id"},{"review_id":2,"repo":"shared-id"}]}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	return daemon
}

type reviewExportFixtureRepository struct {
	Name     string `json:"name"`
	RootPath string `json:"root_path"`
	Identity string `json:"identity"`
}

type reviewExportFixtureInventory struct {
	Repositories []reviewExportFixtureRepository `json:"repos"`
}

type reviewExportFixtureReview struct {
	ReviewID int64  `json:"review_id"`
	Repo     string `json:"repo"`
}

type reviewExportFixtureDocument struct {
	Reviews []reviewExportFixtureReview `json:"reviews"`
}

func fetchReviewExportFixture(t *testing.T, daemon *reviewExportRaceDaemon, path string) []byte {
	t.Helper()
	response, err := http.Get(daemon.server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("fixture %s status=%d", path, response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestReviewMetadataExportIdentityCollisionFixtureExposesTheUnsafeRace(t *testing.T) {
	daemon := newReviewExportRaceDaemon(false)
	defer daemon.server.Close()
	var inventory reviewExportFixtureInventory
	if err := json.Unmarshal(fetchReviewExportFixture(t, daemon, "/api/repos"), &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory.Repositories) != 1 || inventory.Repositories[0].RootPath != "E:/selected" || inventory.Repositories[0].Identity != "shared-id" {
		t.Fatalf("initial inventory=%#v, want selected A with unique shared-id", inventory)
	}
	var export reviewExportFixtureDocument
	if err := json.Unmarshal(fetchReviewExportFixture(t, daemon, "/api/export/reviews?repo=shared-id"), &export); err != nil {
		t.Fatal(err)
	}
	if len(export.Reviews) != 2 || export.Reviews[0].ReviewID != 1 || export.Reviews[1].ReviewID != 2 || export.Reviews[0].Repo != "shared-id" || export.Reviews[1].Repo != "shared-id" {
		t.Fatalf("collision export=%#v, want A+B rows sharing shared-id", export)
	}
	if got := atomic.LoadInt32(&daemon.reposCalls); got != 1 {
		t.Fatalf("fixture /api/repos calls=%d, want 1", got)
	}
	if got := atomic.LoadInt32(&daemon.exportCalls); got != 1 {
		t.Fatalf("fixture /api/export/reviews calls=%d, want 1", got)
	}
}

func TestReviewMetadataExportNameFallbackFixtureExposesTheUnsafeRace(t *testing.T) {
	daemon := newReviewExportRaceDaemon(true)
	defer daemon.server.Close()
	var inventory reviewExportFixtureInventory
	if err := json.Unmarshal(fetchReviewExportFixture(t, daemon, "/api/repos"), &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory.Repositories) != 1 || inventory.Repositories[0].RootPath != "E:/selected" || inventory.Repositories[0].Name != "project-x" || inventory.Repositories[0].Identity != "" {
		t.Fatalf("initial inventory=%#v, want selected A with empty identity and project-x name", inventory)
	}
	var export reviewExportFixtureDocument
	if err := json.Unmarshal(fetchReviewExportFixture(t, daemon, "/api/export/reviews?repo=project-x"), &export); err != nil {
		t.Fatal(err)
	}
	if len(export.Reviews) != 2 || export.Reviews[0].ReviewID != 1 || export.Reviews[1].ReviewID != 2 || export.Reviews[0].Repo != "project-x" || export.Reviews[1].Repo != "project-x" {
		t.Fatalf("fallback export=%#v, want A+B rows sharing project-x effective filter", export)
	}
	if got := atomic.LoadInt32(&daemon.reposCalls); got != 1 {
		t.Fatalf("fixture /api/repos calls=%d, want 1", got)
	}
	if got := atomic.LoadInt32(&daemon.exportCalls); got != 1 {
		t.Fatalf("fixture /api/export/reviews calls=%d, want 1", got)
	}
}

func TestReviewMetadataExportIdentityCollisionRaceIsBlockedBeforeDaemon(t *testing.T) {
	root := `E:\selected`
	daemon := newReviewExportRaceDaemon(false)
	defer daemon.server.Close()
	runner := &reviewExportCountingRunner{}
	client, err := roborev.NewClient("roborev", runner, daemon.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	inspector := &reviewExportInspector{repository: gitx.Repository{Name: "selected", Path: root}}
	server, identity := reviewMetadataExportServer(t, client, inspector, root)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, reviewMetadataExportRequest("/api/exports/reviews?repositoryIdentity="+url.QueryEscape(identity)))
	assertReviewMetadataExportDisabled(t, recorder)
	if got := atomic.LoadInt32(&daemon.reposCalls); got != 0 {
		t.Fatalf("race fixture /api/repos calls=%d, want 0", got)
	}
	if got := atomic.LoadInt32(&daemon.exportCalls); got != 0 {
		t.Fatalf("race fixture /api/export/reviews calls=%d, want 0", got)
	}
	if got := atomic.LoadInt32(&runner.calls); got != 0 {
		t.Fatalf("CLI/process calls=%d, want 0", got)
	}
}

func TestReviewMetadataExportNameFallbackRaceIsBlockedBeforeDaemon(t *testing.T) {
	root := `E:\selected`
	daemon := newReviewExportRaceDaemon(true)
	defer daemon.server.Close()
	runner := &reviewExportCountingRunner{}
	client, err := roborev.NewClient("roborev", runner, daemon.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	inspector := &reviewExportInspector{repository: gitx.Repository{Name: "project-x", Path: root}}
	server, identity := reviewMetadataExportServer(t, client, inspector, root)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, reviewMetadataExportRequest("/api/exports/reviews?repositoryIdentity="+url.QueryEscape(identity)))
	assertReviewMetadataExportDisabled(t, recorder)
	if got := atomic.LoadInt32(&daemon.reposCalls); got != 0 {
		t.Fatalf("fallback race fixture /api/repos calls=%d, want 0", got)
	}
	if got := atomic.LoadInt32(&daemon.exportCalls); got != 0 {
		t.Fatalf("fallback race fixture /api/export/reviews calls=%d, want 0", got)
	}
	if got := atomic.LoadInt32(&runner.calls); got != 0 {
		t.Fatalf("CLI/process calls=%d, want 0", got)
	}
}

func assertReviewMetadataExportDisabled(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusNotImplemented, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"phase":"review-export-repository-isolation-blocked"`) || !strings.Contains(recorder.Body.String(), "atomic canonical-root identity") {
		t.Fatalf("unsupported response=%s", recorder.Body.String())
	}
	if recorder.Header().Get("Content-Disposition") != "" || strings.Contains(recorder.Body.String(), "review_id") || strings.Contains(recorder.Body.String(), "foreign") {
		t.Fatalf("unsupported response exposed download content: headers=%v body=%s", recorder.Header(), recorder.Body.String())
	}
}
