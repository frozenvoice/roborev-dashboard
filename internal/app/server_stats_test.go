package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/gitx"
	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/stats"
)

type statsHTTPService struct {
	fakeService
	version          string
	summaryCaps      bool
	costCaps         bool
	running          bool
	healthState      roborev.DaemonHealthProbeState
	healthCondition  roborev.DaemonHealthCondition
	daemonVersion    string
	daemonVersionSet bool
	healthError      string
	status           json.RawMessage
	installations    int
	summaryCalls     int
	costCalls        int
	lastRepo         string
	lastBranch       string
	lastWindow       string
	summaryErr       error
	costErr          error
}

type statsHealthProductionRunner struct {
	commands int
}

func (r *statsHealthProductionRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands++
	if len(command.Args) > 0 && command.Args[0] == "version" {
		return proc.Result{Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
	}
	return proc.Result{}, nil
}

func (s *statsHTTPService) Installation(context.Context) roborev.Installation {
	s.installations++
	healthState := s.healthState
	if healthState == "" {
		healthState = roborev.DaemonHealthOffline
		if s.running {
			healthState = roborev.DaemonHealthOnline
		}
	}
	healthCondition := s.healthCondition
	if healthCondition == "" && healthState == roborev.DaemonHealthOnline {
		healthCondition = roborev.DaemonConditionHealthy
	}
	status := s.status
	if status == nil {
		status = func() json.RawMessage {
			if s.running {
				return json.RawMessage(`{"running":true}`)
			}
			return json.RawMessage(`{"running":false}`)
		}()
	}
	daemonVersion := s.daemonVersion
	if !s.daemonVersionSet {
		daemonVersion = s.version
	}
	return roborev.Installation{
		Installed: s.version != "",
		Version:   s.version,
		Capabilities: roborev.Capabilities{
			SummaryStats: s.summaryCaps,
			CostStats:    s.costCaps,
		},
		Status:          status,
		Error:           s.healthError,
		DaemonHealth:    healthState,
		DaemonCondition: healthCondition,
		DaemonVersion:   daemonVersion,
	}
}

func (s *statsHTTPService) Summary(_ context.Context, repo, branch, window string) (stats.Summary, error) {
	s.summaryCalls++
	s.lastRepo, s.lastBranch, s.lastWindow = repo, branch, window
	if s.summaryErr != nil {
		return stats.Summary{}, s.summaryErr
	}
	return stats.Summary{RepoPath: repo, Branch: branch, Agents: []stats.Agent{}, JobTypes: []stats.JobType{}, Repos: []stats.Repo{}, Failures: stats.Failures{Errors: map[string]int{}}}, nil
}

func (s *statsHTTPService) Cost(_ context.Context, repo, branch string) (stats.Cost, error) {
	s.costCalls++
	s.lastRepo, s.lastBranch = repo, branch
	if s.costErr != nil {
		return stats.Cost{}, s.costErr
	}
	return stats.Cost{TotalUSD: 0, JobsWithCost: 1, JobsTotal: 1, Complete: true}, nil
}

type statsCountingInspector struct {
	calls int
}

func (i *statsCountingInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	i.calls++
	return gitx.Repository{Path: path, Branch: "main", Head: "abc"}, nil
}

type mismatchStatsInspector struct {
	calls int
}

func (i *mismatchStatsInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	i.calls++
	if i.calls > 1 {
		return gitx.Repository{Path: `E:\other`, Branch: "main", Head: "abc"}, nil
	}
	return gitx.Repository{Path: path, Branch: "main", Head: "abc"}, nil
}

func setupStatsServer(t *testing.T, service *statsHTTPService, inspector RepositoryInspector) (*Server, string) {
	t.Helper()
	if inspector == nil {
		inspector = fakeInspector{}
	}
	registry := NewRegistry(inspector, "")
	path := `E:\한글 프로젝트\repo`
	if _, err := registry.Add(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(path)
	if err != nil {
		t.Fatal(err)
	}
	return server, identity
}

func nativeStatsRequest(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	return request
}

func statsIdentityQuery(identity string) string { return url.QueryEscape(identity) }

func TestStatsSummaryUsesOpaqueIdentityAndExactScope(t *testing.T) {
	service := &statsHTTPService{version: "v0.64.0", summaryCaps: true, costCaps: true, running: true}
	inspector := &statsCountingInspector{}
	server, identity := setupStatsServer(t, service, inspector)
	inspector.calls = 0
	request := nativeStatsRequest("/api/stats/summary?repositoryIdentity=" + statsIdentityQuery(identity) + "&window=30d&branchScope=all")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.summaryCalls != 1 {
		t.Fatalf("status=%d summaryCalls=%d body=%s", response.Code, service.summaryCalls, response.Body.String())
	}
	if inspector.calls != 1 || service.lastRepo != `E:\한글 프로젝트\repo` || service.lastBranch != "" || service.lastWindow != "30d" {
		t.Fatalf("scope repo=%q branch=%q window=%q inspections=%d", service.lastRepo, service.lastBranch, service.lastWindow, inspector.calls)
	}
	if !strings.Contains(response.Body.String(), `"branchScope":"all"`) || !strings.Contains(response.Body.String(), `"window":"30d"`) {
		t.Fatalf("unexpected envelope: %s", response.Body.String())
	}
}

func TestStatsCurrentBranchFreshInspectionAndDetachedRejection(t *testing.T) {
	service := &statsHTTPService{version: "v0.64.0", summaryCaps: true, costCaps: true, running: true}
	inspector := &statsCountingInspector{}
	server, identity := setupStatsServer(t, service, inspector)
	inspector.calls = 0
	request := nativeStatsRequest("/api/stats/cost?repositoryIdentity=" + statsIdentityQuery(identity) + "&branchScope=current")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.costCalls != 1 || inspector.calls != 1 || service.lastBranch != "main" {
		t.Fatalf("status=%d costCalls=%d inspections=%d branch=%q", response.Code, service.costCalls, inspector.calls, service.lastBranch)
	}
	// A fresh inspector returning detached must reject before the service call.
	detached := &detachedStatsInspector{}
	detachedServer, detachedIdentity := setupStatsServer(t, service, detached)
	service.costCalls = 0
	detachedResponse := httptest.NewRecorder()
	detachedServer.Handler().ServeHTTP(detachedResponse, nativeStatsRequest("/api/stats/cost?repositoryIdentity="+statsIdentityQuery(detachedIdentity)+"&branchScope=current"))
	if detachedResponse.Code != http.StatusConflict || service.costCalls != 0 {
		t.Fatalf("detached status=%d costCalls=%d", detachedResponse.Code, service.costCalls)
	}

	// A literal branch named "detached" is still an attached current branch
	// and must be passed to RoboRev as the authoritative branch scope.
	literal := &literalDetachedStatsInspector{}
	literalServer, literalIdentity := setupStatsServer(t, service, literal)
	service.costCalls = 0
	literalResponse := httptest.NewRecorder()
	literalServer.Handler().ServeHTTP(literalResponse, nativeStatsRequest("/api/stats/cost?repositoryIdentity="+statsIdentityQuery(literalIdentity)+"&branchScope=current"))
	if literalResponse.Code != http.StatusOK || service.costCalls != 1 || service.lastBranch != "detached" {
		t.Fatalf("literal detached status=%d costCalls=%d branch=%q body=%s", literalResponse.Code, service.costCalls, service.lastBranch, literalResponse.Body.String())
	}

	// Detached checkouts remain valid for all-branches scope, which has no
	// branch argument to pass through.
	service.summaryCalls = 0
	allResponse := httptest.NewRecorder()
	detachedServer.Handler().ServeHTTP(allResponse, nativeStatsRequest("/api/stats/summary?repositoryIdentity="+statsIdentityQuery(detachedIdentity)+"&window=7d&branchScope=all"))
	if allResponse.Code != http.StatusOK || service.summaryCalls != 1 || service.lastBranch != "" {
		t.Fatalf("all detached status=%d summaryCalls=%d branch=%q body=%s", allResponse.Code, service.summaryCalls, service.lastBranch, allResponse.Body.String())
	}
}

func TestStatsRejectsInspectionRootMismatchBeforeServiceForAllAndCurrent(t *testing.T) {
	service := &statsHTTPService{version: "v0.64.0", summaryCaps: true, costCaps: true, running: true}
	inspector := &mismatchStatsInspector{}
	server, identity := setupStatsServer(t, service, inspector)
	cases := []string{
		"/api/stats/summary?repositoryIdentity=" + statsIdentityQuery(identity) + "&window=7d&branchScope=all",
		"/api/stats/cost?repositoryIdentity=" + statsIdentityQuery(identity) + "&branchScope=all",
		"/api/stats/summary?repositoryIdentity=" + statsIdentityQuery(identity) + "&window=7d&branchScope=current",
		"/api/stats/cost?repositoryIdentity=" + statsIdentityQuery(identity) + "&branchScope=current",
	}
	for _, target := range cases {
		service.summaryCalls, service.costCalls = 0, 0
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, nativeStatsRequest(target))
		if response.Code != http.StatusForbidden || service.summaryCalls != 0 || service.costCalls != 0 {
			t.Fatalf("target=%s status=%d summaryCalls=%d costCalls=%d body=%s", target, response.Code, service.summaryCalls, service.costCalls, response.Body.String())
		}
	}
}

type detachedStatsInspector struct{}

func (detachedStatsInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	return gitx.Repository{Path: path, Branch: "detached", Detached: true, Head: "abc"}, nil
}

type literalDetachedStatsInspector struct{}

func (literalDetachedStatsInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	return gitx.Repository{Path: path, Branch: "detached", Detached: false, Head: "abc"}, nil
}

func TestStatsAuthAndQueryRejectBeforeSideEffects(t *testing.T) {
	service := &statsHTTPService{version: "v0.64.0", summaryCaps: true, costCaps: true, running: true}
	inspector := &statsCountingInspector{}
	server, identity := setupStatsServer(t, service, inspector)
	valid := "/api/stats/summary?repositoryIdentity=" + statsIdentityQuery(identity) + "&window=7d&branchScope=all"
	cases := []struct {
		name string
		make func() *http.Request
	}{
		{name: "missing fetch metadata", make: func() *http.Request { return httptest.NewRequest(http.MethodGet, valid, nil) }},
		{name: "foreign origin", make: func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, valid, nil)
			request.Header.Set("Origin", "https://evil.example")
			return request
		}},
		{name: "null origin", make: func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, valid, nil)
			request.Header.Set("Origin", "null")
			return request
		}},
		{name: "malformed origin", make: func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, valid, nil)
			request.Header.Set("Origin", "http://[")
			return request
		}},
		{name: "duplicate origin", make: func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, valid, nil)
			request.Header.Add("Origin", "http://"+request.Host)
			request.Header.Add("Origin", "http://"+request.Host)
			return request
		}},
		{name: "wrong fetch metadata", make: func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, valid, nil)
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			request.Header.Set("Sec-Fetch-Mode", "navigate")
			request.Header.Set("Sec-Fetch-Dest", "document")
			return request
		}},
		{name: "duplicate fetch metadata", make: func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, valid, nil)
			request.Header.Add("Sec-Fetch-Site", "same-origin")
			request.Header.Add("Sec-Fetch-Site", "same-origin")
			request.Header.Set("Sec-Fetch-Mode", "cors")
			request.Header.Set("Sec-Fetch-Dest", "empty")
			return request
		}},
		{name: "extra path field", make: func() *http.Request { return nativeStatsRequest(valid + "&repoPath=E:\\secret") }},
		{name: "duplicate identity", make: func() *http.Request {
			return nativeStatsRequest("/api/stats/summary?repositoryIdentity=" + statsIdentityQuery(identity) + "&repositoryIdentity=" + statsIdentityQuery(identity) + "&window=7d&branchScope=all")
		}},
		{name: "blank identity", make: func() *http.Request {
			return nativeStatsRequest("/api/stats/summary?repositoryIdentity=&window=7d&branchScope=all")
		}},
		{name: "unknown identity", make: func() *http.Request {
			return nativeStatsRequest("/api/stats/summary?repositoryIdentity=unknown&window=7d&branchScope=all")
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			beforeInstall, beforeCalls, beforeInspect := service.installations, service.summaryCalls, inspector.calls
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, test.make())
			if response.Code != http.StatusForbidden && test.name != "extra path field" && test.name != "duplicate identity" && test.name != "blank identity" && test.name != "unknown identity" {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if (test.name == "extra path field" || test.name == "duplicate identity" || test.name == "blank identity") && response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if service.installations != beforeInstall || service.summaryCalls != beforeCalls || inspector.calls != beforeInspect {
				t.Fatalf("side effect before reject: installs %d->%d calls %d->%d inspect %d->%d", beforeInstall, service.installations, beforeCalls, service.summaryCalls, beforeInspect, inspector.calls)
			}
		})
	}
}

func TestStatsExplicitExactOriginIsAllowed(t *testing.T) {
	service := &statsHTTPService{version: "v0.64.0", summaryCaps: true, costCaps: true, running: true}
	server, identity := setupStatsServer(t, service, nil)
	target := "/api/stats/cost?repositoryIdentity=" + statsIdentityQuery(identity) + "&branchScope=all"
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Origin", "http://"+request.Host)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.costCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, service.costCalls, response.Body.String())
	}
}

func TestStatsCapabilityDaemonAndErrorsAreIndependentAndStable(t *testing.T) {
	service := &statsHTTPService{version: "v0.64.0", summaryCaps: false, costCaps: true, running: true}
	server, identity := setupStatsServer(t, service, nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, nativeStatsRequest("/api/stats/summary?repositoryIdentity="+statsIdentityQuery(identity)+"&window=7d&branchScope=all"))
	if response.Code != http.StatusServiceUnavailable || service.summaryCalls != 0 {
		t.Fatalf("unsupported summary status=%d calls=%d", response.Code, service.summaryCalls)
	}
	service.summaryCaps = true
	service.running = false
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, nativeStatsRequest("/api/stats/summary?repositoryIdentity="+statsIdentityQuery(identity)+"&window=7d&branchScope=all"))
	if response.Code != http.StatusServiceUnavailable || service.summaryCalls != 0 {
		t.Fatalf("offline summary status=%d calls=%d", response.Code, service.summaryCalls)
	}
	service.running = true
	service.summaryErr = context.DeadlineExceeded
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, nativeStatsRequest("/api/stats/summary?repositoryIdentity="+statsIdentityQuery(identity)+"&window=7d&branchScope=all"))
	if response.Code != http.StatusGatewayTimeout || strings.Contains(response.Body.String(), "deadline") {
		t.Fatalf("timeout status=%d body=%s", response.Code, response.Body.String())
	}
	service.summaryErr = errors.New("secret stderr must not escape")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, nativeStatsRequest("/api/stats/summary?repositoryIdentity="+statsIdentityQuery(identity)+"&window=7d&branchScope=all"))
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "secret stderr") {
		t.Fatalf("stable error status=%d body=%s", response.Code, response.Body.String())
	}
	service.summaryErr = roborev.ErrStatsOffline
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, nativeStatsRequest("/api/stats/summary?repositoryIdentity="+statsIdentityQuery(identity)+"&window=7d&branchScope=all"))
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "offline") {
		t.Fatalf("offline status=%d body=%s", response.Code, response.Body.String())
	}
	service.summaryErr = errors.New("malformed RoboRev daemon health: secret protocol detail")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, nativeStatsRequest("/api/stats/summary?repositoryIdentity="+statsIdentityQuery(identity)+"&window=7d&branchScope=all"))
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "secret protocol detail") {
		t.Fatalf("malformed health status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestStatsTypedHealthStateControlsPreflightWithoutErrorTextParsing(t *testing.T) {
	service := &statsHTTPService{version: "v0.64.0", summaryCaps: true, costCaps: true, running: false}
	server, identity := setupStatsServer(t, service, nil)
	summaryTarget := "/api/stats/summary?repositoryIdentity=" + statsIdentityQuery(identity) + "&window=7d&branchScope=all"
	costTarget := "/api/stats/cost?repositoryIdentity=" + statsIdentityQuery(identity) + "&branchScope=all"

	for _, test := range []struct {
		name             string
		state            roborev.DaemonHealthProbeState
		condition        roborev.DaemonHealthCondition
		daemonVersion    string
		setDaemonVersion bool
		error            string
		want             int
	}{
		{name: "offline state wins over malformed-looking error", state: roborev.DaemonHealthOffline, condition: roborev.DaemonConditionUnknown, error: "malformed RoboRev daemon health: offline", want: http.StatusServiceUnavailable},
		{name: "invalid state ignores banana error", state: roborev.DaemonHealthInvalid, condition: roborev.DaemonConditionUnknown, error: "banana", want: http.StatusBadGateway},
		{name: "unknown state is unavailable", state: roborev.DaemonHealthUnknown, condition: roborev.DaemonConditionUnknown, error: "banana", want: http.StatusBadGateway},
		{name: "online unknown condition is unavailable", state: roborev.DaemonHealthOnline, condition: roborev.DaemonConditionUnknown, error: "banana", want: http.StatusBadGateway},
		{name: "unhealthy condition is unavailable", state: roborev.DaemonHealthOnline, condition: roborev.DaemonConditionUnhealthy, error: "banana", want: http.StatusServiceUnavailable},
		{name: "healthy missing daemon version", state: roborev.DaemonHealthOnline, condition: roborev.DaemonConditionHealthy, setDaemonVersion: true, daemonVersion: "", error: "banana", want: http.StatusServiceUnavailable},
		{name: "healthy invalid daemon version", state: roborev.DaemonHealthOnline, condition: roborev.DaemonConditionHealthy, setDaemonVersion: true, daemonVersion: "0.64.0", error: "banana", want: http.StatusServiceUnavailable},
		{name: "healthy mismatched daemon version", state: roborev.DaemonHealthOnline, condition: roborev.DaemonConditionHealthy, setDaemonVersion: true, daemonVersion: "v0.63.0", error: "banana", want: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			service.healthState = test.state
			service.healthCondition = test.condition
			service.daemonVersion = test.daemonVersion
			service.daemonVersionSet = test.setDaemonVersion
			service.healthError = test.error
			for _, target := range []string{summaryTarget, costTarget} {
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, nativeStatsRequest(target))
				if response.Code != test.want || service.summaryCalls != 0 || service.costCalls != 0 {
					t.Fatalf("target=%s status=%d summaryCalls=%d costCalls=%d body=%s", target, response.Code, service.summaryCalls, service.costCalls, response.Body.String())
				}
			}
		})
	}

	// The browser-facing Status JSON is irrelevant to the typed Stats gate once
	// the probe says the daemon is reachable and healthy.
	service.healthState = roborev.DaemonHealthOnline
	service.healthCondition = roborev.DaemonConditionHealthy
	service.daemonVersionSet = false
	service.healthError = "banana"
	service.status = json.RawMessage(`{"running":`)
	service.running = true
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, nativeStatsRequest(summaryTarget))
	if response.Code != http.StatusOK || service.summaryCalls != 1 {
		t.Fatalf("malformed status=%d calls=%d body=%s", response.Code, service.summaryCalls, response.Body.String())
	}
}

func TestStatsMalformedProductionHealthReturns502BeforeStatsCommands(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{name: "top-level null", body: `null`, want: http.StatusBadGateway},
		{name: "healthy null", body: `{"healthy":null}`, want: http.StatusBadGateway},
		{name: "reachable unhealthy", body: `{"healthy":false}`, want: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))
			defer health.Close()
			runner := &statsHealthProductionRunner{}
			client, err := roborev.NewClient("roborev", runner, health.URL)
			if err != nil {
				t.Fatal(err)
			}
			registry := NewRegistry(fakeInspector{}, "")
			path := `E:\repo`
			if _, err := registry.Add(context.Background(), path); err != nil {
				t.Fatal(err)
			}
			server, err := NewServer(client, registry, "")
			if err != nil {
				t.Fatal(err)
			}
			identity, err := registry.Identity(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{
				"/api/stats/summary?repositoryIdentity=" + statsIdentityQuery(identity) + "&window=7d&branchScope=all",
				"/api/stats/cost?repositoryIdentity=" + statsIdentityQuery(identity) + "&branchScope=all",
			} {
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, nativeStatsRequest(target))
				if response.Code != test.want {
					t.Fatalf("target=%s status=%d want=%d body=%s", target, response.Code, test.want, response.Body.String())
				}
			}
			// Installation needs exactly one version probe; no stats command may
			// run after malformed or unhealthy health responses.
			if runner.commands != 1 {
				t.Fatalf("process commands=%d, want only one version probe", runner.commands)
			}
		})
	}
}

func TestStatsSummaryBrowserProjectionRedactsRepositoryPaths(t *testing.T) {
	privatePath := `E:\private\stats-marker`
	encoded, err := json.Marshal(browserStatsSummary(stats.Summary{
		RepoPath: privatePath,
		Repos:    []stats.Repo{{Name: "safe-name", Path: privatePath, Total: 2}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	if strings.Contains(body, privatePath) || strings.Contains(body, "repo_path") || strings.Contains(body, `"path"`) {
		t.Fatalf("stats browser projection leaked repository path: %s", body)
	}
	if !strings.Contains(body, "safe-name") || !strings.Contains(body, `"total":2`) {
		t.Fatalf("path-free aggregate row missing: %s", body)
	}
}
