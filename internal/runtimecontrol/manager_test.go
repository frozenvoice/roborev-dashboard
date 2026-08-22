package runtimecontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	queueErr                            error
	queueStatusCalls                    int
	cancelAfterSet                      context.CancelFunc
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
	f.queueStatusCalls++
	if f.queueErr != nil {
		return roborev.QueueState{}, f.queueErr
	}
	if !f.online {
		return roborev.QueueState{}, roborev.ErrDaemonOffline
	}
	return roborev.QueueState{Known: true, Paused: f.paused}, nil
}
func (f *fakeRuntimeAdapter) SetQueuePaused(_ context.Context, paused bool) (roborev.QueueState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.online {
		return roborev.QueueState{}, roborev.ErrDaemonOffline
	}
	f.paused = paused
	if f.cancelAfterSet != nil {
		f.cancelAfterSet()
	}
	return roborev.QueueState{Known: true, Paused: f.paused}, nil
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
	if state.Result != "failure" || state.Error == "" || !strings.Contains(state.Error, ErrHealthTimeout.Error()) {
		t.Fatalf("stop state = %#v", state)
	}
}

type reachableHealthFailureAdapter struct{ *fakeRuntimeAdapter }

func (a *reachableHealthFailureAdapter) DaemonHealth(context.Context) (bool, error) {
	return true, fmt.Errorf("reachable daemon health payload is invalid")
}

type reachableUnhealthyRestartAdapter struct {
	*fakeRuntimeAdapter
	healthCalls int
}

func (a *reachableUnhealthyRestartAdapter) DaemonHealth(context.Context) (bool, error) {
	a.mu.Lock()
	a.healthCalls++
	a.mu.Unlock()
	return true, roborev.ErrDaemonUnhealthy
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

func TestRestartReachableUnhealthyHealthEndsInFailure(t *testing.T) {
	fake := &fakeRuntimeAdapter{online: true}
	adapter := &reachableUnhealthyRestartAdapter{fakeRuntimeAdapter: fake}
	m := fastManager(t, adapter, fake, operation.NewGate())
	state, err := m.Restart(context.Background())
	if err != nil || !state.Active {
		t.Fatalf("restart acceptance = %#v, %v", state, err)
	}
	deadline := time.Now().Add(time.Second)
	for m.Status().Active && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	state = m.Status()
	if state.Result != "failure" || state.Result == "success" {
		t.Fatalf("reachable unhealthy restart state = %#v, want terminal failure", state)
	}
	adapter.mu.Lock()
	healthCalls := adapter.healthCalls
	restartCalls := adapter.restartCalls
	adapter.mu.Unlock()
	if healthCalls < 2 {
		t.Fatalf("health calls = %d, want bounded retry probes", healthCalls)
	}
	if restartCalls != 1 {
		t.Fatalf("restart calls = %d, want 1", restartCalls)
	}
}

type slowReachableHealthAdapter struct{ *fakeRuntimeAdapter }

func (a *slowReachableHealthAdapter) DaemonHealth(ctx context.Context) (bool, error) {
	select {
	case <-time.After(10 * time.Millisecond):
		return true, fmt.Errorf("reachable health probe timed out")
	case <-ctx.Done():
		return true, fmt.Errorf("reachable health probe interrupted: %w", ctx.Err())
	}
}

func TestStopWithSlowReachableHealthEndsInFailure(t *testing.T) {
	fake := &fakeRuntimeAdapter{online: true}
	adapter := &slowReachableHealthAdapter{fakeRuntimeAdapter: fake}
	m := fastManager(t, adapter, fake, operation.NewGate())
	if _, err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for m.Status().Active && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	state := m.Status()
	if state.Result == "success" || state.Result != "failure" || !strings.Contains(state.Error, ErrHealthTimeout.Error()) {
		t.Fatalf("slow reachable stop state = %#v, want terminal failure", state)
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

func TestQueueControlUsesAuthoritativePostStateWithoutSecondStatusRead(t *testing.T) {
	fake := &fakeRuntimeAdapter{online: true}
	m := fastManager(t, fake, fake, operation.NewGate())
	state, err := m.PauseQueue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Result != "success" || !state.QueueKnown || !state.QueuePaused {
		t.Fatalf("pause state = %#v", state)
	}
	fake.mu.Lock()
	statusCalls := fake.queueStatusCalls
	fake.mu.Unlock()
	if statusCalls != 0 {
		t.Fatalf("manager performed %d second queue status reads after authoritative POST", statusCalls)
	}
}

func TestQueueControlSuccessIsNotTurnedIntoFailureAfterAuthoritativePost(t *testing.T) {
	fake := &fakeRuntimeAdapter{online: true}
	request, cancel := context.WithCancel(context.Background())
	fake.cancelAfterSet = cancel
	m := fastManager(t, fake, fake, operation.NewGate())
	state, err := m.PauseQueue(request)
	if err != nil {
		t.Fatalf("authoritative POST was reported as failure: %v", err)
	}
	if state.Result != "success" || !state.QueueKnown || !state.QueuePaused {
		t.Fatalf("pause state after request cancellation = %#v", state)
	}
}

func TestRefreshInvalidatesStaleQueueStateAfterStatusFailure(t *testing.T) {
	fake := &fakeRuntimeAdapter{online: true, paused: true}
	m := fastManager(t, fake, fake, operation.NewGate())
	state := m.Refresh(context.Background())
	if !state.QueueKnown || !state.QueuePaused {
		t.Fatalf("initial queue state = %#v", state)
	}
	fake.mu.Lock()
	fake.queueErr = errors.New("queue status read failed")
	fake.mu.Unlock()
	state = m.Refresh(context.Background())
	if state.DaemonKnown != true || !state.DaemonRunning || state.QueueKnown || state.QueuePaused {
		t.Fatalf("stale queue state was retained after read failure = %#v", state)
	}
}

func TestRefreshInvalidatesQueueStateWhenDaemonGoesOffline(t *testing.T) {
	fake := &fakeRuntimeAdapter{online: true, paused: true}
	m := fastManager(t, fake, fake, operation.NewGate())
	if state := m.Refresh(context.Background()); !state.QueueKnown {
		t.Fatalf("initial queue state = %#v", state)
	}
	fake.mu.Lock()
	fake.online = false
	fake.mu.Unlock()
	state := m.Refresh(context.Background())
	if state.QueueKnown || state.QueuePaused {
		t.Fatalf("offline refresh retained stale queue state = %#v", state)
	}
}
