// Package streamrelay owns repository-scoped RoboRev JSONL sources and
// exposes only invalidation events. It deliberately has no knowledge of
// RoboRev payload schemas beyond the small event-type allowlist.
package streamrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

const (
	subscriberBuffer  = 1
	idleGrace         = 750 * time.Millisecond
	maxRestartBackoff = 5 * time.Second
)

var (
	ErrUnavailable = errors.New("RoboRev stream source is unavailable")
	ErrShutdown    = errors.New("stream relay is shut down")
)

// Source is the optional typed RoboRev stream adapter.
type Source interface {
	OpenStream(context.Context, string) (proc.Stream, error)
}

// AuthorizeRepository resolves an identity to a freshly authorized canonical
// repository path. It is called before initial start and before every restart.
type AuthorizeRepository func(context.Context, string) (string, error)

type Event struct {
	RepositoryIdentity string `json:"repositoryIdentity"`
	Sequence           uint64 `json:"sequence"`
	Kind               string `json:"kind"`
}

type sourceStatus uint8

const (
	statusStarting sourceStatus = iota + 1
	statusReady
	statusStopping
)

type startAttempt struct {
	done    chan struct{}
	success bool
	err     error
	closed  bool
}

type stopWork struct {
	s       *sourceState
	active  proc.Stream
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
}

// Manager uses one mutex as the source/subscriber state machine. Blocking
// authorization, process start/close/wait, timer waits and WaitGroup waits
// are always performed after releasing this mutex.
type Manager struct {
	source       Source
	authorize    AuthorizeRepository
	after        idleAfterFunc
	beforeFinish func()

	mu           sync.Mutex
	sources      map[string]*sourceState
	stopped      bool
	wg           sync.WaitGroup
	shutdownOnce sync.Once
	shutdownDone chan struct{}
}

type sourceState struct {
	manager  *Manager
	identity string
	ctx      context.Context
	cancel   context.CancelFunc

	status    sourceStatus
	subs      map[uint64]chan Event
	nextSubID uint64
	sequence  uint64
	active    proc.Stream
	attempt   *startAttempt
	pending   int

	// lifecycleDone is closed by the one startSource/runSource goroutine.
	// stopDone is closed only after cleanup has closed the process and observed
	// lifecycleDone. Keeping the state in Manager.sources until then prevents
	// overlapping processes for one identity.
	lifecycleDone chan struct{}
	stopDone      chan struct{}
	idleToken     uint64
	idleTimer     idleTimer
}

type idleTimer interface {
	Stop() bool
}

type idleAfterFunc func(time.Duration, func()) idleTimer

// NewManager allows the server to inject centralized fresh identity
// authorization. A nil authorizer is deliberately fail-closed.
func NewManager(source Source, authorizers ...AuthorizeRepository) *Manager {
	var authorize AuthorizeRepository
	if len(authorizers) > 0 {
		authorize = authorizers[0]
	}
	return newManager(source, authorize, func(delay time.Duration, callback func()) idleTimer {
		return time.AfterFunc(delay, callback)
	})
}

func newManager(source Source, authorize AuthorizeRepository, after idleAfterFunc) *Manager {
	return newManagerWithFinishHook(source, authorize, after, nil)
}

func newManagerWithFinishHook(source Source, authorize AuthorizeRepository, after idleAfterFunc, beforeFinish func()) *Manager {
	if after == nil {
		after = func(delay time.Duration, callback func()) idleTimer {
			return time.AfterFunc(delay, callback)
		}
	}
	return &Manager{source: source, authorize: authorize, after: after, beforeFinish: beforeFinish, sources: make(map[string]*sourceState), shutdownDone: make(chan struct{})}
}

// Subscribe waits for a shared initial startup result before returning. A
// request canceled while startup is pending leaves only its own provisional
// subscriber; when all provisional subscribers leave, the shared startup is
// canceled. Once ready, the source context is independent of any browser
// context. The repository path is intentionally resolved only by authorize.
func (m *Manager) Subscribe(ctx context.Context, identity string) (<-chan Event, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil, func() {}, errors.New("repository identity is required")
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, func() {}, err
		}
		m.mu.Lock()
		if m.stopped || m.source == nil {
			m.mu.Unlock()
			return nil, func() {}, ErrUnavailable
		}
		s := m.sources[identity]
		start := false
		if s == nil {
			sourceCtx, cancel := context.WithCancel(context.Background())
			s = &sourceState{
				manager:       m,
				identity:      identity,
				ctx:           sourceCtx,
				cancel:        cancel,
				status:        statusStarting,
				subs:          make(map[uint64]chan Event),
				attempt:       &startAttempt{done: make(chan struct{})},
				lifecycleDone: make(chan struct{}),
				stopDone:      make(chan struct{}),
			}
			m.sources[identity] = s
			m.wg.Add(1)
			start = true
		}
		switch s.status {
		case statusReady:
			ch, unsubscribe := m.addSubscriberLocked(s)
			m.mu.Unlock()
			return ch, unsubscribe, nil
		case statusStarting:
			attempt := s.attempt
			if attempt == nil {
				m.mu.Unlock()
				return nil, func() {}, ErrUnavailable
			}
			s.pending++
			m.mu.Unlock()
			if start {
				go m.startSource(s, attempt)
			}
			return m.waitForStartup(ctx, s, attempt)
		case statusStopping:
			stopDone := s.stopDone
			m.mu.Unlock()
			select {
			case <-stopDone:
				continue
			case <-ctx.Done():
				return nil, func() {}, ctx.Err()
			}
		default:
			m.mu.Unlock()
			return nil, func() {}, ErrUnavailable
		}
	}
}

func (m *Manager) addSubscriberLocked(s *sourceState) (<-chan Event, func()) {
	s.nextSubID++
	id := s.nextSubID
	ch := make(chan Event, subscriberBuffer)
	s.subs[id] = ch
	var once sync.Once
	return ch, func() { once.Do(func() { m.unsubscribe(s, id) }) }
}

func (m *Manager) waitForStartup(ctx context.Context, s *sourceState, attempt *startAttempt) (<-chan Event, func(), error) {
	select {
	case <-attempt.done:
		if m.beforeFinish != nil {
			m.beforeFinish()
		}
		return m.finishStartupWait(ctx, s, attempt)
	case <-ctx.Done():
		return m.cancelStartupWait(s, attempt, ctx.Err())
	}
}

func (m *Manager) finishStartupWait(ctx context.Context, s *sourceState, attempt *startAttempt) (<-chan Event, func(), error) {
	var work stopWork
	m.mu.Lock()
	decrementPendingLocked(s)
	current := m.sources[s.identity] == s
	if ctx.Err() == nil && attempt.success && current && s.status != statusStopping && s.ctx.Err() == nil {
		ch, unsubscribe := m.addSubscriberLocked(s)
		m.mu.Unlock()
		return ch, unsubscribe, nil
	}
	if ctx.Err() != nil && current && s.status == statusReady && s.pending == 0 && len(s.subs) == 0 {
		m.scheduleIdleLocked(s)
	}
	err := attempt.err
	if err == nil {
		err = ErrUnavailable
	}
	if current && s.status == statusStarting && s.pending == 0 && len(s.subs) == 0 {
		work = m.beginStopLocked(s, ErrUnavailable)
	}
	m.mu.Unlock()
	m.launchStop(work)
	if ctx.Err() != nil {
		return nil, func() {}, ctx.Err()
	}
	return nil, func() {}, err
}

func (m *Manager) cancelStartupWait(s *sourceState, attempt *startAttempt, waitErr error) (<-chan Event, func(), error) {
	var work stopWork
	m.mu.Lock()
	decrementPendingLocked(s)
	if m.sources[s.identity] == s && s.pending == 0 && len(s.subs) == 0 {
		switch s.status {
		case statusReady:
			m.scheduleIdleLocked(s)
		case statusStarting:
			work = m.beginStopLocked(s, ErrUnavailable)
		}
	}
	m.mu.Unlock()
	m.launchStop(work)
	return nil, func() {}, waitErr
}

func decrementPendingLocked(s *sourceState) {
	if s.pending > 0 {
		s.pending--
	}
}

func completeAttemptLocked(attempt *startAttempt, success bool, err error) {
	if attempt == nil || attempt.closed {
		return
	}
	attempt.success = success
	attempt.err = err
	attempt.closed = true
	close(attempt.done)
}

func (m *Manager) startSource(s *sourceState, attempt *startAttempt) {
	defer func() {
		close(s.lifecycleDone)
		m.wg.Done()
	}()
	path, err := m.authorizePath(s.ctx, s.identity)
	var stream proc.Stream
	if err == nil {
		stream, err = m.source.OpenStream(s.ctx, path)
	}
	owned := ownStream(stream)
	if err == nil && owned == nil {
		err = ErrUnavailable
	}
	if err == nil && s.ctx.Err() != nil {
		_ = owned.Close()
		owned = nil
		err = s.ctx.Err()
	}

	var work stopWork
	m.mu.Lock()
	current := m.sources[s.identity] == s
	valid := err == nil && current && s.status == statusStarting && s.attempt == attempt && s.ctx.Err() == nil && s.pending > 0
	if valid {
		s.active = owned
		s.status = statusReady
		completeAttemptLocked(attempt, true, nil)
	} else if current && s.status == statusStarting && s.attempt == attempt {
		completeAttemptLocked(attempt, false, ErrUnavailable)
		work = m.beginStopLocked(s, ErrUnavailable)
	}
	m.mu.Unlock()
	if !valid && owned != nil {
		_ = owned.Close()
	}
	m.launchStop(work)
	if !valid {
		return
	}
	m.runSource(s, owned)
}

func (m *Manager) authorizePath(ctx context.Context, identity string) (string, error) {
	if m.authorize == nil {
		// A stale path must never be reused for a long-lived source. Production
		// managers receive the registry authorizer; a nil authorizer is only a
		// fail-closed configuration.
		return "", ErrUnavailable
	}
	path, err := m.authorize(ctx, identity)
	if err != nil || strings.TrimSpace(path) == "" {
		return "", ErrUnavailable
	}
	return path, nil
}

func (m *Manager) runSource(s *sourceState, stream proc.Stream) {
	for {
		m.consume(s, stream)
		m.mu.Lock()
		if m.stopped || m.sources[s.identity] != s || (len(s.subs) == 0 && s.pending == 0) || s.ctx.Err() != nil {
			work := m.beginStopLocked(s, nil)
			m.mu.Unlock()
			m.launchStop(work)
			return
		}
		attempt := &startAttempt{done: make(chan struct{})}
		s.attempt = attempt
		s.status = statusStarting
		s.active = nil
		m.mu.Unlock()

		backoff := 250 * time.Millisecond
		for {
			if !waitBackoff(s.ctx, backoff) {
				m.mu.Lock()
				work := m.beginStopLocked(s, ErrShutdown)
				m.mu.Unlock()
				m.launchStop(work)
				return
			}
			m.mu.Lock()
			if m.stopped || m.sources[s.identity] != s || (len(s.subs) == 0 && s.pending == 0) || s.ctx.Err() != nil {
				work := m.beginStopLocked(s, ErrShutdown)
				m.mu.Unlock()
				m.launchStop(work)
				return
			}
			m.mu.Unlock()

			path, authErr := m.authorizePath(s.ctx, s.identity)
			var next proc.Stream
			if authErr == nil {
				next, authErr = m.source.OpenStream(s.ctx, path)
			}
			owned := ownStream(next)
			if authErr == nil && owned != nil && s.ctx.Err() == nil {
				m.mu.Lock()
				valid := m.sources[s.identity] == s && s.status == statusStarting && s.attempt == attempt && !m.stopped && s.ctx.Err() == nil
				if valid {
					s.active = owned
					s.status = statusReady
					completeAttemptLocked(attempt, true, nil)
				}
				m.mu.Unlock()
				if valid {
					stream = owned
					break
				}
			}
			if owned != nil {
				_ = owned.Close()
			}
			if backoff < maxRestartBackoff {
				backoff *= 2
				if backoff > maxRestartBackoff {
					backoff = maxRestartBackoff
				}
			}
		}
	}
}

func ownStream(stream proc.Stream) proc.Stream {
	if stream == nil {
		return nil
	}
	return &ownedStream{Stream: stream}
}

type ownedStream struct {
	proc.Stream
	closeOnce sync.Once
	closeErr  error
}

func (s *ownedStream) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.Stream.Close() })
	return s.closeErr
}

func waitBackoff(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (m *Manager) consume(s *sourceState, stream proc.Stream) {
	for line := range stream.Lines() {
		if eventType(line) {
			m.publish(s)
		}
	}
	_ = stream.Wait()
	_ = stream.Close()
	m.mu.Lock()
	if s.active == stream {
		s.active = nil
	}
	m.mu.Unlock()
}

func (m *Manager) publish(s *sourceState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s.sequence++
	event := Event{RepositoryIdentity: s.identity, Sequence: s.sequence, Kind: "jobs-changed"}
	for _, ch := range s.subs {
		select {
		case ch <- event:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- event:
			default:
			}
		}
	}
}

func (m *Manager) unsubscribe(s *sourceState, id uint64) {
	var work stopWork
	m.mu.Lock()
	if current := m.sources[s.identity]; current == s {
		if ch, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(ch)
		}
		if len(s.subs) == 0 {
			if s.status == statusReady {
				m.scheduleIdleLocked(s)
			} else if s.status == statusStarting && s.pending == 0 {
				work = m.beginStopLocked(s, ErrUnavailable)
			}
		}
	}
	m.mu.Unlock()
	m.launchStop(work)
}

func (m *Manager) scheduleIdleLocked(s *sourceState) {
	s.idleToken++
	token := s.idleToken
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	s.idleTimer = m.after(idleGrace, func() { m.expireIdle(s, token) })
}

func (m *Manager) expireIdle(s *sourceState, token uint64) {
	m.mu.Lock()
	if m.sources[s.identity] != s || s.idleToken != token || len(s.subs) != 0 || s.status != statusReady {
		m.mu.Unlock()
		return
	}
	work := m.beginStopLocked(s, nil)
	m.mu.Unlock()
	m.launchStop(work)
}

// beginStopLocked transitions one source to stopping and returns the only
// cleanup owner. The state remains in the identity map until finishStop has
// closed the process, canceled the source context, and observed lifecycleDone.
func (m *Manager) beginStopLocked(s *sourceState, startupErr error) stopWork {
	if s.status == statusStopping {
		return stopWork{s: s, done: s.stopDone}
	}
	s.status = statusStopping
	if startupErr != nil && s.attempt != nil && !s.attempt.closed {
		completeAttemptLocked(s.attempt, false, startupErr)
	}
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
	for id, ch := range s.subs {
		delete(s.subs, id)
		close(ch)
	}
	active := s.active
	s.active = nil
	return stopWork{s: s, active: active, cancel: s.cancel, done: s.stopDone, started: true}
}

func (m *Manager) launchStop(work stopWork) {
	if !work.started {
		return
	}
	go m.finishStop(work)
}

func (m *Manager) finishStop(work stopWork) {
	if work.active != nil {
		_ = work.active.Close()
	}
	if work.cancel != nil {
		work.cancel()
	}
	<-work.s.lifecycleDone
	m.mu.Lock()
	if m.sources[work.s.identity] == work.s && work.s.status == statusStopping {
		delete(m.sources, work.s.identity)
		select {
		case <-work.done:
		default:
			close(work.done)
		}
	}
	m.mu.Unlock()
}

var recognizedEventTypes = map[string]struct{}{
	"job.enqueued": {}, "review.started": {}, "review.completed": {},
	"review.failed": {}, "review.canceled": {}, "review.remapped": {},
}

func eventType(line []byte) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(line, &object); err != nil || object == nil {
		return false
	}
	for _, field := range []string{"type", "event", "event_type"} {
		var value string
		if raw, ok := object[field]; ok && json.Unmarshal(raw, &value) == nil {
			if _, recognized := recognizedEventTypes[strings.TrimSpace(value)]; recognized {
				return true
			}
		}
	}
	return false
}

// Shutdown cancels every source and waits for lifecycle and cleanup goroutines.
// It never exposes process errors, stderr, or upstream payloads to callers.
func (m *Manager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.shutdownOnce.Do(func() {
		m.mu.Lock()
		m.stopped = true
		stops := make([]stopWork, 0, len(m.sources))
		for _, s := range m.sources {
			stops = append(stops, m.beginStopLocked(s, ErrShutdown))
		}
		m.mu.Unlock()
		for _, work := range stops {
			m.launchStop(work)
		}
		go func() {
			m.wg.Wait()
			for _, work := range stops {
				<-work.done
			}
			close(m.shutdownDone)
		}()
	})
	select {
	case <-m.shutdownDone:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown stream relay: %w", ctx.Err())
	}
}
