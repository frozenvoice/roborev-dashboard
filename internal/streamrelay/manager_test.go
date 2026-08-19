package streamrelay

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type fakeSource struct {
	mu      sync.Mutex
	starts  int
	streams []*fakeStream
	err     error
	factory func() *fakeStream
}

func (f *fakeSource) OpenStream(context.Context, string) (proc.Stream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	stream := newFakeStream()
	if f.factory != nil {
		stream = f.factory()
	}
	f.starts++
	f.streams = append(f.streams, stream)
	return stream, nil
}

type fakeStream struct {
	lines      chan []byte
	done       chan struct{}
	once       sync.Once
	mu         sync.Mutex
	closeCalls int
	deferClose bool
}

func newFakeStream() *fakeStream {
	return &fakeStream{lines: make(chan []byte, 8), done: make(chan struct{})}
}
func (f *fakeStream) Lines() <-chan []byte { return f.lines }
func (f *fakeStream) Wait() error          { <-f.done; return nil }
func (f *fakeStream) Close() error {
	if !f.deferClose {
		f.finish()
	}
	f.mu.Lock()
	f.closeCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeStream) finish()          { f.once.Do(func() { close(f.lines); close(f.done) }) }
func (f *fakeStream) emit(line string) { f.lines <- []byte(line) }

func TestManagerSanitizesAndSharesSource(t *testing.T) {
	source := &fakeSource{}
	m := NewManager(source)
	first, unsubFirst, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err != nil {
		t.Fatal(err)
	}
	second, unsubSecond, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err != nil {
		t.Fatal(err)
	}
	source.mu.Lock()
	stream := source.streams[0]
	source.mu.Unlock()
	if source.starts != 1 {
		t.Fatalf("starts=%d, want one source", source.starts)
	}
	stream.emit(`{"type":"job.enqueued","prompt":"secret","diff":"large"}`)
	stream.emit(`not-json`)
	stream.emit(`{"type":"unknown.event","output":"secret"}`)
	select {
	case event := <-first:
		if event.RepositoryIdentity != "repo-a" || event.Sequence != 1 || event.Kind != "jobs-changed" {
			t.Fatalf("event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("first subscriber did not receive event")
	}
	select {
	case event := <-second:
		if event.Sequence != 1 {
			t.Fatalf("second event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("second subscriber did not receive event")
	}
	unsubFirst()
	unsubFirst()
	unsubSecond()
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerCoalescesSubscriberBurstAndIsolatesIdentity(t *testing.T) {
	source := &fakeSource{}
	m := NewManager(source)
	a, unsubA, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err != nil {
		t.Fatal(err)
	}
	b, unsubB, err := m.Subscribe(context.Background(), "repo-b", `C:\repo-b`)
	if err != nil {
		t.Fatal(err)
	}
	source.mu.Lock()
	streamA, streamB := source.streams[0], source.streams[1]
	source.mu.Unlock()
	for i := 0; i < 20; i++ {
		streamA.emit(`{"event":"review.completed"}`)
	}
	deadline := time.After(time.Second)
	var event Event
	for event.Sequence < 20 {
		select {
		case event = <-a:
		case <-deadline:
			t.Fatalf("coalesced event missing; last=%#v", event)
		}
	}
	if event.RepositoryIdentity != "repo-a" {
		t.Fatalf("coalesced=%#v", event)
	}
	select {
	case event := <-b:
		t.Fatalf("identity leaked event=%#v", event)
	case <-time.After(100 * time.Millisecond):
	}
	_ = streamB
	unsubA()
	unsubB()
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerUnavailableDoesNotExposeSourceError(t *testing.T) {
	source := &fakeSource{err: errors.New("stderr secret prompt diff")}
	m := NewManager(source)
	_, _, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err == nil {
		t.Fatal("expected unavailable source")
	}
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("source error leaked: %v", err)
	}
	_ = m.Shutdown(context.Background())
}

func TestManagerRestartsAfterSourceExitWhileSubscribersRemain(t *testing.T) {
	source := &fakeSource{}
	m := NewManager(source)
	updates, unsubscribe, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err != nil {
		t.Fatal(err)
	}
	source.mu.Lock()
	first := source.streams[0]
	source.mu.Unlock()
	first.finish()
	deadline := time.Now().Add(2 * time.Second)
	for {
		source.mu.Lock()
		starts := source.starts
		source.mu.Unlock()
		if starts >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("source was not restarted")
		}
		time.Sleep(20 * time.Millisecond)
	}
	source.mu.Lock()
	second := source.streams[1]
	source.mu.Unlock()
	second.emit(`{"type":"review.completed"}`)
	select {
	case event := <-updates:
		if event.Sequence != 1 || event.RepositoryIdentity != "repo-a" {
			t.Fatalf("event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("restarted source event missing")
	}
	unsubscribe()
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerIdleGraceReusesSourceThenCleansIt(t *testing.T) {
	source := &fakeSource{}
	m := NewManager(source)
	_, unsubscribe, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err != nil {
		t.Fatal(err)
	}
	unsubscribe()
	time.Sleep(100 * time.Millisecond)
	_, unsubscribeAgain, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err != nil {
		t.Fatal(err)
	}
	source.mu.Lock()
	starts := source.starts
	source.mu.Unlock()
	if starts != 1 {
		t.Fatalf("reconnect starts=%d, want source reuse", starts)
	}
	unsubscribeAgain()
	time.Sleep(idleGrace + 150*time.Millisecond)
	_, unsubscribeAfterGrace, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err != nil {
		t.Fatal(err)
	}
	source.mu.Lock()
	starts = source.starts
	source.mu.Unlock()
	if starts != 2 {
		t.Fatalf("post-grace starts=%d, want new source", starts)
	}
	unsubscribeAfterGrace()
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerShutdownTimeoutCanBeFollowedBySuccessfulWait(t *testing.T) {
	stream := newFakeStream()
	stream.deferClose = true
	source := &fakeSource{factory: func() *fakeStream { return stream }}
	m := NewManager(source)
	_, unsubscribe, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err != nil {
		t.Fatal(err)
	}
	unsubscribe()
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = m.Shutdown(short)
	cancel()
	if err == nil {
		t.Fatal("shutdown unexpectedly completed before stream release")
	}
	stream.finish()
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
