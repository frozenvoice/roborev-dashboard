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
	mu          sync.Mutex
	starts      int
	streams     []*fakeStream
	paths       []string
	err         error
	factory     func() *fakeStream
	openEntered chan struct{}
	openGate    <-chan struct{}
	openOnce    sync.Once
	startNotify chan struct{}
}

func passthroughAuthorize(_ context.Context, path string) (string, error) {
	return path, nil
}

func (f *fakeSource) OpenStream(ctx context.Context, path string) (proc.Stream, error) {
	if f.openEntered != nil {
		f.openOnce.Do(func() { close(f.openEntered) })
	}
	if f.openGate != nil {
		select {
		case <-f.openGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
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
	f.paths = append(f.paths, path)
	if f.startNotify != nil {
		select {
		case f.startNotify <- struct{}{}:
		default:
		}
	}
	return stream, nil
}

type fakeStream struct {
	lines        chan []byte
	done         chan struct{}
	once         sync.Once
	mu           sync.Mutex
	closeCalls   int
	deferClose   bool
	closeEntered chan struct{}
	closeRelease <-chan struct{}
	closeOnce    sync.Once
}

func newFakeStream() *fakeStream {
	return &fakeStream{lines: make(chan []byte, 8), done: make(chan struct{})}
}
func (f *fakeStream) Lines() <-chan []byte { return f.lines }
func (f *fakeStream) Wait() error          { <-f.done; return nil }
func (f *fakeStream) Close() error {
	if f.closeEntered != nil {
		f.closeOnce.Do(func() { close(f.closeEntered) })
	}
	if f.closeRelease != nil {
		<-f.closeRelease
	}
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
	m := NewManager(source, passthroughAuthorize)
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
	m := NewManager(source, passthroughAuthorize)
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
	m := NewManager(source, passthroughAuthorize)
	_, _, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err == nil {
		t.Fatal("expected unavailable source")
	}
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("source error leaked: %v", err)
	}
	_ = m.Shutdown(context.Background())
}

func TestManagerWithoutAuthorizerFailsClosedBeforeSourceStart(t *testing.T) {
	source := &fakeSource{}
	m := NewManager(source)
	_, _, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error=%v, want unavailable", err)
	}
	source.mu.Lock()
	starts := source.starts
	source.mu.Unlock()
	if starts != 0 {
		t.Fatalf("source starts=%d, want zero without authorizer", starts)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerRestartsAfterSourceExitWhileSubscribersRemain(t *testing.T) {
	source := &fakeSource{}
	m := NewManager(source, passthroughAuthorize)
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
	m := NewManager(source, passthroughAuthorize)
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
	m := NewManager(source, passthroughAuthorize)
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
	stream.mu.Lock()
	closeCalls := stream.closeCalls
	stream.mu.Unlock()
	if closeCalls != 1 {
		t.Fatalf("stream close calls=%d, want one manager-owned close", closeCalls)
	}
}

func TestManagerConcurrentSubscribersShareInitialStartup(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	source := &fakeSource{openEntered: entered, openGate: release}
	m := NewManager(source, passthroughAuthorize)
	type result struct {
		ch  <-chan Event
		un  func()
		err error
	}
	results := make(chan result, 2)
	go func() {
		ch, un, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
		results <- result{ch: ch, un: un, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("initial source did not start")
	}
	go func() {
		ch, un, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
		results <- result{ch: ch, un: un, err: err}
	}()
	select {
	case <-results:
		t.Fatal("subscriber returned before shared startup completed")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("shared startup errors: %v, %v", first.err, second.err)
	}
	source.mu.Lock()
	starts := source.starts
	source.mu.Unlock()
	if starts != 1 {
		t.Fatalf("starts=%d, want one shared source", starts)
	}
	first.un()
	second.un()
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerSharesInitialStartupFailure(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	source := &fakeSource{openEntered: entered, openGate: release, err: errors.New("secret stderr")}
	m := NewManager(source, passthroughAuthorize)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, _, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
			results <- err
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("initial source did not start")
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("error=%v, want shared unavailable", err)
			}
		case <-time.After(time.Second):
			t.Fatal("startup waiter did not receive shared failure")
		}
	}
	source.mu.Lock()
	starts := source.starts
	source.mu.Unlock()
	if starts != 0 {
		t.Fatalf("starts=%d, want no successful second start", starts)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerCanceledInitialWaiterCancelsSharedStartup(t *testing.T) {
	authEntered := make(chan struct{})
	var once sync.Once
	m := NewManager(&fakeSource{}, func(ctx context.Context, _ string) (string, error) {
		once.Do(func() { close(authEntered) })
		<-ctx.Done()
		return "", ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, _, err := m.Subscribe(ctx, "repo-a", `C:\stale`)
		result <- err
	}()
	select {
	case <-authEntered:
	case <-time.After(time.Second):
		t.Fatal("authorization did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter did not return")
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	if err := m.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

func TestManagerReauthorizesBeforeRestartAndDoesNotUseStalePath(t *testing.T) {
	source := &fakeSource{}
	authCalls := make(chan struct{}, 4)
	var authMu sync.Mutex
	authCount := 0
	m := NewManager(source, func(context.Context, string) (string, error) {
		authMu.Lock()
		authCount++
		call := authCount
		authMu.Unlock()
		authCalls <- struct{}{}
		if call == 1 {
			return `C:\fresh`, nil
		}
		return "", errors.New("repository revoked")
	})
	_, unsubscribe, err := m.Subscribe(context.Background(), "repo-a", `C:\stale`)
	if err != nil {
		t.Fatal(err)
	}
	<-authCalls // initial authorization
	source.mu.Lock()
	first := source.streams[0]
	source.mu.Unlock()
	first.finish()
	select {
	case <-authCalls:
	case <-time.After(2 * time.Second):
		t.Fatal("restart did not reauthorize repository")
	}
	source.mu.Lock()
	starts := source.starts
	paths := append([]string(nil), source.paths...)
	source.mu.Unlock()
	if starts != 1 {
		t.Fatalf("starts=%d, want no restart after authorization failure", starts)
	}
	if len(paths) != 1 || paths[0] != `C:\fresh` {
		t.Fatalf("opened paths=%v, want only fresh canonical path", paths)
	}
	unsubscribe()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	if err := m.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

type manualIdleTimer struct {
	callback func()
	stopped  bool
}

func (t *manualIdleTimer) Stop() bool {
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

type manualIdleTimers struct {
	mu     sync.Mutex
	timers []*manualIdleTimer
}

func (f *manualIdleTimers) after(_ time.Duration, callback func()) idleTimer {
	f.mu.Lock()
	defer f.mu.Unlock()
	timer := &manualIdleTimer{callback: callback}
	f.timers = append(f.timers, timer)
	return timer
}

func (f *manualIdleTimers) fire(index int) {
	f.mu.Lock()
	callback := f.timers[index].callback
	f.mu.Unlock()
	// Deliberately fire even if Stop was called. A queued production timer may
	// race with Stop; the generation check must make that callback harmless.
	callback()
}

func (f *manualIdleTimers) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

func TestManagerCanceledReadyWaiterSchedulesIdleGrace(t *testing.T) {
	timers := &manualIdleTimers{}
	ctx, cancel := context.WithCancel(context.Background())
	m := newManager(&fakeSource{}, passthroughAuthorize, timers.after)
	attempt := &startAttempt{done: make(chan struct{}), success: true, waiters: 1}
	s := &sourceState{
		manager:       m,
		identity:      "repo-a",
		ctx:           ctx,
		cancel:        cancel,
		status:        statusReady,
		subs:          make(map[uint64]chan Event),
		attempt:       attempt,
		pending:       1,
		lifecycleDone: make(chan struct{}),
		stopDone:      make(chan struct{}),
	}
	m.sources[s.identity] = s
	_, _, err := m.cancelStartupWait(s, attempt, context.Canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want cancellation", err)
	}
	if timers.count() != 1 {
		t.Fatalf("idle timers=%d, want one after ready cancellation", timers.count())
	}
	close(s.lifecycleDone)
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerQueuedIdleTimerCannotStopReusedSource(t *testing.T) {
	timers := &manualIdleTimers{}
	source := &fakeSource{}
	m := newManager(source, passthroughAuthorize, timers.after)
	_, unsubscribe, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err != nil {
		t.Fatal(err)
	}
	unsubscribe()
	_, unsubscribeAgain, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err != nil {
		t.Fatal(err)
	}
	unsubscribeAgain()
	if timers.count() != 2 {
		t.Fatalf("idle timers=%d, want two generations", timers.count())
	}
	timers.fire(0)
	third, unsubscribeThird, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-third:
		if !ok {
			t.Fatal("stale timer closed reused subscriber")
		}
	default:
	}
	source.mu.Lock()
	starts := source.starts
	source.mu.Unlock()
	if starts != 1 {
		t.Fatalf("starts=%d, want one child after stale timer", starts)
	}
	unsubscribeThird()
	if timers.count() != 3 {
		t.Fatalf("idle timers=%d, want third cleanup generation", timers.count())
	}
	timers.fire(1)
	timers.fire(2)
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerIdleStopWaitsBeforeStartingReplacement(t *testing.T) {
	timers := &manualIdleTimers{}
	closeEntered := make(chan struct{})
	closeRelease := make(chan struct{})
	first := &fakeStream{lines: make(chan []byte), done: make(chan struct{}), closeEntered: closeEntered, closeRelease: closeRelease}
	created := 0
	source := &fakeSource{factory: func() *fakeStream {
		if created == 0 {
			created++
			return first
		}
		created++
		return newFakeStream()
	}}
	m := newManager(source, passthroughAuthorize, timers.after)
	_, unsubscribe, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
	if err != nil {
		t.Fatal(err)
	}
	unsubscribe()
	if timers.count() != 1 {
		t.Fatalf("idle timers=%d, want one", timers.count())
	}
	timers.fire(0)
	select {
	case <-closeEntered:
	case <-time.After(time.Second):
		t.Fatal("idle cleanup did not begin closing old stream")
	}
	result := make(chan error, 1)
	go func() {
		_, un, subscribeErr := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
		if subscribeErr == nil {
			un()
		}
		result <- subscribeErr
	}()
	select {
	case err := <-result:
		t.Fatalf("replacement subscriber returned while old close blocked: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	source.mu.Lock()
	startsWhileBlocked := source.starts
	source.mu.Unlock()
	if startsWhileBlocked != 1 {
		t.Fatalf("starts while old cleanup blocked=%d, want one", startsWhileBlocked)
	}
	close(closeRelease)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement subscriber did not start after old lifecycle cleanup")
	}
	source.mu.Lock()
	starts := source.starts
	source.mu.Unlock()
	if starts != 2 {
		t.Fatalf("starts=%d, want exactly one replacement", starts)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerSuccessfulWaiterSurvivesImmediateRestartGeneration(t *testing.T) {
	finishEntered := make(chan struct{})
	finishRelease := make(chan struct{})
	startNotify := make(chan struct{}, 4)
	created := 0
	source := &fakeSource{startNotify: startNotify, factory: func() *fakeStream {
		created++
		stream := newFakeStream()
		if created == 1 {
			stream.finish()
		}
		return stream
	}}
	var finishOnce sync.Once
	m := newManagerWithFinishHook(source, passthroughAuthorize, nil, func() {
		finishOnce.Do(func() {
			close(finishEntered)
			<-finishRelease
		})
	})
	type subscribeResult struct {
		updates <-chan Event
		un      func()
		err     error
	}
	result := make(chan subscribeResult, 1)
	go func() {
		updates, un, err := m.Subscribe(context.Background(), "repo-a", `C:\repo-a`)
		result <- subscribeResult{updates: updates, un: un, err: err}
	}()
	select {
	case <-startNotify:
	case <-time.After(time.Second):
		t.Fatal("initial source did not open")
	}
	select {
	case <-finishEntered:
	case <-time.After(time.Second):
		t.Fatal("startup waiter was not held at completion boundary")
	}
	select {
	case <-startNotify:
	case <-time.After(2 * time.Second):
		t.Fatal("immediate EOF did not enter a new restart generation")
	}
	source.mu.Lock()
	starts := source.starts
	source.mu.Unlock()
	if starts != 2 {
		t.Fatalf("starts=%d, want initial plus one restart", starts)
	}
	close(finishRelease)
	select {
	case completed := <-result:
		if completed.err != nil {
			t.Fatal(completed.err)
		}
		completed.un()
	case <-time.After(time.Second):
		t.Fatal("old successful startup waiter did not return")
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
