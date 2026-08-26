package app

import (
	"bytes"
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
	"time"

	"github.com/froze/roborev-dashboard/internal/agenthookmanagement"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type appHookInfo struct {
	name string
	size int64
	mode os.FileMode
	mod  time.Time
}

func (i appHookInfo) Name() string       { return i.name }
func (i appHookInfo) Size() int64        { return i.size }
func (i appHookInfo) Mode() os.FileMode  { return i.mode }
func (i appHookInfo) ModTime() time.Time { return i.mod }
func (i appHookInfo) IsDir() bool        { return i.mode.IsDir() }
func (i appHookInfo) Sys() any           { return nil }

type appHookFile struct {
	*bytes.Reader
	info     appHookInfo
	identity string
}

func (f *appHookFile) Close() error               { return nil }
func (f *appHookFile) Stat() (os.FileInfo, error) { return f.info, nil }
func (f *appHookFile) Identity() (string, error)  { return f.identity, nil }

type appHookEntry struct {
	info     appHookInfo
	data     []byte
	identity string
	reparse  bool
}

type appHookFS struct {
	mu      sync.Mutex
	entries map[string]appHookEntry
}

func newAppHookFS(path string) *appHookFS {
	fs := &appHookFS{entries: map[string]appHookEntry{}}
	parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	current := parts[0]
	if strings.HasSuffix(current, ":") {
		current += string(filepath.Separator)
	}
	fs.entries[current] = appHookEntry{info: appHookInfo{name: filepath.Base(current), mode: os.ModeDir, mod: time.Unix(1, 0)}, identity: current}
	for index, part := range parts {
		if index == 0 && strings.HasSuffix(part, ":") {
			continue
		}
		if index > 0 || !strings.HasSuffix(current, string(filepath.Separator)) {
			current = filepath.Join(current, part)
		}
		fs.entries[current] = appHookEntry{info: appHookInfo{name: filepath.Base(current), mode: os.ModeDir, mod: time.Unix(1, 0)}, identity: current}
	}
	return fs
}

func (f *appHookFS) addFile(path, identity string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[path] = appHookEntry{info: appHookInfo{name: filepath.Base(path), size: 2, mode: 0o600, mod: time.Unix(1, 0)}, data: []byte("{}"), identity: identity}
}

func (f *appHookFS) addDir(path, identity string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[path] = appHookEntry{info: appHookInfo{name: filepath.Base(path), mode: os.ModeDir, mod: time.Unix(1, 0)}, identity: identity}
}

func (f *appHookFS) Lstat(path string) (os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.entries[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return entry.info, nil
}

func (f *appHookFS) Open(path string) (agenthookmanagement.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.entries[path]
	if !ok || entry.info.IsDir() {
		return nil, os.ErrNotExist
	}
	return &appHookFile{Reader: bytes.NewReader(entry.data), info: entry.info, identity: entry.identity}, nil
}

func (f *appHookFS) EvalSymlinks(path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.entries[path]; !ok {
		return "", os.ErrNotExist
	}
	return path, nil
}

func (f *appHookFS) Identity(path string, _ os.FileInfo) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.entries[path]
	if !ok {
		return "", os.ErrNotExist
	}
	return entry.identity, nil
}

func (f *appHookFS) IsReparse(path string, _ os.FileInfo) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entries[path].reparse
}

func (f *appHookFS) setIdentity(path, identity string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := f.entries[path]
	entry.identity = identity
	f.entries[path] = entry
}

func (f *appHookFS) setReparse(path string, value bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := f.entries[path]
	entry.reparse = value
	f.entries[path] = entry
}

type appAgentHookTransport struct {
	*fakeService
	mu             sync.Mutex
	binding        roborev.GitHookBinding
	probeCalls     int
	probeErrAt     int
	probeErr       error
	dryRuns        map[string][]roborev.AgentHookDryRun
	dryRunCalls    []string
	installCalls   int
	installArgs    []string
	installHistory [][]string
	installErr     error
	installExec    roborev.GitHookExecution
	onInstall      func(context.Context)
}

func (f *appAgentHookTransport) ProbeAgentHookBinding(context.Context) (roborev.GitHookBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probeCalls++
	if f.probeErrAt != 0 && f.probeCalls == f.probeErrAt {
		return roborev.GitHookBinding{}, f.probeErr
	}
	return f.binding, nil
}

func (f *appAgentHookTransport) AgentHookDryRun(_ context.Context, agent string, binding roborev.GitHookBinding) (roborev.AgentHookDryRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if binding != f.binding {
		return roborev.AgentHookDryRun{}, roborev.ErrBindingChanged
	}
	f.dryRunCalls = append(f.dryRunCalls, agent)
	queue := f.dryRuns[agent]
	if len(queue) == 0 {
		return roborev.AgentHookDryRun{}, errors.New("no deterministic dry-run fixture")
	}
	result := queue[0]
	f.dryRuns[agent] = queue[1:]
	return result, nil
}

func (f *appAgentHookTransport) RunAgentHookInstall(ctx context.Context, agent string, binding roborev.GitHookBinding) (roborev.GitHookExecution, error) {
	f.mu.Lock()
	if binding != f.binding {
		f.mu.Unlock()
		return roborev.GitHookExecution{}, roborev.ErrBindingChanged
	}
	f.installArgs = []string{"agent-hook", "install", "--agent", agent, "--binary", binding.Binary}
	f.installCalls++
	f.installHistory = append(f.installHistory, append([]string(nil), f.installArgs...))
	onInstall := f.onInstall
	execution := f.installExec
	installErr := f.installErr
	f.mu.Unlock()
	if onInstall != nil {
		onInstall(ctx)
	}
	return execution, installErr
}

func newAppAgentHookServer(t *testing.T, service *appAgentHookTransport, fs *appHookFS) *Server {
	t.Helper()
	server := setupServer(t, service.fakeService)
	server.RoboRev = service
	server.AgentHookConfigFS = fs
	return server
}

func appAgentHookBinding() roborev.GitHookBinding {
	return roborev.GitHookBinding{Binary: `C:\fake\very-secret\roborev.exe`, Version: "v0.64.0", Generation: 1, FileID: "binary-1"}
}

func appAgentHookPath() string { return `C:\fake-home\.codex\hooks.json` }

func appAgentHookDryRun(agent, status, path string) roborev.AgentHookDryRun {
	return roborev.AgentHookDryRun{Agent: agent, Status: roborev.AgentHookReadinessStatus(status), ConfigPath: path}
}

func appAgentHookRequest(server *Server, method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	return request
}

func appAgentHookPreview(t *testing.T, server *Server, agent string) (int, agentHookPreviewDTO) {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/preview", `{"agent":"`+agent+`"}`))
	var dto agentHookPreviewDTO
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &dto); err != nil {
			t.Fatal(err)
		}
	}
	return recorder.Code, dto
}

func TestAgentHookManagementPreviewIsSingleAgentAndSanitized(t *testing.T) {
	path := appAgentHookPath()
	fs := newAppHookFS(path)
	fs.addFile(path, "config-1")
	service := &appAgentHookTransport{fakeService: &fakeService{}, binding: appAgentHookBinding(), dryRuns: map[string][]roborev.AgentHookDryRun{"codex": {appAgentHookDryRun("codex", "needs-update", path)}}}
	server := newAppAgentHookServer(t, service, fs)
	code, dto := appAgentHookPreview(t, server, "codex")
	if code != http.StatusOK || dto.Agent != "codex" || dto.Action != "install-update" || dto.Command.Executable != "roborev" {
		t.Fatalf("preview code=%d dto=%#v", code, dto)
	}
	if strings.Contains(string(mustMarshalAgentHook(t, dto)), "C:\\") || strings.Contains(string(mustMarshalAgentHook(t, dto)), "very-secret") {
		t.Fatalf("preview leaked private path: %#v", dto)
	}
	want := []string{"agent-hook", "install", "--agent", "codex", "--binary", agentHookPreviewBinary}
	if len(dto.Command.Args) != len(want) || strings.Join(dto.Command.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("preview command=%#v", dto.Command.Args)
	}
	if service.installCalls != 0 {
		t.Fatalf("preview launched install=%#v", service.installArgs)
	}
}

func mustMarshalAgentHook(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAgentHookManagementPreviewRejectsCurrentUnavailableAndInvalidWithoutProcess(t *testing.T) {
	for _, test := range []struct {
		name   string
		agent  string
		status roborev.AgentHookReadinessStatus
		code   int
	}{
		{name: "current", agent: "codex", status: roborev.AgentHookReadinessCurrent, code: http.StatusConflict},
		{name: "claude current", agent: "claude", status: roborev.AgentHookReadinessCurrent, code: http.StatusConflict},
		{name: "grok current", agent: "grok", status: roborev.AgentHookReadinessCurrent, code: http.StatusConflict},
		{name: "droid current", agent: "droid", status: roborev.AgentHookReadinessCurrent, code: http.StatusConflict},
		{name: "unavailable", agent: "codex", status: roborev.AgentHookReadinessUnavailable, code: http.StatusConflict},
		{name: "mixed-case agent", agent: "Codex", status: roborev.AgentHookReadinessNeedsUpdate, code: http.StatusBadRequest},
		{name: "padded agent", agent: " codex ", status: roborev.AgentHookReadinessNeedsUpdate, code: http.StatusBadRequest},
		{name: "invalid", agent: "all", status: "", code: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := appAgentHookPath()
			fs := newAppHookFS(path)
			fs.addFile(path, "config-1")
			service := &appAgentHookTransport{fakeService: &fakeService{}, binding: appAgentHookBinding(), dryRuns: map[string][]roborev.AgentHookDryRun{test.agent: {appAgentHookDryRun(test.agent, string(test.status), path)}}}
			server := newAppAgentHookServer(t, service, fs)
			code, _ := appAgentHookPreview(t, server, test.agent)
			if code != test.code || len(service.dryRunCalls) > 1 || service.installCalls != 0 {
				t.Fatalf("code=%d dryRuns=%#v install=%#v", code, service.dryRunCalls, service.installArgs)
			}
		})
	}
}

func TestAgentHookManagementUnsupportedVersionDoesNotProbeAgent(t *testing.T) {
	path := appAgentHookPath()
	fs := newAppHookFS(path)
	fs.addFile(path, "config-1")
	service := &appAgentHookTransport{
		fakeService: &fakeService{},
		binding:     roborev.GitHookBinding{Binary: appAgentHookBinding().Binary, Version: "v0.63.0", Generation: 1, FileID: "old"},
		dryRuns:     map[string][]roborev.AgentHookDryRun{"codex": {appAgentHookDryRun("codex", "needs-update", path)}},
	}
	server := newAppAgentHookServer(t, service, fs)
	code, _ := appAgentHookPreview(t, server, "codex")
	if code != http.StatusServiceUnavailable || len(service.dryRunCalls) != 0 || service.installCalls != 0 {
		t.Fatalf("unsupported version code=%d dryRuns=%#v install=%#v", code, service.dryRunCalls, service.installArgs)
	}
}

func TestAgentHookManagementBindingProbeFailureAfterPreviewIsStale(t *testing.T) {
	path := appAgentHookPath()
	fs := newAppHookFS(path)
	fs.addFile(path, "config-1")
	service := &appAgentHookTransport{
		fakeService: &fakeService{},
		binding:     appAgentHookBinding(),
		probeErrAt:  2,
		probeErr:    errors.New("private binding failure"),
		dryRuns: map[string][]roborev.AgentHookDryRun{
			"codex": {appAgentHookDryRun("codex", "needs-update", path)},
		},
	}
	server := newAppAgentHookServer(t, service, fs)
	code, preview := appAgentHookPreview(t, server, "codex")
	if code != http.StatusOK {
		t.Fatalf("preview status=%d", code)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/execute", `{"token":"`+preview.Token+`"}`))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "stale") || service.installCalls != 0 || len(service.dryRunCalls) != 1 {
		t.Fatalf("binding failure status=%d body=%s probes=%d dryRuns=%#v installs=%d", recorder.Code, recorder.Body.String(), service.probeCalls, service.dryRunCalls, service.installCalls)
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/execute", `{"token":"`+preview.Token+`"}`))
	if recorder.Code != http.StatusConflict || service.installCalls != 0 {
		t.Fatalf("binding failure replay status=%d body=%s installs=%d", recorder.Code, recorder.Body.String(), service.installCalls)
	}
}

func TestAgentHookManagementAuthQueryAndBodyValidationAvoidProcess(t *testing.T) {
	path := appAgentHookPath()
	fs := newAppHookFS(path)
	fs.addFile(path, "config-1")
	service := &appAgentHookTransport{fakeService: &fakeService{}, binding: appAgentHookBinding(), dryRuns: map[string][]roborev.AgentHookDryRun{"codex": {appAgentHookDryRun("codex", "needs-update", path)}}}
	server := newAppAgentHookServer(t, service, fs)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/agent-hooks/preview", strings.NewReader(`{"agent":"codex"}`)),
		appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/preview?evil=1", `{"agent":"codex"}`),
		appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/preview", `{"agent":"codex","extra":1}`),
		appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/preview", `{"agent":"codex","agent":"claude"}`),
	} {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden && recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid request status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	if len(service.dryRunCalls) != 0 || service.installCalls != 0 {
		t.Fatalf("validation launched process dryRuns=%#v install=%#v", service.dryRunCalls, service.installArgs)
	}
}

func TestAgentHookManagementExecuteExactInstallReplayAndGate(t *testing.T) {
	path := appAgentHookPath()
	fs := newAppHookFS(path)
	fs.addFile(path, "config-1")
	service := &appAgentHookTransport{fakeService: &fakeService{}, binding: appAgentHookBinding(), installExec: roborev.GitHookExecution{Started: true}, dryRuns: map[string][]roborev.AgentHookDryRun{"codex": {
		appAgentHookDryRun("codex", "needs-update", path),
		appAgentHookDryRun("codex", "needs-update", path),
		appAgentHookDryRun("codex", "current", path),
	}}}
	server := newAppAgentHookServer(t, service, fs)
	code, preview := appAgentHookPreview(t, server, "codex")
	if code != http.StatusOK {
		t.Fatalf("preview status=%d", code)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/execute", `{"token":"`+preview.Token+`"}`))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "verified-success") {
		t.Fatalf("execute status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.installCalls != 1 || len(service.installArgs) != 6 || strings.Join(service.installArgs, "\x00") != strings.Join([]string{"agent-hook", "install", "--agent", "codex", "--binary", appAgentHookBinding().Binary}, "\x00") {
		t.Fatalf("install args=%#v", service.installArgs)
	}
	for _, forbidden := range []string{"all", "--config", "--codex-config", "--claude-config", "--grok-config", "--command", "status", "dump", "reset", "run", "daemon"} {
		if containsExactArg(service.installArgs, forbidden) {
			t.Fatalf("forbidden install arg=%q args=%#v", forbidden, service.installArgs)
		}
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/execute", `{"token":"`+preview.Token+`"}`))
	if recorder.Code != http.StatusConflict || service.installCalls != 1 {
		t.Fatalf("replay status=%d body=%s install=%#v", recorder.Code, recorder.Body.String(), service.installArgs)
	}

	path2 := `C:\fake-home\.claude\settings.json`
	fs2 := newAppHookFS(path2)
	fs2.addFile(path2, "config-2")
	busyService := &appAgentHookTransport{fakeService: &fakeService{}, binding: appAgentHookBinding(), installExec: roborev.GitHookExecution{Started: true}, dryRuns: map[string][]roborev.AgentHookDryRun{"claude": {appAgentHookDryRun("claude", "needs-update", path2)}}}
	busyServer := newAppAgentHookServer(t, busyService, fs2)
	code, busyPreview := appAgentHookPreview(t, busyServer, "claude")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	release, err := busyServer.Gate.AcquireFix()
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	busyServer.Handler().ServeHTTP(recorder, appAgentHookRequest(busyServer, http.MethodPost, "/api/agent-hooks/execute", `{"token":"`+busyPreview.Token+`"}`))
	release()
	if recorder.Code != http.StatusConflict || busyService.installCalls != 0 {
		t.Fatalf("busy status=%d body=%s install=%#v", recorder.Code, recorder.Body.String(), busyService.installArgs)
	}
	recorder = httptest.NewRecorder()
	busyServer.Handler().ServeHTTP(recorder, appAgentHookRequest(busyServer, http.MethodPost, "/api/agent-hooks/execute", `{"token":"`+busyPreview.Token+`"}`))
	if recorder.Code != http.StatusConflict || busyService.installCalls != 0 {
		t.Fatalf("busy replay status=%d body=%s install=%#v", recorder.Code, recorder.Body.String(), busyService.installArgs)
	}
}

func TestAgentHookManagementExecutesExactlyOnceForEachAllowedAgent(t *testing.T) {
	for _, agent := range []string{"codex", "claude", "grok", "droid"} {
		t.Run(agent, func(t *testing.T) {
			path := appAgentHookPath()
			fs := newAppHookFS(path)
			fs.addFile(path, "config-1")
			service := &appAgentHookTransport{
				fakeService: &fakeService{},
				binding:     appAgentHookBinding(),
				installExec: roborev.GitHookExecution{Started: true},
				dryRuns: map[string][]roborev.AgentHookDryRun{agent: {
					appAgentHookDryRun(agent, "needs-update", path),
					appAgentHookDryRun(agent, "needs-update", path),
					appAgentHookDryRun(agent, "current", path),
				}},
			}
			server := newAppAgentHookServer(t, service, fs)
			code, preview := appAgentHookPreview(t, server, agent)
			if code != http.StatusOK {
				t.Fatalf("preview status=%d", code)
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/execute", `{"token":"`+preview.Token+`"}`))
			if recorder.Code != http.StatusOK || service.installCalls != 1 || len(service.installHistory) != 1 {
				t.Fatalf("execute status=%d body=%s calls=%d history=%#v", recorder.Code, recorder.Body.String(), service.installCalls, service.installHistory)
			}
			want := []string{"agent-hook", "install", "--agent", agent, "--binary", appAgentHookBinding().Binary}
			if strings.Join(service.installHistory[0], "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("install history=%#v, want %#v", service.installHistory, want)
			}
		})
	}
}

func TestAgentHookManagementStaleConfigBindingAndPathBlockBeforeInstall(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*appHookFS, *appAgentHookTransport, string)
	}{
		{name: "config fingerprint", mutate: func(fs *appHookFS, _ *appAgentHookTransport, path string) { fs.setIdentity(path, "changed") }},
		{name: "reparse", mutate: func(fs *appHookFS, _ *appAgentHookTransport, path string) { fs.setReparse(path, true) }},
		{name: "binding", mutate: func(_ *appHookFS, service *appAgentHookTransport, _ string) {
			service.mu.Lock()
			service.binding.Generation++
			service.mu.Unlock()
		}},
		{name: "dry-run path", mutate: func(_ *appHookFS, service *appAgentHookTransport, path string) {
			service.mu.Lock()
			service.dryRuns["codex"] = []roborev.AgentHookDryRun{appAgentHookDryRun("codex", "needs-update", `C:\\changed\\hooks.json`)}
			service.mu.Unlock()
			_ = path
		}},
		{name: "dry-run became current", mutate: func(_ *appHookFS, service *appAgentHookTransport, _ string) {
			service.mu.Lock()
			service.dryRuns["codex"] = []roborev.AgentHookDryRun{appAgentHookDryRun("codex", "current", appAgentHookPath())}
			service.mu.Unlock()
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := appAgentHookPath()
			fs := newAppHookFS(path)
			fs.addFile(path, "config-1")
			service := &appAgentHookTransport{fakeService: &fakeService{}, binding: appAgentHookBinding(), installExec: roborev.GitHookExecution{Started: true}, dryRuns: map[string][]roborev.AgentHookDryRun{"codex": {
				appAgentHookDryRun("codex", "needs-update", path),
				appAgentHookDryRun("codex", "needs-update", path),
			}}}
			server := newAppAgentHookServer(t, service, fs)
			code, preview := appAgentHookPreview(t, server, "codex")
			if code != http.StatusOK {
				t.Fatal(code)
			}
			test.mutate(fs, service, path)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/execute", `{"token":"`+preview.Token+`"}`))
			if recorder.Code != http.StatusConflict || service.installCalls != 0 {
				t.Fatalf("stale status=%d body=%s install=%#v", recorder.Code, recorder.Body.String(), service.installArgs)
			}
		})
	}
}

func TestAgentHookManagementPostconditionAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name       string
		execution  roborev.GitHookExecution
		installErr error
		want       string
		wantError  string
	}{
		{name: "cli error after write", execution: roborev.GitHookExecution{Started: true}, installErr: errors.New("private cli stderr"), want: "verified-success", wantError: "cli-failed"},
		{name: "not started external current", execution: roborev.GitHookExecution{Started: false}, want: "failed-unverified", wantError: "cli-not-started"},
		{name: "post readback still needs update", execution: roborev.GitHookExecution{Started: true}, want: "failed-unverified", wantError: "readback-not-current"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := appAgentHookPath()
			fs := newAppHookFS(path)
			fs.addFile(path, "config-1")
			postStatus := "current"
			if test.name == "post readback still needs update" {
				postStatus = "needs-update"
			}
			service := &appAgentHookTransport{fakeService: &fakeService{}, binding: appAgentHookBinding(), installExec: test.execution, installErr: test.installErr, dryRuns: map[string][]roborev.AgentHookDryRun{"codex": {
				appAgentHookDryRun("codex", "needs-update", path),
				appAgentHookDryRun("codex", "needs-update", path),
				appAgentHookDryRun("codex", postStatus, path),
			}}}
			server := newAppAgentHookServer(t, service, fs)
			code, preview := appAgentHookPreview(t, server, "codex")
			if code != http.StatusOK {
				t.Fatal(code)
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/execute", `{"token":"`+preview.Token+`"}`))
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), test.want) || !strings.Contains(recorder.Body.String(), test.wantError) || strings.Contains(recorder.Body.String(), "private cli stderr") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestAgentHookManagementAbsentIntermediateCreatedByInstallCanVerify(t *testing.T) {
	path := `C:\fake-home\.codex\new-hooks\hooks.json`
	fs := newAppHookFS(`C:\fake-home\.codex`)
	service := &appAgentHookTransport{
		fakeService: &fakeService{},
		binding:     appAgentHookBinding(),
		installExec: roborev.GitHookExecution{Started: true},
		dryRuns: map[string][]roborev.AgentHookDryRun{"codex": {
			appAgentHookDryRun("codex", "needs-update", path),
			appAgentHookDryRun("codex", "needs-update", path),
			appAgentHookDryRun("codex", "current", path),
		}},
	}
	service.onInstall = func(context.Context) {
		fs.addDir(`C:\fake-home\.codex\new-hooks`, "new-hooks-1")
		fs.addFile(path, "config-created")
	}
	server := newAppAgentHookServer(t, service, fs)
	code, preview := appAgentHookPreview(t, server, "codex")
	if code != http.StatusOK {
		t.Fatalf("preview status=%d", code)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/execute", `{"token":"`+preview.Token+`"}`))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "verified-success") || service.installCalls != 1 {
		t.Fatalf("created intermediate status=%d body=%s installs=%d", recorder.Code, recorder.Body.String(), service.installCalls)
	}
}

func TestAgentHookManagementPostReadbackBindingDriftIsUnverified(t *testing.T) {
	path := appAgentHookPath()
	fs := newAppHookFS(path)
	fs.addFile(path, "config-1")
	service := &appAgentHookTransport{
		fakeService: &fakeService{},
		binding:     appAgentHookBinding(),
		installExec: roborev.GitHookExecution{Started: true},
		dryRuns: map[string][]roborev.AgentHookDryRun{"codex": {
			appAgentHookDryRun("codex", "needs-update", path),
			appAgentHookDryRun("codex", "needs-update", path),
			appAgentHookDryRun("codex", "current", path),
		}},
	}
	service.onInstall = func(context.Context) {
		service.mu.Lock()
		service.binding.Generation++
		service.mu.Unlock()
	}
	server := newAppAgentHookServer(t, service, fs)
	code, preview := appAgentHookPreview(t, server, "codex")
	if code != http.StatusOK {
		t.Fatalf("preview status=%d", code)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/execute", `{"token":"`+preview.Token+`"}`))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "failed-unverified") || !strings.Contains(recorder.Body.String(), "binding-drift") || service.installCalls != 1 || len(service.dryRunCalls) != 2 {
		t.Fatalf("post binding drift status=%d body=%s installs=%d dryRuns=%#v", recorder.Code, recorder.Body.String(), service.installCalls, service.dryRunCalls)
	}
}

func TestAgentHookManagementCancellationStillReadsBack(t *testing.T) {
	path := appAgentHookPath()
	fs := newAppHookFS(path)
	fs.addFile(path, "config-1")
	canceled := false
	service := &appAgentHookTransport{fakeService: &fakeService{}, binding: appAgentHookBinding(), installExec: roborev.GitHookExecution{Started: true}, installErr: context.Canceled, dryRuns: map[string][]roborev.AgentHookDryRun{"codex": {
		appAgentHookDryRun("codex", "needs-update", path),
		appAgentHookDryRun("codex", "needs-update", path),
		appAgentHookDryRun("codex", "current", path),
	}}, onInstall: func(context.Context) { canceled = true }}
	server := newAppAgentHookServer(t, service, fs)
	code, preview := appAgentHookPreview(t, server, "codex")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, appAgentHookRequest(server, http.MethodPost, "/api/agent-hooks/execute", `{"token":"`+preview.Token+`"}`))
	if !canceled || !strings.Contains(recorder.Body.String(), "verified-success") || len(service.dryRunCalls) != 3 {
		t.Fatalf("canceled execute body=%s canceled=%v dryRuns=%#v", recorder.Body.String(), canceled, service.dryRunCalls)
	}
}

func containsExactArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
