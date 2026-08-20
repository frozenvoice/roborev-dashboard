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

	"github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/runtimecontrol"
)

type appRuntimeProcessRunner struct {
	mu       sync.Mutex
	commands []process.Command
}

func (r *appRuntimeProcessRunner) Run(_ context.Context, command process.Command) (process.Result, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	if len(command.Args) == 2 && command.Args[0] == "version" && command.Args[1] == "--json" {
		return process.Result{Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
	}
	return process.Result{}, nil
}

func (r *appRuntimeProcessRunner) snapshot() []process.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]process.Command(nil), r.commands...)
}

type appRuntimeAdapter struct {
	mu             sync.Mutex
	online         bool
	paused         bool
	queueErr       error
	restartStarted chan struct{}
	restartRelease chan struct{}
	restartCancel  chan struct{}
}

func (a *appRuntimeAdapter) DaemonHealth(context.Context) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.online, nil
}
func (a *appRuntimeAdapter) StartDaemon(context.Context) error {
	a.mu.Lock()
	a.online = true
	a.mu.Unlock()
	return nil
}
func (a *appRuntimeAdapter) StopDaemon(context.Context) error {
	a.mu.Lock()
	a.online = false
	a.mu.Unlock()
	return nil
}
func (a *appRuntimeAdapter) RestartDaemon(ctx context.Context) error {
	if a.restartStarted != nil {
		close(a.restartStarted)
	}
	if a.restartRelease != nil {
		select {
		case <-ctx.Done():
			if a.restartCancel != nil {
				close(a.restartCancel)
			}
			return ctx.Err()
		case <-a.restartRelease:
		}
	}
	a.mu.Lock()
	a.online = true
	a.mu.Unlock()
	return nil
}
func (a *appRuntimeAdapter) QueueStatus(context.Context) (roborev.QueueState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.online {
		return roborev.QueueState{}, roborev.ErrDaemonOffline
	}
	if a.queueErr != nil {
		return roborev.QueueState{}, a.queueErr
	}
	return roborev.QueueState{Known: true, Paused: a.paused}, nil
}
func (a *appRuntimeAdapter) SetQueuePaused(_ context.Context, paused bool) (roborev.QueueState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.online {
		return roborev.QueueState{}, roborev.ErrDaemonOffline
	}
	a.paused = paused
	return roborev.QueueState{Known: true, Paused: a.paused}, nil
}

func replaceRuntimeManager(t *testing.T, server *Server, adapter *appRuntimeAdapter) {
	t.Helper()
	if err := server.Runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	manager, err := runtimecontrol.NewManagerWithDependencies(runtimecontrol.Dependencies{
		Adapter: adapter, Gate: server.Gate, Timeout: time.Second,
		RetryDelays: []time.Duration{time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Runtime = manager
}

func TestQueueControlUsesAuthorizationGateAndTypedState(t *testing.T) {
	server := setupServer(t, &fakeService{})
	adapter := &appRuntimeAdapter{online: true}
	replaceRuntimeManager(t, server, adapter)

	request := httptest.NewRequest(http.MethodPost, "/api/runtime-control/queue/pause", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("pause without CSRF = %d", recorder.Code)
	}

	release, err := server.Gate.AcquireRefine()
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/runtime-control/queue/pause", nil)
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	release()
	if recorder.Code != http.StatusConflict {
		t.Fatalf("pause during refine = %d: %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/runtime-control/queue/pause", nil)
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("pause = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response runtimeControlDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.QueueState != "paused" || !response.CanResume || response.CanPause {
		t.Fatalf("pause state = %#v", response)
	}
}

func TestRuntimeStatusDisablesQueueActionsWhenQueueStateIsUnknown(t *testing.T) {
	server := setupServer(t, &fakeService{})
	adapter := &appRuntimeAdapter{online: true}
	replaceRuntimeManager(t, server, adapter)

	request := httptest.NewRequest(http.MethodGet, "/api/runtime-control", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("initial runtime status = %d", recorder.Code)
	}
	var state runtimeControlDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.QueueState != "processing" || !state.CanPause || state.CanResume {
		t.Fatalf("initial queue controls = %#v", state)
	}

	adapter.mu.Lock()
	adapter.queueErr = errors.New("queue status read failed")
	adapter.mu.Unlock()
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("failed runtime status = %d", recorder.Code)
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.QueueState != "unknown" || state.CanPause || state.CanResume {
		t.Fatalf("queue controls remained enabled with unknown state = %#v", state)
	}
}

func TestDaemonRestartSurvivesCompletedHTTPRequestAndShutdownCancelsActiveWork(t *testing.T) {
	server := setupServer(t, &fakeService{})
	adapter := &appRuntimeAdapter{restartStarted: make(chan struct{}), restartRelease: make(chan struct{}), restartCancel: make(chan struct{})}
	replaceRuntimeManager(t, server, adapter)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	request, err := http.NewRequest(http.MethodPost, httpServer.URL+"/api/daemon/restart", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	request.Header.Set("Origin", httpServer.URL)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("restart status = %d", response.StatusCode)
	}
	select {
	case <-adapter.restartStarted:
	case <-time.After(time.Second):
		t.Fatal("restart did not start")
	}
	select {
	case <-adapter.restartCancel:
		t.Fatal("completed HTTP request canceled accepted restart")
	case <-time.After(30 * time.Millisecond):
	}

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		shutdownDone <- server.Shutdown(ctx)
	}()
	select {
	case <-adapter.restartCancel:
	case <-time.After(time.Second):
		t.Fatal("Dashboard shutdown did not cancel restart")
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Dashboard shutdown did not wait for runtime operation")
	}
	if _, err := server.Gate.AcquireRuntimeControl(); err != nil {
		t.Fatalf("runtime gate was not released after shutdown: %v", err)
	}
}

func TestDaemonRestartCompletesAfterRequestAndReportsReady(t *testing.T) {
	server := setupServer(t, &fakeService{})
	adapter := &appRuntimeAdapter{restartStarted: make(chan struct{}), restartRelease: make(chan struct{})}
	replaceRuntimeManager(t, server, adapter)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	request, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/api/daemon/restart", nil)
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	request.Header.Set("Origin", httpServer.URL)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("restart status = %d", response.StatusCode)
	}
	<-adapter.restartStarted
	close(adapter.restartRelease)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		get, getErr := http.Get(httpServer.URL + "/api/runtime-control")
		if getErr != nil {
			t.Fatal(getErr)
		}
		var state runtimeControlDTO
		decodeErr := json.NewDecoder(get.Body).Decode(&state)
		get.Body.Close()
		if decodeErr == nil && !state.OperationActive && state.DaemonRunning {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("restart did not reach terminal ready state")
}

func TestDaemonStartWithV064HealthSchemaReachesReadyAndQueueKnown(t *testing.T) {
	const realV064HealthPayload = `{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true,"uptime":"1h2m3s","version":"v0.64.0","components":[{"name":"database","healthy":true},{"name":"workers","healthy":true}],"recent_errors":null,"error_count_24h":0}`
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(realV064HealthPayload))
		case "/api/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"queue_paused":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()

	runner := &appRuntimeProcessRunner{}
	client, err := roborev.NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(client, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}()

	request := httptest.NewRequest(http.MethodPost, "/api/daemon/start", nil)
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("daemon start = %d: %s", recorder.Code, recorder.Body.String())
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		statusRequest := httptest.NewRequest(http.MethodGet, "/api/runtime-control", nil)
		statusRecorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(statusRecorder, statusRequest)
		var state runtimeControlDTO
		if statusRecorder.Code == http.StatusOK && json.Unmarshal(statusRecorder.Body.Bytes(), &state) == nil &&
			!state.OperationActive && state.DaemonRunning && state.QueueState == "processing" && state.Error == "" {
			for _, command := range runner.snapshot() {
				if len(command.Args) == 2 && command.Args[0] == "daemon" && command.Args[1] == "start" {
					return
				}
			}
			t.Fatal("daemon start did not reach the fake process runner")
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("daemon start did not reach terminal ready state with known processing queue")
}

type unsupportedRuntimeService struct {
	*fakeService
	version string
}

func (s *unsupportedRuntimeService) Installation(context.Context) roborev.Installation {
	return roborev.Installation{Installed: true, Version: s.version}
}

func TestRuntimeStatusFailsClosedAndUsesBackendCommandPreviews(t *testing.T) {
	server := setupServer(t, &fakeService{})
	request := httptest.NewRequest(http.MethodGet, "/api/runtime-control", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("runtime status = %d", recorder.Code)
	}
	var response struct {
		CanStart, CanStop, CanRestart, CanPause, CanResume bool
		DisabledReason                                     string
		DaemonCommands                                     map[string]struct {
			Executable string   `json:"executable"`
			Args       []string `json:"args"`
		} `json:"daemonCommands"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.DaemonCommands) != 3 || response.DaemonCommands["restart"].Executable != "roborev" || strings.Join(response.DaemonCommands["restart"].Args, " ") != "daemon restart" {
		t.Fatalf("backend previews = %#v", response.DaemonCommands)
	}

	unsupported := &unsupportedRuntimeService{fakeService: &fakeService{}, version: "v0.65.0"}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	blocked, err := NewServer(unsupported, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/runtime-control", nil)
	recorder = httptest.NewRecorder()
	blocked.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unsupported runtime status = %d", recorder.Code)
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.CanStart || response.CanStop || response.CanRestart || response.CanPause || response.CanResume || response.DisabledReason == "" {
		t.Fatalf("unsupported runtime controls were enabled: %#v", response)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/daemon/restart", nil)
	request.Header.Set("X-CSRF-Token", blocked.csrfToken)
	recorder = httptest.NewRecorder()
	blocked.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsupported runtime action = %d", recorder.Code)
	}
}

func TestDecodeBodyRejectsTrailingJSONValues(t *testing.T) {
	server := setupServer(t, &fakeService{})
	for _, body := range []string{`{"comment":"ok"}{"comment":"trailing"}`, `{"comment":"ok"} 42`} {
		request := httptest.NewRequest(http.MethodPost, "/api/reviews/42/comment", strings.NewReader(body))
		request.Header.Set("X-CSRF-Token", server.csrfToken)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("trailing body %q = %d", body, recorder.Code)
		}
	}
}
