// Package runtimecontrol coordinates daemon lifecycle and queue controls.
//
// The manager deliberately owns process lifetime independently from HTTP
// request contexts for lifecycle operations. A request only accepts the
// operation; the manager's root context and bounded timeout own execution.
package runtimecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const defaultOperationTimeout = 2 * time.Minute

var (
	ErrBusy               = operation.ErrBusy
	ErrDaemonOffline      = roborev.ErrDaemonOffline
	ErrQueueStateMismatch = roborev.ErrQueueStateMismatch
	ErrManagerClosed      = errors.New("runtime-control manager is closed")
	ErrUnsupported        = errors.New("runtime-control adapter does not support this operation")
	ErrHealthTimeout      = errors.New("daemon health did not reach the requested state")
)

// HealthRetryDelays is the bounded-health retry schedule used after daemon
// lifecycle commands. The final one-second delay is repeated until the
// operation ceiling or manager shutdown is reached.
var HealthRetryDelays = []time.Duration{
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
}

type Operation string

const (
	OperationIdle    Operation = "idle"
	OperationStart   Operation = "start"
	OperationStop    Operation = "stop"
	OperationRestart Operation = "restart"
	OperationPause   Operation = "pause"
	OperationResume  Operation = "resume"
)

// State is a typed, process-local snapshot. DaemonKnown and QueueKnown avoid
// presenting stale false values when a cold dashboard has not yet probed the
// daemon or when the daemon is offline.
type State struct {
	DaemonRunning bool `json:"daemonRunning"`
	DaemonKnown   bool `json:"daemonKnown"`
	QueueKnown    bool `json:"queueKnown"`
	QueuePaused   bool `json:"queuePaused"`

	Active      bool       `json:"active"`
	Operation   Operation  `json:"operation"`
	Result      string     `json:"result,omitempty"`
	StartedAt   time.Time  `json:"startedAt,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Error       string     `json:"error,omitempty"`

	Capabilities   map[string]bool `json:"capabilities,omitempty"`
	DisabledReason string          `json:"disabledReason,omitempty"`

	// Compatibility aliases for clients that describe liveness/queue fields
	// using the shorter names. They are kept out of JSON to avoid duplicate
	// wire fields while remaining useful to Go callers.
	Running      bool `json:"-"`
	RunningKnown bool `json:"-"`
}

// Adapter is the narrow, machine-facing runtime contract implemented by
// roborev.Client. Tests may provide a deterministic fake with the same typed
// methods; no generic shell or database handle is accepted.
type Adapter interface {
	DaemonHealth(context.Context) (bool, error)
	StartDaemon(context.Context) error
	StopDaemon(context.Context) error
	RestartDaemon(context.Context) error
	QueueStatus(context.Context) (roborev.QueueState, error)
	SetQueuePaused(context.Context, bool) (roborev.QueueState, error)
}

type Dependencies struct {
	// Adapter accepts a concrete roborev.Client or a deterministic fake. The
	// manager uses narrow typed interfaces at each call site, so adapters can
	// implement only the operations they expose.
	Adapter        any
	RoboRev        any
	Controller     any
	Gate           *operation.Gate
	Timeout        time.Duration
	Wait           func(context.Context, time.Duration) error
	RetryDelays    []time.Duration
	Capabilities   map[string]bool
	DisabledReason string
}

type Manager struct {
	adapter                  any
	gate                     *operation.Gate
	timeout                  time.Duration
	waitFn                   func(context.Context, time.Duration) error
	retryDelays              []time.Duration
	configuredDisabledReason string

	root       context.Context
	rootCancel context.CancelFunc

	mu          sync.RWMutex
	state       State
	cancel      context.CancelFunc
	gateRelease func()
	closing     bool
	wg          sync.WaitGroup
}

// NewManager builds a process-scoped runtime manager. The optional gate is
// provided for compatibility with callers that construct managers before the
// shared app gate is wired; a private gate is used when omitted.
func NewManager(adapter any, gates ...*operation.Gate) (*Manager, error) {
	if deps, ok := adapter.(Dependencies); ok {
		if len(gates) > 0 && deps.Gate == nil {
			deps.Gate = gates[0]
		}
		return NewManagerWithDependencies(deps)
	}
	gate := (*operation.Gate)(nil)
	if len(gates) > 0 {
		gate = gates[0]
	}
	return NewManagerWithDependencies(Dependencies{Adapter: adapter, Gate: gate})
}

func NewManagerWithOptions(deps Dependencies) (*Manager, error) {
	return NewManagerWithDependencies(deps)
}

func NewManagerWithGate(adapter any, gate *operation.Gate) (*Manager, error) {
	return NewManagerWithDependencies(Dependencies{Adapter: adapter, Gate: gate})
}

func NewManagerWithDependencies(deps Dependencies) (*Manager, error) {
	adapter := deps.Adapter
	if adapter == nil {
		adapter = deps.RoboRev
	}
	if adapter == nil {
		adapter = deps.Controller
	}
	if adapter == nil {
		return nil, errors.New("runtime-control adapter is required")
	}
	gate := deps.Gate
	if gate == nil {
		gate = operation.NewGate()
	}
	timeout := deps.Timeout
	if timeout <= 0 {
		timeout = defaultOperationTimeout
	}
	wait := deps.Wait
	if wait == nil {
		wait = waitContext
	}
	delays := append([]time.Duration(nil), deps.RetryDelays...)
	if len(delays) == 0 {
		delays = append([]time.Duration(nil), HealthRetryDelays...)
	}
	root, cancel := context.WithCancel(context.Background())
	now := time.Now().UTC()
	m := &Manager{
		adapter:                  adapter,
		gate:                     gate,
		timeout:                  timeout,
		waitFn:                   wait,
		retryDelays:              delays,
		configuredDisabledReason: deps.DisabledReason,
		root:                     root,
		rootCancel:               cancel,
		state:                    State{Operation: OperationIdle, UpdatedAt: now, Capabilities: cloneCapabilities(deps.Capabilities), DisabledReason: deps.DisabledReason},
	}
	return m, nil
}

func cloneCapabilities(values map[string]bool) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	copy := make(map[string]bool, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

// Status returns the latest snapshot. The variadic context keeps both
// Status() and setup-style Status(ctx) call sites source-compatible.
func (m *Manager) Status(_ ...context.Context) State {
	return m.Snapshot()
}

func (m *Manager) Snapshot() State {
	if m == nil {
		return State{Operation: OperationIdle, DisabledReason: "runtime-control manager is unavailable"}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneState(m.state)
}

func (m *Manager) State() State { return m.Snapshot() }

// Refresh obtains machine-facing daemon/queue state without invoking any CLI
// command. It is safe to call while an async lifecycle operation is running.
func (m *Manager) Refresh(ctx context.Context) State {
	if m == nil {
		return State{Operation: OperationIdle, DisabledReason: "runtime-control manager is unavailable"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := m.ensureSupported(ctx); err != nil {
		m.setDisabled(err)
		return m.Snapshot()
	}
	online, known, healthErr := m.health(ctx)
	if healthErr != nil {
		// Reachable but malformed/unhealthy health responses are not an
		// offline proof. Clear liveness and disable controls until a valid
		// probe succeeds.
		m.observeDaemon(false, false)
		m.invalidateQueue()
		m.setDisabled(healthErr)
		return m.Snapshot()
	}
	m.observeDaemon(online, known)
	m.mu.Lock()
	m.state.DisabledReason = m.configuredDisabledReason
	m.mu.Unlock()
	if online {
		if queue, err := m.queueStatus(ctx); err == nil && queue.Known {
			m.observeQueue(queue.Paused, queue.Known)
		} else {
			m.invalidateQueue()
		}
	} else {
		m.invalidateQueue()
	}
	return m.Snapshot()
}

func cloneState(state State) State {
	state.Capabilities = cloneCapabilities(state.Capabilities)
	if state.Running || state.RunningKnown {
		state.DaemonRunning = state.Running
		state.DaemonKnown = state.RunningKnown
	}
	state.Running = state.DaemonRunning
	state.RunningKnown = state.DaemonKnown
	return state
}

func (m *Manager) observeDaemon(running, known bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.DaemonRunning, m.state.DaemonKnown = running, known
	m.state.Running, m.state.RunningKnown = running, known
	m.state.UpdatedAt = time.Now().UTC()
}

func (m *Manager) observeQueue(paused, known bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.QueuePaused, m.state.QueueKnown = paused, known
	m.state.UpdatedAt = time.Now().UTC()
}

func (m *Manager) invalidateQueue() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.QueuePaused = false
	m.state.QueueKnown = false
	m.state.UpdatedAt = time.Now().UTC()
}

func (m *Manager) Start(parent context.Context) (State, error) {
	return m.startAsync(parent, OperationStart)
}

func (m *Manager) StartDaemon(parent context.Context) (State, error) {
	return m.Start(parent)
}

func (m *Manager) Stop(parent context.Context) (State, error) {
	return m.startAsync(parent, OperationStop)
}

func (m *Manager) StopDaemon(parent context.Context) (State, error) {
	return m.Stop(parent)
}

func (m *Manager) Restart(parent context.Context) (State, error) {
	return m.startAsync(parent, OperationRestart)
}

func (m *Manager) RestartDaemon(parent context.Context) (State, error) {
	return m.Restart(parent)
}

func (m *Manager) startAsync(parent context.Context, op Operation) (State, error) {
	if m == nil {
		return State{}, ErrManagerClosed
	}
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return m.Snapshot(), err
	}
	if err := m.ensureSupported(parent); err != nil {
		m.setDisabled(err)
		return m.Snapshot(), err
	}
	release, err := m.gate.AcquireRuntimeControl()
	if err != nil {
		return m.Snapshot(), err
	}
	return m.accept(parent, release, op)
}

func (m *Manager) accept(parent context.Context, release func(), op Operation) (State, error) {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		release()
		return m.Snapshot(), ErrManagerClosed
	}
	if m.cancel != nil || m.state.Active {
		m.mu.Unlock()
		release()
		return m.Snapshot(), ErrBusy
	}
	// This is the final request-context check. Once it passes, all work is
	// derived from the manager root and survives request disconnects.
	if err := parent.Err(); err != nil {
		m.mu.Unlock()
		release()
		return m.Snapshot(), err
	}
	processCtx, cancel := context.WithTimeout(m.root, m.timeout)
	now := time.Now().UTC()
	m.state.Active = true
	m.state.Operation = op
	m.state.Result = ""
	m.state.Error = ""
	m.state.StartedAt = now
	m.state.CompletedAt = nil
	m.state.UpdatedAt = now
	m.cancel = cancel
	m.gateRelease = release
	m.wg.Add(1)
	m.mu.Unlock()

	go func() {
		var runErr error
		switch op {
		case OperationStart:
			runErr = m.runLifecycle(processCtx, "start", true)
		case OperationStop:
			runErr = m.runLifecycle(processCtx, "stop", false)
		case OperationRestart:
			// The upstream restart command owns the stop/start sequence and
			// starts an already-stopped daemon. Do not split it into two calls.
			runErr = m.runLifecycle(processCtx, "restart", true)
		default:
			runErr = ErrUnsupported
		}
		m.finish(op, runErr, cancel)
	}()
	return m.Snapshot(), nil
}

func (m *Manager) runLifecycle(ctx context.Context, action string, wantOnline bool) error {
	if err := m.daemonAction(ctx, action); err != nil {
		return err
	}
	return m.waitForHealth(ctx, wantOnline)
}

func (m *Manager) waitForHealth(ctx context.Context, wantOnline bool) error {
	var lastErr error
	for attempt := 0; ; attempt++ {
		online, known, err := m.health(ctx)
		if err != nil {
			lastErr = err
		} else {
			m.observeDaemon(online, known)
			if known && online == wantOnline {
				return nil
			}
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				if !wantOnline && lastErr == nil {
					return fmt.Errorf("%w: daemon remained online", ErrHealthTimeout)
				}
				return fmt.Errorf("%w: %v", ErrHealthTimeout, ctxErr)
			}
			return ctxErr
		}
		delay := m.retryDelays[len(m.retryDelays)-1]
		if attempt < len(m.retryDelays) {
			delay = m.retryDelays[attempt]
		}
		if err := m.wait(ctx, delay); err != nil {
			if errors.Is(err, context.DeadlineExceeded) && !wantOnline && lastErr == nil {
				return fmt.Errorf("%w: daemon remained online", ErrHealthTimeout)
			}
			return err
		}
	}
}

func (m *Manager) finish(op Operation, runErr error, cancel context.CancelFunc) {
	if cancel != nil {
		cancel()
	}
	m.mu.Lock()
	now := time.Now().UTC()
	m.state.Active = false
	m.state.CompletedAt = &now
	m.state.UpdatedAt = now
	m.state.Operation = op
	switch {
	case runErr == nil:
		m.state.Result = "success"
		m.state.Error = ""
	case errors.Is(runErr, context.Canceled), errors.Is(runErr, context.DeadlineExceeded):
		m.state.Result = "canceled"
		m.state.Error = runErr.Error()
	default:
		m.state.Result = "failure"
		m.state.Error = runErr.Error()
	}
	m.cancel = nil
	release := m.gateRelease
	m.gateRelease = nil
	m.mu.Unlock()
	if release != nil {
		release()
	}
	m.wg.Done()
}

// SetQueuePaused is request-scoped by design. The runtime gate makes the
// check-and-update atomic with respect to lifecycle and other queue actions.
func (m *Manager) SetQueuePaused(parent context.Context, paused bool) (State, error) {
	if m == nil {
		return State{}, ErrManagerClosed
	}
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return m.Snapshot(), err
	}
	if err := m.ensureSupported(parent); err != nil {
		m.setDisabled(err)
		return m.Snapshot(), err
	}
	release, err := m.gate.AcquireRuntimeControl()
	if err != nil {
		return m.Snapshot(), err
	}
	defer release()
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return m.Snapshot(), ErrManagerClosed
	}
	if m.state.Active {
		m.mu.Unlock()
		return m.Snapshot(), ErrBusy
	}
	if err := parent.Err(); err != nil {
		m.mu.Unlock()
		return m.Snapshot(), err
	}
	now := time.Now().UTC()
	m.state.Active = true
	if paused {
		m.state.Operation = OperationPause
	} else {
		m.state.Operation = OperationResume
	}
	m.state.Result = ""
	m.state.Error = ""
	m.state.StartedAt = now
	m.state.CompletedAt = nil
	m.state.UpdatedAt = now
	m.mu.Unlock()

	online, known, healthErr := m.health(parent)
	if healthErr == nil {
		m.observeDaemon(online, known)
	}
	if healthErr != nil {
		m.observeDaemon(false, false)
		m.invalidateQueue()
		return m.finishSyncQueue(healthErr)
	}
	if !known || !online {
		m.invalidateQueue()
		return m.finishSyncQueue(ErrDaemonOffline)
	}
	queue, err := m.setQueue(parent, paused)
	if err != nil {
		if queue.Known {
			// A verified POST response remains authoritative even when it
			// reports a mismatch. Preserve it for the UI; do not issue a
			// second GET that can race with the mutation.
			m.observeQueue(queue.Paused, true)
		} else {
			m.invalidateQueue()
		}
		return m.finishSyncQueue(err)
	}
	if !queue.Known {
		m.invalidateQueue()
		return m.finishSyncQueue(fmt.Errorf("%w: queue state was not verified", roborev.ErrQueueStateMismatch))
	}
	m.observeQueue(queue.Paused, true)
	if queue.Paused != paused {
		return m.finishSyncQueue(fmt.Errorf("%w: requested %t, daemon returned %t", roborev.ErrQueueStateMismatch, paused, queue.Paused))
	}
	return m.finishSyncQueue(nil)
}

func (m *Manager) PauseQueue(ctx context.Context) (State, error) {
	return m.SetQueuePaused(ctx, true)
}

func (m *Manager) Pause(ctx context.Context) (State, error) {
	return m.PauseQueue(ctx)
}

func (m *Manager) QueuePause(ctx context.Context) (State, error) {
	return m.PauseQueue(ctx)
}

func (m *Manager) ResumeQueue(ctx context.Context) (State, error) {
	return m.SetQueuePaused(ctx, false)
}

func (m *Manager) Resume(ctx context.Context) (State, error) {
	return m.ResumeQueue(ctx)
}

func (m *Manager) QueueUnpause(ctx context.Context) (State, error) {
	return m.ResumeQueue(ctx)
}

func (m *Manager) finishSyncQueue(runErr error) (State, error) {
	m.mu.Lock()
	now := time.Now().UTC()
	m.state.Active = false
	m.state.CompletedAt = &now
	m.state.UpdatedAt = now
	if runErr == nil {
		m.state.Result = "success"
		m.state.Error = ""
	} else if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		m.state.Result = "canceled"
		m.state.Error = runErr.Error()
	} else {
		m.state.Result = "failure"
		m.state.Error = runErr.Error()
	}
	state := cloneState(m.state)
	m.mu.Unlock()
	return state, runErr
}

// Shutdown closes acceptance, cancels all accepted process work, and waits
// for every worker to release its runtime lease. closing is set while holding
// the same mutex used by accept, preventing a WaitGroup Add/Wait race.
func (m *Manager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if !m.closing {
		m.closing = true
		m.rootCancel()
	}
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) daemonAction(ctx context.Context, action string) error {
	if adapter, ok := m.adapter.(interface {
		Daemon(context.Context, string) error
	}); ok {
		return adapter.Daemon(ctx, action)
	}
	var method interface {
		RunDaemon(context.Context, string) error
	}
	if adapter, ok := m.adapter.(interface {
		RunDaemon(context.Context, string) error
	}); ok {
		method = adapter
	}
	if method != nil {
		return method.RunDaemon(ctx, action)
	}
	switch action {
	case "start":
		if adapter, ok := m.adapter.(interface{ StartDaemon(context.Context) error }); ok {
			return adapter.StartDaemon(ctx)
		}
	case "stop":
		if adapter, ok := m.adapter.(interface{ StopDaemon(context.Context) error }); ok {
			return adapter.StopDaemon(ctx)
		}
	case "restart":
		if adapter, ok := m.adapter.(interface{ RestartDaemon(context.Context) error }); ok {
			return adapter.RestartDaemon(ctx)
		}
	}
	return fmt.Errorf("%w: daemon %s", ErrUnsupported, action)
}

func (m *Manager) setDisabled(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.DisabledReason = err.Error()
	m.state.UpdatedAt = time.Now().UTC()
}

func (m *Manager) ensureSupported(ctx context.Context) error {
	if adapter, ok := m.adapter.(interface {
		Installation(context.Context) roborev.Installation
	}); ok {
		installation := adapter.Installation(ctx)
		if !installation.Installed || !roborevcompat.IsSupportedVersion(installation.Version) {
			version := strings.TrimSpace(installation.Version)
			if version == "" {
				version = "unknown"
			}
			return fmt.Errorf("%w: runtime controls require RoboRev v0.64.0 (detected %s)", roborevcompat.ErrUnsupportedVersion, version)
		}
	}
	return nil
}

func (m *Manager) health(ctx context.Context) (online, known bool, err error) {
	if adapter, ok := m.adapter.(interface {
		DaemonHealth(context.Context) (bool, error)
	}); ok {
		running, err := adapter.DaemonHealth(ctx)
		return running, true, err
	}
	if adapter, ok := m.adapter.(interface {
		CheckDaemonHealth(context.Context) (bool, error)
	}); ok {
		running, err := adapter.CheckDaemonHealth(ctx)
		return running, true, err
	}
	if adapter, ok := m.adapter.(interface {
		IsDaemonRunning(context.Context) (bool, error)
	}); ok {
		running, err := adapter.IsDaemonRunning(ctx)
		return running, true, err
	}
	if adapter, ok := m.adapter.(interface {
		Health(context.Context) (bool, error)
	}); ok {
		running, err := adapter.Health(ctx)
		return running, true, err
	}
	if adapter, ok := m.adapter.(interface{ Health(context.Context) error }); ok {
		err := adapter.Health(ctx)
		if err == nil {
			return true, true, nil
		}
		if ctx.Err() != nil {
			return false, true, ctx.Err()
		}
		// An untyped Health(error) adapter cannot prove that an error means
		// transport unreachability. Preserve the error so stop never turns a
		// reachable malformed/500 response into a false offline success.
		return false, true, err
	}
	if adapter, ok := m.adapter.(interface {
		Installation(context.Context) roborev.Installation
	}); ok {
		installation := adapter.Installation(ctx)
		var status struct {
			Running bool  `json:"running"`
			Known   *bool `json:"known"`
		}
		if err := json.Unmarshal(installation.Status, &status); err != nil {
			return false, false, err
		}
		known := status.Known == nil || *status.Known
		return status.Running, known, nil
	}
	return false, false, ErrUnsupported
}

func (m *Manager) queueStatus(ctx context.Context) (roborev.QueueState, error) {
	if adapter, ok := m.adapter.(interface {
		QueueStatus(context.Context) (roborev.QueueState, error)
	}); ok {
		return adapter.QueueStatus(ctx)
	}
	if adapter, ok := m.adapter.(interface {
		GetQueueStatus(context.Context) (roborev.QueueState, error)
	}); ok {
		return adapter.GetQueueStatus(ctx)
	}
	if adapter, ok := m.adapter.(interface {
		QueuePaused(context.Context) (bool, error)
	}); ok {
		paused, err := adapter.QueuePaused(ctx)
		return roborev.QueueState{Known: err == nil, Paused: paused}, err
	}
	return roborev.QueueState{}, ErrUnsupported
}

func (m *Manager) setQueue(ctx context.Context, paused bool) (roborev.QueueState, error) {
	if adapter, ok := m.adapter.(interface {
		SetQueuePaused(context.Context, bool) (roborev.QueueState, error)
	}); ok {
		return adapter.SetQueuePaused(ctx, paused)
	}
	if paused {
		if adapter, ok := m.adapter.(interface {
			PauseQueue(context.Context) (roborev.QueueState, error)
		}); ok {
			return adapter.PauseQueue(ctx)
		}
	} else if adapter, ok := m.adapter.(interface {
		ResumeQueue(context.Context) (roborev.QueueState, error)
	}); ok {
		return adapter.ResumeQueue(ctx)
	}
	return roborev.QueueState{}, fmt.Errorf("%w: queue control", ErrUnsupported)
}

func (m *Manager) wait(ctx context.Context, duration time.Duration) error {
	if m.waitFn == nil {
		return waitContext(ctx, duration)
	}
	return m.waitFn(ctx, duration)
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Ensure strings imported by this package remain useful to callers that pass
// unsupported operation values; this also avoids accepting shell-like text in
// future adapter extensions.
func validOperation(op Operation) bool {
	return strings.Contains("start stop restart pause resume", string(op))
}
