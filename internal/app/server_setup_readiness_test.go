package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/setupreadiness"
)

func setupReadinessRequest(server *Server, identity, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/repositories/setup-readiness", strings.NewReader(body))
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	return request
}

func setupReadinessBody(t *testing.T, identity string) string {
	t.Helper()
	data, err := json.Marshal(struct {
		RepositoryIdentity string `json:"repositoryIdentity"`
	}{RepositoryIdentity: identity})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func newReadinessTestServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	root := t.TempDir()
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	service := &fakeService{}
	server, err := NewServerWithSetup(service, registry, "", operation.NewGate(), nil)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(root)
	if err != nil {
		t.Fatal(err)
	}
	return server, root, identity
}

func installReadinessTestChecker(server *Server, root string, calls *int) {
	server.SetupReadiness = setupreadiness.New(setupreadiness.Dependencies{
		Configuration: func(context.Context, string) (string, error) {
			if calls != nil {
				*calls++
			}
			return ".roborev", nil
		},
		Snapshot: func(context.Context, string, string) (setupreadiness.SnapshotObservation, error) {
			if calls != nil {
				*calls++
			}
			return setupreadiness.SnapshotObservation{Ignored: true}, nil
		},
		Hooks: func(context.Context, string, string) (setupreadiness.GitHooks, error) {
			if calls != nil {
				*calls++
			}
			return setupreadiness.GitHooksReady, nil
		},
		Daemon: func(context.Context) (setupreadiness.DaemonObservation, error) {
			if calls != nil {
				*calls++
			}
			return setupreadiness.DaemonObservation{State: setupreadiness.DaemonOnline, InventoryAvailable: true, InventoryRoots: []string{root}}, nil
		},
	})
}

func TestRepositorySetupReadinessReturnsExactNestedPathFreeDTO(t *testing.T) {
	server, root, identity := newReadinessTestServer(t)
	var calls int
	installReadinessTestChecker(server, root, &calls)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, setupReadinessRequest(server, identity, setupReadinessBody(t, identity)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control=%q", response.Header().Get("Cache-Control"))
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"version", "repositoryIdentity", "overall", "configuration", "snapshot", "gitHooks", "daemon", "tracking"}
	if len(body) != len(wantKeys) {
		t.Fatalf("body keys=%#v", body)
	}
	for _, key := range wantKeys {
		if _, ok := body[key]; !ok {
			t.Fatalf("missing key %q: %s", key, response.Body.String())
		}
	}
	for _, key := range []string{"configuration", "snapshot", "gitHooks", "daemon", "tracking"} {
		value, ok := body[key].(map[string]any)
		if !ok || len(value) != 1 {
			t.Fatalf("component %q=%#v", key, body[key])
		}
		if _, ok := value["status"]; !ok {
			t.Fatalf("component %q missing status", key)
		}
	}
	pathFreeAlias, ok := body["repositoryIdentity"].(string)
	if !ok || !snapshotIgnoreAliasPattern.MatchString(pathFreeAlias) || pathFreeAlias == identity || strings.Contains(pathFreeAlias, root) {
		t.Fatalf("readiness identity is not a path-free snapshot alias: %q legacy=%q root=%q", pathFreeAlias, identity, root)
	}
	privateMarkers := []string{root, `C:\Users\private\source\secret-project`, `C:\Users\private\.roborev`, `C:\fake\very-secret\roborev.exe`, "PRIVATE_SNAPSHOT_DIR_MARKER", "PRIVATE_GITIGNORE_MARKER", "PRIVATE_STDERR_MARKER", "stderr", "command"}
	for _, marker := range privateMarkers {
		if strings.Contains(response.Body.String(), marker) {
			t.Fatalf("private marker %q leaked: %s", marker, response.Body.String())
		}
	}
	if calls != 4 {
		t.Fatalf("checker calls=%d want 4", calls)
	}
}

func TestRepositorySetupReadinessSanitizesComponentErrors(t *testing.T) {
	server, root, identity := newReadinessTestServer(t)
	privateMarkers := []string{
		root,
		`C:\Users\private\source\secret-project`,
		`C:\Users\private\.roborev`,
		`C:\fake\very-secret\roborev.exe`,
		"PRIVATE_SNAPSHOT_DIR_MARKER",
		"PRIVATE_GITIGNORE_MARKER",
		"PRIVATE_STDERR_MARKER",
	}
	server.SetupReadiness = setupreadiness.New(setupreadiness.Dependencies{
		Configuration: func(context.Context, string) (string, error) {
			return "", errors.New(strings.Join(privateMarkers, " "))
		},
		Hooks: func(context.Context, string, string) (setupreadiness.GitHooks, error) {
			return setupreadiness.GitHooksReady, nil
		},
		Daemon: func(context.Context) (setupreadiness.DaemonObservation, error) {
			return setupreadiness.DaemonObservation{State: setupreadiness.DaemonOffline}, nil
		},
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, setupReadinessRequest(server, identity, setupReadinessBody(t, identity)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for _, marker := range privateMarkers {
		if strings.Contains(response.Body.String(), marker) {
			t.Fatalf("private marker %q leaked: %s", marker, response.Body.String())
		}
	}
}

func TestRepositorySetupReadinessRejectsAuthorityAndSelectionBeforeProbes(t *testing.T) {
	server, root, identity := newReadinessTestServer(t)
	var calls int
	installReadinessTestChecker(server, root, &calls)
	tests := []struct {
		name    string
		request func() *http.Request
		want    int
	}{
		{name: "missing fetch metadata", request: func() *http.Request {
			r := setupReadinessRequest(server, identity, setupReadinessBody(t, identity))
			r.Header.Del("Sec-Fetch-Site")
			return r
		}, want: http.StatusForbidden},
		{name: "csrf missing", request: func() *http.Request {
			r := setupReadinessRequest(server, identity, setupReadinessBody(t, identity))
			r.Header.Del("X-CSRF-Token")
			return r
		}, want: http.StatusForbidden},
		{name: "csrf duplicate", request: func() *http.Request {
			r := setupReadinessRequest(server, identity, setupReadinessBody(t, identity))
			r.Header.Add("X-CSRF-Token", server.csrfToken)
			return r
		}, want: http.StatusForbidden},
		{name: "query", request: func() *http.Request {
			r := setupReadinessRequest(server, identity, setupReadinessBody(t, identity))
			r.URL.RawQuery = "unexpected=1"
			return r
		}, want: http.StatusBadRequest},
		{name: "missing identity", request: func() *http.Request {
			return setupReadinessRequest(server, identity, `{}`)
		}, want: http.StatusBadRequest},
		{name: "empty identity", request: func() *http.Request {
			return setupReadinessRequest(server, identity, `{"repositoryIdentity":""}`)
		}, want: http.StatusBadRequest},
		{name: "unknown body field", request: func() *http.Request {
			return setupReadinessRequest(server, identity, `{"repositoryIdentity":"opaque","path":"private-marker"}`)
		}, want: http.StatusBadRequest},
		{name: "duplicate body field", request: func() *http.Request {
			return setupReadinessRequest(server, identity, `{"repositoryIdentity":"first","repositoryIdentity":"other"}`)
		}, want: http.StatusBadRequest},
		{name: "trimmed identity", request: func() *http.Request {
			return setupReadinessRequest(server, " "+identity, `{"repositoryIdentity":" bad "}`)
		}, want: http.StatusBadRequest},
		{name: "unknown identity", request: func() *http.Request {
			return setupReadinessRequest(server, "unknown", `{"repositoryIdentity":"unknown"}`)
		}, want: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, test.request())
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if calls != 0 {
		t.Fatalf("downstream checker calls=%d want zero", calls)
	}
}

func TestRepositorySetupReadinessRejectsSelectionSwitchAndFreshRootDrift(t *testing.T) {
	server, root, identity := newReadinessTestServer(t)
	var calls int
	installReadinessTestChecker(server, root, &calls)
	other := t.TempDir()
	if _, err := server.Registry.Add(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if err := server.Registry.Select(other); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, setupReadinessRequest(server, identity, setupReadinessBody(t, identity)))
	if response.Code != http.StatusConflict || calls != 0 {
		t.Fatalf("selection status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}

	inspector := &mutableInspector{repository: gitx.Repository{Path: root}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	driftIdentity, err := registry.Identity(root)
	if err != nil {
		t.Fatal(err)
	}
	inspector.repository.Path = other
	driftServer, err := NewServerWithSetup(&fakeService{}, registry, "", operation.NewGate(), nil)
	if err != nil {
		t.Fatal(err)
	}
	installReadinessTestChecker(driftServer, root, &calls)
	response = httptest.NewRecorder()
	driftServer.Handler().ServeHTTP(response, setupReadinessRequest(driftServer, driftIdentity, setupReadinessBody(t, driftIdentity)))
	if response.Code != http.StatusConflict || calls != 0 {
		t.Fatalf("drift status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
}

func TestRepositorySetupReadinessMethodAndBodyFailuresDoNotMutateGate(t *testing.T) {
	server, root, identity := newReadinessTestServer(t)
	var calls int
	installReadinessTestChecker(server, root, &calls)
	release, err := server.Gate.AcquireMutation()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	get := httptest.NewRequest(http.MethodGet, "/api/repositories/setup-readiness", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, get)
	if response.Code != http.StatusNotFound || calls != 0 || !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("method status=%d calls=%d body=%s headers=%v", response.Code, calls, response.Body.String(), response.Header())
	}
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, setupReadinessRequest(server, identity, setupReadinessBody(t, identity)+" trailing"))
	if response.Code != http.StatusBadRequest || calls != 0 {
		t.Fatalf("body status=%d calls=%d", response.Code, calls)
	}
}
