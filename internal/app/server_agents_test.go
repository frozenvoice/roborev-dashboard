package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/agentstatus"
	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type appAgentResolver struct{ paths map[string]string }

func (r appAgentResolver) LookPath(command string) (string, error) {
	if path, ok := r.paths[command]; ok {
		return path, nil
	}
	return "", os.ErrNotExist
}
func (appAgentResolver) SameFile(left, right string) bool { return left == right }

type appIdentityProbe struct{}

func (appIdentityProbe) Run(context.Context, string, []string) agentstatus.ProbeResult {
	return agentstatus.ProbeResult{Output: "Cursor 1.0"}
}

type switchingIdentityProbe struct{ switchRepository func() }

func (p switchingIdentityProbe) Run(context.Context, string, []string) agentstatus.ProbeResult {
	if p.switchRepository != nil {
		p.switchRepository()
	}
	return agentstatus.ProbeResult{Output: "Cursor 1.0"}
}

type appHealthChecker struct {
	calls int
	agent string
	repo  string
}

func (c *appHealthChecker) CheckAgent(_ context.Context, agent string) (roborev.AgentHealthResult, error) {
	c.calls++
	c.agent = agent
	return roborev.AgentHealthResult{Agent: agent, Status: roborev.AgentHealthPassed}, nil
}
func (c *appHealthChecker) CheckAgentInRepo(_ context.Context, repo, agent string) (roborev.AgentHealthResult, error) {
	c.calls++
	c.repo = repo
	c.agent = agent
	return roborev.AgentHealthResult{Agent: agent, Status: roborev.AgentHealthPassed}, nil
}

func agentTestServer(t *testing.T) (*Server, *appHealthChecker) {
	t.Helper()
	server := setupServer(t, &fakeService{})
	global := filepath.Join(t.TempDir(), "config.toml")
	config := "anthropic_api_key='SECRET_NEVER_RETURN'\nclaude_code_cmd='claude-custom'\n[review.subagents.claude]\nagent='claude'\nmodel='sonnet'\nreasoning='high'\ninstructions='SECRET_INSTRUCTION'\n"
	if err := os.WriteFile(global, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	checker := &appHealthChecker{}
	server.Agents = agentstatus.NewServiceWithSeams(
		&roborev.PanelResolver{GlobalConfigPath: global}, checker,
		appAgentResolver{paths: map[string]string{"claude-custom": `C:\fake\claude.exe`}}, appIdentityProbe{},
	)
	return server, checker
}

func agentCheckRequestBody(t *testing.T, agent, identity string) string {
	t.Helper()
	raw, err := json.Marshal(agentCheckBody{Agent: agent, RepositoryIdentity: identity})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestAgentsEndpointIsZeroQuotaAndReturnsOnlySafeConfiguredSubset(t *testing.T) {
	server, checker := agentTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET agents = %d: %s", recorder.Code, recorder.Body.String())
	}
	if checker.calls != 0 {
		t.Fatalf("GET agents ran a quota-consuming health check %d times", checker.calls)
	}
	if strings.Contains(recorder.Body.String(), "SECRET_") || strings.Contains(recorder.Body.String(), "anthropic") || strings.Contains(recorder.Body.String(), "instructions") {
		t.Fatalf("agent response exposed secret config: %s", recorder.Body.String())
	}
	var response agentsDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.RoboRevVersion != "v0.64.0" || response.RepositoryIdentity != gitx.RepositoryKey(`E:\repo`) || len(response.Configured) != 1 {
		t.Fatalf("agents response = %#v", response)
	}
	agent := response.Configured[0]
	if agent.Name != "claude" || agent.Agent != "claude-code" || agent.Command != "claude-custom" || !agent.Detected || agent.ResolvedPath == "" || agent.Health != "not_checked" {
		t.Fatalf("configured agent = %#v", agent)
	}
}

func TestAgentHealthRequiresMutationAuthorizationAndStrictCanonicalBody(t *testing.T) {
	server, checker := agentTestServer(t)

	request := httptest.NewRequest(http.MethodPost, "/api/agents/check", strings.NewReader(`{"agent":"claude-code"}`))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || checker.calls != 0 {
		t.Fatalf("missing CSRF = %d, calls=%d", recorder.Code, checker.calls)
	}

	for _, body := range []string{`{"agent":"claude"}`, `{"agent":"unknown","repositoryIdentity":"repo"}`, `{"agent":"claude-code","repositoryIdentity":"repo","command":"evil"}`, `{"agent":"claude-code","repositoryIdentity":"repo","prompt":"evil"}`} {
		request = httptest.NewRequest(http.MethodPost, "/api/agents/check", strings.NewReader(body))
		request.Header.Set("X-CSRF-Token", server.csrfToken)
		recorder = httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || checker.calls != 0 {
			t.Fatalf("unsafe health body %s = %d, calls=%d", body, recorder.Code, checker.calls)
		}
	}

	request = httptest.NewRequest(http.MethodPost, "/api/agents/check", strings.NewReader(agentCheckRequestBody(t, "claude", gitx.RepositoryKey(`E:\repo`))))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || checker.calls != 1 || checker.agent != "claude-code" || checker.repo != `E:\repo` {
		t.Fatalf("authorized health = %d, calls=%d agent=%q repo=%q body=%s", recorder.Code, checker.calls, checker.agent, checker.repo, recorder.Body.String())
	}
}

func TestAgentHealthRechecksCurrentScopeEligibilityBeforeRunner(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*Server)
		reason string
	}{
		{
			name: "allowlisted but unconfigured",
			setup: func(server *Server) {
				server.Agents.Panels = &roborev.PanelResolver{GlobalConfigPath: filepath.Join(t.TempDir(), "missing.toml")}
			},
			reason: "unconfigured",
		},
		{
			name: "configured but executable missing",
			setup: func(server *Server) {
				server.Agents.Resolver = appAgentResolver{}
			},
			reason: "missing executable",
		},
		{
			name: "discovery failure",
			setup: func(server *Server) {
				invalid := filepath.Join(t.TempDir(), "invalid.toml")
				if err := os.WriteFile(invalid, []byte("[invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
				server.Agents.Panels = &roborev.PanelResolver{GlobalConfigPath: invalid}
			},
			reason: "discovery failure",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, checker := agentTestServer(t)
			test.setup(server)
			request := httptest.NewRequest(http.MethodPost, "/api/agents/check", strings.NewReader(agentCheckRequestBody(t, "claude", gitx.RepositoryKey(`E:\repo`))))
			request.Header.Set("X-CSRF-Token", server.csrfToken)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusConflict || checker.calls != 0 {
				t.Fatalf("%s = status %d calls=%d body=%s", test.reason, recorder.Code, checker.calls, recorder.Body.String())
			}
		})
	}
}

func TestAgentHealthRejectsRepositorySwitchBeforeQuotaRunner(t *testing.T) {
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
	service := &fakeService{}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(global, []byte("[review.subagents.cursor]\nagent='cursor'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checker := &appHealthChecker{}
	server.Agents = agentstatus.NewServiceWithSeams(
		&roborev.PanelResolver{GlobalConfigPath: global}, checker,
		appAgentResolver{paths: map[string]string{"agent": `C:\fake\cursor.exe`, "grok": `C:\fake\grok.exe`}},
		switchingIdentityProbe{switchRepository: func() { _ = registry.Select(`E:\repo-b`) }},
	)
	request := httptest.NewRequest(http.MethodPost, "/api/agents/check", strings.NewReader(agentCheckRequestBody(t, "cursor", gitx.RepositoryKey(`E:\repo-a`))))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || checker.calls != 0 {
		t.Fatalf("repository switch accepted check: status=%d calls=%d body=%s", recorder.Code, checker.calls, recorder.Body.String())
	}
}

func TestAgentHealthRejectsStaleRequestRepositoryIdentity(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo-a`); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(context.Background(), `E:\repo-b`); err != nil {
		t.Fatal(err)
	}
	if err := registry.Select(`E:\repo-b`); err != nil {
		t.Fatal(err)
	}
	service := &fakeService{}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(global, []byte("[review.subagents.claude]\nagent='claude'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checker := &appHealthChecker{}
	server.Agents = agentstatus.NewServiceWithSeams(
		&roborev.PanelResolver{GlobalConfigPath: global}, checker,
		appAgentResolver{paths: map[string]string{"claude": `C:\fake\claude.exe`}}, appIdentityProbe{},
	)
	request := httptest.NewRequest(http.MethodPost, "/api/agents/check", strings.NewReader(agentCheckRequestBody(t, "claude", gitx.RepositoryKey(`E:\repo-a`))))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || checker.calls != 0 {
		t.Fatalf("stale repository identity accepted: status=%d calls=%d body=%s", recorder.Code, checker.calls, recorder.Body.String())
	}
}

type unsupportedAgentService struct {
	*fakeService
	version string
}

func (s *unsupportedAgentService) Installation(context.Context) roborev.Installation {
	return roborev.Installation{Installed: true, Version: s.version}
}

func TestAgentEndpointsFailClosedOutsidePinnedRoboRevVersion(t *testing.T) {
	base := &fakeService{}
	service := &unsupportedAgentService{fakeService: base, version: "v0.63.0"}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	checker := &appHealthChecker{}
	server.Agents = agentstatus.NewServiceWithSeams(nil, checker, appAgentResolver{}, appIdentityProbe{})
	request := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || checker.calls != 0 {
		t.Fatalf("unsupported GET agents = %d calls=%d", recorder.Code, checker.calls)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/agents/check", strings.NewReader(`{"agent":"claude"}`))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || checker.calls != 0 {
		t.Fatalf("unsupported agent check = %d calls=%d", recorder.Code, checker.calls)
	}
}
