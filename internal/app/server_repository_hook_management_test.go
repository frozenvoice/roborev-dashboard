package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/hookmanagement"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type hookManagementInspector struct {
	*reviewExportInspector
	layout          gitx.HookLayout
	layoutCalls     int
	layoutErrorCall int
}

func (i *hookManagementInspector) ResolveHookLayout(context.Context, string) (gitx.HookLayout, error) {
	i.layoutCalls++
	if i.layoutErrorCall > 0 && i.layoutCalls == i.layoutErrorCall {
		return gitx.HookLayout{}, errors.New("layout readback failed")
	}
	return i.layout, nil
}

type hookManagementTransport struct {
	*fakeService
	binding           roborev.GitHookBinding
	probeCalls        int
	fs                *hookStatusMemoryFS
	mutate            bool
	partial           bool
	failAfterMutation bool
	cancelOnRun       context.CancelFunc
	commands          []struct {
		root   string
		action roborev.GitHookAction
		args   []string
	}
}

func (s *hookManagementTransport) ProbeGitHookBinding(context.Context) (roborev.GitHookBinding, error) {
	s.probeCalls++
	return s.binding, nil
}

func (s *hookManagementTransport) RunGitHook(_ context.Context, root string, action roborev.GitHookAction, binding roborev.GitHookBinding) (roborev.GitHookExecution, error) {
	s.commands = append(s.commands, struct {
		root   string
		action roborev.GitHookAction
		args   []string
	}{root: root, action: action, args: []string{binding.Binary}})
	if !s.mutate {
		return roborev.GitHookExecution{Started: true, ExitCode: 0}, nil
	}
	current := func(name, command string) {
		path := filepath.Join(s.fsPath(), name)
		marker := hookmanagement.PostCommitMarker
		if name == hookmanagement.PostRewrite {
			marker = hookmanagement.PostRewriteMarker
		}
		content := "#!/bin/sh\n# roborev " + marker + " - generated\nROBOREV=" + strconv.Quote(binding.Binary) + "\n\"$ROBOREV\" " + command + "\n"
		entry := s.fs.entries[path]
		entry.content = []byte(content)
		entry.info.size = int64(len(entry.content))
		if entry.info.name == "" {
			entry.info.name = name
			entry.info.mode = 0o700
		}
		s.fs.entries[path] = entry
	}
	current(hookmanagement.PostCommit, "post-commit")
	if !s.partial {
		current(hookmanagement.PostRewrite, "remap --quiet")
	}
	if s.partial {
		return roborev.GitHookExecution{Started: true, ExitCode: 1}, errors.New("one hook failed")
	}
	if s.cancelOnRun != nil {
		s.cancelOnRun()
	}
	if s.failAfterMutation {
		return roborev.GitHookExecution{Started: true, ExitCode: 1}, errors.New("ambiguous CLI result")
	}
	return roborev.GitHookExecution{Started: true, ExitCode: 0}, nil
}

func (s *hookManagementTransport) fsPath() string {
	for path := range s.fs.entries {
		return filepath.Dir(path)
	}
	return `E:\hook-files`
}

func newHookManagementApp(t *testing.T) (*Server, *hookManagementTransport, string) {
	t.Helper()
	root := `E:\hook-repo`
	hooks := `E:\hook-files`
	inspector := &hookManagementInspector{reviewExportInspector: &reviewExportInspector{repository: gitx.Repository{Name: "hook-repo", Path: root}}, layout: gitx.HookLayout{Root: root, GitDir: `E:\hook-repo\.git`, CommonDir: `E:\hook-repo\.git`, HooksPath: hooks, Safe: true}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(root)
	if err != nil {
		t.Fatal(err)
	}
	fs := newHookStatusMemoryFS(hooks, "", "")
	fs.entries = make(map[string]hookStatusMemoryEntry)
	transport := &hookManagementTransport{fakeService: &fakeService{}, binding: roborev.GitHookBinding{Binary: `E:\very-secret\private\roborev.exe`, Version: "v0.64.0", Generation: 1, FileID: "1"}, fs: fs}
	server, err := NewServer(transport, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	server.HookManagement.FS = fs
	return server, transport, identity
}

func hookManagementMutationRequest(server *Server, method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Host = "example.com"
	request.Header.Set("Origin", "http://example.com")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func hookManagementActionBody(identity, action string) string {
	value, _ := json.Marshal(map[string]string{"repositoryIdentity": identity, "action": action})
	return string(value)
}

func assertHookManagementResponseHasNoSecret(t *testing.T, body, secret string) {
	t.Helper()
	escapedSecret, err := json.Marshal(secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, secret) || strings.Contains(body, `E:\very-secret`) || strings.Contains(body, string(escapedSecret)) || strings.Contains(body, "raw hook content") {
		t.Fatalf("browser response leaked server-side hook data: %s", body)
	}
}

func assertSanitizedHookPreflight(t *testing.T, preflight hookmanagement.Preflight) {
	t.Helper()
	if preflight.PostCommit.Content != nil || preflight.PostRewrite.Content != nil || preflight.Root != "" || preflight.HooksDir != "" || preflight.LayoutDigest != "" {
		t.Fatalf("unsanitized hook preflight=%+v", preflight)
	}
}

func TestHookManagementPreviewBindsExactActionWithoutExecuting(t *testing.T) {
	server, transport, identity := newHookManagementApp(t)
	recorder := httptest.NewRecorder()
	request := hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/preview", hookManagementActionBody(identity, "install"))
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(transport.commands) != 0 {
		t.Fatalf("status=%d commands=%d body=%s", recorder.Code, len(transport.commands), recorder.Body.String())
	}
	var response hookManagementPreviewDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Action != hookmanagement.Install || response.Token == "" || response.Command.Executable != hookManagementPreviewExecutable || strings.Join(response.Command.Args, "\x00") != strings.Join([]string{"install-hook", "--binary", hookManagementPreviewBinary}, "\x00") {
		t.Fatalf("response=%+v", response)
	}
	assertHookManagementResponseHasNoSecret(t, recorder.Body.String(), transport.binding.Binary)
	assertSanitizedHookPreflight(t, response.Preflight)
}

func TestHookManagementPreviewUsesSanitizedCommandShapeForEveryAction(t *testing.T) {
	for _, test := range []struct {
		action hookmanagement.Action
		args   []string
	}{
		{action: hookmanagement.Install, args: []string{"install-hook", "--binary", hookManagementPreviewBinary}},
		{action: hookmanagement.Repair, args: []string{"install-hook", "repair", "--binary", hookManagementPreviewBinary}},
		{action: hookmanagement.Remove, args: []string{"uninstall-hook"}},
	} {
		t.Run(string(test.action), func(t *testing.T) {
			preview := hookCommandPreview(test.action)
			if preview.Executable != hookManagementPreviewExecutable || strings.Join(preview.Args, "\x00") != strings.Join(test.args, "\x00") {
				t.Fatalf("preview=%+v want executable=%q args=%q", preview, hookManagementPreviewExecutable, test.args)
			}
		})
	}
}

func TestHookManagementBrowserDTOsHideBinaryWhileExecutionKeepsBinding(t *testing.T) {
	server, transport, identity := newHookManagementApp(t)
	secret := transport.binding.Binary

	management := httptest.NewRecorder()
	managementRequest := hookManagementMutationRequest(server, http.MethodGet, "/api/repositories/hooks/management?repositoryIdentity="+url.QueryEscape(identity), "")
	server.Handler().ServeHTTP(management, managementRequest)
	if management.Code != http.StatusOK {
		t.Fatalf("management status=%d body=%s", management.Code, management.Body.String())
	}
	assertHookManagementResponseHasNoSecret(t, management.Body.String(), secret)
	var managementPreflight hookmanagement.Preflight
	if err := json.Unmarshal(management.Body.Bytes(), &managementPreflight); err != nil {
		t.Fatal(err)
	}
	assertSanitizedHookPreflight(t, managementPreflight)

	preview := httptest.NewRecorder()
	server.Handler().ServeHTTP(preview, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/preview", hookManagementActionBody(identity, "install")))
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	assertHookManagementResponseHasNoSecret(t, preview.Body.String(), secret)
	var prepared hookManagementPreviewDTO
	if err := json.Unmarshal(preview.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Command.Executable != hookManagementPreviewExecutable || strings.Join(prepared.Command.Args, "\x00") != strings.Join([]string{"install-hook", "--binary", hookManagementPreviewBinary}, "\x00") {
		t.Fatalf("sanitized command=%+v", prepared.Command)
	}
	assertSanitizedHookPreflight(t, prepared.Preflight)

	transport.mutate = true
	result := httptest.NewRecorder()
	server.Handler().ServeHTTP(result, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/execute", `{"token":"`+prepared.Token+`"}`))
	if result.Code != http.StatusOK {
		t.Fatalf("result status=%d body=%s", result.Code, result.Body.String())
	}
	assertHookManagementResponseHasNoSecret(t, result.Body.String(), secret)
	var resultDTO hookManagementResultDTO
	if err := json.Unmarshal(result.Body.Bytes(), &resultDTO); err != nil {
		t.Fatal(err)
	}
	assertSanitizedHookPreflight(t, resultDTO.Preflight)
	quotedSecret := strconv.Quote(secret)
	sawSecretHookContent := false
	for _, entry := range transport.fs.entries {
		if strings.Contains(string(entry.content), secret) || strings.Contains(string(entry.content), quotedSecret) {
			sawSecretHookContent = true
			break
		}
	}
	if !sawSecretHookContent {
		t.Fatal("fake execution did not create secret-bearing hook content")
	}
	if len(transport.commands) != 1 || len(transport.commands[0].args) != 1 || transport.commands[0].args[0] != secret {
		t.Fatalf("execute lost server-side binding: %#v", transport.commands)
	}

	error := httptest.NewRecorder()
	server.Handler().ServeHTTP(error, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/execute", `{"token":"missing"}`))
	if error.Code != http.StatusConflict {
		t.Fatalf("error status=%d body=%s", error.Code, error.Body.String())
	}
	assertHookManagementResponseHasNoSecret(t, error.Body.String(), secret)
}

func TestHookManagementExecutePartialReadbackAndSingleUseToken(t *testing.T) {
	server, transport, identity := newHookManagementApp(t)
	recorder := httptest.NewRecorder()
	preview := hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/preview", hookManagementActionBody(identity, "install"))
	server.Handler().ServeHTTP(recorder, preview)
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var prepared hookManagementPreviewDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	transport.mutate, transport.partial = true, true
	execute := hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/execute", `{"token":"`+prepared.Token+`"}`)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, execute)
	if recorder.Code != http.StatusOK || len(transport.commands) != 1 {
		t.Fatalf("status=%d commands=%d body=%s", recorder.Code, len(transport.commands), recorder.Body.String())
	}
	var result hookManagementResultDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != hookmanagement.Partial {
		t.Fatalf("result=%+v", result)
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/execute", `{"token":"`+prepared.Token+`"}`))
	if recorder.Code != http.StatusConflict || len(transport.commands) != 1 {
		t.Fatalf("replay status=%d commands=%d body=%s", recorder.Code, len(transport.commands), recorder.Body.String())
	}
}

func TestHookManagementKeepsVerifiedSuccessWhenCLIReportsErrorAfterFullReadback(t *testing.T) {
	server, transport, identity := newHookManagementApp(t)
	recorder := httptest.NewRecorder()
	preview := hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/preview", hookManagementActionBody(identity, "install"))
	server.Handler().ServeHTTP(recorder, preview)
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var prepared hookManagementPreviewDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	transport.mutate, transport.failAfterMutation = true, true
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/execute", `{"token":"`+prepared.Token+`"}`))
	if recorder.Code != http.StatusOK || len(transport.commands) != 1 {
		t.Fatalf("status=%d commands=%d body=%s", recorder.Code, len(transport.commands), recorder.Body.String())
	}
	var result hookManagementResultDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != hookmanagement.VerifiedSuccess || result.ErrorCode != "cli-failed" {
		t.Fatalf("result=%+v", result)
	}
}

func TestHookManagementReadbackSurvivesRequestCancellationAfterLaunch(t *testing.T) {
	server, transport, identity := newHookManagementApp(t)
	recorder := httptest.NewRecorder()
	preview := hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/preview", hookManagementActionBody(identity, "install"))
	server.Handler().ServeHTTP(recorder, preview)
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var prepared hookManagementPreviewDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	transport.mutate = true
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport.cancelOnRun = cancel
	execute := hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/execute", `{"token":"`+prepared.Token+`"}`).WithContext(requestContext)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, execute)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var result hookManagementResultDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != hookmanagement.VerifiedSuccess {
		t.Fatalf("canceled request did not get detached readback: %+v", result)
	}
}

func TestHookManagementBindingDriftIsStalePreview(t *testing.T) {
	server, transport, identity := newHookManagementApp(t)
	recorder := httptest.NewRecorder()
	preview := hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/preview", hookManagementActionBody(identity, "install"))
	server.Handler().ServeHTTP(recorder, preview)
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var prepared hookManagementPreviewDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	transport.binding.Generation++
	execute := hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/execute", `{"token":"`+prepared.Token+`"}`)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, execute)
	if recorder.Code != http.StatusConflict || len(transport.commands) != 0 {
		t.Fatalf("binding drift status=%d commands=%d body=%s", recorder.Code, len(transport.commands), recorder.Body.String())
	}
}

func TestHookManagementReadbackErrorReturnsStructuredUnavailablePreflight(t *testing.T) {
	server, transport, identity := newHookManagementApp(t)
	inspector := server.Registry.inspector.(*hookManagementInspector)
	inspector.layoutErrorCall = 3 // preview, execute pre-read, mandatory post-read
	recorder := httptest.NewRecorder()
	preview := hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/preview", hookManagementActionBody(identity, "install"))
	server.Handler().ServeHTTP(recorder, preview)
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var prepared hookManagementPreviewDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	transport.mutate = true
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/execute", `{"token":"`+prepared.Token+`"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("execute status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var result hookManagementResultDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != hookmanagement.FailedUnverified || result.ErrorCode != "readback-unavailable" || result.Preflight.Install.Reason != "unsafe-hooks-directory" || result.Preflight.Repair.Reason != "unsafe-hooks-directory" || result.Preflight.Remove.Reason != "unsafe-hooks-directory" || len(result.Preflight.WarningCodes) != 1 || result.Preflight.WarningCodes[0] != "unsafe-hooks-directory" || result.Preflight.PostCommit.Classification != hookmanagement.Unreadable || result.Preflight.PostRewrite.Classification != hookmanagement.Unreadable || !result.Preflight.Safety.UnreadableHookPresent {
		t.Fatalf("unstructured readback result=%+v", result)
	}
}

func TestHookManagementRejectsForeignAndDuplicateBeforeBindingProbe(t *testing.T) {
	server, transport, identity := newHookManagementApp(t)
	for _, body := range []string{
		hookManagementActionBody("foreign", "install"),
		`{"repositoryIdentity":"` + strings.ReplaceAll(identity, `\`, `\\`) + `","repositoryIdentity":"other","action":"install"}`,
		`{"repositoryIdentity":"` + strings.ReplaceAll(identity, `\`, `\\`) + `","action":"install","extra":true}`,
	} {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/preview", body))
		if recorder.Code != http.StatusForbidden && recorder.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	if len(transport.commands) != 0 || transport.installationCalls != 0 || transport.probeCalls != 0 || server.Registry.inspector.(*hookManagementInspector).layoutCalls != 0 {
		t.Fatalf("mutation side effects commands=%d installation=%d probes=%d layout=%d", len(transport.commands), transport.installationCalls, transport.probeCalls, server.Registry.inspector.(*hookManagementInspector).layoutCalls)
	}
}

func TestHookManagementGETStrictAuthAndQueryRejectBeforeVersionAndLayout(t *testing.T) {
	server, transport, identity := newHookManagementApp(t)
	identityQuery := url.QueryEscape(identity)
	for _, test := range []struct {
		name string
		path string
		auth bool
		want int
	}{
		{name: "missing browser auth", path: "/api/repositories/hooks/management?repositoryIdentity=" + identityQuery, want: http.StatusForbidden},
		{name: "missing identity", path: "/api/repositories/hooks/management", auth: true, want: http.StatusBadRequest},
		{name: "extra query", path: "/api/repositories/hooks/management?repositoryIdentity=" + identityQuery + "&extra=1", auth: true, want: http.StatusBadRequest},
		{name: "duplicate identity", path: "/api/repositories/hooks/management?repositoryIdentity=" + identityQuery + "&repositoryIdentity=other", auth: true, want: http.StatusBadRequest},
		{name: "foreign identity", path: "/api/repositories/hooks/management?repositoryIdentity=foreign", auth: true, want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Host = "example.com"
			if test.auth {
				request.Header.Set("Origin", "http://example.com")
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
	if len(transport.commands) != 0 || transport.probeCalls != 0 || server.Registry.inspector.(*hookManagementInspector).layoutCalls != 0 {
		t.Fatalf("GET validation caused side effects commands=%d probes=%d layout=%d", len(transport.commands), transport.probeCalls, server.Registry.inspector.(*hookManagementInspector).layoutCalls)
	}
}

func TestHookManagementRejectsExecuteActionRepositoryAndExtraFieldsBeforeCLI(t *testing.T) {
	server, transport, identity := newHookManagementApp(t)
	identityJSON := strings.ReplaceAll(identity, `\`, `\\`)
	for _, body := range []string{
		`{"token":"missing","action":"install"}`,
		`{"token":"missing","repositoryIdentity":"` + identityJSON + `"}`,
		`{"token":"missing","extra":true}`,
	} {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/execute", body))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, recorder.Code, recorder.Body.String())
		}
	}
	if len(transport.commands) != 0 {
		t.Fatalf("malformed execute launched CLI: %#v", transport.commands)
	}
}

func TestHookManagementPreviewFingerprintAndLayoutDriftRejectBeforeCLI(t *testing.T) {
	for _, drift := range []string{"fingerprint", "layout"} {
		t.Run(drift, func(t *testing.T) {
			server, transport, identity := newHookManagementApp(t)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/preview", hookManagementActionBody(identity, "install")))
			if recorder.Code != http.StatusOK {
				t.Fatalf("preview status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var prepared hookManagementPreviewDTO
			if err := json.Unmarshal(recorder.Body.Bytes(), &prepared); err != nil {
				t.Fatal(err)
			}
			if drift == "fingerprint" {
				path := filepath.Join(`E:\hook-files`, hookmanagement.PostCommit)
				content := []byte("#!/bin/sh\necho changed\n")
				transport.fs.entries[path] = hookStatusMemoryEntry{content: content, info: hookStatusMemoryInfo{name: hookmanagement.PostCommit, size: int64(len(content)), mode: 0o700}}
			} else {
				server.Registry.inspector.(*hookManagementInspector).layout.HooksPath = `E:\other-hooks`
			}
			recorder = httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/execute", `{"token":"`+prepared.Token+`"}`))
			if recorder.Code != http.StatusConflict || len(transport.commands) != 0 {
				t.Fatalf("%s drift status=%d commands=%d body=%s", drift, recorder.Code, len(transport.commands), recorder.Body.String())
			}
		})
	}
}

func TestHookManagementBusyGateConsumesTokenWithoutCLI(t *testing.T) {
	server, transport, identity := newHookManagementApp(t)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/preview", hookManagementActionBody(identity, "install")))
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var prepared hookManagementPreviewDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	release, err := server.Gate.AcquireFix()
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/execute", `{"token":"`+prepared.Token+`"}`))
	release()
	if recorder.Code != http.StatusConflict || len(transport.commands) != 0 {
		t.Fatalf("busy execute status=%d commands=%d body=%s", recorder.Code, len(transport.commands), recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/execute", `{"token":"`+prepared.Token+`"}`))
	if recorder.Code != http.StatusConflict || len(transport.commands) != 0 {
		t.Fatalf("consumed token replay status=%d commands=%d body=%s", recorder.Code, len(transport.commands), recorder.Body.String())
	}
}

func TestHookManagementUnsupportedBindingAndNoManagedRepairRejectBeforeCLI(t *testing.T) {
	server, transport, identity := newHookManagementApp(t)
	transport.binding.Version = "v0.63.0"
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/preview", hookManagementActionBody(identity, "install")))
	if recorder.Code != http.StatusServiceUnavailable || len(transport.commands) != 0 {
		t.Fatalf("unsupported binding status=%d commands=%d body=%s", recorder.Code, len(transport.commands), recorder.Body.String())
	}
	server, transport, identity = newHookManagementApp(t)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, hookManagementMutationRequest(server, http.MethodPost, "/api/repositories/hooks/preview", hookManagementActionBody(identity, "repair")))
	if recorder.Code != http.StatusConflict || len(transport.commands) != 0 {
		t.Fatalf("no-managed repair status=%d commands=%d body=%s", recorder.Code, len(transport.commands), recorder.Body.String())
	}
}

var _ roborev.GitHookTransport = (*hookManagementTransport)(nil)
