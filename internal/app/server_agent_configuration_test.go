package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type agentConfigurationHTTPInspector struct {
	mu         sync.Mutex
	repo       gitx.Repository
	rootCalls  int
	rootFailAt int
}

func (i *agentConfigurationHTTPInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	repo := i.repo
	if repo.Path == "" {
		repo.Path = path
	}
	return repo, nil
}

func (i *agentConfigurationHTTPInspector) InspectRoot(_ context.Context, path string) (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.rootCalls++
	if i.rootFailAt > 0 && i.rootCalls == i.rootFailAt {
		return "", errors.New("root changed")
	}
	if i.repo.Path != "" && !gitx.SameRepositoryPath(i.repo.Path, path) {
		return "", errors.New("root changed")
	}
	return path, nil
}

type fakeAgentConfigurationTransport struct {
	*fakeService
	binding        roborev.RepositoryAgentConfigurationBinding
	rows           []roborev.ConfigRow
	global         roborev.RepositoryAgentConfigurationScopedValue
	repository     roborev.RepositoryAgentConfigurationScopedValue
	setResult      roborev.RepositoryAgentConfigurationResult
	setCalls       int
	launchCalls    int
	statusCalls    int
	preflightCalls int
	readbackCalls  int
	guardCalls     int
	onLaunch       func()
	mu             sync.Mutex
}

func (f *fakeAgentConfigurationTransport) RepositoryAgentConfigurationStatus(_ context.Context, _ string) (roborev.RepositoryAgentConfigurationStatusSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls++
	return roborev.RepositoryAgentConfigurationStatusSnapshot{
		Binding: f.binding, Rows: append([]roborev.ConfigRow(nil), f.rows...), Global: f.global, Repository: f.repository,
	}, nil
}

func (f *fakeAgentConfigurationTransport) RepositoryAgentConfigurationPreflight(_ context.Context, _ string, scope roborev.ConfigScope) (roborev.RepositoryAgentConfigurationPreflight, error) {
	f.mu.Lock()
	f.preflightCalls++
	defer f.mu.Unlock()
	value := f.global
	if scope == roborev.ConfigScopeLocal {
		value = f.repository
	}
	return roborev.RepositoryAgentConfigurationPreflight{Binding: f.binding, Rows: append([]roborev.ConfigRow(nil), f.rows...), Value: value}, nil
}

func (f *fakeAgentConfigurationTransport) RepositoryAgentConfigurationSet(_ context.Context, _ string, _ roborev.ConfigScope, _ string, _ roborev.RepositoryAgentConfigurationBinding, guard roborev.RepositoryAgentConfigurationPreLaunchGuard) (roborev.RepositoryAgentConfigurationResult, error) {
	f.mu.Lock()
	f.setCalls++
	f.mu.Unlock()
	if guard != nil {
		if err := guard(context.Background()); err != nil {
			return roborev.RepositoryAgentConfigurationResult{}, err
		}
	}
	f.mu.Lock()
	f.guardCalls++
	f.launchCalls++
	result := f.setResult
	onLaunch := f.onLaunch
	f.mu.Unlock()
	if onLaunch != nil {
		onLaunch()
	}
	return result, nil
}

func (f *fakeAgentConfigurationTransport) RepositoryAgentConfigurationReadback(context.Context, string, roborev.ConfigScope, string, roborev.RepositoryAgentConfigurationBinding) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readbackCalls++
	return nil
}

func newAgentConfigurationHTTPServer(t *testing.T) (*Server, *fakeAgentConfigurationTransport, *agentConfigurationHTTPInspector, string) {
	t.Helper()
	root := t.TempDir()
	inspector := &agentConfigurationHTTPInspector{repo: gitx.Repository{Path: root, Name: "repo", Branch: "main", Head: strings.Repeat("a", 40), Detached: false}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	service := &fakeAgentConfigurationTransport{
		fakeService: &fakeService{},
		binding:     roborev.RepositoryAgentConfigurationBinding{Binary: "roborev", Version: "v0.64.0", Generation: 1, FileID: "binary"},
		rows:        []roborev.ConfigRow{{Origin: "default", Key: "max_workers", Value: "4"}},
		setResult:   roborev.RepositoryAgentConfigurationResult{Started: true, ExitCode: 0},
	}
	server, err := NewServerWithSetup(service, registry, "", operation.NewGate(), nil)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(root)
	if err != nil {
		t.Fatal(err)
	}
	return server, service, inspector, identity
}

func agentConfigurationHTTPBody(t *testing.T, server *Server, method, path, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("X-CSRF-Token", server.csrfToken)
	if mutate != nil {
		mutate(req)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	return recorder
}

func agentIdentityBody(t *testing.T, identity string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"repositoryIdentity": identity})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestRepositoryAgentConfigurationStrictValidationPrecedesTransport(t *testing.T) {
	server, transport, _, identity := newAgentConfigurationHTTPServer(t)
	valid := agentIdentityBody(t, identity)
	encodedIdentity, _ := json.Marshal(identity)
	cases := []struct {
		name   string
		path   string
		body   string
		mutate func(*http.Request)
	}{
		{"missing-fetch", "/api/repository-agent-configuration/status", valid, func(req *http.Request) { req.Header.Del("Sec-Fetch-Site") }},
		{"missing-csrf", "/api/repository-agent-configuration/status", valid, func(req *http.Request) { req.Header.Del("X-CSRF-Token") }},
		{"query", "/api/repository-agent-configuration/status?x=1", valid, nil},
		{"unknown", "/api/repository-agent-configuration/status", strings.TrimSuffix(valid, "}") + `,"path":"C:\\private"}`, nil},
		{"duplicate", "/api/repository-agent-configuration/status", `{"repositoryIdentity":` + string(encodedIdentity) + `,"repositoryIdentity":` + string(encodedIdentity) + `}`, nil},
		{"missing", "/api/repository-agent-configuration/status", `{}`, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			before := transport.fakeService.installationCalls
			recorder := agentConfigurationHTTPBody(t, server, http.MethodPost, test.path, test.body, test.mutate)
			if recorder.Code < 400 {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			transport.mu.Lock()
			calls := transport.statusCalls + transport.preflightCalls + transport.setCalls + transport.readbackCalls + transport.guardCalls
			transport.mu.Unlock()
			if calls != 0 || transport.fakeService.installationCalls != before {
				t.Fatalf("side effects calls=%d installation=%d before=%d", calls, transport.fakeService.installationCalls, before)
			}
		})
	}
}

func TestRepositoryAgentConfigurationStatusRedactsCustomAndReturnsAlias(t *testing.T) {
	server, transport, _, identity := newAgentConfigurationHTTPServer(t)
	transport.global = roborev.RepositoryAgentConfigurationScopedValue{Set: true, Value: `acp.PRIVATE_CUSTOM_AGENT`}
	transport.repository = roborev.RepositoryAgentConfigurationScopedValue{Set: true, Value: "codex"}
	recorder := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/status", agentIdentityBody(t, identity), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["repositoryIdentity"] == identity || response["repositoryIdentity"] == "" {
		t.Fatalf("status returned legacy identity: %#v", response)
	}
	if _, ok := response["version"]; !ok {
		t.Fatalf("missing version: %#v", response)
	}
	for key := range response {
		if key != "version" && key != "repositoryIdentity" && key != "global" && key != "repository" {
			t.Fatalf("unexpected status field %q", key)
		}
	}
	global, _ := response["global"].(map[string]any)
	if global["status"] != "custom" || global["agent"] != nil {
		t.Fatalf("custom row leaked/incorrect: %#v", global)
	}
	if strings.Contains(recorder.Body.String(), "acp.PRIVATE_CUSTOM_AGENT") || strings.Contains(recorder.Body.String(), "PRIVATE_CONFIG_CONTENT") {
		t.Fatalf("private value leaked: %s", recorder.Body.String())
	}
	repository, _ := response["repository"].(map[string]any)
	if repository["status"] != "canonical" || repository["agent"] != "codex" {
		t.Fatalf("canonical row=%#v", repository)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.statusCalls != 1 || transport.preflightCalls != 0 {
		t.Fatalf("status calls=%d scoped preflight calls=%d", transport.statusCalls, transport.preflightCalls)
	}
}

func TestRepositoryAgentConfigurationPreviewNoopAndExactCommands(t *testing.T) {
	server, transport, _, identity := newAgentConfigurationHTTPServer(t)
	transport.global = roborev.RepositoryAgentConfigurationScopedValue{Set: true, Value: "codex"}
	alias := selectedAgentAlias(t, server, identity)
	noop := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/preview", `{"repositoryIdentity":"`+alias+`","scope":"global","agent":"codex"}`, nil)
	if noop.Code != http.StatusOK {
		t.Fatalf("noop status=%d body=%s", noop.Code, noop.Body.String())
	}
	var noopJSON map[string]any
	_ = json.Unmarshal(noop.Body.Bytes(), &noopJSON)
	if noopJSON["action"] != "already-configured" || noopJSON["currentStatus"] != "canonical" || noopJSON["currentAgent"] != "codex" || noopJSON["token"] != nil || noopJSON["command"] != nil {
		t.Fatalf("noop wire=%#v", noopJSON)
	}
	transport.global = roborev.RepositoryAgentConfigurationScopedValue{}
	alias = selectedAgentAlias(t, server, identity)
	preview := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/preview", `{"repositoryIdentity":"`+alias+`","scope":"global","agent":"claude-code"}`, nil)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewJSON map[string]any
	_ = json.Unmarshal(preview.Body.Bytes(), &previewJSON)
	command, ok := previewJSON["command"].(map[string]any)
	if !ok || command["executable"] != "roborev" || previewJSON["action"] != "set-agent" {
		t.Fatalf("preview wire=%#v", previewJSON)
	}
	args, ok := command["args"].([]any)
	if !ok || len(args) != 5 || args[0] != "config" || args[1] != "set" || args[2] != "default_agent" || args[3] != "claude-code" || args[4] != "--global" {
		t.Fatalf("global command args=%#v", command["args"])
	}
	transport.repository = roborev.RepositoryAgentConfigurationScopedValue{}
	local := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/preview", `{"repositoryIdentity":"`+alias+`","scope":"repository","agent":"claude-code"}`, nil)
	if local.Code != http.StatusOK {
		t.Fatalf("local preview status=%d body=%s", local.Code, local.Body.String())
	}
	var localJSON map[string]any
	_ = json.Unmarshal(local.Body.Bytes(), &localJSON)
	localCommand, commandOK := localJSON["command"].(map[string]any)
	localArgs, argsOK := localCommand["args"].([]any)
	if !commandOK || !argsOK || len(localArgs) != 5 || localArgs[2] != "agent" || localArgs[3] != "claude-code" || localArgs[4] != "--local" || localJSON["currentStatus"] != "unset" || localJSON["currentAgent"] != nil {
		t.Fatalf("local wire=%#v", localJSON)
	}
}

func selectedAgentAlias(t *testing.T, server *Server, identity string) string {
	t.Helper()
	root, err := server.Registry.ResolveIdentity(identity)
	if err != nil {
		t.Fatal(err)
	}
	return server.repositoryMutationAlias(root)
}

func TestRepositoryAgentConfigurationExecuteClaimsBeforeBusyAndReturnsStatusOnly(t *testing.T) {
	server, transport, _, identity := newAgentConfigurationHTTPServer(t)
	alias := selectedAgentAlias(t, server, identity)
	preview := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/preview", `{"repositoryIdentity":"`+alias+`","scope":"global","agent":"codex"}`, nil)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewJSON map[string]any
	_ = json.Unmarshal(preview.Body.Bytes(), &previewJSON)
	token, _ := previewJSON["token"].(string)
	if token == "" {
		t.Fatal("missing token")
	}
	release, err := server.Gate.AcquireFix()
	if err != nil {
		t.Fatal(err)
	}
	busy := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/execute", `{"token":"`+token+`"}`, nil)
	release()
	if busy.Code != http.StatusConflict {
		t.Fatalf("busy status=%d body=%s", busy.Code, busy.Body.String())
	}
	replayed := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/execute", `{"token":"`+token+`"}`, nil)
	if replayed.Code != http.StatusConflict {
		t.Fatalf("replay status=%d body=%s", replayed.Code, replayed.Body.String())
	}
	transport.mu.Lock()
	if transport.setCalls != 0 {
		t.Fatalf("busy token launched set: %d", transport.setCalls)
	}
	transport.mu.Unlock()
}

func TestRepositoryAgentConfigurationExecuteRejectsSelectionSwitch(t *testing.T) {
	server, transport, inspector, identity := newAgentConfigurationHTTPServer(t)
	alias := selectedAgentAlias(t, server, identity)
	preview := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/preview", `{"repositoryIdentity":"`+alias+`","scope":"global","agent":"codex"}`, nil)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewJSON map[string]any
	if err := json.Unmarshal(preview.Body.Bytes(), &previewJSON); err != nil {
		t.Fatal(err)
	}
	token, _ := previewJSON["token"].(string)
	if token == "" {
		t.Fatal("missing token")
	}

	// Admit another registered root and switch selection before execute. The
	// selected-repository lease must reject this token before any preflight or
	// set process boundary is entered.
	otherRoot := t.TempDir()
	inspector.mu.Lock()
	inspector.repo.Path = otherRoot
	inspector.mu.Unlock()
	if _, err := server.Registry.Add(context.Background(), otherRoot); err != nil {
		t.Fatal(err)
	}
	if err := server.Registry.Select(otherRoot); err != nil {
		t.Fatal(err)
	}
	result := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/execute", `{"token":"`+token+`"}`, nil)
	if result.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.preflightCalls != 1 || transport.setCalls != 0 || transport.readbackCalls != 0 {
		t.Fatalf("selection switch crossed process boundary: preflight=%d set=%d readback=%d", transport.preflightCalls, transport.setCalls, transport.readbackCalls)
	}
}

func TestRepositoryAgentConfigurationExecuteStartedFalseCannotCredit(t *testing.T) {
	server, transport, _, identity := newAgentConfigurationHTTPServer(t)
	transport.setResult = roborev.RepositoryAgentConfigurationResult{Started: false, ExitCode: -1}
	alias := selectedAgentAlias(t, server, identity)
	preview := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/preview", `{"repositoryIdentity":"`+alias+`","scope":"global","agent":"codex"}`, nil)
	var previewJSON map[string]any
	_ = json.Unmarshal(preview.Body.Bytes(), &previewJSON)
	token, _ := previewJSON["token"].(string)
	result := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/execute", `{"token":"`+token+`"}`, nil)
	if result.Code != http.StatusOK || result.Body.String() != "{\"status\":\"failed-unverified\"}\n" {
		t.Fatalf("result status=%d body=%s", result.Code, result.Body.String())
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.readbackCalls != 0 {
		t.Fatalf("started=false performed readback: %d", transport.readbackCalls)
	}
}

func TestRepositoryAgentConfigurationExecuteUsesScopeSpecificLeases(t *testing.T) {
	for _, test := range []struct {
		name      string
		scope     string
		wantRepos int
	}{
		{name: "global", scope: "global", wantRepos: 0},
		{name: "repository", scope: "repository", wantRepos: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, transport, _, identity := newAgentConfigurationHTTPServer(t)
			alias := selectedAgentAlias(t, server, identity)
			preview := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/preview", `{"repositoryIdentity":"`+alias+`","scope":"`+test.scope+`","agent":"codex"}`, nil)
			if preview.Code != http.StatusOK {
				t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
			}
			var previewJSON map[string]any
			if err := json.Unmarshal(preview.Body.Bytes(), &previewJSON); err != nil {
				t.Fatal(err)
			}
			token, _ := previewJSON["token"].(string)
			if token == "" {
				t.Fatal("missing token")
			}

			var observed operation.DetailedSnapshot
			selectionAttempt := make(chan struct{})
			transport.onLaunch = func() {
				observed = server.Gate.SnapshotDetailed()
				go func() {
					_ = server.Registry.Select(server.Registry.Selected())
					close(selectionAttempt)
				}()
				select {
				case <-selectionAttempt:
					t.Errorf("selected-repository lease was not held during launch")
				case <-time.After(20 * time.Millisecond):
				}
			}
			result := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/execute", `{"token":"`+token+`"}`, nil)
			if result.Code != http.StatusOK || result.Body.String() != "{\"status\":\"verified-success\"}\n" {
				t.Fatalf("result status=%d body=%s", result.Code, result.Body.String())
			}
			select {
			case <-selectionAttempt:
			case <-time.After(time.Second):
				t.Fatal("selection lease did not release")
			}
			if observed.Fixes != 1 || observed.RepositoryLeases != test.wantRepos {
				t.Fatalf("scope %s leases=%#v, want fixes=1 repositories=%d", test.scope, observed, test.wantRepos)
			}
		})
	}
}

func TestRepositoryAgentConfigurationGlobalAllowsDirtyButRepositoryDoesNot(t *testing.T) {
	server, _, inspector, identity := newAgentConfigurationHTTPServer(t)
	inspector.mu.Lock()
	inspector.repo.Dirty = true
	inspector.mu.Unlock()
	alias := selectedAgentAlias(t, server, identity)
	global := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/preview", `{"repositoryIdentity":"`+alias+`","scope":"global","agent":"codex"}`, nil)
	if global.Code != http.StatusOK {
		t.Fatalf("dirty global status=%d body=%s", global.Code, global.Body.String())
	}
	local := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/preview", `{"repositoryIdentity":"`+alias+`","scope":"repository","agent":"codex"}`, nil)
	if local.Code != http.StatusConflict {
		t.Fatalf("dirty repository status=%d body=%s", local.Code, local.Body.String())
	}
}

func TestRepositoryAgentConfigurationFinalRootGuardStopsLaunch(t *testing.T) {
	server, transport, inspector, identity := newAgentConfigurationHTTPServer(t)
	alias := selectedAgentAlias(t, server, identity)
	preview := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/preview", `{"repositoryIdentity":"`+alias+`","scope":"global","agent":"codex"}`, nil)
	var previewJSON map[string]any
	_ = json.Unmarshal(preview.Body.Bytes(), &previewJSON)
	token, _ := previewJSON["token"].(string)
	inspector.mu.Lock()
	// Preview and execute revalidation each consume one root witness; fail the
	// third witness, which is the final Git-root guard immediately before launch.
	inspector.rootFailAt = inspector.rootCalls + 2
	inspector.mu.Unlock()
	result := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/execute", `{"token":"`+token+`"}`, nil)
	if result.Code != http.StatusOK || result.Body.String() != "{\"status\":\"failed-unverified\"}\n" {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.setCalls != 1 || transport.launchCalls != 0 {
		t.Fatalf("final guard launch entry=%d launch=%d", transport.setCalls, transport.launchCalls)
	}
}

func TestRepositoryAgentConfigurationLocalSuccessMayLeaveDirtyButStateDriftRejects(t *testing.T) {
	server, transport, inspector, identity := newAgentConfigurationHTTPServer(t)
	alias := selectedAgentAlias(t, server, identity)
	preview := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/preview", `{"repositoryIdentity":"`+alias+`","scope":"repository","agent":"codex"}`, nil)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewJSON map[string]any
	_ = json.Unmarshal(preview.Body.Bytes(), &previewJSON)
	token, _ := previewJSON["token"].(string)
	transport.onLaunch = func() {
		inspector.mu.Lock()
		inspector.repo.Dirty = true // the local config write itself may dirty Git.
		inspector.mu.Unlock()
	}
	result := agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/execute", `{"token":"`+token+`"}`, nil)
	if result.Code != http.StatusOK || result.Body.String() != "{\"status\":\"verified-success\"}\n" {
		t.Fatalf("local result status=%d body=%s", result.Code, result.Body.String())
	}

	// A separate preview binds the clean HEAD/branch/detached state. Changing
	// branch metadata before execute must prevent the set boundary.
	inspector.mu.Lock()
	inspector.repo.Dirty = false
	inspector.mu.Unlock()
	preview = agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/preview", `{"repositoryIdentity":"`+alias+`","scope":"repository","agent":"gemini"}`, nil)
	_ = json.Unmarshal(preview.Body.Bytes(), &previewJSON)
	token, _ = previewJSON["token"].(string)
	inspector.mu.Lock()
	inspector.repo.Branch = "feature"
	inspector.mu.Unlock()
	before := transport.setCalls
	result = agentConfigurationHTTPBody(t, server, http.MethodPost, "/api/repository-agent-configuration/execute", `{"token":"`+token+`"}`, nil)
	if result.Code != http.StatusConflict {
		t.Fatalf("branch drift status=%d body=%s", result.Code, result.Body.String())
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.setCalls != before {
		t.Fatalf("branch drift launched set: before=%d after=%d", before, transport.setCalls)
	}
}
