package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/operation"
	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/setup"
)

type setupRunner struct{}

func (setupRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	if len(command.Args) > 0 && command.Args[0] == "version" {
		return proc.Result{Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
	}
	return proc.Result{}, nil
}

type setupVersionRunner struct{ version string }

func (r setupVersionRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	if len(command.Args) > 0 && command.Args[0] == "version" {
		return proc.Result{Stdout: `{"name":"roborev","version":"` + r.version + `"}`}, nil
	}
	return proc.Result{}, nil
}

type setupBlockedRunner struct{}

func (setupBlockedRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	if len(command.Args) > 0 && command.Args[0] == "version" {
		return proc.Result{}, syscall.Errno(1260)
	}
	return proc.Result{}, nil
}

type setupInstaller struct{ target string }

func (i setupInstaller) Target() string                          { return i.target }
func (i setupInstaller) Install(context.Context) (string, error) { return i.target, nil }

type retryLifecycleRunner struct {
	started  chan struct{}
	release  chan struct{}
	canceled chan struct{}
}

func (r *retryLifecycleRunner) Run(ctx context.Context, command proc.Command) (proc.Result, error) {
	if len(command.Args) > 0 && command.Args[0] == "version" {
		return proc.Result{Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
	}
	if len(command.Args) == 2 && command.Args[0] == "daemon" && command.Args[1] == "start" {
		close(r.started)
		select {
		case <-ctx.Done():
			close(r.canceled)
			return proc.Result{}, ctx.Err()
		case <-r.release:
			return proc.Result{}, nil
		}
	}
	return proc.Result{}, nil
}

type requestLifecycleSetupInstaller struct {
	target   string
	started  chan struct{}
	release  chan struct{}
	canceled chan struct{}
}

func (i *requestLifecycleSetupInstaller) Target() string { return i.target }
func (i *requestLifecycleSetupInstaller) Install(ctx context.Context) (string, error) {
	close(i.started)
	select {
	case <-ctx.Done():
		close(i.canceled)
		return "", ctx.Err()
	case <-i.release:
		if err := os.WriteFile(i.target, []byte("fake RoboRev binary"), 0o700); err != nil {
			return "", err
		}
		return i.target, nil
	}
}

type countingSetupInstaller struct {
	target string
	calls  atomic.Int32
}

func (i *countingSetupInstaller) Target() string { return i.target }
func (i *countingSetupInstaller) Install(context.Context) (string, error) {
	i.calls.Add(1)
	return i.target, nil
}

func setupHTTPServer(t *testing.T) (*Server, *roborev.Client) {
	t.Helper()
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	client, err := roborev.NewClient("", setupRunner{}, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := setup.NewManager(client, setupInstaller{target: `C:\managed\roborev.exe`})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithSetup(client, registry, "", operation.NewGate(), manager)
	if err != nil {
		t.Fatal(err)
	}
	return server, client
}

func TestSetupInstallEndpointRequiresCSRFAndExactlyEmptyBody(t *testing.T) {
	server, _ := setupHTTPServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/setup/roborev/install", strings.NewReader("{}"))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/setup/roborev/install", strings.NewReader("{}"))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("nonempty body status = %d", recorder.Code)
	}
}

func TestSetupInstallReturnsAcceptedStateAndStatusEndpoint(t *testing.T) {
	server, _ := setupHTTPServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/setup/roborev/install", nil)
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("install status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var state setup.State
	if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil || !state.OperationActive {
		t.Fatalf("state = %#v, err=%v", state, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/setup/roborev", nil)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("setup status = %d", recorder.Code)
	}
}

func TestSetupInstallHTTPResponseDoesNotCancelAcceptedOperation(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true}`))
	}))
	defer health.Close()
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	client, err := roborev.NewClient("", setupRunner{}, health.URL)
	if err != nil {
		t.Fatal(err)
	}
	installer := &requestLifecycleSetupInstaller{
		target:   filepath.Join(t.TempDir(), "roborev.exe"),
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	manager, err := setup.NewManager(client, installer)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithSetup(client, registry, "", operation.NewGate(), manager)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	request, err := http.NewRequest(http.MethodPost, httpServer.URL+"/api/setup/roborev/install", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted {
		response.Body.Close()
		t.Fatalf("install status = %d", response.StatusCode)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	<-installer.started
	select {
	case <-installer.canceled:
		t.Fatal("accepted install canceled when HTTP request ended")
	case <-time.After(100 * time.Millisecond):
	}
	close(installer.release)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		getResponse, getErr := http.Get(httpServer.URL + "/api/setup/roborev")
		if getErr == nil {
			var state setup.State
			decodeErr := json.NewDecoder(getResponse.Body).Decode(&state)
			_ = getResponse.Body.Close()
			if decodeErr == nil && getResponse.StatusCode == http.StatusOK && state.Phase == setup.PhaseReady && !state.OperationActive {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("accepted install did not reach ready: %#v", manager.Status(context.Background()))
}

func TestSetupInstallConflictsWithSharedMutationGate(t *testing.T) {
	server, _ := setupHTTPServer(t)
	release, err := server.Gate.AcquireMutation()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	request := httptest.NewRequest(http.MethodPost, "/api/setup/roborev/install", nil)
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestSetupMutationsRejectWrongOriginHostAndNonRetryableState(t *testing.T) {
	server, _ := setupHTTPServer(t)
	server.AllowedHost = "dashboard.test"
	request := httptest.NewRequest(http.MethodPost, "/api/setup/roborev/install", nil)
	request.Host = "evil.test"
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("host status = %d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/setup/roborev/install", nil)
	request.Host = "dashboard.test"
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	request.Header.Set("Origin", "https://evil.test")
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("origin status = %d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/setup/roborev/install", strings.NewReader(" \n"))
	request.Host = "dashboard.test"
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("whitespace body status = %d", recorder.Code)
	}

	unsupported := unsupportedSetupServer(t)
	for _, path := range []string{"/api/setup/roborev/install", "/api/setup/roborev/retry"} {
		request = httptest.NewRequest(http.MethodPost, path, nil)
		request.Header.Set("X-CSRF-Token", unsupported.csrfToken)
		recorder = httptest.NewRecorder()
		unsupported.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusConflict {
			t.Fatalf("%s status = %d", path, recorder.Code)
		}
	}
}

func TestSetupRetryEndpointStartsHealthyExistingDaemonWithoutDownload(t *testing.T) {
	var healthCalls atomic.Int32
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthCalls.Add(1) == 1 {
			http.Error(w, "daemon stopped", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true}`))
	}))
	defer health.Close()
	binary := filepath.Join(t.TempDir(), "roborev.exe")
	if err := os.WriteFile(binary, []byte("candidate"), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := roborev.NewClient(binary, setupVersionRunner{version: "v0.64.0"}, health.URL)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	installer := &countingSetupInstaller{target: filepath.Join(t.TempDir(), "managed.exe")}
	manager, err := setup.NewManager(client, installer)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithSetup(client, registry, "", operation.NewGate(), manager)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/setup/roborev", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("setup status = %d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/setup/roborev/retry", nil)
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var state setup.State
	if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !state.OperationActive && state.Phase != setup.PhaseReady {
		t.Fatalf("retry state = %#v", state)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state = manager.Status(context.Background())
		if state.Phase == setup.PhaseReady && !state.OperationActive {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if state.Phase != setup.PhaseReady || state.DaemonRunning != true {
		t.Fatalf("final retry state = %#v", state)
	}
	if got := installer.calls.Load(); got != 0 {
		t.Fatalf("installer calls = %d", got)
	}
}

func TestSetupRetryHTTPResponseDoesNotCancelDaemonRecovery(t *testing.T) {
	var healthy atomic.Bool
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			http.Error(w, "daemon stopped", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true}`))
	}))
	defer health.Close()
	binary := filepath.Join(t.TempDir(), "roborev.exe")
	if err := os.WriteFile(binary, []byte("fake RoboRev binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &retryLifecycleRunner{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	client, err := roborev.NewClient(binary, runner, health.URL)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	manager, err := setup.NewManager(client, setupInstaller{target: filepath.Join(t.TempDir(), "managed.exe")})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithSetup(client, registry, "", operation.NewGate(), manager)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	// Prime the setup status so Retry takes the daemon-only path.
	if state := manager.Status(context.Background()); state.Phase != setup.PhaseReady || state.DaemonRunning {
		t.Fatalf("initial setup state = %#v", state)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	request, err := http.NewRequest(http.MethodPost, httpServer.URL+"/api/setup/roborev/retry", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted {
		response.Body.Close()
		t.Fatalf("retry status = %d", response.StatusCode)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	<-runner.started
	select {
	case <-runner.canceled:
		t.Fatal("accepted daemon recovery canceled when HTTP request ended")
	case <-time.After(50 * time.Millisecond):
	}
	healthy.Store(true)
	close(runner.release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		getResponse, getErr := http.Get(httpServer.URL + "/api/setup/roborev")
		if getErr == nil {
			var state setup.State
			decodeErr := json.NewDecoder(getResponse.Body).Decode(&state)
			_ = getResponse.Body.Close()
			if decodeErr == nil && getResponse.StatusCode == http.StatusOK && state.Phase == setup.PhaseReady && !state.OperationActive && state.DaemonRunning {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("daemon recovery did not reach ready: %#v", manager.Status(context.Background()))
}

func TestBlockedInstallEndpointReturnsConflictWithoutRedownload(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "roborev.exe")
	if err := os.WriteFile(binary, []byte("blocked"), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := roborev.NewClient(binary, setupBlockedRunner{}, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	installer := &countingSetupInstaller{target: binary}
	manager, err := setup.NewManager(client, installer)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithSetup(client, registry, "", operation.NewGate(), manager)
	if err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRequest(http.MethodGet, "/api/setup/roborev", nil)
	server.Handler().ServeHTTP(httptest.NewRecorder(), get)
	request := httptest.NewRequest(http.MethodPost, "/api/setup/roborev/install", nil)
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("blocked install status = %d", recorder.Code)
	}
	if got := installer.calls.Load(); got != 0 {
		t.Fatalf("installer calls = %d", got)
	}
}

func unsupportedSetupServer(t *testing.T) *Server {
	t.Helper()
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "roborev.exe")
	if err := os.WriteFile(binary, []byte("candidate"), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := roborev.NewClient(binary, setupVersionRunner{version: "v0.65.0"}, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := setup.NewManager(client, setupInstaller{target: filepath.Join(t.TempDir(), "managed.exe")})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithSetup(client, registry, "", operation.NewGate(), manager)
	if err != nil {
		t.Fatal(err)
	}
	return server
}
