package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type trackingTestInspector struct {
	root                string
	inspectRootValue    string
	inspectRootErr      error
	registrationRootErr error
	dirty               bool
	inspectCalls        int
}

func (i *trackingTestInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	i.inspectCalls++
	return gitx.Repository{Path: path, Name: filepath.Base(path), Branch: "main", Head: strings.Repeat("a", 40), Dirty: i.dirty}, nil
}

func (i *trackingTestInspector) RegistrationRoot(_ context.Context, path string) (string, error) {
	if i.registrationRootErr != nil {
		return "", i.registrationRootErr
	}
	if i.root != "" {
		return i.root, nil
	}
	return path, nil
}

func (i *trackingTestInspector) InspectRoot(_ context.Context, path string) (string, error) {
	if i.inspectRootErr != nil {
		return "", i.inspectRootErr
	}
	if i.inspectRootValue != "" {
		return i.inspectRootValue, nil
	}
	return path, nil
}

type trackingTestTransport struct {
	mu            sync.Mutex
	preflights    []roborev.RepositoryTrackingPreflight
	preflightN    int
	registers     int
	registerRoot  string
	guardEvents   []string
	result        roborev.RepositoryTrackingResult
	registerErr   error
	onPreflight   func(int)
	preflightErrs []error
	events        []string
}

func (t *trackingTestTransport) RepositoryTrackingPreflight(_ context.Context) (roborev.RepositoryTrackingPreflight, error) {
	t.mu.Lock()
	index := t.preflightN
	t.preflightN++
	if len(t.preflights) == 0 {
		t.mu.Unlock()
		return roborev.RepositoryTrackingPreflight{}, context.Canceled
	}
	value := t.preflights[0]
	t.preflights = t.preflights[1:]
	if index == 0 {
		t.events = append(t.events, "PREVIEW_PREFLIGHT")
	} else if index == 1 {
		t.events = append(t.events, "EXECUTE_PREFLIGHT")
	} else if index == 2 {
		t.events = append(t.events, "DETACHED_ROOT_VERIFY", "DETACHED_BINDING_HEALTH", "DETACHED_INVENTORY")
	}
	hook := t.onPreflight
	var preflightErr error
	if len(t.preflightErrs) > 0 {
		preflightErr = t.preflightErrs[0]
		t.preflightErrs = t.preflightErrs[1:]
	}
	t.mu.Unlock()
	if hook != nil {
		hook(index)
	}
	if preflightErr != nil {
		return roborev.RepositoryTrackingPreflight{}, preflightErr
	}
	return value, nil
}

func (t *trackingTestTransport) RegisterRepository(_ context.Context, root string, _ roborev.RepositoryTrackingBinding, guard roborev.RepositoryTrackingPreLaunchGuard) (roborev.RepositoryTrackingResult, error) {
	if guard == nil {
		return roborev.RepositoryTrackingResult{}, context.Canceled
	}
	if err := guard(context.Background()); err != nil {
		return roborev.RepositoryTrackingResult{}, err
	}
	t.mu.Lock()
	result, resultErr := t.result, t.registerErr
	if result.Attempted {
		t.registers++
		t.registerRoot = root
		t.guardEvents = append(t.guardEvents, "FINAL_ROOT_GUARD", "REGISTER_POST")
		t.events = append(t.events, "FINAL_ROOT_GUARD", "REGISTER_POST")
	}
	t.mu.Unlock()
	return result, resultErr
}

type trackingTestService struct {
	fakeService
	transport *trackingTestTransport
}

func (s *trackingTestService) RepositoryTrackingPreflight(ctx context.Context) (roborev.RepositoryTrackingPreflight, error) {
	return s.transport.RepositoryTrackingPreflight(ctx)
}

func (s *trackingTestService) RegisterRepository(ctx context.Context, root string, binding roborev.RepositoryTrackingBinding, guard roborev.RepositoryTrackingPreLaunchGuard) (roborev.RepositoryTrackingResult, error) {
	return s.transport.RegisterRepository(ctx, root, binding, guard)
}

func trackingInventory(root string, tracked bool) repoinventory.Inventory {
	if !tracked {
		return repoinventory.Inventory{Repositories: []repoinventory.Repository{}, TotalCount: 0}
	}
	return repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "repo", RootPath: root, Count: 0}}, TotalCount: 0}
}

func trackingRequest(server *Server, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("X-CSRF-Token", server.csrfToken)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Sec-Fetch-Mode", "cors")
	r.Header.Set("Sec-Fetch-Dest", "empty")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, r)
	return response
}

func newTrackingTestServer(t *testing.T, transport *trackingTestTransport) (*Server, string, string) {
	t.Helper()
	root := t.TempDir()
	inspector := &trackingTestInspector{root: root}
	server, root, alias, _ := newTrackingTestServerWithInspector(t, transport, inspector, root)
	return server, root, alias
}

func newTrackingTestServerWithInspector(t *testing.T, transport *trackingTestTransport, inspector *trackingTestInspector, root string) (*Server, string, string, *trackingTestInspector) {
	t.Helper()
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(&trackingTestService{transport: transport}, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	return server, root, server.repositoryTrackingAlias(root), inspector
}

func trackingPreviewToken(t *testing.T, server *Server, alias string) string {
	t.Helper()
	preview := trackingRequest(server, "/api/repository-tracking/preview", `{"repositoryIdentity":"`+alias+`"}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var dto struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil || dto.Token == "" {
		t.Fatalf("preview dto=%s err=%v", preview.Body.String(), err)
	}
	return dto.Token
}

func trackingExecute(t *testing.T, server *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	return trackingRequest(server, "/api/repository-tracking/execute", `{"token":"`+token+`"}`)
}

func trackingBinding() roborev.RepositoryTrackingBinding {
	return roborev.RepositoryTrackingBinding{Version: "v0.64.0", Generation: 1, FileID: "fixture", DaemonID: "daemon-fixture"}
}

func TestRepositoryTrackingPreviewExecuteUsesOnePostAndDetachedReadback(t *testing.T) {
	transport := &trackingTestTransport{result: roborev.RepositoryTrackingResult{Attempted: true, StatusCode: http.StatusOK}}
	server, root, alias := newTrackingTestServer(t, transport)
	binding := roborev.RepositoryTrackingBinding{Version: "v0.64.0", Generation: 1, FileID: "fixture", DaemonID: "daemon-fixture"}
	transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: binding, Inventory: trackingInventory(root, false)}, {Binding: binding, Inventory: trackingInventory(root, false)}, {Binding: binding, Inventory: trackingInventory(root, true)}}
	preview := trackingRequest(server, "/api/repository-tracking/preview", `{"repositoryIdentity":"`+alias+`"}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var dto struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil || dto.Token == "" {
		t.Fatalf("preview dto=%s err=%v", preview.Body.String(), err)
	}
	execute := trackingRequest(server, "/api/repository-tracking/execute", `{"token":"`+dto.Token+`"}`)
	if execute.Code != http.StatusOK || !strings.Contains(execute.Body.String(), `"verified-success"`) {
		t.Fatalf("execute status=%d body=%s", execute.Code, execute.Body.String())
	}
	if transport.registers != 1 {
		t.Fatalf("register calls=%d", transport.registers)
	}
	if got := strings.Join(transport.guardEvents, ","); got != "FINAL_ROOT_GUARD,REGISTER_POST" {
		t.Fatalf("events=%s", got)
	}
	if got := strings.Join(transport.events, ","); got != "PREVIEW_PREFLIGHT,EXECUTE_PREFLIGHT,FINAL_ROOT_GUARD,REGISTER_POST,DETACHED_ROOT_VERIFY,DETACHED_BINDING_HEALTH,DETACHED_INVENTORY" {
		t.Fatalf("deterministic ordering=%s", got)
	}
}

func TestRepositoryTrackingLateRootDriftStopsPost(t *testing.T) {
	transport := &trackingTestTransport{}
	server, root, alias := newTrackingTestServer(t, transport)
	binding := roborev.RepositoryTrackingBinding{Version: "v0.64.0", Generation: 1, FileID: "fixture", DaemonID: "daemon-fixture"}
	transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: binding, Inventory: trackingInventory(root, false)}, {Binding: binding, Inventory: trackingInventory(root, false)}}
	transport.onPreflight = func(index int) {
		if index != 1 {
			return
		}
		moved := root + "-moved"
		if err := os.Rename(root, moved); err != nil {
			return
		}
		_ = os.Mkdir(root, 0o755)
	}
	preview := trackingRequest(server, "/api/repository-tracking/preview", `{"repositoryIdentity":"`+alias+`"}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var dto struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	execute := trackingRequest(server, "/api/repository-tracking/execute", `{"token":"`+dto.Token+`"}`)
	if execute.Code < 400 || transport.registers != 0 {
		t.Fatalf("execute status=%d body=%s register=%d", execute.Code, execute.Body.String(), transport.registers)
	}
}

func TestRepositoryTrackingRejectsBrowserAuthorityBeforeProbes(t *testing.T) {
	transport := &trackingTestTransport{}
	server, _, alias := newTrackingTestServer(t, transport)
	cases := []func(*http.Request){
		func(r *http.Request) { r.Header.Del("X-CSRF-Token") },
		func(r *http.Request) { r.Header.Add("X-CSRF-Token", server.csrfToken) },
		func(r *http.Request) { r.Header.Del("Sec-Fetch-Site") },
		func(r *http.Request) { r.Header.Add("Sec-Fetch-Site", "same-origin") },
		func(r *http.Request) { r.Header.Set("Origin", "https://cross-origin.invalid") },
		func(r *http.Request) { r.URL.RawQuery = "repositoryIdentity=private" },
	}
	for index, configure := range cases {
		r := httptest.NewRequest(http.MethodPost, "/api/repository-tracking/preview", strings.NewReader(`{"repositoryIdentity":"`+alias+`"}`))
		r.Header.Set("X-CSRF-Token", server.csrfToken)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("Sec-Fetch-Mode", "cors")
		r.Header.Set("Sec-Fetch-Dest", "empty")
		configure(r)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, r)
		if response.Code < 400 {
			t.Fatalf("case %d status=%d", index, response.Code)
		}
	}
	for index, body := range []string{`{}`, `{"repositoryIdentity":"` + alias + `","extra":"x"}`, `{"repositoryIdentity":"` + alias + `","repositoryIdentity":"` + alias + `"}`} {
		response := trackingRequest(server, "/api/repository-tracking/preview", body)
		if response.Code < 400 {
			t.Fatalf("body case %d status=%d", index, response.Code)
		}
	}
	if transport.preflightN != 0 {
		t.Fatalf("preflight calls=%d", transport.preflightN)
	}
}

func TestRepositoryTrackingRejectsInvalidAndNonSelectedAliasesBeforePreflight(t *testing.T) {
	t.Run("invalid alias", func(t *testing.T) {
		transport := &trackingTestTransport{}
		server, _, _ := newTrackingTestServer(t, transport)
		response := trackingRequest(server, "/api/repository-tracking/preview", `{"repositoryIdentity":"not-a-server-alias"}`)
		if response.Code < 400 || transport.preflightN != 0 {
			t.Fatalf("status=%d preflights=%d body=%s", response.Code, transport.preflightN, response.Body.String())
		}
	})

	t.Run("non-selected alias", func(t *testing.T) {
		transport := &trackingTestTransport{}
		first := t.TempDir()
		second := t.TempDir()
		inspector := &trackingTestInspector{root: first}
		registry := NewRegistry(inspector, "")
		if _, err := registry.Add(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.Add(context.Background(), second); err != nil {
			t.Fatal(err)
		}
		server, err := NewServer(&trackingTestService{transport: transport}, registry, "")
		if err != nil {
			t.Fatal(err)
		}
		response := trackingRequest(server, "/api/repository-tracking/preview", `{"repositoryIdentity":"`+server.repositoryTrackingAlias(second)+`"}`)
		if response.Code < 400 || transport.preflightN != 0 {
			t.Fatalf("status=%d preflights=%d body=%s", response.Code, transport.preflightN, response.Body.String())
		}
	})
}

func TestRepositoryTrackingExecuteRequiresExactTokenBodyBeforeClaim(t *testing.T) {
	transport := &trackingTestTransport{}
	server, _, _ := newTrackingTestServer(t, transport)
	for _, body := range []string{
		`{}`,
		`{"token":"token","extra":"PRIVATE_MARKER"}`,
		`{"token":"token","token":"token"}`,
		`{"token":"token","repo_path":"C:\\private\\secret"}`,
	} {
		response := trackingRequest(server, "/api/repository-tracking/execute", body)
		if response.Code < 400 || transport.preflightN != 0 || strings.Contains(response.Body.String(), "PRIVATE_") {
			t.Fatalf("body=%s status=%d preflights=%d response=%s", body, response.Code, transport.preflightN, response.Body.String())
		}
	}
}

func TestRepositoryTrackingAlreadyTrackedDoesNotIssueToken(t *testing.T) {
	transport := &trackingTestTransport{}
	server, root, alias := newTrackingTestServer(t, transport)
	binding := roborev.RepositoryTrackingBinding{Version: "v0.64.0", Generation: 1, FileID: "fixture", DaemonID: "daemon-fixture"}
	transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: binding, Inventory: trackingInventory(root, true)}}
	response := trackingRequest(server, "/api/repository-tracking/preview", `{"repositoryIdentity":"`+alias+`"}`)
	if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), `"token"`) || transport.registers != 0 {
		t.Fatalf("status=%d body=%s registers=%d", response.Code, response.Body.String(), transport.registers)
	}
}

func TestRepositoryTrackingPreviewFailsClosedAcrossEligibilityMatrix(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*trackingTestInspector, *trackingTestTransport, string)
	}{
		{
			name: "preflight error",
			configure: func(_ *trackingTestInspector, transport *trackingTestTransport, root string) {
				transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: trackingBinding(), Inventory: trackingInventory(root, false)}}
				transport.preflightErrs = []error{errors.New("PRIVATE_DAEMON_ERROR")}
			},
		},
		{
			name: "invalid binding",
			configure: func(_ *trackingTestInspector, transport *trackingTestTransport, root string) {
				binding := trackingBinding()
				binding.Version = "v0.63.0"
				transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: binding, Inventory: trackingInventory(root, false)}}
			},
		},
		{
			name: "duplicate equivalent roots",
			configure: func(_ *trackingTestInspector, transport *trackingTestTransport, root string) {
				inventory := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "one", RootPath: root}, {Name: "two", RootPath: root + string(filepath.Separator) + "."}}, TotalCount: 0}
				transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: trackingBinding(), Inventory: inventory}}
			},
		},
		{
			name: "typed malformed inventory",
			configure: func(_ *trackingTestInspector, transport *trackingTestTransport, root string) {
				inventory := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "repo", RootPath: root, Count: 0}}, TotalCount: 1}
				transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: trackingBinding(), Inventory: inventory}}
			},
		},
		{
			name: "unsafe root",
			configure: func(_ *trackingTestInspector, _ *trackingTestTransport, root string) {
				_ = os.RemoveAll(root)
			},
		},
		{
			name: "registration root unavailable",
			configure: func(inspector *trackingTestInspector, transport *trackingTestTransport, root string) {
				inspector.registrationRootErr = errors.New("PRIVATE_GIT_ROOT_ERROR")
				transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: trackingBinding(), Inventory: trackingInventory(root, false)}}
			},
		},
		{
			name: "selected root drift",
			configure: func(inspector *trackingTestInspector, transport *trackingTestTransport, root string) {
				inspector.inspectRootValue = root + "-different"
				transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: trackingBinding(), Inventory: trackingInventory(root, false)}}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &trackingTestTransport{}
			inspector := &trackingTestInspector{}
			root := t.TempDir()
			inspector.root = root
			server, _, alias, _ := newTrackingTestServerWithInspector(t, transport, inspector, root)
			test.configure(inspector, transport, root)
			response := trackingRequest(server, "/api/repository-tracking/preview", `{"repositoryIdentity":"`+alias+`"}`)
			if response.Code < 400 || strings.Contains(response.Body.String(), `"token"`) || strings.Contains(response.Body.String(), "PRIVATE_") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRepositoryTrackingPreviewAllowsDirtyRepository(t *testing.T) {
	transport := &trackingTestTransport{}
	inspector := &trackingTestInspector{dirty: true}
	root := t.TempDir()
	inspector.root = root
	server, _, alias, _ := newTrackingTestServerWithInspector(t, transport, inspector, root)
	richInspectCalls := inspector.inspectCalls
	transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: trackingBinding(), Inventory: trackingInventory(root, false)}}
	if token := trackingPreviewToken(t, server, alias); token == "" {
		t.Fatal("dirty repository did not receive preview token")
	}
	if inspector.inspectCalls != richInspectCalls {
		t.Fatalf("tracking preview performed rich dirty/HEAD/branch inspection: before=%d after=%d", richInspectCalls, inspector.inspectCalls)
	}
}

func TestRepositoryTrackingRegistersSelectedRootAndMatchesEffectiveInventory(t *testing.T) {
	selectedRoot := t.TempDir()
	effectiveRoot := t.TempDir()
	transport := &trackingTestTransport{result: roborev.RepositoryTrackingResult{Attempted: true, StatusCode: http.StatusOK}}
	registry := NewRegistry(&trackingTestInspector{root: effectiveRoot}, "")
	if _, err := registry.Add(context.Background(), selectedRoot); err != nil {
		t.Fatal(err)
	}
	service := &trackingTestService{transport: transport}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	alias := server.repositoryTrackingAlias(selectedRoot)
	binding := roborev.RepositoryTrackingBinding{Version: "v0.64.0", Generation: 1, FileID: "fixture", DaemonID: "daemon-fixture"}
	transport.preflights = []roborev.RepositoryTrackingPreflight{
		{Binding: binding, Inventory: trackingInventory(effectiveRoot, false)},
		{Binding: binding, Inventory: trackingInventory(effectiveRoot, false)},
		{Binding: binding, Inventory: trackingInventory(effectiveRoot, true)},
	}
	preview := trackingRequest(server, "/api/repository-tracking/preview", `{"repositoryIdentity":"`+alias+`"}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var dto struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if response := trackingRequest(server, "/api/repository-tracking/execute", `{"token":"`+dto.Token+`"}`); response.Code != http.StatusOK {
		t.Fatalf("execute status=%d body=%s", response.Code, response.Body.String())
	}
	if transport.registerRoot != selectedRoot {
		t.Fatalf("register root=%q, want selected root=%q", transport.registerRoot, selectedRoot)
	}
}

func TestRepositoryTrackingBindingDriftStopsPost(t *testing.T) {
	transport := &trackingTestTransport{}
	server, root, alias := newTrackingTestServer(t, transport)
	previewBinding := roborev.RepositoryTrackingBinding{Version: "v0.64.0", Generation: 1, FileID: "fixture", DaemonID: "daemon-a"}
	executeBinding := previewBinding
	executeBinding.DaemonID = "daemon-b"
	transport.preflights = []roborev.RepositoryTrackingPreflight{
		{Binding: previewBinding, Inventory: trackingInventory(root, false)},
		{Binding: executeBinding, Inventory: trackingInventory(root, false)},
	}
	preview := trackingRequest(server, "/api/repository-tracking/preview", `{"repositoryIdentity":"`+alias+`"}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var dto struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	execute := trackingRequest(server, "/api/repository-tracking/execute", `{"token":"`+dto.Token+`"}`)
	if execute.Code != http.StatusConflict || transport.registers != 0 {
		t.Fatalf("execute status=%d body=%s registers=%d", execute.Code, execute.Body.String(), transport.registers)
	}
}

func TestRepositoryTrackingExecuteConsumesTokenBeforeBusyAndRejectsReplay(t *testing.T) {
	transport := &trackingTestTransport{}
	server, root, alias := newTrackingTestServer(t, transport)
	transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: trackingBinding(), Inventory: trackingInventory(root, false)}}
	token := trackingPreviewToken(t, server, alias)
	release, err := server.Gate.AcquireFix()
	if err != nil {
		t.Fatal(err)
	}
	first := trackingExecute(t, server, token)
	release()
	if first.Code != http.StatusConflict || transport.preflightN != 1 || transport.registers != 0 {
		t.Fatalf("busy execute status=%d body=%s preflights=%d registers=%d", first.Code, first.Body.String(), transport.preflightN, transport.registers)
	}
	replay := trackingExecute(t, server, token)
	if replay.Code != http.StatusConflict || transport.preflightN != 1 || transport.registers != 0 {
		t.Fatalf("replay status=%d body=%s preflights=%d registers=%d", replay.Code, replay.Body.String(), transport.preflightN, transport.registers)
	}
}

func TestRepositoryTrackingExecuteAtomicallySerializesSelectedAndEffectiveRoots(t *testing.T) {
	selectedRoot := t.TempDir()
	effectiveRoot := t.TempDir()
	transport := &trackingTestTransport{}
	inspector := &trackingTestInspector{root: effectiveRoot}
	server, _, alias, _ := newTrackingTestServerWithInspector(t, transport, inspector, selectedRoot)
	transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: trackingBinding(), Inventory: trackingInventory(effectiveRoot, false)}}
	token := trackingPreviewToken(t, server, alias)
	release, err := server.Gate.AcquireRepository(gitx.RepositoryKey(effectiveRoot))
	if err != nil {
		t.Fatal(err)
	}
	response := trackingExecute(t, server, token)
	release()
	if response.Code != http.StatusConflict || transport.preflightN != 1 || transport.registers != 0 {
		t.Fatalf("repository-busy status=%d body=%s preflights=%d registers=%d", response.Code, response.Body.String(), transport.preflightN, transport.registers)
	}
	if replay := trackingExecute(t, server, token); replay.Code != http.StatusConflict {
		t.Fatalf("repository-busy token replay status=%d body=%s", replay.Code, replay.Body.String())
	}
}

func TestRepositoryTrackingExecuteClaimsBeforeTransportAvailabilityCheck(t *testing.T) {
	transport := &trackingTestTransport{}
	server, root, alias := newTrackingTestServer(t, transport)
	transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: trackingBinding(), Inventory: trackingInventory(root, false)}}
	token := trackingPreviewToken(t, server, alias)
	server.RepositoryTracking = nil
	first := trackingExecute(t, server, token)
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("availability status=%d body=%s", first.Code, first.Body.String())
	}
	replay := trackingExecute(t, server, token)
	if replay.Code != http.StatusConflict {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
}

func TestRepositoryTrackingExecuteRejectsFreshAuthorityDriftBeforePost(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Server, *trackingTestInspector, *trackingTestTransport, string)
	}{
		{
			name: "selected repository",
			configure: func(server *Server, _ *trackingTestInspector, _ *trackingTestTransport, root string) {
				other := filepath.Join(filepath.Dir(root), "other-selected")
				if err := os.Mkdir(other, 0o755); err != nil {
					return
				}
				if _, err := server.Registry.Add(context.Background(), other); err == nil {
					_ = server.Registry.Select(other)
				}
			},
		},
		{
			name: "selected Git root",
			configure: func(_ *Server, inspector *trackingTestInspector, _ *trackingTestTransport, root string) {
				inspector.inspectRootValue = root + "-drifted"
			},
		},
		{
			name: "effective registration root",
			configure: func(_ *Server, inspector *trackingTestInspector, _ *trackingTestTransport, _ string) {
				inspector.root = filepath.Join(filepath.Dir(inspector.root), "effective-drifted")
				_ = os.Mkdir(inspector.root, 0o755)
			},
		},
		{
			name: "already tracked",
			configure: func(_ *Server, _ *trackingTestInspector, transport *trackingTestTransport, root string) {
				transport.preflights = append(transport.preflights, roborev.RepositoryTrackingPreflight{Binding: trackingBinding(), Inventory: trackingInventory(root, true)})
			},
		},
		{
			name: "ambiguous inventory",
			configure: func(_ *Server, _ *trackingTestInspector, transport *trackingTestTransport, root string) {
				inventory := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "one", RootPath: root}, {Name: "two", RootPath: root + string(filepath.Separator) + "."}}, TotalCount: 0}
				transport.preflights = append(transport.preflights, roborev.RepositoryTrackingPreflight{Binding: trackingBinding(), Inventory: inventory})
			},
		},
		{
			name: "binding drift",
			configure: func(_ *Server, _ *trackingTestInspector, transport *trackingTestTransport, root string) {
				binding := trackingBinding()
				binding.DaemonID = "changed-daemon"
				transport.preflights = append(transport.preflights, roborev.RepositoryTrackingPreflight{Binding: binding, Inventory: trackingInventory(root, false)})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &trackingTestTransport{}
			inspector := &trackingTestInspector{}
			root := t.TempDir()
			inspector.root = root
			server, _, alias, _ := newTrackingTestServerWithInspector(t, transport, inspector, root)
			transport.preflights = []roborev.RepositoryTrackingPreflight{{Binding: trackingBinding(), Inventory: trackingInventory(root, false)}}
			token := trackingPreviewToken(t, server, alias)
			test.configure(server, inspector, transport, root)
			response := trackingExecute(t, server, token)
			if response.Code != http.StatusConflict || transport.registers != 0 {
				t.Fatalf("status=%d body=%s preflights=%d registers=%d", response.Code, response.Body.String(), transport.preflightN, transport.registers)
			}
		})
	}
}

func TestRepositoryTrackingExecuteReadbackAndHTTPOutcomeMatrix(t *testing.T) {
	tests := []struct {
		name          string
		statusCode    int
		tracked       bool
		inventory     repoinventory.Inventory
		preflightErrs []error
		registerErr   error
		wantStatus    string
		postDrift     bool
	}{
		{name: "200 tracked", statusCode: http.StatusOK, tracked: true, wantStatus: "verified-success"},
		{name: "200 missing", statusCode: http.StatusOK, tracked: false, wantStatus: "failed-unverified"},
		{name: "200 malformed readback", statusCode: http.StatusOK, inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{RootPath: "relative", Count: 0}}, TotalCount: 0}, wantStatus: "failed-unverified"},
		{name: "200 duplicate readback", statusCode: http.StatusOK, inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{RootPath: "PLACEHOLDER", Count: 0}, {RootPath: "PLACEHOLDER", Count: 0}}, TotalCount: 0}, wantStatus: "failed-unverified"},
		{name: "transport error tracked", statusCode: 0, tracked: true, registerErr: errors.New("PRIVATE_DAEMON_ERROR C:\\private\\secret"), wantStatus: "verified-success"},
		{name: "500 tracked", statusCode: http.StatusInternalServerError, tracked: true, registerErr: errors.New("PRIVATE_DAEMON_ERROR"), wantStatus: "verified-success"},
		{name: "600 tracked", statusCode: 600, tracked: true, registerErr: errors.New("PRIVATE_DAEMON_ERROR"), wantStatus: "failed-unverified"},
		{name: "700 tracked", statusCode: 700, tracked: true, registerErr: errors.New("PRIVATE_DAEMON_ERROR"), wantStatus: "failed-unverified"},
		{name: "307 tracked", statusCode: http.StatusTemporaryRedirect, tracked: true, registerErr: errors.New("PRIVATE_DAEMON_ERROR"), wantStatus: "failed-unverified"},
		{name: "400 tracked", statusCode: http.StatusBadRequest, tracked: true, registerErr: errors.New("PRIVATE_DAEMON_ERROR"), wantStatus: "failed-unverified"},
		{name: "transport error missing", statusCode: 0, tracked: false, registerErr: errors.New("PRIVATE_DAEMON_ERROR"), wantStatus: "failed-unverified"},
		{name: "post root drift", statusCode: http.StatusOK, tracked: true, postDrift: true, wantStatus: "failed-unverified"},
		{name: "readback transport error", statusCode: http.StatusOK, tracked: false, preflightErrs: []error{nil, nil, errors.New("PRIVATE_DAEMON_ERROR")}, wantStatus: "failed-unverified"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &trackingTestTransport{result: roborev.RepositoryTrackingResult{Attempted: true, StatusCode: test.statusCode}, registerErr: test.registerErr}
			inspector := &trackingTestInspector{}
			root := t.TempDir()
			inspector.root = root
			server, _, alias, _ := newTrackingTestServerWithInspector(t, transport, inspector, root)
			preflightInventory := trackingInventory(root, false)
			detachedInventory := test.inventory
			if detachedInventory.Repositories == nil && detachedInventory.TotalCount == 0 {
				detachedInventory = trackingInventory(root, test.tracked)
			}
			for index, repository := range detachedInventory.Repositories {
				if repository.RootPath == "PLACEHOLDER" {
					detachedInventory.Repositories[index].RootPath = root
				}
			}
			transport.preflights = []roborev.RepositoryTrackingPreflight{
				{Binding: trackingBinding(), Inventory: preflightInventory},
				{Binding: trackingBinding(), Inventory: preflightInventory},
				{Binding: trackingBinding(), Inventory: detachedInventory},
			}
			transport.preflightErrs = test.preflightErrs
			if test.postDrift {
				transport.onPreflight = func(index int) {
					if index != 2 {
						return
					}
					moved := root + "-moved"
					if err := os.Rename(root, moved); err == nil {
						_ = os.Mkdir(root, 0o755)
					}
				}
			}
			token := trackingPreviewToken(t, server, alias)
			response := trackingExecute(t, server, token)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"`+test.wantStatus+`"`) || strings.Contains(response.Body.String(), "PRIVATE_") || transport.registers != 1 {
				t.Fatalf("status=%d body=%s preflights=%d registers=%d", response.Code, response.Body.String(), transport.preflightN, transport.registers)
			}
			if test.postDrift && strings.Contains(response.Body.String(), `"verified-success"`) {
				t.Fatalf("post-root drift was credited: %s", response.Body.String())
			}
		})
	}
}

func TestRepositoryTrackingDTOAndErrorsArePathFreeAndAllowlisted(t *testing.T) {
	transport := &trackingTestTransport{result: roborev.RepositoryTrackingResult{Attempted: true, StatusCode: http.StatusOK}, registerErr: errors.New("PRIVATE_DAEMON_ERROR C:\\Users\\private\\source\\secret-project")}
	server, root, alias := newTrackingTestServer(t, transport)
	transport.preflights = []roborev.RepositoryTrackingPreflight{
		{Binding: trackingBinding(), Inventory: trackingInventory(root, false)},
		{Binding: trackingBinding(), Inventory: trackingInventory(root, false)},
		{Binding: trackingBinding(), Inventory: trackingInventory(root, true)},
	}
	preview := trackingRequest(server, "/api/repository-tracking/preview", `{"repositoryIdentity":"`+alias+`"}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewObject map[string]json.RawMessage
	if err := json.Unmarshal(preview.Body.Bytes(), &previewObject); err != nil {
		t.Fatal(err)
	}
	wantKeys := map[string]bool{"version": true, "repositoryIdentity": true, "action": true, "target": true, "summary": true, "token": true, "expiresAt": true}
	if len(previewObject) != len(wantKeys) {
		t.Fatalf("preview keys=%v", previewObject)
	}
	for key := range previewObject {
		if !wantKeys[key] {
			t.Fatalf("unexpected preview key %q", key)
		}
	}
	var previewDTO struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &previewDTO); err != nil {
		t.Fatal(err)
	}
	response := trackingExecute(t, server, previewDTO.Token)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"verified-success"`) {
		t.Fatalf("execute status=%d body=%s", response.Code, response.Body.String())
	}
	var resultObject map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &resultObject); err != nil {
		t.Fatal(err)
	}
	if len(resultObject) != 1 {
		t.Fatalf("result keys=%v", resultObject)
	}
	for _, marker := range []string{root, "PRIVATE_DAEMON_ERROR", "127.0.0.1:7373", "repo_path", "binary", "inventory"} {
		if strings.Contains(preview.Body.String(), marker) || strings.Contains(response.Body.String(), marker) {
			t.Fatalf("privacy marker %q leaked preview=%s execute=%s", marker, preview.Body.String(), response.Body.String())
		}
	}
}

func TestRepositoryTrackingAttemptedFalseNeverCreditsExternalTrackedState(t *testing.T) {
	transport := &trackingTestTransport{result: roborev.RepositoryTrackingResult{Attempted: false, StatusCode: http.StatusOK}}
	server, root, alias := newTrackingTestServer(t, transport)
	transport.preflights = []roborev.RepositoryTrackingPreflight{
		{Binding: trackingBinding(), Inventory: trackingInventory(root, false)},
		{Binding: trackingBinding(), Inventory: trackingInventory(root, false)},
	}
	token := trackingPreviewToken(t, server, alias)
	response := trackingExecute(t, server, token)
	if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), "verified-success") || transport.preflightN != 2 || transport.registers != 0 {
		t.Fatalf("status=%d body=%s preflights=%d registers=%d", response.Code, response.Body.String(), transport.preflightN, transport.registers)
	}
}
