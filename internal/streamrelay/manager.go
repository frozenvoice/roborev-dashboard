// Package streamrelay owns repository-scoped RoboRev JSONL sources and
// exposes only invalidation events.  It deliberately has no knowledge of
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

// Source is the optional typed RoboRev stream adapter.  Repository
// authorization and fresh inspection are performed by the app registry before
// Subscribe is called.
type Source interface {
	OpenStream(context.Context, string) (proc.Stream, error)
}

type Event struct {
	RepositoryIdentity string `json:"repositoryIdentity"`
	Sequence           uint64 `json:"sequence"`
	Kind               string `json:"kind"`
}

type Manager struct {
	source Source

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
	repoPath string
	ctx      context.Context
	cancel   context.CancelFunc

	mu        sync.Mutex
	subs      map[uint64]chan Event
	nextSubID uint64
	sequence  uint64
	active    proc.Stream
	stopOnce  sync.Once
	done      chan struct{}
}

func NewManager(source Source) *Manager {
	return &Manager{source: source, sources: make(map[string]*sourceState), shutdownDone: make(chan struct{})}
}

// Subscribe starts at most one source for an identity and returns a bounded
// coalescing subscriber channel.  The returned unsubscribe function is
// idempotent and does not cancel any sibling subscriber.
func (m *Manager) Subscribe(ctx context.Context, identity, repoPath string) (<-chan Event, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	identity = strings.TrimSpace(identity)
	if identity == "" || strings.TrimSpace(repoPath) == "" {
		return nil, func() {}, errors.New("repository identity and path are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, func() {}, err
	}

	m.mu.Lock()
	if m.stopped || m.source == nil {
		m.mu.Unlock()
		return nil, func() {}, ErrUnavailable
	}
	s := m.sources[identity]
	newSource := false
	tracked := false
	if s == nil {
		sourceCtx, cancel := context.WithCancel(context.Background())
		s = &sourceState{manager: m, identity: identity, repoPath: repoPath, ctx: sourceCtx, cancel: cancel, subs: make(map[uint64]chan Event), done: make(chan struct{})}
		m.sources[identity] = s
		newSource = true
		// Reserve the wait-group slot before releasing the manager lock so
		// Shutdown cannot race a later Add.
		m.wg.Add(1)
		tracked = true
	}
	s.mu.Lock()
	s.nextSubID++
	id := s.nextSubID
	ch := make(chan Event, subscriberBuffer)
	s.subs[id] = ch
	s.mu.Unlock()
	m.mu.Unlock()

	if newSource {
		stream, err := m.source.OpenStream(s.ctx, repoPath)
		if err == nil && stream != nil && s.ctx.Err() != nil {
			_ = stream.Close()
			stream = nil
			err = s.ctx.Err()
		}
		if err != nil || stream == nil {
			s.removeSubscriber(id)
			m.removeSource(s)
			if err == nil {
				err = ErrUnavailable
			} else {
				// Source adapters must not accidentally turn bounded process
				// diagnostics into an HTTP/SSE error body.
				err = ErrUnavailable
			}
			if tracked {
				m.wg.Done()
			}
			return nil, func() {}, err
		}
		s.mu.Lock()
		s.active = stream
		s.mu.Unlock()
		go func() {
			defer m.wg.Done()
			s.run(stream)
		}()
	}

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			if s.removeSubscriber(id) == 0 {
				s.scheduleIdleStop()
			}
		})
	}
	return ch, unsubscribe, nil
}

func (s *sourceState) removeSubscriber(id uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.subs[id]; ok {
		delete(s.subs, id)
		close(ch)
	}
	return len(s.subs)
}

func (s *sourceState) hasSubscribers() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs) != 0
}

func (s *sourceState) scheduleIdleStop() {
	timer := time.AfterFunc(idleGrace, func() {
		if !s.hasSubscribers() {
			s.manager.removeSource(s)
		}
	})
	// The timer owns no references other than the source state and is bounded;
	// reconnects simply leave it harmlessly observing the new subscriber set.
	_ = timer
}

func (m *Manager) removeSource(s *sourceState) {
	m.mu.Lock()
	if current := m.sources[s.identity]; current == s {
		delete(m.sources, s.identity)
	}
	m.mu.Unlock()
	s.stop()
}

func (s *sourceState) stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		s.mu.Lock()
		active := s.active
		s.active = nil
		for id, ch := range s.subs {
			delete(s.subs, id)
			close(ch)
		}
		s.mu.Unlock()
		if active != nil {
			_ = active.Close()
		}
	})
}

func (s *sourceState) run(stream proc.Stream) {
	defer close(s.done)
	defer s.stop()
	defer s.manager.removeSource(s)
	for {
		s.consume(stream)
		if s.ctx.Err() != nil || !s.hasSubscribers() {
			return
		}
		backoff := 250 * time.Millisecond
		for {
			timer := time.NewTimer(backoff)
			select {
			case <-timer.C:
			case <-s.ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			}
			if !s.hasSubscribers() {
				return
			}
			next, err := s.manager.source.OpenStream(s.ctx, s.repoPath)
			if err == nil && next != nil && s.ctx.Err() == nil {
				s.mu.Lock()
				s.active = next
				s.mu.Unlock()
				stream = next
				break
			}
			if next != nil {
				_ = next.Close()
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

func (s *sourceState) consume(stream proc.Stream) {
	for line := range stream.Lines() {
		if eventType(line) {
			s.publish()
		}
	}
	_ = stream.Wait() // raw process errors are intentionally not relayed
	_ = stream.Close()
	s.mu.Lock()
	if s.active == stream {
		s.active = nil
	}
	s.mu.Unlock()
}

func (s *sourceState) publish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	event := Event{RepositoryIdentity: s.identity, Sequence: s.sequence, Kind: "jobs-changed"}
	for _, ch := range s.subs {
		select {
		case ch <- event:
		default:
			// A full one-slot channel is replaced with the latest sequence.
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

// Shutdown cancels every source and waits for stream goroutines.  It never
// exposes process errors, stderr, or upstream payloads to callers.
func (m *Manager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.shutdownOnce.Do(func() {
		m.mu.Lock()
		m.stopped = true
		sources := make([]*sourceState, 0, len(m.sources))
		for _, s := range m.sources {
			sources = append(sources, s)
		}
		m.sources = make(map[string]*sourceState)
		m.mu.Unlock()
		for _, s := range sources {
			s.stop()
		}
		go func() {
			m.wg.Wait()
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
