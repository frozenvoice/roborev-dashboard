package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type skillsTestInfo struct {
	name string
	size int64
	mode os.FileMode
	mod  time.Time
}

func (i skillsTestInfo) Name() string       { return i.name }
func (i skillsTestInfo) Size() int64        { return i.size }
func (i skillsTestInfo) Mode() os.FileMode  { return i.mode }
func (i skillsTestInfo) ModTime() time.Time { return i.mod }
func (i skillsTestInfo) IsDir() bool        { return i.mode.IsDir() }
func (i skillsTestInfo) Sys() any           { return nil }

type skillsTestFile struct {
	*bytes.Reader
	info     skillsTestInfo
	identity string
}

func (f *skillsTestFile) Close() error               { return nil }
func (f *skillsTestFile) Stat() (os.FileInfo, error) { return f.info, nil }
func (f *skillsTestFile) Identity() (string, error)  { return f.identity, nil }

type skillsTestEntry struct {
	info     skillsTestInfo
	data     []byte
	identity string
	reparse  bool
}

type skillsTestFS struct {
	mu      sync.Mutex
	entries map[string]skillsTestEntry
}

func newSkillsTestFS() *skillsTestFS {
	fs := &skillsTestFS{entries: make(map[string]skillsTestEntry)}
	fs.addDir(`C:\`, "volume")
	return fs
}

func (f *skillsTestFS) addDir(path, identity string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path = filepath.Clean(path)
	f.entries[path] = skillsTestEntry{info: skillsTestInfo{name: filepath.Base(path), mode: os.ModeDir, mod: time.Unix(1, 0)}, identity: identity}
}

func (f *skillsTestFS) addFile(path, data, identity string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path = filepath.Clean(path)
	f.entries[path] = skillsTestEntry{info: skillsTestInfo{name: filepath.Base(path), size: int64(len(data)), mode: 0o600, mod: time.Unix(1, 0)}, data: []byte(data), identity: identity}
}

func (f *skillsTestFS) setReparse(path string, reparse bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := f.entries[filepath.Clean(path)]
	entry.reparse = reparse
	f.entries[filepath.Clean(path)] = entry
}

func (f *skillsTestFS) setIdentity(path, identity string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := f.entries[filepath.Clean(path)]
	entry.identity = identity
	f.entries[filepath.Clean(path)] = entry
}

func (f *skillsTestFS) setFileData(path, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	clean := filepath.Clean(path)
	entry := f.entries[clean]
	entry.data = []byte(data)
	entry.info.size = int64(len(data))
	f.entries[clean] = entry
}

func (f *skillsTestFS) Lstat(path string) (os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.entries[filepath.Clean(path)]
	if !ok {
		return nil, os.ErrNotExist
	}
	return entry.info, nil
}

func (f *skillsTestFS) Open(path string) (pathsafety.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.entries[filepath.Clean(path)]
	if !ok || entry.info.IsDir() {
		return nil, os.ErrNotExist
	}
	return &skillsTestFile{Reader: bytes.NewReader(entry.data), info: entry.info, identity: entry.identity}, nil
}

func (f *skillsTestFS) EvalSymlinks(path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.entries[filepath.Clean(path)]; !ok {
		return "", os.ErrNotExist
	}
	return filepath.Clean(path), nil
}

func (f *skillsTestFS) Identity(path string, _ os.FileInfo) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.entries[filepath.Clean(path)]
	if !ok {
		return "", os.ErrNotExist
	}
	return entry.identity, nil
}

func (f *skillsTestFS) IsReparse(path string, _ os.FileInfo) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entries[filepath.Clean(path)].reparse
}

type skillsManagementTestTransport struct {
	*fakeService
	mu           sync.Mutex
	binding      roborev.GitHookBinding
	readiness    []roborev.SkillsReadiness
	readCalls    int
	probeCalls   int
	runCalls     int
	runAgent     string
	runDir       string
	runBinding   roborev.GitHookBinding
	execution    roborev.GitHookExecution
	runErr       error
	readinessErr error
	bindingErr   error
	onRun        func()
	events       *[]string
	readCanceled []bool
	bindCanceled []bool
}

func (f *skillsManagementTestTransport) SkillsReadiness(ctx context.Context) (roborev.SkillsReadiness, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readCalls++
	f.readCanceled = append(f.readCanceled, ctx.Err() != nil)
	if f.events != nil {
		*f.events = append(*f.events, "readiness")
	}
	if f.readinessErr != nil {
		return roborev.SkillsReadiness{}, f.readinessErr
	}
	if len(f.readiness) == 0 {
		return roborev.SkillsReadiness{}, roborev.ErrSkillsReadinessUnavailable
	}
	index := f.readCalls - 1
	if index >= len(f.readiness) {
		index = len(f.readiness) - 1
	}
	return f.readiness[index], nil
}

func (f *skillsManagementTestTransport) ProbeSkillsBinding(ctx context.Context) (roborev.GitHookBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probeCalls++
	f.bindCanceled = append(f.bindCanceled, ctx.Err() != nil)
	if f.events != nil {
		*f.events = append(*f.events, "binding")
	}
	if f.bindingErr != nil {
		return roborev.GitHookBinding{}, f.bindingErr
	}
	return f.binding, nil
}

func (f *skillsManagementTestTransport) RunSkillsInstall(_ context.Context, agent, skillsDir string, binding roborev.GitHookBinding) (roborev.GitHookExecution, error) {
	f.mu.Lock()
	f.runCalls++
	if f.events != nil {
		*f.events = append(*f.events, "run")
	}
	f.runAgent, f.runDir, f.runBinding = agent, skillsDir, binding
	execution, runErr, onRun := f.execution, f.runErr, f.onRun
	f.mu.Unlock()
	if onRun != nil {
		onRun()
	}
	return execution, runErr
}

func skillsNeedsReadiness() roborev.SkillsReadiness {
	return roborev.SkillsReadiness{Version: "v0.64.0", Agents: []roborev.SkillsReadinessAgent{
		{Agent: "codex", Status: "needs-install-update", Current: 9, Outdated: 1, Total: 10},
		{Agent: "claude", Status: "current", Current: 10, Total: 10},
		{Agent: "grok", Status: "current", Current: 10, Total: 10},
		{Agent: "droid", Status: "current", Current: 10, Total: 10},
	}}
}

func skillsCurrentReadiness() roborev.SkillsReadiness {
	readiness := skillsNeedsReadiness()
	readiness.Agents[0] = roborev.SkillsReadinessAgent{Agent: "codex", Status: "current", Current: 10, Total: 10}
	return readiness
}

func skillsConfigUnavailableReadiness() roborev.SkillsReadiness {
	readiness := skillsNeedsReadiness()
	readiness.Agents[0] = roborev.SkillsReadinessAgent{Agent: "codex", Status: "config-unavailable", Total: 10}
	return readiness
}

func skillsTestBinding() roborev.GitHookBinding {
	return roborev.GitHookBinding{Binary: `C:\private\roborev.exe`, Version: "v0.64.0", Generation: 1, FileID: "file-1"}
}

func newSkillsManagementTestServer(t *testing.T, transport *skillsManagementTestTransport, fs *skillsTestFS, env []string) *Server {
	t.Helper()
	server := setupServer(t, transport.fakeService)
	server.RoboRev = transport
	server.SkillsManagementFS = fs
	server.SkillsManagementHome = `C:\fake-home`
	server.SkillsManagementEnv = func() []string { return append([]string(nil), env...) }
	server.skillsPreviews = newSkillsPreviewStore()
	return server
}

func skillsTargetFS() *skillsTestFS {
	fs := newSkillsTestFS()
	fs.addDir(`C:\fake-home`, "home")
	fs.addDir(`C:\fake-home\.codex`, "codex-root")
	return fs
}

func skillsMutationRequest(server *Server, path, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	return request
}

func TestSkillsManagementPreviewAndExecuteUsesSanitizedSingleAgentContract(t *testing.T) {
	fs := skillsTargetFS()
	transport := &skillsManagementTestTransport{fakeService: &fakeService{}, binding: roborev.GitHookBinding{Binary: `C:\private\roborev.exe`, Version: "v0.64.0", Generation: 1, FileID: "file-1"}, readiness: []roborev.SkillsReadiness{skillsNeedsReadiness(), skillsNeedsReadiness(), skillsCurrentReadiness()}, execution: roborev.GitHookExecution{Started: true, ExitCode: 0}}
	server := newSkillsManagementTestServer(t, transport, fs, []string{"CODEX_HOME=C:\\fake-home\\.codex", "HOME=C:\\private-home", "GIT_DIR=C:\\evil", "ROBOREV_TOKEN=secret"})

	preview := httptest.NewRecorder()
	server.Handler().ServeHTTP(preview, skillsMutationRequest(server, "/api/skills/preview", `{"agent":"codex"}`))
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var dto skillsManagementPreviewDTO
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if got, want := dto.Command.Args, []string{"skills", "install", "--path", skillsManagementPreviewPath, "--agent", "codex"}; !equalStrings(got, want) {
		t.Fatalf("preview args=%#v want=%#v", got, want)
	}
	for _, private := range []string{`C:\private`, `C:\\fake-home`, "secret", "GIT_DIR"} {
		if strings.Contains(preview.Body.String(), private) {
			t.Fatalf("preview leaked %q: %s", private, preview.Body.String())
		}
	}

	execute := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"token": dto.Token})
	server.Handler().ServeHTTP(execute, skillsMutationRequest(server, "/api/skills/execute", string(body)))
	if execute.Code != http.StatusOK || !strings.Contains(execute.Body.String(), `"status":"verified-success"`) {
		t.Fatalf("execute status=%d body=%s", execute.Code, execute.Body.String())
	}
	if transport.runCalls != 1 || transport.runAgent != "codex" || transport.runDir != `C:\fake-home\.codex\skills` {
		t.Fatalf("run calls=%d agent=%q dir=%q", transport.runCalls, transport.runAgent, transport.runDir)
	}
	if strings.Contains(execute.Body.String(), `C:\private`) || strings.Contains(execute.Body.String(), "raw stdout") {
		t.Fatalf("execute leaked private data: %s", execute.Body.String())
	}

	replay := httptest.NewRecorder()
	server.Handler().ServeHTTP(replay, skillsMutationRequest(server, "/api/skills/execute", string(body)))
	if replay.Code != http.StatusConflict || transport.runCalls != 1 {
		t.Fatalf("replay status=%d calls=%d body=%s", replay.Code, transport.runCalls, replay.Body.String())
	}
}

func TestSkillsManagementRejectsInvalidInputBeforeTransport(t *testing.T) {
	transport := &skillsManagementTestTransport{fakeService: &fakeService{}, binding: skillsTestBinding(), readiness: []roborev.SkillsReadiness{skillsNeedsReadiness()}}
	server := newSkillsManagementTestServer(t, transport, skillsTargetFS(), []string{"CODEX_HOME=C:\\fake-home\\.codex"})
	for _, test := range []struct {
		name string
		path string
		body string
	}{
		{name: "query", path: "/api/skills/preview?path=C:", body: `{"agent":"codex"}`},
		{name: "browser path", path: "/api/skills/preview", body: `{"agent":"codex","path":"C:\\evil"}`},
		{name: "duplicate", path: "/api/skills/preview", body: `{"agent":"codex","agent":"claude"}`},
		{name: "all", path: "/api/skills/preview", body: `{"agent":"all"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := transport.readCalls
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, skillsMutationRequest(server, test.path, test.body))
			if recorder.Code < http.StatusBadRequest || recorder.Code >= http.StatusInternalServerError || transport.readCalls != before || transport.runCalls != 0 {
				t.Fatalf("status=%d reads=%d runs=%d body=%s", recorder.Code, transport.readCalls, transport.runCalls, recorder.Body.String())
			}
		})
	}
}

func TestSkillsManagementExecuteRejectsQueryExtraAndDuplicateBeforeClaim(t *testing.T) {
	transport := &skillsManagementTestTransport{fakeService: &fakeService{}, binding: skillsTestBinding(), readiness: []roborev.SkillsReadiness{skillsNeedsReadiness(), skillsNeedsReadiness(), skillsCurrentReadiness()}, execution: roborev.GitHookExecution{Started: true}}
	server := newSkillsManagementTestServer(t, transport, skillsTargetFS(), []string{"CODEX_HOME=C:\\fake-home\\.codex"})
	preview := httptest.NewRecorder()
	server.Handler().ServeHTTP(preview, skillsMutationRequest(server, "/api/skills/preview", `{"agent":"codex"}`))
	var dto skillsManagementPreviewDTO
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		name string
		path string
		body string
	}{
		{name: "query", path: "/api/skills/execute?unexpected=1", body: `{"token":"` + dto.Token + `"}`},
		{name: "extra", path: "/api/skills/execute", body: `{"token":"` + dto.Token + `","path":"C:\\evil"}`},
		{name: "duplicate", path: "/api/skills/execute", body: `{"token":"` + dto.Token + `","token":"other"}`},
	} {
		t.Run(bad.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, skillsMutationRequest(server, bad.path, bad.body))
			if recorder.Code != http.StatusBadRequest || transport.runCalls != 0 {
				t.Fatalf("status=%d runs=%d body=%s", recorder.Code, transport.runCalls, recorder.Body.String())
			}
		})
	}
	valid := httptest.NewRecorder()
	server.Handler().ServeHTTP(valid, skillsMutationRequest(server, "/api/skills/execute", `{"token":"`+dto.Token+`"}`))
	if valid.Code != http.StatusOK || transport.runCalls != 1 {
		t.Fatalf("valid status=%d runs=%d body=%s", valid.Code, transport.runCalls, valid.Body.String())
	}
}

func TestSkillsManagementBlocksCurrentRelativeAndLegacyTargets(t *testing.T) {
	tests := []struct {
		name string
		env  []string
		fs   func() *skillsTestFS
	}{
		{name: "current", env: []string{"CODEX_HOME=C:\\fake-home\\.codex"}, fs: skillsTargetFS},
		{name: "relative", env: []string{"CODEX_HOME=relative\\codex"}, fs: skillsTargetFS},
		{name: "legacy", env: []string{"CODEX_HOME=C:\\fake-home\\.codex"}, fs: func() *skillsTestFS {
			fs := skillsTargetFS()
			fs.addDir(`C:\fake-home\.codex\skills`, "skills")
			fs.addFile(`C:\fake-home\.codex\skills\roborev-address`, "legacy", "legacy")
			return fs
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := skillsNeedsReadiness()
			if test.name == "current" {
				status = skillsCurrentReadiness()
			}
			transport := &skillsManagementTestTransport{fakeService: &fakeService{}, binding: skillsTestBinding(), readiness: []roborev.SkillsReadiness{status}}
			server := newSkillsManagementTestServer(t, transport, test.fs(), test.env)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, skillsMutationRequest(server, "/api/skills/preview", `{"agent":"codex"}`))
			if recorder.Code != http.StatusConflict || transport.runCalls != 0 {
				t.Fatalf("status=%d runs=%d body=%s", recorder.Code, transport.runCalls, recorder.Body.String())
			}
		})
	}
}

func TestSkillsManagementExecuteBlocksReadinessDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		second roborev.SkillsReadiness
	}{
		{name: "current", second: skillsCurrentReadiness()},
		{name: "config unavailable", second: skillsConfigUnavailableReadiness()},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &skillsManagementTestTransport{fakeService: &fakeService{}, binding: skillsTestBinding(), readiness: []roborev.SkillsReadiness{skillsNeedsReadiness(), test.second}}
			server := newSkillsManagementTestServer(t, transport, skillsTargetFS(), []string{"CODEX_HOME=C:\\fake-home\\.codex"})
			preview := httptest.NewRecorder()
			server.Handler().ServeHTTP(preview, skillsMutationRequest(server, "/api/skills/preview", `{"agent":"codex"}`))
			var dto skillsManagementPreviewDTO
			if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
				t.Fatal(err)
			}
			execute := httptest.NewRecorder()
			server.Handler().ServeHTTP(execute, skillsMutationRequest(server, "/api/skills/execute", `{"token":"`+dto.Token+`"}`))
			if execute.Code != http.StatusConflict || transport.runCalls != 0 {
				t.Fatalf("status=%d runs=%d body=%s", execute.Code, transport.runCalls, execute.Body.String())
			}
		})
	}
}

func TestSkillsManagementGateBusyConsumesToken(t *testing.T) {
	transport := &skillsManagementTestTransport{fakeService: &fakeService{}, binding: skillsTestBinding(), readiness: []roborev.SkillsReadiness{skillsNeedsReadiness(), skillsNeedsReadiness()}}
	server := newSkillsManagementTestServer(t, transport, skillsTargetFS(), []string{"CODEX_HOME=C:\\fake-home\\.codex"})
	preview := httptest.NewRecorder()
	server.Handler().ServeHTTP(preview, skillsMutationRequest(server, "/api/skills/preview", `{"agent":"codex"}`))
	var dto skillsManagementPreviewDTO
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	release, err := server.Gate.AcquireFix()
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, skillsMutationRequest(server, "/api/skills/execute", `{"token":"`+dto.Token+`"}`))
	release()
	if recorder.Code != http.StatusConflict || transport.runCalls != 0 {
		t.Fatalf("status=%d runs=%d body=%s", recorder.Code, transport.runCalls, recorder.Body.String())
	}
	replay := httptest.NewRecorder()
	server.Handler().ServeHTTP(replay, skillsMutationRequest(server, "/api/skills/execute", `{"token":"`+dto.Token+`"}`))
	if replay.Code != http.StatusConflict || transport.runCalls != 0 {
		t.Fatalf("replay status=%d runs=%d body=%s", replay.Code, transport.runCalls, replay.Body.String())
	}
}

func TestSkillsManagementAuthAndCSRFRejectBeforeAnyAdapterWork(t *testing.T) {
	transport := &skillsManagementTestTransport{fakeService: &fakeService{}, readiness: []roborev.SkillsReadiness{skillsNeedsReadiness()}}
	server := newSkillsManagementTestServer(t, transport, skillsTargetFS(), []string{"CODEX_HOME=C:\\fake-home\\.codex"})
	missing := httptest.NewRequest(http.MethodPost, "/api/skills/preview", strings.NewReader(`{"agent":"codex"}`))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, missing)
	if recorder.Code != http.StatusForbidden || transport.readCalls != 0 || transport.probeCalls != 0 || transport.runCalls != 0 {
		t.Fatalf("missing auth status=%d reads=%d probes=%d runs=%d", recorder.Code, transport.readCalls, transport.probeCalls, transport.runCalls)
	}
	badCSRF := skillsMutationRequest(server, "/api/skills/preview", `{"agent":"codex"}`)
	badCSRF.Header.Set("X-CSRF-Token", "wrong")
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, badCSRF)
	if recorder.Code != http.StatusForbidden || transport.readCalls != 0 || transport.probeCalls != 0 || transport.runCalls != 0 {
		t.Fatalf("bad csrf status=%d reads=%d probes=%d runs=%d", recorder.Code, transport.readCalls, transport.probeCalls, transport.runCalls)
	}
}

func TestSkillsManagementStalePreviewBoundariesDoNotLaunch(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*skillsTestFS)
		mutate  func(*skillsTestFS, *skillsManagementTestTransport, *[]string)
	}{
		{name: "managed file appears", mutate: func(fs *skillsTestFS, _ *skillsManagementTestTransport, _ *[]string) {
			fs.addDir(`C:\fake-home\.codex\skills`, "skills")
			fs.addDir(`C:\fake-home\.codex\skills\roborev-fix`, "fix-dir")
			fs.addFile(`C:\fake-home\.codex\skills\roborev-fix\SKILL.md`, "changed", "fix-file")
		}},
		{name: "managed fingerprint drift", prepare: func(fs *skillsTestFS) {
			fs.addDir(`C:\fake-home\.codex\skills`, "skills")
			fs.addDir(`C:\fake-home\.codex\skills\roborev-fix`, "fix-dir")
			fs.addFile(`C:\fake-home\.codex\skills\roborev-fix\SKILL.md`, "before", "fix-file")
		}, mutate: func(fs *skillsTestFS, _ *skillsManagementTestTransport, _ *[]string) {
			fs.setFileData(`C:\fake-home\.codex\skills\roborev-fix\SKILL.md`, "after")
		}},
		{name: "managed identity-only replacement", prepare: func(fs *skillsTestFS) {
			fs.addDir(`C:\fake-home\.codex\skills`, "skills")
			fs.addDir(`C:\fake-home\.codex\skills\roborev-fix`, "fix-dir")
			fs.addFile(`C:\fake-home\.codex\skills\roborev-fix\SKILL.md`, "same", "old-file")
		}, mutate: func(fs *skillsTestFS, _ *skillsManagementTestTransport, _ *[]string) {
			fs.setIdentity(`C:\fake-home\.codex\skills\roborev-fix\SKILL.md`, "replacement-file")
		}},
		{name: "managed skill directory reparse", prepare: func(fs *skillsTestFS) {
			fs.addDir(`C:\fake-home\.codex\skills`, "skills")
			fs.addDir(`C:\fake-home\.codex\skills\roborev-fix`, "fix-dir")
		}, mutate: func(fs *skillsTestFS, _ *skillsManagementTestTransport, _ *[]string) {
			fs.setReparse(`C:\fake-home\.codex\skills\roborev-fix`, true)
		}},
		{name: "skills directory replacement", prepare: func(fs *skillsTestFS) {
			fs.addDir(`C:\fake-home\.codex\skills`, "skills")
		}, mutate: func(fs *skillsTestFS, _ *skillsManagementTestTransport, _ *[]string) {
			fs.setIdentity(`C:\fake-home\.codex\skills`, "replacement-skills")
		}},
		{name: "config root identity", mutate: func(fs *skillsTestFS, _ *skillsManagementTestTransport, _ *[]string) {
			fs.setIdentity(`C:\fake-home\.codex`, "changed-root")
		}},
		{name: "binding", mutate: func(_ *skillsTestFS, transport *skillsManagementTestTransport, _ *[]string) {
			transport.binding.FileID = "changed-binding"
		}},
		{name: "target", mutate: func(_ *skillsTestFS, _ *skillsManagementTestTransport, env *[]string) {
			*env = []string{"CODEX_HOME=C:\\different-home\\.codex"}
		}},
		{name: "legacy appears", mutate: func(fs *skillsTestFS, _ *skillsManagementTestTransport, _ *[]string) {
			fs.addDir(`C:\fake-home\.codex\skills`, "skills")
			fs.addFile(`C:\fake-home\.codex\skills\roborev-address`, "legacy", "legacy")
		}},
		{name: "readiness current", mutate: func(_ *skillsTestFS, transport *skillsManagementTestTransport, _ *[]string) {
			transport.readiness[1] = skillsCurrentReadiness()
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := []string{"CODEX_HOME=C:\\fake-home\\.codex"}
			transport := &skillsManagementTestTransport{fakeService: &fakeService{}, binding: skillsTestBinding(), readiness: []roborev.SkillsReadiness{skillsNeedsReadiness(), skillsNeedsReadiness()}}
			server := newSkillsManagementTestServer(t, transport, skillsTargetFS(), env)
			server.SkillsManagementEnv = func() []string { return append([]string(nil), env...) }
			if test.prepare != nil {
				test.prepare(server.SkillsManagementFS.(*skillsTestFS))
			}
			preview := httptest.NewRecorder()
			server.Handler().ServeHTTP(preview, skillsMutationRequest(server, "/api/skills/preview", `{"agent":"codex"}`))
			if preview.Code != http.StatusOK {
				t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
			}
			var dto skillsManagementPreviewDTO
			if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
				t.Fatal(err)
			}
			test.mutate(server.SkillsManagementFS.(*skillsTestFS), transport, &env)
			execute := httptest.NewRecorder()
			server.Handler().ServeHTTP(execute, skillsMutationRequest(server, "/api/skills/execute", `{"token":"`+dto.Token+`"}`))
			if execute.Code != http.StatusConflict || transport.runCalls != 0 {
				t.Fatalf("status=%d runs=%d body=%s", execute.Code, transport.runCalls, execute.Body.String())
			}
		})
	}
}

func skillsReadinessCountsFixture(current, outdated, missing int) roborev.SkillsReadiness {
	readiness := skillsNeedsReadiness()
	readiness.Agents[0] = roborev.SkillsReadinessAgent{Agent: "codex", Status: "needs-install-update", Current: current, Outdated: outdated, Missing: missing, Total: 10}
	return readiness
}

func TestSkillsManagementResultClassificationAndDetachedReadback(t *testing.T) {
	tests := []struct {
		name       string
		pre        roborev.SkillsReadiness
		post       roborev.SkillsReadiness
		execution  roborev.GitHookExecution
		runErr     error
		mutatePost func(*skillsManagementTestTransport, *[]string)
		wantStatus string
		wantCode   string
	}{
		{name: "verified", pre: skillsNeedsReadiness(), post: skillsCurrentReadiness(), execution: roborev.GitHookExecution{Started: true}, wantStatus: "verified-success"},
		{name: "verified cli error", pre: skillsNeedsReadiness(), post: skillsCurrentReadiness(), execution: roborev.GitHookExecution{Started: true, ExitCode: 9}, runErr: errors.New("fake cli failure"), wantStatus: "verified-success", wantCode: "cli-failed"},
		{name: "partial", pre: skillsReadinessCountsFixture(8, 2, 0), post: skillsReadinessCountsFixture(9, 1, 0), execution: roborev.GitHookExecution{Started: true}, wantStatus: "partial"},
		{name: "partial cli error", pre: skillsReadinessCountsFixture(8, 2, 0), post: skillsReadinessCountsFixture(9, 1, 0), execution: roborev.GitHookExecution{Started: true, ExitCode: 3}, runErr: errors.New("partial fake cli failure"), wantStatus: "partial", wantCode: "cli-failed"},
		{name: "no improvement", pre: skillsNeedsReadiness(), post: skillsNeedsReadiness(), execution: roborev.GitHookExecution{Started: true}, wantStatus: "failed-unverified", wantCode: "no-improvement"},
		{name: "post config unavailable", pre: skillsNeedsReadiness(), post: skillsConfigUnavailableReadiness(), execution: roborev.GitHookExecution{Started: true}, wantStatus: "failed-unverified", wantCode: "readback-not-current"},
		{name: "not started external current", pre: skillsNeedsReadiness(), post: skillsCurrentReadiness(), execution: roborev.GitHookExecution{Started: false}, wantStatus: "failed-unverified", wantCode: "cli-not-started"},
		{name: "post binding drift", pre: skillsNeedsReadiness(), post: skillsCurrentReadiness(), execution: roborev.GitHookExecution{Started: true}, mutatePost: func(transport *skillsManagementTestTransport, _ *[]string) { transport.binding.FileID = "post-drift" }, wantStatus: "failed-unverified", wantCode: "binding-drift"},
		{name: "post target drift", pre: skillsNeedsReadiness(), post: skillsCurrentReadiness(), execution: roborev.GitHookExecution{Started: true}, mutatePost: func(_ *skillsManagementTestTransport, env *[]string) {
			*env = []string{"CODEX_HOME=C:\\different-home\\.codex"}
		}, wantStatus: "failed-unverified", wantCode: "target-drift"},
		{name: "post readback unavailable", pre: skillsNeedsReadiness(), post: skillsCurrentReadiness(), execution: roborev.GitHookExecution{Started: true}, mutatePost: func(transport *skillsManagementTestTransport, _ *[]string) {
			transport.readinessErr = errors.New("readback unavailable")
		}, wantStatus: "failed-unverified", wantCode: "readback-unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := []string{"CODEX_HOME=C:\\fake-home\\.codex"}
			transport := &skillsManagementTestTransport{fakeService: &fakeService{}, binding: skillsTestBinding(), readiness: []roborev.SkillsReadiness{test.pre, test.pre, test.post}, execution: test.execution, runErr: test.runErr}
			transport.onRun = func() {
				if test.mutatePost != nil {
					test.mutatePost(transport, &env)
				}
			}
			server := newSkillsManagementTestServer(t, transport, skillsTargetFS(), env)
			server.SkillsManagementEnv = func() []string { return append([]string(nil), env...) }
			preview := httptest.NewRecorder()
			server.Handler().ServeHTTP(preview, skillsMutationRequest(server, "/api/skills/preview", `{"agent":"codex"}`))
			if test.execution.Started == false {
				// The post fixture is intentionally current; an unstarted command
				// must not receive credit for that external state.
			}
			var dto skillsManagementPreviewDTO
			if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
				t.Fatal(err)
			}
			execute := httptest.NewRecorder()
			server.Handler().ServeHTTP(execute, skillsMutationRequest(server, "/api/skills/execute", `{"token":"`+dto.Token+`"}`))
			var result skillsManagementResultDTO
			if err := json.Unmarshal(execute.Body.Bytes(), &result); err != nil {
				t.Fatalf("status=%d body=%s: %v", execute.Code, execute.Body.String(), err)
			}
			if execute.Code != http.StatusOK || result.Status != test.wantStatus || result.ErrorCode != test.wantCode {
				t.Fatalf("status=%d result=%#v want status=%s code=%s", execute.Code, result, test.wantStatus, test.wantCode)
			}
		})
	}
}

func TestSkillsManagementDetachedReadbackIgnoresRequestCancellation(t *testing.T) {
	transport := &skillsManagementTestTransport{fakeService: &fakeService{}, binding: skillsTestBinding(), readiness: []roborev.SkillsReadiness{skillsNeedsReadiness(), skillsNeedsReadiness(), skillsCurrentReadiness()}, execution: roborev.GitHookExecution{Started: true}}
	server := newSkillsManagementTestServer(t, transport, skillsTargetFS(), []string{"CODEX_HOME=C:\\fake-home\\.codex"})
	preview := httptest.NewRecorder()
	server.Handler().ServeHTTP(preview, skillsMutationRequest(server, "/api/skills/preview", `{"agent":"codex"}`))
	var dto skillsManagementPreviewDTO
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport.onRun = cancel
	request := skillsMutationRequest(server, "/api/skills/execute", `{"token":"`+dto.Token+`"}`).WithContext(requestContext)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"verified-success"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(transport.readCanceled) == 0 || transport.readCanceled[len(transport.readCanceled)-1] {
		t.Fatalf("post-readback reused canceled request context: %#v", transport.readCanceled)
	}
}

func skillsReadinessForAgent(agent string) roborev.SkillsReadiness {
	readiness := skillsCurrentReadiness()
	index := skillsReadinessAgentIndex(agent)
	readiness.Agents[index] = roborev.SkillsReadinessAgent{Agent: agent, Status: "needs-install-update", Current: 9, Outdated: 1, Total: 10}
	return readiness
}

func TestSkillsManagementUsesExactTargetSemanticsForEveryAgent(t *testing.T) {
	tests := []struct {
		agent      string
		env        []string
		configRoot string
		skillsDir  string
	}{
		{agent: "codex", env: []string{"CODEX_HOME=C:\\custom-codex"}, configRoot: `C:\custom-codex`, skillsDir: `C:\custom-codex\skills`},
		{agent: "claude", env: []string{"CLAUDE_CONFIG_DIR=C:\\custom-claude"}, configRoot: `C:\custom-claude`, skillsDir: `C:\custom-claude\skills`},
		{agent: "grok", env: []string{"GROK_HOME=C:\\custom-grok"}, configRoot: `C:\custom-grok`, skillsDir: `C:\custom-grok\skills`},
		{agent: "droid", env: []string{"HOME=C:\\custom-droid"}, configRoot: `C:\custom-droid\.factory`, skillsDir: `C:\custom-droid\.factory\skills`},
	}
	for _, test := range tests {
		t.Run(test.agent, func(t *testing.T) {
			fs := newSkillsTestFS()
			if test.agent == "droid" {
				fs.addDir(`C:\custom-droid`, "droid-home")
			}
			fs.addDir(test.configRoot, "config-root")
			transport := &skillsManagementTestTransport{fakeService: &fakeService{}, binding: skillsTestBinding(), readiness: []roborev.SkillsReadiness{skillsReadinessForAgent(test.agent)}}
			server := newSkillsManagementTestServer(t, transport, fs, test.env)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, skillsMutationRequest(server, "/api/skills/preview", `{"agent":"`+test.agent+`"}`))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var dto skillsManagementPreviewDTO
			if err := json.Unmarshal(recorder.Body.Bytes(), &dto); err != nil {
				t.Fatal(err)
			}
			server.skillsPreviews.mu.Lock()
			record := server.skillsPreviews.pending[dto.Token]
			server.skillsPreviews.mu.Unlock()
			if record.Target.ConfigDir != test.configRoot || record.Target.SkillsDir != test.skillsDir || record.Agent != test.agent {
				t.Fatalf("target=%#v want config=%q skills=%q", record.Target, test.configRoot, test.skillsDir)
			}
		})
	}
}

func TestSkillsManagementBlocksEveryLegacyPathForm(t *testing.T) {
	for _, form := range []string{"directory", "file", "reparse"} {
		t.Run(form, func(t *testing.T) {
			fs := skillsTargetFS()
			fs.addDir(`C:\fake-home\.codex\skills`, "skills")
			legacy := `C:\fake-home\.codex\skills\roborev-address`
			if form == "directory" {
				fs.addDir(legacy, "legacy-dir")
			} else {
				fs.addFile(legacy, "legacy", "legacy-file")
			}
			if form == "reparse" {
				fs.setReparse(legacy, true)
			}
			transport := &skillsManagementTestTransport{fakeService: &fakeService{}, binding: skillsTestBinding(), readiness: []roborev.SkillsReadiness{skillsNeedsReadiness()}}
			server := newSkillsManagementTestServer(t, transport, fs, []string{"CODEX_HOME=C:\\fake-home\\.codex"})
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, skillsMutationRequest(server, "/api/skills/preview", `{"agent":"codex"}`))
			if recorder.Code != http.StatusConflict || transport.probeCalls != 0 {
				t.Fatalf("status=%d probes=%d body=%s", recorder.Code, transport.probeCalls, recorder.Body.String())
			}
		})
	}
}

func TestSkillsManagementBlocksUnavailableOrUnsafeConfigRoots(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*skillsTestFS)
	}{
		{name: "absent", setup: func(*skillsTestFS) {}},
		{name: "reparse", setup: func(fs *skillsTestFS) {
			fs.addDir(`C:\fake-home\.codex`, "codex-root")
			fs.setReparse(`C:\fake-home\.codex`, true)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fs := newSkillsTestFS()
			test.setup(fs)
			transport := &skillsManagementTestTransport{fakeService: &fakeService{}, binding: skillsTestBinding(), readiness: []roborev.SkillsReadiness{skillsNeedsReadiness()}}
			server := newSkillsManagementTestServer(t, transport, fs, []string{"CODEX_HOME=C:\\fake-home\\.codex"})
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, skillsMutationRequest(server, "/api/skills/preview", `{"agent":"codex"}`))
			if recorder.Code != http.StatusConflict || transport.probeCalls != 0 {
				t.Fatalf("status=%d probes=%d body=%s", recorder.Code, transport.probeCalls, recorder.Body.String())
			}
		})
	}
}

func TestSkillsManagementPreservesRequiredCallOrdering(t *testing.T) {
	events := make([]string, 0, 8)
	transport := &skillsManagementTestTransport{fakeService: &fakeService{}, binding: skillsTestBinding(), readiness: []roborev.SkillsReadiness{skillsNeedsReadiness(), skillsNeedsReadiness(), skillsCurrentReadiness()}, execution: roborev.GitHookExecution{Started: true}, events: &events}
	server := newSkillsManagementTestServer(t, transport, skillsTargetFS(), []string{"CODEX_HOME=C:\\fake-home\\.codex"})
	preview := httptest.NewRecorder()
	server.Handler().ServeHTTP(preview, skillsMutationRequest(server, "/api/skills/preview", `{"agent":"codex"}`))
	var dto skillsManagementPreviewDTO
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	execute := httptest.NewRecorder()
	server.Handler().ServeHTTP(execute, skillsMutationRequest(server, "/api/skills/execute", `{"token":"`+dto.Token+`"}`))
	if execute.Code != http.StatusOK || !equalStrings(events, []string{"readiness", "binding", "binding", "readiness", "binding", "run", "readiness", "binding"}) {
		t.Fatalf("status=%d events=%#v body=%s", execute.Code, events, execute.Body.String())
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

var _ io.ReadCloser = (*skillsTestFile)(nil)
