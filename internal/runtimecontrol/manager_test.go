package runtimecontrol

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type fakeRuntimeAdapter struct {
	mu                                  sync.Mutex
	online                              bool
	paused                              bool
	startCalls, stopCalls, restartCalls int
	startEntered                        chan struct{}
	startRelease                        chan struct{}
	waitForHealth                       func()
}

func (f *fakeRuntimeAdapter) DaemonHealth(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.online, nil
}
func (f *fakeRuntimeAdapter) StartDaemon(ctx context.Context) error {
	f.mu.Lock()
	f.startCalls++
	entered := f.startEntered
	release := f.startRelease
	f.mu.Unlock()
	if entered != nil {
		close(entered)
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	f.online = true
	f.mu.Unlock()
	return nil
}
func (f *fakeRuntimeAdapter) StopDaemon(context.Context) error {
	f.mu.Lock()
	f.stopCalls++
	f.online = false
	f.mu.Unlock()
	return nil
}
func (f *fakeRuntimeAdapter) RestartDaemon(context.Context) error {
	f.mu.Lock()
	f.restartCalls++
	f.online = true // models upstream restart's already-stopped -> start behavior.
	f.mu.Unlock()
	return nil
}
func (f *fakeRuntimeAdapter) QueueStatus(context.Context) (roborev.QueueState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.online {
		return roborev.QueueState{}, roborev.ErrDaemonOffline
	}
	return roborev.QueueState{Known: true, Paused: f.paused}, nil
}
func (f *fakeRuntimeAdapter) SetQueuePaused(_ context.Context, paused bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.online {
		return roborev.ErrDaemonOffline
	}
	f.paused = paused
	return nil
}

func fastManager(t *testing.T, adapter any, fake *fakeRuntimeAdapter, gate *operation.Gate) *Manager {
	t.Helper()
	m, err := NewManagerWithDependencies(Dependencies{
		Adapter:     adapter,
		Gate:        gate,
		Timeout:     150 * time.Millisecond,
		RetryDelays: []time.Duration{time.Millisecond},
		Wait: func(ctx context.Context, d time.Duration) error {
			if fake.waitForHealth != nil {
				fake.waitForHealth()
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRestartCallsUpstreamOnceAndAcceptsAlreadyStoppedDaemon(t *testing.T) {
	fake := &fakeRuntimeAdapter{online: false}
	gate := operation.NewGate()
	m := fastManager(t, fake, fake, gate)
	state, err := m.Restart(context.Background())
	if err != nil || !state.Active {
		t.Fatalf("restart acceptance = %#v, %v", state, err)
	}
	deadline := time.Now().Add(time.Second)
	for m.Status().Active && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	state = m.Status()
	if state.Result != "success" || !state.DaemonRunning {
		t.Fatalf("restart state = %#v", state)
	}
	fake.mu.Lock()
	calls := fake.restartCalls
	fake.mu.Unlock()
	if calls != 1 {
		t.Fatalf("restart calls = %d, want 1", calls)
	}
	if _, err := gate.AcquireMutation(); err != nil {
		t.Fatalf("gate was not released: %v", err)
	}
}

func TestStopFailsWhenHealthRemainsOnline(t *testing.T) {
	fake := &fakeRuntimeAdapter{online: true}
	fakeAdapter := &onlineStopAdapter{fakeRuntimeAdapter: fake}
	m := fastManager(t, fakeAdapter, fake, operation.NewGate())
	state, err := m.Stop(context.Background())
	if err != nil || !state.Active {
		t.Fatalf("stop acceptance = %#v, %v", state, err)
	}
	deadline := time.Now().Add(time.Second)
	for m.Status().Active && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	state = m.Status()
	if state.Result != "failure" || state.Error == "" {
		t.Fatalf("stop state = %#v", state)
	}
}

type reachableHealthFailureAdapter struct{ *fakeRuntimeAdapter }

func (a *reachableHealthFailureAdapter) DaemonHealth(context.Context) (bool, error) {
	return true, fmt.Errorf("reachable daemon health payload is invalid")
}

func TestStopDoesNotTreatReachableHealthFailureAsOffline(t *testing.T) {
	fake := &fakeRuntimeAdapter{online: true}
	adapter := &reachableHealthFailureAdapter{fakeRuntimeAdapter: fake}
	m := fastManager(t, adapter, fake, operation.NewGate())
	if _, err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for m.Status().Active && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if state := m.Status(); state.Result != "failure" {
		t.Fatalf("reachable health failure stop state = %#v", state)
	}
}

// onlineStopAdapter embeds the fake while overriding stop so health stays
// online and the bounded offline verification must fail.
type onlineStopAdapter struct{ *fakeRuntimeAdapter }

func (f *onlineStopAdapter) StopDaemon(context.Context) error {
	f.mu.Lock()
	f.stopCalls++
	f.online = true
	f.mu.Unlock()
	return nil
}

func TestAcceptedStartSurvivesRequestCancellation(t *testing.T) {
	fake := &fakeRuntimeAdapter{online: false}
	m := fastManager(t, fake, fake, operation.NewGate())
	request, cancelRequest := context.WithCancel(context.Background())
	state, err := m.Start(request)
	if err != nil || !state.Active {
		t.Fatalf("start acceptance = %#v, %v", state, err)
	}
	cancelRequest()
	deadline := time.Now().Add(time.Second)
	for m.Status().Active && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := m.Status(); got.Result != "success" {
		t.Fatalf("request cancellation canceled accepted operation: %#v", got)
	}
}

func TestShutdownCancelsWorkerAndReleasesRuntimeGate(t *testing.T) {
	fake := &fakeRuntimeAdapter{online: false, startEntered: make(chan struct{}), startRelease: make(chan struct{})}
	gate := operation.NewGate()
	m := fastManager(t, fake, fake, gate)
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fake.startEntered:
	case <-time.After(time.Second):
		t.Fatal("start did not begin")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if m.Status().Active {
		t.Fatal("worker remained active after shutdown")
	}
	release, err := gate.AcquireRuntimeControl()
	if err != nil {
		t.Fatalf("runtime gate remained held: %v", err)
	}
	release()
}

func TestQueueControlIsOfflineDisabledAndUsesRuntimeGate(t *testing.T) {
	fake := &fakeRuntimeAdapter{online: false}
	gate := operation.NewGate()
	m := fastManager(t, fake, fake, gate)
	_, err := m.PauseQueue(context.Background())
	if !errors.Is(err, ErrDaemonOffline) {
		t.Fatalf("offline pause = %v", err)
	}
	if _, err := gate.AcquireMutation(); err != nil {
		t.Fatalf("queue failure leaked gate: %v", err)
	}
}
