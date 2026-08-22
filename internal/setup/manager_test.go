package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

type fakeProber struct {
	mu            sync.Mutex
	binary        string
	override      bool
	version       string
	err           string
	failureCode   string
	rebindErr     error
	rebinds       []string
	rebindStarted chan struct{}
	rebindRelease <-chan struct{}
	rebindCtxErr  chan error
}

func (f *fakeProber) BinaryPath() string        { f.mu.Lock(); defer f.mu.Unlock(); return f.binary }
func (f *fakeProber) HasExplicitOverride() bool { return f.override }
func (f *fakeProber) Rebind(ctx context.Context, path string) error {
	if f.rebindStarted != nil {
		close(f.rebindStarted)
		select {
		case <-ctx.Done():
			if f.rebindCtxErr != nil {
				f.rebindCtxErr <- ctx.Err()
			}
			return ctx.Err()
		case <-f.rebindRelease:
		}
	}
	f.mu.Lock()
	if f.rebindErr != nil {
		err := f.rebindErr
		f.mu.Unlock()
		return err
	}
	f.binary = path
	f.rebinds = append(f.rebinds, path)
	f.mu.Unlock()
	return nil
}
func (f *fakeProber) Installation(context.Context) roborev.Installation {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.version == "" {
		return roborev.Installation{Binary: f.binary, Error: f.err, FailureCode: f.failureCode}
	}
	return roborev.Installation{Installed: true, Binary: f.binary, Version: f.version, Error: f.err, Status: json.RawMessage(`{"running":true,"known":true}`), DaemonHealth: roborev.DaemonHealthOnline, DaemonCondition: roborev.DaemonConditionHealthy, DaemonVersion: f.version}
}

type fakeInstaller struct {
	mu      sync.Mutex
	target  string
	calls   int
	install func(context.Context) (string, error)
}

type preflightCancelProber struct {
	*fakeProber
	cancel context.CancelFunc
	once   sync.Once
}

func (p *preflightCancelProber) Installation(ctx context.Context) roborev.Installation {
	p.once.Do(p.cancel)
	return p.fakeProber.Installation(ctx)
}

type daemonProber struct {
	mu            sync.Mutex
	binary        string
	version       string
	healthyAfter  int
	installations int
	starts        int
	startErrors   int
	startStarted  chan struct{}
	startRelease  <-chan struct{}
	startCtxErr   chan error
}

type conditionDaemonProber struct {
	mu                 sync.Mutex
	binary             string
	version            string
	daemonVersion      string
	daemonVersionAfter int
	healthyAfter       int
	probes             int
	starts             int
	rebinds            int
	conditions         []roborev.DaemonHealthCondition
}

type typedHealthProber struct {
	installation roborev.Installation
}

func (p *typedHealthProber) BinaryPath() string        { return p.installation.Binary }
func (p *typedHealthProber) HasExplicitOverride() bool { return false }
func (p *typedHealthProber) Rebind(_ context.Context, path string) error {
	p.installation.Binary = path
	return nil
}
func (p *typedHealthProber) Installation(context.Context) roborev.Installation {
	return p.installation
}

type refreshSequenceProber struct {
	mu      sync.Mutex
	binary  string
	samples []roborev.Installation
	gates   []<-chan struct{}
	started []chan struct{}
	calls   int
}

func (p *refreshSequenceProber) BinaryPath() string        { return p.binary }
func (p *refreshSequenceProber) HasExplicitOverride() bool { return false }
func (p *refreshSequenceProber) Rebind(_ context.Context, path string) error {
	p.mu.Lock()
	p.binary = path
	p.mu.Unlock()
	return nil
}
func (p *refreshSequenceProber) Installation(ctx context.Context) roborev.Installation {
	p.mu.Lock()
	index := p.calls
	p.calls++
	var sample roborev.Installation
	if len(p.samples) > 0 {
		sample = p.samples[index%len(p.samples)]
	}
	var gate <-chan struct{}
	if index < len(p.gates) {
		gate = p.gates[index]
	}
	var started chan struct{}
	if index < len(p.started) {
		started = p.started[index]
	}
	p.mu.Unlock()
	if started != nil {
		close(started)
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	return sample
}

func awaitSetupSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for deterministic setup probe")
	}
}

func (p *conditionDaemonProber) BinaryPath() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.binary
}
func (p *conditionDaemonProber) HasExplicitOverride() bool { return false }
func (p *conditionDaemonProber) Rebind(_ context.Context, path string) error {
	p.mu.Lock()
	p.binary, p.version = path, "v0.64.0"
	p.rebinds++
	p.mu.Unlock()
	return nil
}
func (p *conditionDaemonProber) Installation(context.Context) roborev.Installation {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.probes++
	if p.version == "" {
		return roborev.Installation{Binary: p.binary}
	}
	daemonVersion := p.daemonVersion
	if daemonVersion == "" {
		daemonVersion = p.version
	}
	if p.daemonVersionAfter > 0 && p.probes >= p.daemonVersionAfter {
		daemonVersion = p.version
	}
	installation := roborev.Installation{Installed: true, Binary: p.binary, Version: p.version, Status: json.RawMessage(`{"running":true,"known":true}`), DaemonHealth: roborev.DaemonHealthOnline, DaemonCondition: roborev.DaemonConditionUnhealthy, DaemonVersion: daemonVersion}
	if p.healthyAfter > 0 && p.probes >= p.healthyAfter {
		installation.DaemonCondition = roborev.DaemonConditionHealthy
	}
	p.conditions = append(p.conditions, installation.DaemonCondition)
	return installation
}
func (p *conditionDaemonProber) StartDaemon(context.Context) error {
	p.mu.Lock()
	p.starts++
	p.mu.Unlock()
	return nil
}

func (p *daemonProber) BinaryPath() string        { p.mu.Lock(); defer p.mu.Unlock(); return p.binary }
func (p *daemonProber) HasExplicitOverride() bool { return false }
func (p *daemonProber) Rebind(_ context.Context, path string) error {
	p.mu.Lock()
	p.binary, p.version = path, "v0.64.0"
	p.mu.Unlock()
	return nil
}
func (p *daemonProber) Installation(context.Context) roborev.Installation {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.installations++
	if p.version == "" {
		return roborev.Installation{Binary: p.binary}
	}
	running := p.installations >= p.healthyAfter
	status, _ := json.Marshal(map[string]bool{"running": running, "known": true})
	installation := roborev.Installation{Installed: true, Binary: p.binary, Version: p.version, Status: status}
	if running {
		installation.DaemonHealth = roborev.DaemonHealthOnline
		installation.DaemonCondition = roborev.DaemonConditionHealthy
		installation.DaemonVersion = p.version
	} else {
		installation.DaemonHealth = roborev.DaemonHealthOffline
		installation.DaemonCondition = roborev.DaemonConditionUnknown
	}
	return installation
}
func (p *daemonProber) StartDaemon(ctx context.Context) error {
	if p.startStarted != nil {
		close(p.startStarted)
		select {
		case <-ctx.Done():
			if p.startCtxErr != nil {
				p.startCtxErr <- ctx.Err()
			}
			return ctx.Err()
		case <-p.startRelease:
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts++
	if p.startErrors > 0 {
		p.startErrors--
		return errors.New("daemon start failed")
	}
	return nil
}

func (f *fakeInstaller) Target() string { return f.target }
func (f *fakeInstaller) Install(ctx context.Context) (string, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.install(ctx)
}

func waitSetup(t *testing.T, manager *Manager) State {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state := manager.stateCopy()
		if !state.InProgress {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("setup did not settle")
	return State{}
}

func TestMissingInstallRebindsAndStrictlyVerifies(t *testing.T) {
	client := &fakeProber{}
	verified := make(chan struct{})
	installer := &fakeInstaller{target: `C:\Users\test\.roborev\bin\roborev.exe`, install: func(context.Context) (string, error) {
		<-verified
		return `C:\Users\test\.roborev\bin\roborev.exe`, nil
	}}
	manager, err := NewManager(client, installer)
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.StartInstall(context.Background())
	if err != nil || !state.InProgress {
		t.Fatalf("start = %#v, %v", state, err)
	}
	client.mu.Lock()
	client.version = "v0.64.0"
	client.mu.Unlock()
	close(verified)
	final := waitSetup(t, manager)
	if final.Phase != PhaseReady || final.Progress != 100 || !final.Supported {
		t.Fatalf("final = %#v", final)
	}
	if len(client.rebinds) != 1 {
		t.Fatalf("rebinds = %#v", client.rebinds)
	}
}

func TestInstallOutlivesRequestContextCancellation(t *testing.T) {
	client := &fakeProber{}
	started := make(chan struct{})
	canceled := make(chan struct{})
	releaseInstall := make(chan struct{})
	target := filepath.Join(t.TempDir(), "roborev.exe")
	installer := &fakeInstaller{target: target, install: func(ctx context.Context) (string, error) {
		close(started)
		select {
		case <-ctx.Done():
			close(canceled)
			return "", ctx.Err()
		case <-releaseInstall:
			return target, nil
		}
	}}
	manager, err := NewManager(client, installer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	requestContext, cancelRequest := context.WithCancel(context.Background())
	if _, err := manager.StartInstall(requestContext); err != nil {
		t.Fatal(err)
	}
	// A completed HTTP handler cancels its request context. The accepted
	// install must keep running on the manager-owned process context.
	cancelRequest()
	<-started
	select {
	case <-canceled:
		t.Fatal("install canceled with request context")
	case <-time.After(100 * time.Millisecond):
	}
	client.mu.Lock()
	client.version = "v0.64.0"
	client.mu.Unlock()
	close(releaseInstall)
	state := waitSetupLong(t, manager)
	if state.Phase != PhaseReady {
		t.Fatalf("state after request cancellation = %#v", state)
	}
}

func TestRetryInstallOutlivesRequestContextCancellation(t *testing.T) {
	client := &fakeProber{}
	started := make(chan struct{})
	canceled := make(chan struct{})
	releaseInstall := make(chan struct{})
	var calls int
	var callsMu sync.Mutex
	target := filepath.Join(t.TempDir(), "roborev.exe")
	installer := &fakeInstaller{target: target, install: func(ctx context.Context) (string, error) {
		callsMu.Lock()
		calls++
		call := calls
		callsMu.Unlock()
		if call == 1 {
			return "", syscall.EACCES
		}
		close(started)
		select {
		case <-ctx.Done():
			close(canceled)
			return "", ctx.Err()
		case <-releaseInstall:
			return target, nil
		}
	}}
	manager, err := NewManager(client, installer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	if _, err := manager.StartInstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := waitSetupLong(t, manager)
	if state.Phase != PhaseFailed || !state.CanRetry {
		t.Fatalf("initial retryable state = %#v", state)
	}
	requestContext, cancelRequest := context.WithCancel(context.Background())
	if _, err := manager.Retry(requestContext); err != nil {
		t.Fatal(err)
	}
	cancelRequest()
	<-started
	select {
	case <-canceled:
		t.Fatal("retry install canceled with request context")
	case <-time.After(100 * time.Millisecond):
	}
	client.mu.Lock()
	client.version = "v0.64.0"
	client.mu.Unlock()
	close(releaseInstall)
	state = waitSetupLong(t, manager)
	if state.Phase != PhaseReady {
		t.Fatalf("retry state after request cancellation = %#v", state)
	}
}

func cleanupSetupManager(t *testing.T, manager *Manager) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
}

func assertSetupGateReleased(t *testing.T, gate *operation.Gate) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		release, err := gate.AcquireMutation()
		if err == nil {
			release()
			return
		}
		if !errors.Is(err, operation.ErrBusy) {
			t.Fatalf("gate remains held: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("gate was not released")
}

func TestPreCanceledStartInstallDoesNotAcceptOperation(t *testing.T) {
	gate := operation.NewGate()
	installer := &fakeInstaller{install: func(context.Context) (string, error) {
		t.Fatal("pre-canceled install invoked installer")
		return "", nil
	}}
	manager, err := NewManagerWithGate(&fakeProber{}, installer, gate)
	if err != nil {
		t.Fatal(err)
	}
	cleanupSetupManager(t, manager)
	request, cancel := context.WithCancel(context.Background())
	cancel()
	state, err := manager.StartInstall(request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled start error = %v", err)
	}
	if state.InProgress || manager.stateCopy().InProgress {
		t.Fatalf("pre-canceled start became active: %#v", state)
	}
	installer.mu.Lock()
	calls := installer.calls
	installer.mu.Unlock()
	if calls != 0 {
		t.Fatalf("pre-canceled installer calls = %d", calls)
	}
	assertSetupGateReleased(t, gate)
}

func TestPreflightCancellationRejectsFinalInstallAcceptance(t *testing.T) {
	gate := operation.NewGate()
	request, cancel := context.WithCancel(context.Background())
	client := &preflightCancelProber{fakeProber: &fakeProber{}, cancel: cancel}
	installer := &fakeInstaller{install: func(context.Context) (string, error) {
		t.Fatal("preflight-canceled install invoked installer")
		return "", nil
	}}
	manager, err := NewManagerWithGate(client, installer, gate)
	if err != nil {
		t.Fatal(err)
	}
	cleanupSetupManager(t, manager)
	state, err := manager.StartInstall(request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("preflight cancellation error = %v", err)
	}
	if state.InProgress || manager.stateCopy().InProgress {
		t.Fatalf("preflight cancellation became active: %#v", state)
	}
	installer.mu.Lock()
	calls := installer.calls
	installer.mu.Unlock()
	if calls != 0 {
		t.Fatalf("preflight-canceled installer calls = %d", calls)
	}
	assertSetupGateReleased(t, gate)
}

func newBlockedRecoveryManager(t *testing.T, gate *operation.Gate, rebindRelease <-chan struct{}, rebindCtxErr chan error) (*Manager, *fakeProber, string) {
	t.Helper()
	target := filepath.Join(t.TempDir(), "roborev.exe")
	if err := os.WriteFile(target, []byte("blocked binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	client := &fakeProber{binary: target, err: "blocked", failureCode: "application_control", rebindStarted: make(chan struct{}), rebindRelease: rebindRelease, rebindCtxErr: rebindCtxErr}
	installer := &fakeInstaller{target: target, install: func(context.Context) (string, error) {
		return target, errors.New("installer must not run for blocked recovery")
	}}
	manager, err := NewManagerWithGate(client, installer, gate)
	if err != nil {
		t.Fatal(err)
	}
	if state := manager.Status(context.Background()); state.Phase != PhaseBlocked {
		t.Fatalf("blocked setup state = %#v", state)
	}
	client.mu.Lock()
	client.err, client.failureCode, client.version, client.rebindErr = "", "", "v0.64.0", nil
	client.mu.Unlock()
	cleanupSetupManager(t, manager)
	return manager, client, target
}

func TestBlockedRetryOutlivesRequestContextCancellation(t *testing.T) {
	gate := operation.NewGate()
	rebindRelease := make(chan struct{})
	rebindCtxErr := make(chan error, 1)
	manager, client, _ := newBlockedRecoveryManager(t, gate, rebindRelease, rebindCtxErr)
	request, cancel := context.WithCancel(context.Background())
	state, err := manager.Retry(request)
	if err != nil || !state.InProgress || state.Phase != PhaseBinding {
		t.Fatalf("blocked retry acceptance = %#v, %v", state, err)
	}
	cancel()
	<-client.rebindStarted
	select {
	case err := <-rebindCtxErr:
		t.Fatalf("blocked retry observed request cancellation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(rebindRelease)
	state = waitSetupLong(t, manager)
	if state.Phase != PhaseReady || state.InProgress {
		t.Fatalf("blocked retry final state = %#v", state)
	}
	assertSetupGateReleased(t, gate)
}

func newDaemonRecoveryManager(t *testing.T, gate *operation.Gate, startRelease <-chan struct{}, startCtxErr chan error) (*Manager, *daemonProber) {
	t.Helper()
	client := &daemonProber{binary: `C:\managed\roborev.exe`, version: "v0.64.0", healthyAfter: 2, startStarted: make(chan struct{}), startRelease: startRelease, startCtxErr: startCtxErr}
	installer := &fakeInstaller{target: client.binary, install: func(context.Context) (string, error) {
		return "", errors.New("installer must not run for daemon recovery")
	}}
	manager, err := NewManagerWithGate(client, installer, gate)
	if err != nil {
		t.Fatal(err)
	}
	manager.wait = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	if state := manager.Status(context.Background()); state.Phase != PhaseReady || state.DaemonRunning {
		t.Fatalf("daemon setup state = %#v", state)
	}
	cleanupSetupManager(t, manager)
	return manager, client
}

func TestDaemonRetryOutlivesRequestContextCancellation(t *testing.T) {
	gate := operation.NewGate()
	startRelease := make(chan struct{})
	startCtxErr := make(chan error, 1)
	manager, client := newDaemonRecoveryManager(t, gate, startRelease, startCtxErr)
	request, cancel := context.WithCancel(context.Background())
	state, err := manager.Retry(request)
	if err != nil || !state.InProgress || state.Phase != PhaseStartingDaemon {
		t.Fatalf("daemon retry acceptance = %#v, %v", state, err)
	}
	cancel()
	<-client.startStarted
	select {
	case err := <-startCtxErr:
		t.Fatalf("daemon retry observed request cancellation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(startRelease)
	state = waitSetupLong(t, manager)
	if state.Phase != PhaseReady || !state.DaemonRunning || state.InProgress {
		t.Fatalf("daemon retry final state = %#v", state)
	}
	assertSetupGateReleased(t, gate)
}

func TestPreCanceledRecoveryRetryDoesNotStartWorker(t *testing.T) {
	t.Run("blocked", func(t *testing.T) {
		gate := operation.NewGate()
		manager, client, _ := newBlockedRecoveryManager(t, gate, make(chan struct{}), make(chan error, 1))
		request, cancel := context.WithCancel(context.Background())
		cancel()
		state, err := manager.Retry(request)
		if !errors.Is(err, context.Canceled) || state.InProgress || manager.stateCopy().InProgress {
			t.Fatalf("pre-canceled blocked retry = %#v, %v", state, err)
		}
		select {
		case <-client.rebindStarted:
			t.Fatal("pre-canceled blocked retry started Rebind")
		default:
		}
		assertSetupGateReleased(t, gate)
	})
	t.Run("daemon", func(t *testing.T) {
		gate := operation.NewGate()
		manager, client := newDaemonRecoveryManager(t, gate, make(chan struct{}), make(chan error, 1))
		request, cancel := context.WithCancel(context.Background())
		cancel()
		state, err := manager.Retry(request)
		if !errors.Is(err, context.Canceled) || state.InProgress || manager.stateCopy().InProgress {
			t.Fatalf("pre-canceled daemon retry = %#v, %v", state, err)
		}
		select {
		case <-client.startStarted:
			t.Fatal("pre-canceled daemon retry started daemon")
		default:
		}
		assertSetupGateReleased(t, gate)
	})
}

func TestShutdownCancelsBlockedAndDaemonRecovery(t *testing.T) {
	t.Run("blocked", func(t *testing.T) {
		gate := operation.NewGate()
		rebindCtxErr := make(chan error, 1)
		manager, client, _ := newBlockedRecoveryManager(t, gate, make(chan struct{}), rebindCtxErr)
		if _, err := manager.Retry(context.Background()); err != nil {
			t.Fatal(err)
		}
		<-client.rebindStarted
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdown); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-rebindCtxErr:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("blocked shutdown context error = %v", err)
			}
		default:
			t.Fatal("blocked Rebind did not observe shutdown cancellation")
		}
		state := manager.stateCopy()
		if state.Phase != PhaseBlocked || !state.Installed || !state.Blocked || !state.CanRetry || !state.Retryable || state.CanInstall || state.InProgress || state.Compatible == nil || *state.Compatible || state.PanelRefineCompatible == nil || *state.PanelRefineCompatible {
			t.Fatalf("blocked shutdown state = %#v", state)
		}
		assertSetupGateReleased(t, gate)
	})
	t.Run("daemon", func(t *testing.T) {
		gate := operation.NewGate()
		startCtxErr := make(chan error, 1)
		manager, client := newDaemonRecoveryManager(t, gate, make(chan struct{}), startCtxErr)
		if _, err := manager.Retry(context.Background()); err != nil {
			t.Fatal(err)
		}
		<-client.startStarted
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdown); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-startCtxErr:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("daemon shutdown context error = %v", err)
			}
		default:
			t.Fatal("daemon StartDaemon did not observe shutdown cancellation")
		}
		state := manager.stateCopy()
		if state.Phase != PhaseFailed || !state.Installed || !state.Supported || state.DaemonRunning || !state.CanStartDaemon || !state.CanRetry || !state.Retryable || state.FailureCode != "daemon_start_failed" || state.InProgress {
			t.Fatalf("daemon shutdown state = %#v", state)
		}
		assertSetupGateReleased(t, gate)
	})
}

func TestRecoverySafetyCeilingCancelsBlockedAndDaemon(t *testing.T) {
	t.Run("blocked", func(t *testing.T) {
		gate := operation.NewGate()
		rebindCtxErr := make(chan error, 1)
		manager, client, _ := newBlockedRecoveryManager(t, gate, make(chan struct{}), rebindCtxErr)
		manager.recoveryCeiling = 20 * time.Millisecond
		if _, err := manager.Retry(context.Background()); err != nil {
			t.Fatal(err)
		}
		<-client.rebindStarted
		state := waitSetupLong(t, manager)
		select {
		case err := <-rebindCtxErr:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("blocked timeout context error = %v", err)
			}
		default:
			t.Fatal("blocked Rebind did not observe recovery ceiling")
		}
		if state.Phase != PhaseBlocked || !state.Installed || !state.Blocked || !state.CanRetry || !state.Retryable || state.CanInstall || state.InProgress || state.Compatible == nil || *state.Compatible || state.PanelRefineCompatible == nil || *state.PanelRefineCompatible {
			t.Fatalf("blocked timeout state = %#v", state)
		}
		assertSetupGateReleased(t, gate)
	})
	t.Run("daemon", func(t *testing.T) {
		gate := operation.NewGate()
		startCtxErr := make(chan error, 1)
		manager, client := newDaemonRecoveryManager(t, gate, make(chan struct{}), startCtxErr)
		manager.recoveryCeiling = 20 * time.Millisecond
		if _, err := manager.Retry(context.Background()); err != nil {
			t.Fatal(err)
		}
		<-client.startStarted
		state := waitSetupLong(t, manager)
		select {
		case err := <-startCtxErr:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("daemon timeout context error = %v", err)
			}
		default:
			t.Fatal("daemon StartDaemon did not observe recovery ceiling")
		}
		if state.Phase != PhaseFailed || !state.Installed || !state.Supported || state.DaemonRunning || !state.CanStartDaemon || !state.CanRetry || !state.Retryable || state.FailureCode != "daemon_start_failed" || state.InProgress {
			t.Fatalf("daemon timeout state = %#v", state)
		}
		assertSetupGateReleased(t, gate)
	})
}

func TestSupportedVersionDoesNotDownloadAgain(t *testing.T) {
	client := &fakeProber{binary: `C:\roborev.exe`, version: "v0.64.0"}
	installer := &fakeInstaller{install: func(context.Context) (string, error) { return "", errors.New("must not download") }}
	manager, _ := NewManager(client, installer)
	state, err := manager.StartInstall(context.Background())
	if err != nil || state.Phase != PhaseReady {
		t.Fatalf("supported install = %#v, %v", state, err)
	}
	installer.mu.Lock()
	calls := installer.calls
	installer.mu.Unlock()
	if calls != 0 {
		t.Fatalf("installer calls = %d", calls)
	}
}

func TestNewerUnknownAndExplicitInstallAreIneligible(t *testing.T) {
	for _, test := range []struct {
		name   string
		client *fakeProber
	}{
		{"newer", &fakeProber{binary: `C:\roborev.exe`, version: "v0.65.0"}},
		{"explicit", &fakeProber{override: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, _ := NewManager(test.client, &fakeInstaller{install: func(context.Context) (string, error) { return "", nil }})
			_, err := manager.StartInstall(context.Background())
			if !errors.Is(err, ErrIneligible) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestExistingBinaryProbeFailuresAreVisibleAndNeverAutoReplaced(t *testing.T) {
	for _, test := range []struct {
		name        string
		failureCode string
		wantPhase   Phase
	}{
		{name: "application control", failureCode: "application_control", wantPhase: PhaseBlocked},
		{name: "malformed version", failureCode: "invalid_binary", wantPhase: PhaseFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "roborev.exe")
			if err := os.WriteFile(binary, []byte("candidate"), 0o700); err != nil {
				t.Fatal(err)
			}
			client := &fakeProber{binary: binary, err: "version probe failed", failureCode: test.failureCode}
			installer := &fakeInstaller{target: `C:\managed\roborev.exe`, install: func(context.Context) (string, error) { return "", errors.New("must not install") }}
			manager, _ := NewManager(client, installer)
			state := manager.Status(context.Background())
			if state.Phase != test.wantPhase || state.CanInstall || state.InstallAvailable {
				t.Fatalf("state = %#v", state)
			}
			if _, err := manager.StartInstall(context.Background()); !errors.Is(err, ErrIneligible) {
				t.Fatalf("direct install err = %v", err)
			}
			installer.mu.Lock()
			calls := installer.calls
			installer.mu.Unlock()
			if calls != 0 {
				t.Fatalf("installer calls = %d", calls)
			}
		})
	}
}

func TestExistingBlockedBinaryVerificationRetryDoesNotInstall(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "roborev.exe")
	if err := os.WriteFile(binary, []byte("candidate"), 0o700); err != nil {
		t.Fatal(err)
	}
	client := &fakeProber{binary: binary, err: "blocked", failureCode: "application_control"}
	installer := &fakeInstaller{target: filepath.Join(t.TempDir(), "managed.exe"), install: func(context.Context) (string, error) { return "", errors.New("must not install") }}
	manager, _ := NewManager(client, installer)
	state := manager.Status(context.Background())
	if state.Phase != PhaseBlocked || !state.Installed || !state.CanRetry {
		t.Fatalf("startup blocked state = %#v", state)
	}
	client.mu.Lock()
	client.err, client.failureCode, client.version = "", "", "v0.64.0"
	client.mu.Unlock()
	state, err := manager.Retry(context.Background())
	if err != nil {
		t.Fatalf("startup verification retry = %#v, %v", state, err)
	}
	state = waitSetup(t, manager)
	if state.Phase != PhaseReady {
		t.Fatalf("startup verification retry final state = %#v", state)
	}
	installer.mu.Lock()
	calls := installer.calls
	installer.mu.Unlock()
	if calls != 0 {
		t.Fatalf("installer calls = %d", calls)
	}
}

func TestApplicationControlDetailIsPreserved(t *testing.T) {
	client := &fakeProber{}
	installer := &fakeInstaller{install: func(context.Context) (string, error) { return "", errors.New("permission denied") }}
	manager, _ := NewManager(client, installer)
	if _, err := manager.StartInstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := waitSetup(t, manager)
	if state.Phase != PhaseFailed || state.Error == "" {
		t.Fatalf("state = %#v", state)
	}
}

func TestExactApplicationControlIsBlockedButPermissionIsRetryable(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		phase    Phase
		code     string
		canRetry bool
		blocked  bool
	}{
		{name: "application control without published target", err: syscall.Errno(1260), phase: PhaseFailed, code: "application_control"},
		{name: "permission", err: syscall.EACCES, phase: PhaseFailed, code: "permission_denied", canRetry: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			installer := &fakeInstaller{install: func(context.Context) (string, error) { return "", test.err }}
			manager, _ := NewManager(&fakeProber{}, installer)
			if _, err := manager.StartInstall(context.Background()); err != nil {
				t.Fatal(err)
			}
			state := waitSetup(t, manager)
			if state.Phase != test.phase || state.FailureCode != test.code || state.CanRetry != test.canRetry || state.Blocked != test.blocked {
				t.Fatalf("state = %#v", state)
			}
			if test.name == "permission" {
				if _, err := manager.Retry(context.Background()); err != nil {
					t.Fatal(err)
				}
				_ = waitSetup(t, manager)
				installer.mu.Lock()
				calls := installer.calls
				installer.mu.Unlock()
				if calls != 2 {
					t.Fatalf("permission retry installer calls = %d", calls)
				}
			}
		})
	}
}

func TestBlockedPublishedTargetRetriesVerificationWithoutInstaller(t *testing.T) {
	target := filepath.Join(t.TempDir(), "roborev.exe")
	if err := os.WriteFile(target, []byte("published"), 0o700); err != nil {
		t.Fatal(err)
	}
	client := &fakeProber{rebindErr: syscall.Errno(1260)}
	installer := &fakeInstaller{target: target, install: func(context.Context) (string, error) { return target, nil }}
	manager, _ := NewManager(client, installer)
	if _, err := manager.StartInstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := waitSetup(t, manager)
	if state.Phase != PhaseBlocked || !state.Installed || !state.Blocked || !state.CanRetry || state.CanInstall {
		t.Fatalf("blocked state = %#v", state)
	}
	if _, err := manager.StartInstall(context.Background()); !errors.Is(err, ErrIneligible) {
		t.Fatalf("direct install on blocked state = %v", err)
	}
	client.mu.Lock()
	client.rebindErr = nil
	client.version = "v0.64.0"
	client.mu.Unlock()
	state, err := manager.Retry(context.Background())
	if err != nil {
		t.Fatalf("verification retry = %#v, %v", state, err)
	}
	state = waitSetup(t, manager)
	if state.Phase != PhaseReady {
		t.Fatalf("verification retry final state = %#v", state)
	}
	installer.mu.Lock()
	calls := installer.calls
	installer.mu.Unlock()
	if calls != 1 {
		t.Fatalf("installer calls after successful retry = %d", calls)
	}
}

func TestBlockedPublishedTargetStaysBlockedWhenVerificationFails(t *testing.T) {
	target := filepath.Join(t.TempDir(), "roborev.exe")
	if err := os.WriteFile(target, []byte("published"), 0o700); err != nil {
		t.Fatal(err)
	}
	client := &fakeProber{rebindErr: syscall.Errno(1260)}
	installer := &fakeInstaller{target: target, install: func(context.Context) (string, error) { return target, nil }}
	manager, _ := NewManager(client, installer)
	if _, err := manager.StartInstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := waitSetup(t, manager)
	if state.Phase != PhaseBlocked || !state.CanRetry {
		t.Fatalf("blocked state = %#v", state)
	}
	if _, err := manager.Retry(context.Background()); err != nil {
		t.Fatalf("blocked verification retry acceptance = %v", err)
	}
	state = waitSetup(t, manager)
	if state.Phase != PhaseBlocked || !state.Installed || !state.CanRetry || state.CanInstall {
		t.Fatalf("retry state = %#v", state)
	}
	installer.mu.Lock()
	calls := installer.calls
	installer.mu.Unlock()
	if calls != 1 {
		t.Fatalf("installer calls after failed retry = %d", calls)
	}
}

func TestBlockedRetryDoesNotDownloadWhenTargetDisappears(t *testing.T) {
	target := filepath.Join(t.TempDir(), "roborev.exe")
	if err := os.WriteFile(target, []byte("published"), 0o700); err != nil {
		t.Fatal(err)
	}
	client := &fakeProber{rebindErr: syscall.Errno(1260)}
	installer := &fakeInstaller{target: target, install: func(context.Context) (string, error) { return target, nil }}
	manager, _ := NewManager(client, installer)
	if _, err := manager.StartInstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := waitSetup(t, manager)
	if state.Phase != PhaseBlocked {
		t.Fatalf("blocked state = %#v", state)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Retry(context.Background()); !errors.Is(err, ErrIneligible) {
		t.Fatalf("missing target retry = %v", err)
	}
	state = manager.Status(context.Background())
	if state.Phase != PhaseFailed || state.CanInstall || state.CanRetry {
		t.Fatalf("missing target state = %#v", state)
	}
	installer.mu.Lock()
	calls := installer.calls
	installer.mu.Unlock()
	if calls != 1 {
		t.Fatalf("installer calls after missing target = %d", calls)
	}
}

func TestNonRetryableFailureFailsClosedForDirectInstallAndRetry(t *testing.T) {
	manager, _ := NewManager(&fakeProber{}, &fakeInstaller{install: func(context.Context) (string, error) {
		return "", fmt.Errorf("%w: test", roborevcompat.ErrDownloadHash)
	}})
	if _, err := manager.StartInstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := waitSetup(t, manager)
	if state.CanRetry || state.CanInstall || state.Retryable || state.FailureCode != "checksum_mismatch" {
		t.Fatalf("state = %#v", state)
	}
	if _, err := manager.StartInstall(context.Background()); !errors.Is(err, ErrIneligible) {
		t.Fatalf("direct install err = %v", err)
	}
	if _, err := manager.Retry(context.Background()); !errors.Is(err, ErrIneligible) {
		t.Fatalf("retry err = %v", err)
	}
}

func TestDaemonInstallHealthMatrixAndRetryDoesNotDownload(t *testing.T) {
	tests := []struct {
		name          string
		healthyAfter  int
		startErrors   int
		wantPhase     Phase
		wantRetry     bool
		wantDownloads int
	}{
		{name: "immediate health", healthyAfter: 3, wantPhase: PhaseReady, wantDownloads: 1},
		{name: "delayed health", healthyAfter: 5, wantPhase: PhaseReady, wantDownloads: 1},
		{name: "start error but health ready", healthyAfter: 3, startErrors: 1, wantPhase: PhaseReady, wantDownloads: 1},
		{name: "start error then retry", healthyAfter: 4, startErrors: 1, wantPhase: PhaseFailed, wantRetry: true, wantDownloads: 1},
		{name: "health timeout", healthyAfter: 100, wantPhase: PhaseFailed, wantRetry: true, wantDownloads: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &daemonProber{healthyAfter: test.healthyAfter, startErrors: test.startErrors}
			target := filepath.Join(t.TempDir(), "roborev.exe")
			installer := &fakeInstaller{target: target, install: func(context.Context) (string, error) { return target, nil }}
			manager, err := NewManager(client, installer)
			if err != nil {
				t.Fatal(err)
			}
			manager.wait = func(ctx context.Context, _ time.Duration) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return nil
				}
			}
			if _, err := manager.StartInstall(context.Background()); err != nil {
				t.Fatal(err)
			}
			state := waitSetupLong(t, manager)
			if test.name == "start error then retry" {
				if state.Phase == PhaseFailed {
					if !state.CanRetry {
						t.Fatalf("initial state = %#v", state)
					}
					if _, err := manager.Retry(context.Background()); err != nil {
						t.Fatal(err)
					}
					state = waitSetupLong(t, manager)
				}
				if state.Phase != PhaseReady {
					t.Fatalf("retry state = %#v", state)
				}
			}
			if state.Phase != test.wantPhase && !(test.name == "start error then retry" && state.Phase == PhaseReady) {
				t.Fatalf("state = %#v", state)
			}
			installer.mu.Lock()
			calls := installer.calls
			installer.mu.Unlock()
			if calls != test.wantDownloads {
				t.Fatalf("installer calls = %d, want %d", calls, test.wantDownloads)
			}
		})
	}
}

func TestDaemonInstallWaitsThroughReachableUnhealthySequence(t *testing.T) {
	client := &conditionDaemonProber{healthyAfter: 4}
	target := filepath.Join(t.TempDir(), "roborev.exe")
	installer := &fakeInstaller{target: target, install: func(context.Context) (string, error) { return target, nil }}
	manager, err := NewManager(client, installer)
	if err != nil {
		t.Fatal(err)
	}
	manager.wait = func(context.Context, time.Duration) error { return nil }
	if _, err := manager.StartInstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := waitSetupLong(t, manager)
	if state.Phase != PhaseReady || state.DaemonCondition != roborev.DaemonConditionHealthy || !state.DaemonRunning {
		t.Fatalf("healthy final state = %#v", state)
	}
	client.mu.Lock()
	conditions := append([]roborev.DaemonHealthCondition(nil), client.conditions...)
	client.mu.Unlock()
	want := []roborev.DaemonHealthCondition{roborev.DaemonConditionUnhealthy, roborev.DaemonConditionUnhealthy, roborev.DaemonConditionHealthy}
	if len(conditions) < len(want) || !equalDaemonConditions(conditions[:len(want)], want) {
		t.Fatalf("health sequence = %#v, want prefix %#v", conditions, want)
	}
}

func TestPermanentReachableUnhealthyDaemonIsRetryableAndRemainsRunning(t *testing.T) {
	client := &conditionDaemonProber{binary: `C:\managed\roborev.exe`, version: "v0.64.0", healthyAfter: 100}
	installer := &fakeInstaller{target: client.binary, install: func(context.Context) (string, error) {
		return "", errors.New("installer must not run for daemon recovery")
	}}
	manager, err := NewManager(client, installer)
	if err != nil {
		t.Fatal(err)
	}
	manager.wait = func(context.Context, time.Duration) error { return nil }
	state := manager.Status(context.Background())
	if state.Phase != PhaseFailed || state.FailureCode != "daemon_unhealthy" || !state.DaemonRunning || state.DaemonCondition != roborev.DaemonConditionUnhealthy || !state.CanRetry || !state.Retryable {
		t.Fatalf("permanent unhealthy state = %#v", state)
	}
}

func TestInstallPermanentReachableUnhealthyDaemonIsRetryableAndRemainsRunning(t *testing.T) {
	client := &conditionDaemonProber{healthyAfter: 100}
	target := filepath.Join(t.TempDir(), "roborev.exe")
	installer := &fakeInstaller{target: target, install: func(context.Context) (string, error) {
		return target, nil
	}}
	manager, err := NewManager(client, installer)
	if err != nil {
		t.Fatal(err)
	}
	manager.wait = func(context.Context, time.Duration) error { return nil }
	if _, err := manager.StartInstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := waitSetupLong(t, manager)
	if state.Phase != PhaseFailed || state.Message == "RoboRev is ready" || !state.DaemonRunning || state.DaemonCondition != roborev.DaemonConditionUnhealthy || !state.CanRetry || !state.Retryable {
		t.Fatalf("permanent unhealthy install state = %#v", state)
	}
}

func TestRefreshTypedProbeFailureMatrixKeepsOfflineStartUX(t *testing.T) {
	for _, test := range []struct {
		name          string
		health        roborev.DaemonHealthProbeState
		condition     roborev.DaemonHealthCondition
		daemonVersion string
		wantPhase     Phase
		wantCanStart  bool
		wantRetryable bool
		wantCode      string
	}{
		{name: "offline remains startable", health: roborev.DaemonHealthOffline, condition: roborev.DaemonConditionUnknown, wantPhase: PhaseReady, wantCanStart: true},
		{name: "invalid fails closed", health: roborev.DaemonHealthInvalid, condition: roborev.DaemonConditionUnknown, wantPhase: PhaseFailed, wantCanStart: true, wantRetryable: true, wantCode: "daemon_health_invalid"},
		{name: "unknown fails closed", health: roborev.DaemonHealthUnknown, condition: roborev.DaemonConditionUnknown, wantPhase: PhaseFailed, wantCanStart: true, wantRetryable: true, wantCode: "daemon_health_invalid"},
		{name: "daemon version missing", health: roborev.DaemonHealthOnline, condition: roborev.DaemonConditionHealthy, wantPhase: PhaseFailed, wantCanStart: true, wantRetryable: true, wantCode: "daemon_version_missing"},
		{name: "daemon version invalid", health: roborev.DaemonHealthOnline, condition: roborev.DaemonConditionHealthy, daemonVersion: "0.64.0", wantPhase: PhaseFailed, wantCanStart: true, wantRetryable: true, wantCode: "daemon_version_invalid"},
		{name: "daemon version mismatch", health: roborev.DaemonHealthOnline, condition: roborev.DaemonConditionHealthy, daemonVersion: "v0.63.0", wantPhase: PhaseFailed, wantCanStart: true, wantRetryable: true, wantCode: "daemon_version_mismatch"},
		{name: "unhealthy with invalid daemon version reports incompatibility", health: roborev.DaemonHealthOnline, condition: roborev.DaemonConditionUnhealthy, daemonVersion: "0.64.0", wantPhase: PhaseFailed, wantCanStart: true, wantRetryable: true, wantCode: "daemon_version_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &typedHealthProber{installation: roborev.Installation{
				Installed:       true,
				Binary:          `C:\managed\roborev.exe`,
				Version:         "v0.64.0",
				Status:          json.RawMessage(`{"running":false,"known":true}`),
				DaemonHealth:    test.health,
				DaemonCondition: test.condition,
				DaemonVersion:   test.daemonVersion,
			}}
			installer := &fakeInstaller{target: client.installation.Binary, install: func(context.Context) (string, error) {
				return "", errors.New("installer must not run during refresh")
			}}
			manager, err := NewManager(client, installer)
			if err != nil {
				t.Fatal(err)
			}
			state := manager.Status(context.Background())
			if state.Phase != test.wantPhase || state.DaemonRunning || state.CanStartDaemon != test.wantCanStart || state.DaemonCondition != test.condition {
				t.Fatalf("typed probe state = %#v", state)
			}
			if state.FailureCode != test.wantCode || (test.wantRetryable && (!state.CanRetry || !state.Retryable || state.Message == "RoboRev is ready")) {
				t.Fatalf("typed probe failure state = %#v", state)
			}
		})
	}
}

func TestDaemonRetryDoesNotBecomeReadyBeforeHealthyProbe(t *testing.T) {
	client := &conditionDaemonProber{binary: `C:\managed\roborev.exe`, version: "v0.64.0", healthyAfter: 3}
	installer := &fakeInstaller{target: client.binary, install: func(context.Context) (string, error) {
		return "", errors.New("installer must not run for daemon recovery")
	}}
	gate := operation.NewGate()
	manager, err := NewManagerWithGate(client, installer, gate)
	if err != nil {
		t.Fatal(err)
	}
	cleanupSetupManager(t, manager)
	firstWait := make(chan struct{})
	releaseWait := make(chan struct{})
	manager.wait = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-firstWait:
		case <-ctx.Done():
			return ctx.Err()
		default:
			close(firstWait)
			select {
			case <-releaseWait:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	state := manager.Status(context.Background())
	if state.Phase != PhaseFailed || !state.DaemonRunning {
		t.Fatalf("initial unhealthy state = %#v", state)
	}
	if _, err := manager.Retry(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitSetupSignal(t, firstWait)
	if release, err := gate.AcquireMutation(); !errors.Is(err, operation.ErrBusy) {
		if err == nil {
			release()
		}
		t.Fatalf("mutation gate acquisition during health-only recovery = %v, want busy", err)
	}
	intermediate := manager.stateCopy()
	if intermediate.Phase == PhaseReady || intermediate.DaemonCondition == roborev.DaemonConditionHealthy {
		t.Fatalf("retry became ready before healthy probe = %#v", intermediate)
	}
	close(releaseWait)
	final := waitSetupLong(t, manager)
	if final.Phase != PhaseReady || final.DaemonCondition != roborev.DaemonConditionHealthy || !final.DaemonRunning {
		t.Fatalf("retry final state = %#v", final)
	}
	client.mu.Lock()
	starts, rebinds := client.starts, client.rebinds
	client.mu.Unlock()
	if starts != 0 || rebinds != 0 {
		t.Fatalf("health-only retry lifecycle calls = start %d rebind %d, want both zero", starts, rebinds)
	}
}

func TestDaemonVersionMismatchRecoveryDoesNotStartOrRebind(t *testing.T) {
	client := &conditionDaemonProber{binary: `C:\managed\roborev.exe`, version: "v0.64.0", daemonVersion: "v0.63.0", healthyAfter: 1}
	installer := &fakeInstaller{target: client.binary, install: func(context.Context) (string, error) {
		return "", errors.New("installer must not run for daemon recovery")
	}}
	manager, err := NewManager(client, installer)
	if err != nil {
		t.Fatal(err)
	}
	manager.wait = func(context.Context, time.Duration) error { return nil }
	state := manager.Status(context.Background())
	if state.Phase != PhaseFailed || state.FailureCode != "daemon_version_mismatch" || !state.CanRetry {
		t.Fatalf("mismatched daemon state = %#v", state)
	}
	if _, err := manager.Retry(context.Background()); err != nil {
		t.Fatal(err)
	}
	state = waitSetupLong(t, manager)
	if state.Phase != PhaseFailed || state.FailureCode != "daemon_version_mismatch" || !state.CanRetry || state.DaemonVersion != "v0.63.0" {
		t.Fatalf("mismatched recovery state = %#v", state)
	}
	client.mu.Lock()
	starts, rebinds := client.starts, client.rebinds
	client.mu.Unlock()
	if starts != 0 || rebinds != 0 {
		t.Fatalf("mismatched recovery lifecycle calls = start %d rebind %d, want both zero", starts, rebinds)
	}
}

func TestDaemonVersionMismatchRecoveryPublishesOnlyAfterExactMatch(t *testing.T) {
	client := &conditionDaemonProber{binary: `C:\managed\roborev.exe`, version: "v0.64.0", daemonVersion: "v0.63.0", daemonVersionAfter: 3, healthyAfter: 1}
	installer := &fakeInstaller{target: client.binary, install: func(context.Context) (string, error) {
		return "", errors.New("installer must not run for health recovery")
	}}
	manager, err := NewManager(client, installer)
	if err != nil {
		t.Fatal(err)
	}
	manager.wait = func(context.Context, time.Duration) error { return nil }
	state := manager.Status(context.Background())
	if state.Phase != PhaseFailed || state.FailureCode != "daemon_version_mismatch" {
		t.Fatalf("initial mismatch state = %#v", state)
	}
	if _, err := manager.Retry(context.Background()); err != nil {
		t.Fatal(err)
	}
	state = waitSetupLong(t, manager)
	if state.Phase != PhaseReady || state.DaemonVersion != "v0.64.0" || !state.DaemonRunning {
		t.Fatalf("eventual exact-match state = %#v", state)
	}
	client.mu.Lock()
	starts, rebinds := client.starts, client.rebinds
	client.mu.Unlock()
	installer.mu.Lock()
	installs := installer.calls
	installer.mu.Unlock()
	if starts != 0 || rebinds != 0 || installs != 0 {
		t.Fatalf("health recovery lifecycle calls = start %d rebind %d install %d, want all zero", starts, rebinds, installs)
	}
}

func TestDaemonReadyRequiresExactSupportedVersionPair(t *testing.T) {
	base := roborev.Installation{Installed: true, Version: "v0.64.0", DaemonHealth: roborev.DaemonHealthOnline, DaemonCondition: roborev.DaemonConditionHealthy, DaemonVersion: "v0.64.0"}
	for _, test := range []struct {
		name string
		edit func(*roborev.Installation)
		want bool
	}{
		{name: "matching", want: true},
		{name: "missing daemon version", edit: func(i *roborev.Installation) { i.DaemonVersion = "" }},
		{name: "invalid daemon version", edit: func(i *roborev.Installation) { i.DaemonVersion = "0.64.0" }},
		{name: "mismatched daemon version", edit: func(i *roborev.Installation) { i.DaemonVersion = "v0.63.0" }},
		{name: "unhealthy", edit: func(i *roborev.Installation) { i.DaemonCondition = roborev.DaemonConditionUnhealthy }},
		{name: "offline", edit: func(i *roborev.Installation) {
			i.DaemonHealth = roborev.DaemonHealthOffline
			i.DaemonCondition = roborev.DaemonConditionUnknown
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			installation := base
			if test.edit != nil {
				test.edit(&installation)
			}
			if got := daemonReady(installation); got != test.want {
				t.Fatalf("daemonReady = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRefreshGenerationPublishesOnlyNewestProbe(t *testing.T) {
	gateOne := make(chan struct{})
	gateTwo := make(chan struct{})
	startedOne := make(chan struct{})
	startedTwo := make(chan struct{})
	first := roborev.Installation{Installed: true, Binary: `C:\first\roborev.exe`, Version: "v0.64.0", Status: json.RawMessage(`{"running":true,"known":true}`), DaemonHealth: roborev.DaemonHealthOnline, DaemonCondition: roborev.DaemonConditionHealthy, DaemonVersion: "v0.64.0"}
	second := first
	second.Binary = `C:\second\roborev.exe`
	client := &refreshSequenceProber{binary: first.Binary, samples: []roborev.Installation{first, second}, gates: []<-chan struct{}{gateOne, gateTwo}, started: []chan struct{}{startedOne, startedTwo}}
	manager, err := NewManager(client, &fakeInstaller{target: first.Binary, install: func(context.Context) (string, error) { return first.Binary, nil }})
	if err != nil {
		t.Fatal(err)
	}
	cleanupSetupManager(t, manager)
	firstDone := make(chan struct{})
	go func() {
		manager.refresh(context.Background())
		close(firstDone)
	}()
	awaitSetupSignal(t, startedOne)
	secondDone := make(chan struct{})
	go func() {
		manager.refresh(context.Background())
		close(secondDone)
	}()
	awaitSetupSignal(t, startedTwo)
	close(gateTwo)
	awaitSetupSignal(t, secondDone)
	if state := manager.stateCopy(); state.Binary != second.Binary || state.Phase != PhaseReady {
		t.Fatalf("newest refresh state = %#v", state)
	}
	close(gateOne)
	awaitSetupSignal(t, firstDone)
	if state := manager.stateCopy(); state.Binary != second.Binary || state.Phase != PhaseReady {
		t.Fatalf("stale refresh overwrote state = %#v", state)
	}
}

func TestCanceledRefreshDoesNotPublish(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{})
	client := &refreshSequenceProber{
		binary:  `C:\stale\roborev.exe`,
		samples: []roborev.Installation{{Installed: true, Binary: `C:\stale\roborev.exe`, Version: "v0.64.0", Status: json.RawMessage(`{"running":true}`), DaemonHealth: roborev.DaemonHealthOnline, DaemonCondition: roborev.DaemonConditionHealthy, DaemonVersion: "v0.64.0"}},
		gates:   []<-chan struct{}{gate},
		started: []chan struct{}{started},
	}
	manager, err := NewManager(client, &fakeInstaller{target: client.binary, install: func(context.Context) (string, error) { return client.binary, nil }})
	if err != nil {
		t.Fatal(err)
	}
	cleanupSetupManager(t, manager)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.refresh(ctx)
		close(done)
	}()
	awaitSetupSignal(t, started)
	cancel()
	awaitSetupSignal(t, done)
	state := manager.stateCopy()
	if state.Phase != PhaseIdle || state.Binary != "" || state.Installed {
		t.Fatalf("canceled refresh published state = %#v", state)
	}
	fresh := manager.Status(context.Background())
	if fresh.Phase != PhaseReady || fresh.Binary != client.binary || !fresh.Installed || fresh.DaemonHealth != roborev.DaemonHealthOnline || fresh.DaemonVersion != "v0.64.0" {
		t.Fatalf("fresh refresh state = %#v", fresh)
	}
}

func TestAcceptedSetupOperationInvalidatesPendingRefresh(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{})
	client := &refreshSequenceProber{binary: `C:\pending\roborev.exe`, samples: []roborev.Installation{{Installed: true, Binary: `C:\pending\roborev.exe`, Version: "v0.64.0", Status: json.RawMessage(`{"running":true}`), DaemonHealth: roborev.DaemonHealthOnline, DaemonCondition: roborev.DaemonConditionHealthy, DaemonVersion: "v0.64.0"}}, gates: []<-chan struct{}{gate}, started: []chan struct{}{started}}
	manager, err := NewManager(client, &fakeInstaller{target: client.binary, install: func(context.Context) (string, error) { return client.binary, nil }})
	if err != nil {
		t.Fatal(err)
	}
	cleanupSetupManager(t, manager)
	refreshDone := make(chan struct{})
	go func() {
		manager.refresh(context.Background())
		close(refreshDone)
	}()
	awaitSetupSignal(t, started)
	if _, err := manager.acceptAsync(context.Background(), nil, time.Second, func(state State) State {
		state.Phase, state.Status = PhaseChecking, PhaseChecking
		return state
	}, func(ctx context.Context) {
		manager.finishError(ctx, errors.New("deterministic terminal setup failure"), `C:\terminal\roborev.exe`)
	}); err != nil {
		t.Fatal(err)
	}
	terminalDeadline := time.Now().Add(time.Second)
	for time.Now().Before(terminalDeadline) {
		state := manager.stateCopy()
		if !state.InProgress && state.Phase == PhaseFailed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	terminal := manager.stateCopy()
	if terminal.InProgress || terminal.Phase != PhaseFailed || !strings.Contains(terminal.Error, "deterministic terminal setup failure") {
		t.Fatalf("accepted operation did not publish terminal state before stale release = %#v", terminal)
	}
	close(gate)
	awaitSetupSignal(t, refreshDone)
	state := manager.stateCopy()
	if state.Phase != PhaseFailed || !strings.Contains(state.Error, "deterministic terminal setup failure") {
		t.Fatalf("pending refresh overwrote terminal operation = %#v", state)
	}
}

func equalDaemonConditions(left, right []roborev.DaemonHealthCondition) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestSetupGateIsHeldForEntireInstallLifetime(t *testing.T) {
	client := &fakeProber{}
	started := make(chan struct{})
	releaseInstall := make(chan struct{})
	installer := &fakeInstaller{target: `C:\managed\roborev.exe`, install: func(context.Context) (string, error) {
		close(started)
		<-releaseInstall
		return `C:\managed\roborev.exe`, nil
	}}
	gate := operation.NewGate()
	manager, err := NewManagerWithGate(client, installer, gate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartInstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := gate.AcquireMutation(); !errors.Is(err, operation.ErrBusy) {
		t.Fatalf("mutation during setup = %v", err)
	}
	client.mu.Lock()
	client.version = "v0.64.0"
	client.mu.Unlock()
	close(releaseInstall)
	state := waitSetupLong(t, manager)
	if state.Phase != PhaseReady {
		t.Fatalf("state = %#v", state)
	}
	releaseMutation, err := gate.AcquireMutation()
	if err != nil {
		t.Fatal(err)
	}
	releaseMutation()
}

func waitSetupLong(t *testing.T, manager *Manager) State {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		state := manager.stateCopy()
		if !state.InProgress {
			return state
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("setup did not settle")
	return State{}
}

func TestShutdownCancelsBackgroundInstall(t *testing.T) {
	client := &fakeProber{}
	started := make(chan struct{})
	canceled := make(chan struct{})
	gate := operation.NewGate()
	installer := &fakeInstaller{install: func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return "", ctx.Err()
	}}
	manager, _ := NewManagerWithGate(client, installer, gate)
	if _, err := manager.StartInstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := gate.AcquireMutation(); !errors.Is(err, operation.ErrBusy) {
		t.Fatalf("mutation during setup = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("installer did not observe shutdown cancellation")
	}
	if state := manager.stateCopy(); state.Phase != PhaseCanceled || state.FailureCode != "canceled" || state.Error == "" {
		t.Fatalf("state = %#v", state)
	}
	release, err := gate.AcquireMutation()
	if err != nil {
		t.Fatalf("mutation after shutdown = %v", err)
	}
	release()
}

func TestInstallSafetyCeilingCancelsBackgroundOperation(t *testing.T) {
	client := &fakeProber{}
	started := make(chan struct{})
	deadlineErr := make(chan error, 1)
	installer := &fakeInstaller{install: func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		deadlineErr <- ctx.Err()
		return "", ctx.Err()
	}}
	manager, err := NewManager(client, installer)
	if err != nil {
		t.Fatal(err)
	}
	manager.setupCeiling = 20 * time.Millisecond
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	if _, err := manager.StartInstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-started
	state := waitSetupLong(t, manager)
	select {
	case err := <-deadlineErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("installer context error = %v, want deadline exceeded", err)
		}
	default:
		t.Fatal("installer did not observe setup safety ceiling")
	}
	if state.Phase != PhaseCanceled || state.FailureCode != "canceled" {
		t.Fatalf("timeout state = %#v", state)
	}
}

func TestInstallDaemonCancellationPreservesRetryableInstalledState(t *testing.T) {
	tests := []struct {
		name        string
		ceiling     time.Duration
		wantErr     error
		useShutdown bool
	}{
		{name: "shutdown", wantErr: context.Canceled, useShutdown: true},
		{name: "timeout", ceiling: 20 * time.Millisecond, wantErr: context.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gate := operation.NewGate()
			startCtxErr := make(chan error, 1)
			client := &daemonProber{healthyAfter: 100, startStarted: make(chan struct{}), startRelease: make(chan struct{}), startCtxErr: startCtxErr}
			target := filepath.Join(t.TempDir(), "roborev.exe")
			installer := &fakeInstaller{target: target, install: func(context.Context) (string, error) { return target, nil }}
			manager, err := NewManagerWithGate(client, installer, gate)
			if err != nil {
				t.Fatal(err)
			}
			cleanupSetupManager(t, manager)
			if test.ceiling > 0 {
				manager.setupCeiling = test.ceiling
			}
			if _, err := manager.StartInstall(context.Background()); err != nil {
				t.Fatal(err)
			}
			<-client.startStarted
			client.mu.Lock()
			installationsBeforeCancel := client.installations
			client.mu.Unlock()
			if test.useShutdown {
				shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
				if err := manager.Shutdown(shutdown); err != nil {
					cancel()
					t.Fatal(err)
				}
				cancel()
			} else {
				_ = waitSetupLong(t, manager)
			}
			select {
			case err := <-startCtxErr:
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("daemon context error = %v, want %v", err, test.wantErr)
				}
			default:
				t.Fatal("daemon start did not observe cancellation")
			}
			state := manager.stateCopy()
			if state.Phase != PhaseFailed || !state.Installed || !state.Supported || state.DaemonRunning || !state.CanStartDaemon || !state.CanRetry || !state.Retryable || state.FailureCode != "daemon_start_failed" || state.InProgress {
				t.Fatalf("install daemon cancellation state = %#v", state)
			}
			client.mu.Lock()
			installationsAfterCancel := client.installations
			client.mu.Unlock()
			if installationsAfterCancel != installationsBeforeCancel {
				t.Fatalf("health probes after cancellation = %d, before = %d", installationsAfterCancel, installationsBeforeCancel)
			}
			assertSetupGateReleased(t, gate)
		})
	}
}
