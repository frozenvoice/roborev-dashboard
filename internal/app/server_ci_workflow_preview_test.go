package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/froze/roborev-dashboard/internal/ciworkflowpreview"
	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type ciPreviewHTTPGenerator struct {
	mu      sync.Mutex
	calls   int
	root    string
	output  string
	content []byte
	err     error
}

func (g *ciPreviewHTTPGenerator) GenerateCIWorkflow(ctx context.Context, root, output string, guard roborev.CIWorkflowPreLaunchGuard) error {
	g.mu.Lock()
	g.calls++
	g.root, g.output = root, output
	err := g.err
	content := append([]byte(nil), g.content...)
	g.mu.Unlock()
	if guard == nil {
		return errors.New("missing CI workflow pre-launch guard")
	}
	if err := guard(ctx); err != nil {
		return err
	}
	if err != nil {
		return err
	}
	return os.WriteFile(output, content, 0o600)
}

func newCIWorkflowHTTPServer(t *testing.T, content []byte) (*Server, *ciPreviewHTTPGenerator, string) {
	t.Helper()
	repo := t.TempDir()
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(&fakeService{}, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	generator := &ciPreviewHTTPGenerator{content: content}
	server.CIWorkflowPreview = ciworkflowpreview.NewService(generator)
	identity, err := registry.Identity("")
	if err != nil {
		t.Fatal(err)
	}
	return server, generator, identity
}

func ciWorkflowHTTPPost(server *Server, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func ciWorkflowIdentityBody(identity string) string {
	quoted, _ := json.Marshal(identity)
	return `{"repositoryIdentity":` + string(quoted) + `}`
}

func TestCIWorkflowPreviewEndpointReturnsExactWorkflowDTO(t *testing.T) {
	content := []byte("name: RoboRev\n# ${{ secrets.OPENAI_API_KEY }}\n")
	server, generator, identity := newCIWorkflowHTTPServer(t, content)
	recorder := ciWorkflowHTTPPost(server, "/api/ci-workflow/preview", ciWorkflowIdentityBody(identity))
	if recorder.Code != http.StatusOK || generator.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, generator.calls, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q", recorder.Header().Get("Cache-Control"))
	}
	wantHash := sha256.Sum256(content)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("DTO keys=%v", got)
	}
	var version, gotIdentity, target string
	if err := json.Unmarshal(got["version"], &version); err != nil || version != "v0.64.0" {
		t.Fatalf("version=%q err=%v", version, err)
	}
	if err := json.Unmarshal(got["repositoryIdentity"], &gotIdentity); err != nil || gotIdentity != identity {
		t.Fatalf("identity=%q err=%v", gotIdentity, err)
	}
	if err := json.Unmarshal(got["target"], &target); err != nil || target != ciworkflowpreview.WorkflowTarget {
		t.Fatalf("target=%q err=%v", target, err)
	}
	var workflow struct {
		Content string `json:"content"`
		SHA256  string `json:"sha256"`
	}
	if err := json.Unmarshal(got["workflow"], &workflow); err != nil || workflow.Content != string(content) || workflow.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("workflow=%#v err=%v", workflow, err)
	}
	if strings.Contains(recorder.Body.String(), generator.output) || strings.Contains(recorder.Body.String(), generator.root) || generator.root != identityPath(t, server, identity) || generator.output == generator.root || filepath.Dir(generator.output) == generator.root {
		t.Fatalf("private path leaked or output not isolated root=%q output=%q", generator.root, generator.output)
	}
}

func identityPath(t *testing.T, server *Server, identity string) string {
	t.Helper()
	path, err := server.Registry.ResolveIdentity(identity)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCIWorkflowPreviewAuthAndBodyRejectBeforeGenerator(t *testing.T) {
	server, generator, identity := newCIWorkflowHTTPServer(t, []byte("name: x\n"))
	tests := []struct {
		name  string
		path  string
		body  string
		setup func(*http.Request)
	}{
		{name: "missing auth", path: "/api/ci-workflow/preview", body: "not-json", setup: func(r *http.Request) {
			r.Header.Del("X-CSRF-Token")
			r.Header.Del("Sec-Fetch-Site")
			r.Header.Del("Sec-Fetch-Mode")
			r.Header.Del("Sec-Fetch-Dest")
		}},
		{name: "query", path: "/api/ci-workflow/preview?repositoryIdentity=evil", body: "not-json"},
		{name: "missing repository identity", path: "/api/ci-workflow/preview", body: `{}`},
		{name: "unknown opaque identity", path: "/api/ci-workflow/preview", body: ciWorkflowIdentityBody("unknown-identity")},
		{name: "duplicate body", path: "/api/ci-workflow/preview", body: ciWorkflowDuplicateBody(identity)},
		{name: "unknown field path", path: "/api/ci-workflow/preview", body: ciWorkflowExtraBody(identity, "path")},
		{name: "browser output field", path: "/api/ci-workflow/preview", body: ciWorkflowExtraBody(identity, "output")},
		{name: "agent field", path: "/api/ci-workflow/preview", body: ciWorkflowExtraBody(identity, "agent")},
		{name: "version field", path: "/api/ci-workflow/preview", body: ciWorkflowExtraBody(identity, "version")},
		{name: "force field", path: "/api/ci-workflow/preview", body: ciWorkflowExtraBody(identity, "force")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("X-CSRF-Token", server.csrfToken)
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			request.Header.Set("Sec-Fetch-Mode", "cors")
			request.Header.Set("Sec-Fetch-Dest", "empty")
			if test.setup != nil {
				test.setup(request)
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code < 400 || generator.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, generator.calls, recorder.Body.String())
			}
		})
	}
}

type ciWorkflowCountingInspector struct{ calls atomic.Int32 }

func (i *ciWorkflowCountingInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	i.calls.Add(1)
	return gitx.Repository{Path: path}, nil
}

func TestCIWorkflowPreviewAuthRejectsBeforeRegistryInspection(t *testing.T) {
	repo := t.TempDir()
	inspector := &ciWorkflowCountingInspector{}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity("")
	if err != nil {
		t.Fatal(err)
	}
	baseline := inspector.calls.Load()
	server, err := NewServer(&fakeService{}, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	generator := &ciPreviewHTTPGenerator{content: []byte("name: x\n")}
	server.CIWorkflowPreview = ciworkflowpreview.NewService(generator)
	for _, configure := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("X-CSRF-Token") },
		func(r *http.Request) { r.Header.Del("Sec-Fetch-Site") },
		func(r *http.Request) { r.Header.Add("X-CSRF-Token", server.csrfToken) },
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/ci-workflow/preview", strings.NewReader(ciWorkflowIdentityBody(identity)))
		request.Header.Set("X-CSRF-Token", server.csrfToken)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		request.Header.Set("Sec-Fetch-Mode", "cors")
		request.Header.Set("Sec-Fetch-Dest", "empty")
		configure(request)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code < 400 {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	if inspector.calls.Load() != baseline || generator.calls != 0 {
		t.Fatalf("inspector calls=%d baseline=%d generator calls=%d", inspector.calls.Load(), baseline, generator.calls)
	}
}

func TestCIWorkflowPreviewRejectsUnselectedIdentityWithoutGenerator(t *testing.T) {
	server, generator, selected := newCIWorkflowHTTPServer(t, []byte("name: x\n"))
	other := t.TempDir()
	if _, err := server.Registry.Add(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	otherIdentity, err := server.Registry.Identity(other)
	if err != nil {
		t.Fatal(err)
	}
	if otherIdentity == selected {
		t.Fatal("test identities unexpectedly match")
	}
	recorder := ciWorkflowHTTPPost(server, "/api/ci-workflow/preview", ciWorkflowIdentityBody(otherIdentity))
	if recorder.Code != http.StatusConflict || generator.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, generator.calls, recorder.Body.String())
	}
}

func TestCIWorkflowPreviewErrorDoesNotExposePrivateDetails(t *testing.T) {
	server, generator, identity := newCIWorkflowHTTPServer(t, []byte("name: x\n"))
	generator.err = errors.New(`private C:\secret-repo\stdout-marker STDERR_SECRET HOME=C:\Users\secret config=C:\Users\secret\.roborev\config.toml binary=C:\private\roborev.exe API_KEY=actual-secret`)
	recorder := ciWorkflowHTTPPost(server, "/api/ci-workflow/preview", ciWorkflowIdentityBody(identity))
	for _, marker := range []string{"private", "secret-repo", "stdout-marker", "STDERR_SECRET", "C:\\Users\\secret", "config.toml", "roborev.exe", "actual-secret"} {
		if strings.Contains(recorder.Body.String(), marker) {
			t.Fatalf("private marker %q leaked: %s", marker, recorder.Body.String())
		}
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func ciWorkflowDuplicateBody(identity string) string {
	quoted, _ := json.Marshal(identity)
	return `{"repositoryIdentity":` + string(quoted) + `,"repositoryIdentity":` + string(quoted) + `}`
}

func ciWorkflowExtraBody(identity, key string) string {
	quoted, _ := json.Marshal(identity)
	return `{"repositoryIdentity":` + string(quoted) + `,"` + key + `":"C:\\private"}`
}
