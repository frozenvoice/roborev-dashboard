package app

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"time"

	"github.com/froze/roborev-dashboard/internal/ciworkflowmanagement"
	"github.com/froze/roborev-dashboard/internal/ciworkflowpreview"
	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type ciManagementInspector struct {
	mu          sync.Mutex
	root        string
	state       gitx.CIWorkflowRepositoryState
	ciCalls     atomic.Int32
	onCIInspect func(int32)
}

func (i *ciManagementInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return gitx.Repository{Path: path, Branch: i.state.Branch, Head: i.state.Head, Dirty: i.state.Dirty}, nil
}

func (i *ciManagementInspector) InspectCIWorkflow(_ context.Context, path string) (gitx.CIWorkflowRepositoryState, error) {
	call := i.ciCalls.Add(1)
	i.mu.Lock()
	state := i.state
	if state.Root == "" {
		state.Root = path
	}
	hook := i.onCIInspect
	i.mu.Unlock()
	if hook != nil {
		hook(call)
		i.mu.Lock()
		state = i.state
		if state.Root == "" {
			state.Root = path
		}
		i.mu.Unlock()
	}
	return state, nil
}

func (i *ciManagementInspector) setState(state gitx.CIWorkflowRepositoryState) {
	i.mu.Lock()
	i.state = state
	i.mu.Unlock()
}

func newCIManagementHTTPServer(t *testing.T, content []byte) (*Server, *ciManagementInspector, string) {
	server, inspector, identity, _ := newCIManagementHTTPServerWithTransport(t, content)
	return server, inspector, identity
}

func newCIManagementHTTPServerWithTransport(t *testing.T, content []byte) (*Server, *ciManagementInspector, string, *ciManagementPreviewTransport) {
	t.Helper()
	root := t.TempDir()
	inspector := &ciManagementInspector{root: root, state: gitx.CIWorkflowRepositoryState{Root: root, Branch: "main", Head: strings.Repeat("a", 40)}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(&fakeService{}, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	transport := &ciManagementPreviewTransport{content: append([]byte(nil), content...)}
	server.CIWorkflowPreview = ciworkflowpreview.NewService(transport)
	identity, err := registry.Identity("")
	if err != nil {
		t.Fatal(err)
	}
	return server, inspector, identity, transport
}

// Keep the preview transport fake local to this test so execute assertions
// can count all RoboRev preview calls without depending on process execution.
type ciManagementPreviewTransport struct {
	content []byte
	calls   atomic.Int32
}

func (g *ciManagementPreviewTransport) GenerateCIWorkflow(ctx context.Context, root, output string, guard roborev.CIWorkflowPreLaunchGuard) error {
	if err := guard(ctx); err != nil {
		return err
	}
	g.calls.Add(1)
	return os.WriteFile(output, g.content, 0o600)
}

func ciworkflowpreviewForTest(content []byte) *ciworkflowpreview.Service {
	return ciworkflowpreview.NewService(&ciManagementPreviewTransport{content: append([]byte(nil), content...)})
}

func managementPost(server *Server, path, body string) *httptest.ResponseRecorder {
	return managementPostContext(server, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
}

func managementPostContext(server *Server, request *http.Request) *httptest.ResponseRecorder {
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

// ciManagementHookFS delegates every operation to the production OS safety
// implementation while exposing deterministic hooks at the app boundary.
// The hook is used only by cancellation and private-error tests.
type ciManagementHookFS struct {
	base       ciworkflowmanagement.OSFileSystem
	cancel     func()
	cancelOnce sync.Once
	createErr  error
}

func (f *ciManagementHookFS) Lstat(path string) (os.FileInfo, error) {
	return f.base.Lstat(path)
}
func (f *ciManagementHookFS) Open(path string) (pathsafety.File, error) { return f.base.Open(path) }
func (f *ciManagementHookFS) EvalSymlinks(path string) (string, error) {
	return f.base.EvalSymlinks(path)
}
func (f *ciManagementHookFS) Identity(path string, info os.FileInfo) (string, error) {
	return f.base.Identity(path, info)
}
func (f *ciManagementHookFS) IsReparse(path string, info os.FileInfo) bool {
	return f.base.IsReparse(path, info)
}
func (f *ciManagementHookFS) Mkdir(path string, mode os.FileMode) error {
	err := f.base.Mkdir(path, mode)
	if f.cancel != nil {
		f.cancelOnce.Do(f.cancel)
	}
	return err
}
func (f *ciManagementHookFS) CreateExclusive(path string, mode os.FileMode) (ciworkflowmanagement.File, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.base.CreateExclusive(path, mode)
}

func TestCIWorkflowManagementPreviewAndExecuteUseExactServerBytes(t *testing.T) {
	content := []byte("name: RoboRev\n# exact preview bytes\n")
	server, inspector, identity := newCIManagementHTTPServer(t, content)
	quoted, _ := json.Marshal(identity)
	preview := managementPost(server, "/api/ci-workflow/preview", `{"repositoryIdentity":`+string(quoted)+`}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var dto struct {
		Workflow struct {
			Content string `json:"content"`
		} `json:"workflow"`
		Write struct {
			Status    string `json:"status"`
			Token     string `json:"token"`
			ExpiresAt string `json:"expiresAt"`
		} `json:"write"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Write.Status != "available" || dto.Write.Token == "" || dto.Write.ExpiresAt == "" || dto.Workflow.Content != string(content) {
		t.Fatalf("preview dto=%#v", dto)
	}
	var writeBody map[string]any
	if err := json.Unmarshal([]byte(`{"token":"`+dto.Write.Token+`"}`), &writeBody); err != nil {
		t.Fatal(err)
	}
	created := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+dto.Write.Token+`"}`)
	if created.Code != http.StatusOK || created.Body.String() != `{"status":"verified-created"}`+"\n" {
		t.Fatalf("execute status=%d body=%q", created.Code, created.Body.String())
	}
	path := filepath.Join(inspector.root, ".github", "workflows", "roborev.yml")
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("target bytes=%q err=%v", got, err)
	}
}

func TestCIWorkflowManagementPreviewStatusAndTokenShapes(t *testing.T) {
	content := []byte("name: x\n")
	server, inspector, identity := newCIManagementHTTPServer(t, content)
	quoted, _ := json.Marshal(identity)
	preview := func() map[string]json.RawMessage {
		recorder := managementPost(server, "/api/ci-workflow/preview", `{"repositoryIdentity":`+string(quoted)+`}`)
		if recorder.Code != http.StatusOK {
			t.Fatalf("preview status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var dto map[string]json.RawMessage
		if err := json.Unmarshal(previewBody(recorder), &dto); err != nil {
			t.Fatal(err)
		}
		return dto
	}
	dto := preview()
	var write map[string]json.RawMessage
	if err := json.Unmarshal(dto["write"], &write); err != nil || len(write) != 3 {
		t.Fatalf("available write=%s err=%v", dto["write"], err)
	}
	inspector.setState(gitx.CIWorkflowRepositoryState{Root: inspector.root, Branch: "main", Head: strings.Repeat("a", 40), Dirty: true})
	dto = preview()
	if string(dto["write"]) != `{"status":"dirty"}` {
		t.Fatalf("dirty write=%s", dto["write"])
	}
	inspector.setState(gitx.CIWorkflowRepositoryState{Root: inspector.root, Branch: "main", Head: strings.Repeat("a", 40), Ignored: true, Dirty: true})
	dto = preview()
	if string(dto["write"]) != `{"status":"ignored"}` {
		t.Fatalf("ignored write=%s", dto["write"])
	}
	os.MkdirAll(filepath.Join(inspector.root, ".github", "workflows"), 0o755)
	if err := os.WriteFile(filepath.Join(inspector.root, ".github", "workflows", "roborev.yml"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	inspector.setState(gitx.CIWorkflowRepositoryState{Root: inspector.root, Branch: "main", Head: strings.Repeat("a", 40), Ignored: true, Dirty: true})
	dto = preview()
	if string(dto["write"]) != `{"status":"exists"}` {
		t.Fatalf("exists write=%s", dto["write"])
	}

	unsafeServer, unsafeInspector, unsafeIdentity := newCIManagementHTTPServer(t, content)
	if err := os.WriteFile(filepath.Join(unsafeInspector.root, ".github"), []byte("not-a-directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	unsafeInspector.setState(gitx.CIWorkflowRepositoryState{Root: unsafeInspector.root, Branch: "main", Head: strings.Repeat("a", 40), Ignored: true, Dirty: true})
	unsafeQuoted, _ := json.Marshal(unsafeIdentity)
	unsafePreview := managementPost(unsafeServer, "/api/ci-workflow/preview", `{"repositoryIdentity":`+string(unsafeQuoted)+`}`)
	var unsafeDTO struct {
		Write map[string]json.RawMessage `json:"write"`
	}
	if err := json.Unmarshal(unsafePreview.Body.Bytes(), &unsafeDTO); err != nil || string(unsafeDTO.Write["status"]) != `"unsafe"` {
		t.Fatalf("unsafe precedence status=%s err=%v", unsafeDTO.Write["status"], err)
	}
}

func TestCIWorkflowManagementExistingComparisonIsSanitizedAndTokenFree(t *testing.T) {
	content := []byte("name: RoboRev\n")
	for _, test := range []struct {
		name     string
		existing []byte
		want     string
	}{
		{name: "exact", existing: content, want: "exact-match"},
		{name: "different", existing: []byte("name: Other\n"), want: "different"},
		{name: "private marker", existing: []byte("name: RoboRev\nEXISTING_PRIVATE_WORKFLOW_MARKER_DO_NOT_EXPOSE\n"), want: "different"},
		{name: "over bound", existing: append([]byte("name: RoboRev\n"), bytes.Repeat([]byte("x"), int(ciworkflowmanagement.MaxBytes+1))...), want: "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, inspector, identity, transport := newCIManagementHTTPServerWithTransport(t, content)
			target := filepath.Join(inspector.root, ".github", "workflows", "roborev.yml")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, test.existing, 0o644); err != nil {
				t.Fatal(err)
			}
			quoted, _ := json.Marshal(identity)
			recorder := managementPost(server, "/api/ci-workflow/preview", `{"repositoryIdentity":`+string(quoted)+`}`)
			if recorder.Code != http.StatusOK {
				t.Fatalf("preview status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if transport.calls.Load() != 1 {
				t.Fatalf("generation calls=%d", transport.calls.Load())
			}
			var dto struct {
				Workflow struct {
					Content string `json:"content"`
				} `json:"workflow"`
				Write struct {
					Status string `json:"status"`
					Token  string `json:"token"`
				} `json:"write"`
				Existing *struct {
					Comparison string `json:"comparison"`
				} `json:"existing"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &dto); err != nil {
				t.Fatal(err)
			}
			if dto.Write.Status != "exists" || dto.Write.Token != "" || dto.Existing == nil || dto.Existing.Comparison != test.want {
				t.Fatalf("dto=%#v", dto)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			var existingRaw map[string]json.RawMessage
			if err := json.Unmarshal(raw["existing"], &existingRaw); err != nil || len(existingRaw) != 1 {
				t.Fatalf("existing shape=%s err=%v", raw["existing"], err)
			}
			if dto.Workflow.Content != string(content) || strings.Contains(recorder.Body.String(), "EXISTING_PRIVATE_WORKFLOW_MARKER_DO_NOT_EXPOSE") || strings.Contains(recorder.Body.String(), inspector.root) {
				t.Fatalf("private content/path leaked: %s", recorder.Body.String())
			}
		})
	}
}

func previewBody(recorder *httptest.ResponseRecorder) []byte { return recorder.Body.Bytes() }

func TestCIWorkflowManagementRejectsReplayAndDoesNotUseRoboRev(t *testing.T) {
	content := []byte("name: x\n")
	server, inspector, identity, transport := newCIManagementHTTPServerWithTransport(t, content)
	quoted, _ := json.Marshal(identity)
	preview := managementPost(server, "/api/ci-workflow/preview", `{"repositoryIdentity":`+string(quoted)+`}`)
	var dto struct {
		Write struct {
			Token string `json:"token"`
		} `json:"write"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil || dto.Write.Token == "" {
		t.Fatalf("preview=%s err=%v", preview.Body.String(), err)
	}
	if got := transport.calls.Load(); got != 1 {
		t.Fatalf("preview transport calls=%d want=1", got)
	}
	first := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+dto.Write.Token+`"}`)
	second := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+dto.Write.Token+`"}`)
	if first.Code != http.StatusOK || second.Code != http.StatusConflict {
		t.Fatalf("first=%d second=%d", first.Code, second.Code)
	}
	if _, err := os.Stat(filepath.Join(inspector.root, ".github", "workflows", "roborev.yml")); err != nil {
		t.Fatalf("first execute did not create target: %v", err)
	}
	if got := transport.calls.Load(); got != 1 {
		t.Fatalf("execute invoked preview transport calls=%d", got)
	}
}

func ciManagementPreviewToken(t *testing.T, server *Server, identity string) string {
	t.Helper()
	quoted, _ := json.Marshal(identity)
	preview := managementPost(server, "/api/ci-workflow/preview", `{"repositoryIdentity":`+string(quoted)+`}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var dto struct {
		Write struct {
			Status string `json:"status"`
			Token  string `json:"token"`
		} `json:"write"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Write.Status != "available" || dto.Write.Token == "" {
		t.Fatalf("write=%#v", dto.Write)
	}
	return dto.Write.Token
}

func ciManagementTarget(root string) string {
	return filepath.Join(root, ".github", "workflows", "roborev.yml")
}

func TestCIWorkflowManagementExecuteRejectsRequestBeforeClaim(t *testing.T) {
	server, _, identity := newCIManagementHTTPServer(t, []byte("name: x\n"))
	token := ciManagementPreviewToken(t, server, identity)
	paths := []struct {
		name string
		path string
		body string
	}{
		{name: "query", path: "/api/ci-workflow/execute?token=hidden", body: `{"token":"` + token + `"}`},
		{name: "extra", path: "/api/ci-workflow/execute", body: `{"token":"` + token + `","content":"private"}`},
		{name: "duplicate", path: "/api/ci-workflow/execute", body: `{"token":"` + token + `","token":"` + token + `"}`},
		{name: "trailing", path: "/api/ci-workflow/execute", body: `{"token":"` + token + `"}{}`},
		{name: "missing", path: "/api/ci-workflow/execute", body: `{}`},
		{name: "oversize", path: "/api/ci-workflow/execute", body: strings.Repeat("x", 1<<20)},
	}
	for _, test := range paths {
		t.Run(test.name, func(t *testing.T) {
			recorder := managementPost(server, test.path, test.body)
			if recorder.Code < 400 || strings.Contains(recorder.Body.String(), token) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	// Body validation happens before the atomic claim: the still-valid token
	// can be used once after each malformed request.
	if got := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+token+`"}`).Code; got != http.StatusOK {
		t.Fatalf("valid execute after rejected bodies=%d", got)
	}
}

func TestCIWorkflowManagementStrictAuthPrecedesBodyAndTargetMutation(t *testing.T) {
	server, inspector, identity := newCIManagementHTTPServer(t, []byte("name: x\n"))
	token := ciManagementPreviewToken(t, server, identity)
	request := httptest.NewRequest(http.MethodPost, "/api/ci-workflow/execute", strings.NewReader("not-json"))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	// Deliberately omit Fetch Metadata. Strict authorization must reject the
	// request before body decoding and before any filesystem or token effects.
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || strings.Contains(recorder.Body.String(), "not-json") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+token+`"}`).Code; got != http.StatusOK {
		t.Fatalf("token was consumed before auth: status=%d", got)
	}
	if _, err := os.Stat(ciManagementTarget(inspector.root)); err != nil {
		t.Fatalf("target was not created after valid request: %v", err)
	}
}

func TestCIWorkflowManagementBusyAndStaleConsumeTokenWithoutWrite(t *testing.T) {
	staleCases := []struct {
		name   string
		mutate func(*ciManagementInspector)
	}{
		{name: "head", mutate: func(i *ciManagementInspector) {
			i.setState(gitx.CIWorkflowRepositoryState{Root: i.root, Branch: "main", Head: strings.Repeat("b", 40)})
		}},
		{name: "branch", mutate: func(i *ciManagementInspector) {
			i.setState(gitx.CIWorkflowRepositoryState{Root: i.root, Branch: "release", Head: strings.Repeat("a", 40)})
		}},
		{name: "dirty", mutate: func(i *ciManagementInspector) {
			i.setState(gitx.CIWorkflowRepositoryState{Root: i.root, Branch: "main", Head: strings.Repeat("a", 40), Dirty: true})
		}},
		{name: "ignored", mutate: func(i *ciManagementInspector) {
			i.setState(gitx.CIWorkflowRepositoryState{Root: i.root, Branch: "main", Head: strings.Repeat("a", 40), Ignored: true})
		}},
		{name: "parent drift", mutate: func(i *ciManagementInspector) { _ = os.MkdirAll(filepath.Join(i.root, ".github"), 0o755) }},
		{name: "root drift", mutate: func(i *ciManagementInspector) {
			i.setState(gitx.CIWorkflowRepositoryState{Root: filepath.Dir(i.root), Branch: "main", Head: strings.Repeat("a", 40)})
		}},
	}
	for _, test := range staleCases {
		t.Run(test.name, func(t *testing.T) {
			server, inspector, identity := newCIManagementHTTPServer(t, []byte("name: x\n"))
			token := ciManagementPreviewToken(t, server, identity)
			test.mutate(inspector)
			recorder := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+token+`"}`)
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if _, err := os.Stat(ciManagementTarget(inspector.root)); !os.IsNotExist(err) {
				t.Fatalf("stale execute wrote target: err=%v", err)
			}
			if got := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+token+`"}`).Code; got != http.StatusConflict {
				t.Fatalf("stale token replay status=%d", got)
			}
		})
	}

	server, inspector, identity := newCIManagementHTTPServer(t, []byte("name: x\n"))
	token := ciManagementPreviewToken(t, server, identity)
	release, err := server.Gate.AcquireFix()
	if err != nil {
		t.Fatal(err)
	}
	busy := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+token+`"}`)
	release()
	if busy.Code != http.StatusConflict || !strings.Contains(busy.Body.String(), "operation") {
		t.Fatalf("busy status=%d body=%s", busy.Code, busy.Body.String())
	}
	if got := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+token+`"}`).Code; got != http.StatusConflict {
		t.Fatalf("busy token replay status=%d", got)
	}
	if _, err := os.Stat(ciManagementTarget(inspector.root)); !os.IsNotExist(err) {
		t.Fatalf("busy execute wrote target: err=%v", err)
	}
}

func TestCIWorkflowManagementInvalidExpiredAndTargetRaceNeverWrite(t *testing.T) {
	server, inspector, identity := newCIManagementHTTPServer(t, []byte("name: x\n"))
	invalid := managementPost(server, "/api/ci-workflow/execute", `{"token":"not-a-preview-token"}`)
	if invalid.Code != http.StatusConflict || !os.IsNotExist(statErr(ciManagementTarget(inspector.root))) {
		t.Fatalf("invalid status=%d target=%v body=%s", invalid.Code, statErr(ciManagementTarget(inspector.root)), invalid.Body.String())
	}
	expired := ciManagementPreviewToken(t, server, identity)
	server.ciWorkflowWrites.mu.Lock()
	record := server.ciWorkflowWrites.pending[expired]
	record.Expires = time.Now().Add(-time.Nanosecond)
	server.ciWorkflowWrites.pending[expired] = record
	server.ciWorkflowWrites.mu.Unlock()
	response := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+expired+`"}`)
	if response.Code != http.StatusConflict || !os.IsNotExist(statErr(ciManagementTarget(inspector.root))) {
		t.Fatalf("expired status=%d target=%v body=%s", response.Code, statErr(ciManagementTarget(inspector.root)), response.Body.String())
	}

	traced := ciManagementPreviewToken(t, server, identity)
	if err := os.MkdirAll(filepath.Dir(ciManagementTarget(inspector.root)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ciManagementTarget(inspector.root), []byte("racer"), 0o644); err != nil {
		t.Fatal(err)
	}
	race := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+traced+`"}`)
	if race.Code != http.StatusConflict || string(mustReadFile(t, ciManagementTarget(inspector.root))) != "racer" {
		t.Fatalf("target race status=%d body=%s target=%q", race.Code, race.Body.String(), mustReadFile(t, ciManagementTarget(inspector.root)))
	}
	if got := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+traced+`"}`).Code; got != http.StatusConflict {
		t.Fatalf("target-race token replay status=%d", got)
	}
}

func TestCIWorkflowManagementSelectedSwitchAndRepositoryBusyConsumeToken(t *testing.T) {
	server, inspector, identity := newCIManagementHTTPServer(t, []byte("name: x\n"))
	secondRoot := t.TempDir()
	secondState := gitx.CIWorkflowRepositoryState{Root: secondRoot, Branch: "main", Head: strings.Repeat("b", 40)}
	// Add a second identity through the same inspector; selection is switched
	// after preview, so the claimed token cannot authorize the other checkout.
	inspector.setState(secondState)
	if _, err := server.Registry.Add(context.Background(), secondRoot); err != nil {
		t.Fatal(err)
	}
	inspector.setState(gitx.CIWorkflowRepositoryState{Root: inspector.root, Branch: "main", Head: strings.Repeat("a", 40)})
	// Restore selection to the original repo for preview, then switch before
	// execute. This avoids asking the preview endpoint to issue a token for a
	// non-selected repository.
	if err := server.Registry.Select(inspector.root); err != nil {
		t.Fatal(err)
	}
	token := ciManagementPreviewToken(t, server, identity)
	if err := server.Registry.Select(secondRoot); err != nil {
		t.Fatal(err)
	}
	switched := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+token+`"}`)
	if switched.Code != http.StatusConflict || !os.IsNotExist(statErr(ciManagementTarget(inspector.root))) {
		t.Fatalf("switch status=%d target=%v body=%s", switched.Code, statErr(ciManagementTarget(inspector.root)), switched.Body.String())
	}
	if got := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+token+`"}`).Code; got != http.StatusConflict {
		t.Fatalf("switch token replay status=%d", got)
	}

	if err := server.Registry.Select(inspector.root); err != nil {
		t.Fatal(err)
	}
	busyToken := ciManagementPreviewToken(t, server, identity)
	release, err := server.Gate.AcquireRepository(gitx.RepositoryKey(inspector.root))
	if err != nil {
		t.Fatal(err)
	}
	busy := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+busyToken+`"}`)
	release()
	if busy.Code != http.StatusConflict || !os.IsNotExist(statErr(ciManagementTarget(inspector.root))) {
		t.Fatalf("repository busy status=%d target=%v body=%s", busy.Code, statErr(ciManagementTarget(inspector.root)), busy.Body.String())
	}
	if got := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+busyToken+`"}`).Code; got != http.StatusConflict {
		t.Fatalf("repository busy token replay status=%d", got)
	}
}

func statErr(path string) error {
	_, err := os.Stat(path)
	return err
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCIWorkflowWriteStoreCopiesBytesExpiresAndClaimsOnce(t *testing.T) {
	now := time.Unix(100, 0)
	clock := now
	store := newCIWorkflowWriteStoreWith(&incrementingReader{}, func() time.Time { return clock })
	content := []byte("exact")
	record := ciWorkflowWriteRecord{Content: content, Expires: clock.Add(ciWorkflowWriteTTL)}
	token, err := store.put(record)
	if err != nil {
		t.Fatal(err)
	}
	content[0] = 'X'
	claimed, ok := store.claim(token)
	if !ok || string(claimed.Content) != "exact" {
		t.Fatalf("claim=%#v ok=%v", claimed, ok)
	}
	if _, ok := store.claim(token); ok {
		t.Fatal("token replay succeeded")
	}
	clock = clock.Add(ciWorkflowWriteTTL)
	token, err = store.put(record)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Nanosecond)
	if _, ok := store.claim(token); ok {
		t.Fatal("expired token claimed")
	}
}

type incrementingReader struct{ next byte }

func (r *incrementingReader) Read(p []byte) (int, error) {
	for index := range p {
		p[index] = r.next
		r.next++
	}
	return len(p), nil
}

type uniqueTokenReader struct{ calls uint64 }

func (r *uniqueTokenReader) Read(p []byte) (int, error) {
	value := r.calls
	r.calls++
	for index := range p {
		p[index] = byte(value >> (8 * (index % 8)))
	}
	return len(p), nil
}

func TestCIWorkflowWriteStoreTokenShapeCapacityAndConcurrentClaim(t *testing.T) {
	clock := time.Unix(500, 0)
	store := newCIWorkflowWriteStoreWith(&uniqueTokenReader{}, func() time.Time { return clock })
	firstTokens := make([]string, 0, ciWorkflowWriteMax)
	for index := 0; index < ciWorkflowWriteMax; index++ {
		token, err := store.put(ciWorkflowWriteRecord{Content: []byte("x"), Expires: clock.Add(ciWorkflowWriteTTL)})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || len(decoded) != 32 {
			t.Fatalf("token=%q decoded=%d err=%v", token, len(decoded), err)
		}
		firstTokens = append(firstTokens, token)
	}
	if len(store.pending) != ciWorkflowWriteMax {
		t.Fatalf("pending=%d want=%d", len(store.pending), ciWorkflowWriteMax)
	}
	// Distinguish the first record by expiry so capacity eviction is
	// deterministic even though the backing map is unordered.
	store.pending[firstTokens[0]] = ciWorkflowWriteRecord{Content: []byte("old"), Expires: clock.Add(time.Minute)}
	newToken, err := store.put(ciWorkflowWriteRecord{Content: []byte("new"), Expires: clock.Add(ciWorkflowWriteTTL)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.pending[firstTokens[0]]; ok {
		t.Fatal("oldest live token was not evicted")
	}
	if _, ok := store.pending[newToken]; !ok {
		t.Fatal("new token missing after capacity insertion")
	}

	claimToken, err := store.put(ciWorkflowWriteRecord{Content: []byte("claim"), Expires: clock.Add(ciWorkflowWriteTTL)})
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for index := 0; index < 32; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := store.claim(claimToken); ok {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := winners.Load(); got != 1 {
		t.Fatalf("concurrent claim winners=%d", got)
	}
}

func TestCIWorkflowWriteStoreExpiryIsExactBoundary(t *testing.T) {
	clock := time.Unix(600, 0)
	store := newCIWorkflowWriteStoreWith(&uniqueTokenReader{}, func() time.Time { return clock })
	token, err := store.put(ciWorkflowWriteRecord{Content: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.pending[token].Expires; !got.Equal(clock.Add(ciWorkflowWriteTTL)) {
		t.Fatalf("default expiry=%v want=%v", got, clock.Add(ciWorkflowWriteTTL))
	}
	clock = clock.Add(ciWorkflowWriteTTL - time.Nanosecond)
	if _, ok := store.claim(token); !ok {
		t.Fatal("token expired before exact boundary")
	}
	token, err = store.put(ciWorkflowWriteRecord{Content: []byte("x"), Expires: clock.Add(time.Nanosecond)})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Nanosecond)
	if _, ok := store.claim(token); ok {
		t.Fatal("token remained valid at exact expiry boundary")
	}
}

func TestCIWorkflowManagementCancellationBeforeAndDuringMutation(t *testing.T) {
	server, inspector, identity := newCIManagementHTTPServer(t, []byte("name: cancel\n"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	token := ciManagementPreviewToken(t, server, identity)
	request := httptest.NewRequest(http.MethodPost, "/api/ci-workflow/execute", strings.NewReader(`{"token":"`+token+`"}`)).WithContext(ctx)
	recorder := managementPostContext(server, request)
	if recorder.Code != http.StatusRequestTimeout || !os.IsNotExist(statErr(ciManagementTarget(inspector.root))) {
		t.Fatalf("pre-cancel status=%d target=%v body=%s", recorder.Code, statErr(ciManagementTarget(inspector.root)), recorder.Body.String())
	}

	server, inspector, identity = newCIManagementHTTPServer(t, []byte("name: cancel-after-start\n"))
	requestContext, cancelRequest := context.WithCancel(context.Background())
	hookedFS := &ciManagementHookFS{cancel: cancelRequest}
	server.CIWorkflowManagement = ciworkflowmanagement.New(hookedFS)
	token = ciManagementPreviewToken(t, server, identity)
	request = httptest.NewRequest(http.MethodPost, "/api/ci-workflow/execute", strings.NewReader(`{"token":"`+token+`"}`)).WithContext(requestContext)
	recorder = managementPostContext(server, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"status":"verified-created"}`+"\n" {
		t.Fatalf("post-start cancellation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := string(mustReadFile(t, ciManagementTarget(inspector.root))); got != "name: cancel-after-start\n" {
		t.Fatalf("post-start bytes=%q", got)
	}
}

func TestCIWorkflowManagementPostGitDriftFailsWhileLeasesRemainHeld(t *testing.T) {
	server, inspector, identity := newCIManagementHTTPServer(t, []byte("name: drift\n"))
	token := ciManagementPreviewToken(t, server, identity)
	var fixBusy, repoBusy atomic.Bool
	inspector.onCIInspect = func(call int32) {
		if call != 3 {
			return
		}
		if release, err := server.Gate.AcquireFix(); err != nil {
			fixBusy.Store(true)
		} else {
			release()
		}
		if release, err := server.Gate.AcquireRepository(gitx.RepositoryKey(inspector.root)); err != nil {
			repoBusy.Store(true)
		} else {
			release()
		}
		inspector.setState(gitx.CIWorkflowRepositoryState{Root: inspector.root, Branch: "release", Head: strings.Repeat("b", 40)})
	}
	recorder := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+token+`"}`)
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"status":"failed-unverified"}`+"\n" {
		t.Fatalf("post drift status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !fixBusy.Load() || !repoBusy.Load() {
		t.Fatalf("post-Git inspection did not observe held leases: fix=%v repo=%v", fixBusy.Load(), repoBusy.Load())
	}
	if strings.Contains(recorder.Body.String(), inspector.root) || strings.Contains(recorder.Body.String(), token) {
		t.Fatalf("private detail leaked: %s", recorder.Body.String())
	}
	if _, err := os.Stat(ciManagementTarget(inspector.root)); err != nil {
		t.Fatalf("target missing after failed verification: %v", err)
	}
}

func TestCIWorkflowManagementFilesystemPrivateErrorIsSanitized(t *testing.T) {
	server, inspector, identity := newCIManagementHTTPServer(t, []byte("name: private\n"))
	privateErr := errors.New(`private C:\Users\private\repo secret-token`)
	failingFS := &ciManagementHookFS{createErr: privateErr}
	server.CIWorkflowManagement = ciworkflowmanagement.New(failingFS)
	token := ciManagementPreviewToken(t, server, identity)
	recorder := managementPost(server, "/api/ci-workflow/execute", `{"token":"`+token+`"}`)
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"status":"failed-unverified"}`+"\n" {
		t.Fatalf("private error status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, marker := range []string{"private", "secret-token", "Users", token, inspector.root} {
		if strings.Contains(recorder.Body.String(), marker) {
			t.Fatalf("private marker %q leaked: %s", marker, recorder.Body.String())
		}
	}
}
