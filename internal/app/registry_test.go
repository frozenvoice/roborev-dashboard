package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
)

type mappingInspector struct {
	repositories map[string]gitx.Repository
	errors       map[string]error
}

type slowRegistryInspector struct {
	delays  map[string]time.Duration
	active  atomic.Int32
	maximum atomic.Int32
	started chan string
}

type blockingRegistryInspector struct {
	active      atomic.Int32
	maximum     atomic.Int32
	calls       atomic.Int32
	started     chan string
	release     chan struct{}
	releaseOnce sync.Once
}

type delayedRegistryInspector struct {
	delay time.Duration
	mu    sync.Mutex
	calls map[string]int
}

type deadlineSuccessInspector struct{}

func (deadlineSuccessInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	time.Sleep(5 * time.Millisecond)
	return gitx.Repository{Path: path}, nil
}

func (s *delayedRegistryInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	time.Sleep(s.delay) // Deliberately ignores context to make admission deterministic.
	s.mu.Lock()
	s.calls[path]++
	s.mu.Unlock()
	return gitx.Repository{Path: path}, nil
}

func (s *delayedRegistryInspector) Calls(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[path]
}

func (s *blockingRegistryInspector) Release() {
	s.releaseOnce.Do(func() { close(s.release) })
}

func (s *blockingRegistryInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	s.calls.Add(1)
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		maximum := s.maximum.Load()
		if active <= maximum || s.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	select {
	case s.started <- path:
	default:
	}
	<-s.release // Deliberately ignores context to model a blocked OS probe.
	return gitx.Repository{Path: path}, nil
}

func (s *slowRegistryInspector) Inspect(ctx context.Context, path string) (gitx.Repository, error) {
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		maximum := s.maximum.Load()
		if active <= maximum || s.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	if s.started != nil {
		select {
		case s.started <- path:
		default:
		}
	}
	timer := time.NewTimer(s.delays[path])
	defer timer.Stop()
	select {
	case <-timer.C:
		return gitx.Repository{Path: path}, nil
	case <-ctx.Done():
		return gitx.Repository{}, ctx.Err()
	}
}

func (m mappingInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	if err, ok := m.errors[path]; ok {
		return gitx.Repository{}, err
	}
	if repo, ok := m.repositories[path]; ok {
		return repo, nil
	}
	return gitx.Repository{Path: path, Head: "head"}, nil
}

func failingRegistryConfig(t *testing.T) string {
	t.Helper()
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(parentFile, "config.json")
}

func TestRegistryLoadPreservesLastSelection(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.json")
	registry := NewRegistry(fakeInspector{}, configFile)
	if _, err := registry.Add(context.Background(), `E:\repo-one`); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(context.Background(), `E:\repo-two`); err != nil {
		t.Fatal(err)
	}
	if err := registry.Select(`E:\repo-two`); err != nil {
		t.Fatal(err)
	}

	reloaded := NewRegistry(fakeInspector{}, configFile)
	reloaded.Load(context.Background())
	if got := reloaded.Selected(); got != `E:\repo-two` {
		t.Fatalf("last selection was not preserved: %q", got)
	}
}

func TestRegistryLoadUsesFirstPersistedRepositoryAsFallback(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"recentRepositories":["E:\\repo-two","E:\\repo-one"],"lastSelectedRepository":"E:\\missing"}`)
	if err := os.WriteFile(configFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(fakeInspector{}, configFile)
	registry.Load(context.Background())
	if got := registry.Selected(); got != `E:\repo-two` {
		t.Fatalf("expected deterministic first fallback, got %q", got)
	}
}

func TestRegistryAddPersistenceFailureRollsBackExistingEntry(t *testing.T) {
	registry := NewRegistry(mappingInspector{repositories: map[string]gitx.Repository{
		"requested": {Path: "canonical", Head: "new-head"},
	}}, "")
	key := registryKey("canonical")
	registry.paths[key] = "previous-value"
	registry.selected = "selected-before"
	registry.configFile = failingRegistryConfig(t)

	if _, err := registry.Add(context.Background(), "requested"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if got := registry.paths[key]; got != "previous-value" {
		t.Fatalf("existing entry changed after failed save: %q", got)
	}
	if got := registry.Selected(); got != "selected-before" {
		t.Fatalf("selection changed after failed save: %q", got)
	}
}

func TestRegistryAddPersistenceFailureDoesNotInitializeSelection(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, failingRegistryConfig(t))
	if _, err := registry.Add(context.Background(), "new-repository"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if registry.Contains("new-repository") {
		t.Fatal("failed add remained authorized in memory")
	}
	if got := registry.Selected(); got != "" {
		t.Fatalf("failed add initialized selection: %q", got)
	}
}

func TestRegistrySelectPersistenceFailureKeepsPreviousSelection(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), "repo-one"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(context.Background(), "repo-two"); err != nil {
		t.Fatal(err)
	}
	registry.configFile = failingRegistryConfig(t)

	if err := registry.Select("repo-two"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if got := registry.Selected(); got != "repo-one" {
		t.Fatalf("selection changed after failed save: %q", got)
	}
}

func TestRegistryInspectRejectsCanonicalRootDrift(t *testing.T) {
	registry := NewRegistry(mappingInspector{repositories: map[string]gitx.Repository{
		"registered": {Path: "different-root"},
	}}, "")
	registry.paths[registryKey("registered")] = "registered"
	registry.selected = "registered"

	_, err := registry.Inspect(context.Background(), "registered")
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected clear root mismatch error, got %v", err)
	}
}

func TestRegistryInspectCaseEquivalenceFollowsPlatform(t *testing.T) {
	registered := `E:\Workspace\Repo`
	inspected := strings.ToLower(registered)
	registry := NewRegistry(mappingInspector{repositories: map[string]gitx.Repository{
		registered: {Path: inspected},
	}}, "")
	registry.paths[registryKey(registered)] = registered
	registry.selected = registered

	_, err := registry.Inspect(context.Background(), registered)
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("Windows-equivalent casing was rejected: %v", err)
		}
	} else if err == nil {
		t.Fatal("case-sensitive platform accepted differently-cased root")
	}
}

func TestRegistryInspectAllOmitsDriftedRoots(t *testing.T) {
	registry := NewRegistry(mappingInspector{repositories: map[string]gitx.Repository{
		"valid-root":   {Path: "valid-root", Head: "valid"},
		"drifted-root": {Path: "replacement-root", Head: "drifted"},
	}}, "")
	registry.paths[registryKey("valid-root")] = "valid-root"
	registry.paths[registryKey("drifted-root")] = "drifted-root"

	repos := registry.InspectAll(context.Background())
	if len(repos) != 1 || repos[0].Path != "valid-root" {
		t.Fatalf("expected only valid registered root, got %#v", repos)
	}
}

func TestRegistryFreshRegisteredPathsOmitsDriftedRoots(t *testing.T) {
	inspector := mappingInspector{repositories: map[string]gitx.Repository{}, errors: map[string]error{}}
	registry := NewRegistry(inspector, "")
	for _, path := range []string{"valid-root", "stale-root", "deleted-root"} {
		if _, err := registry.Add(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	inspector.repositories["stale-root"] = gitx.Repository{Path: "replacement-root"}
	inspector.errors["deleted-root"] = os.ErrNotExist

	got, incomplete, err := registry.FreshRegisteredPathsWithStatus(context.Background())
	if err != nil || incomplete {
		t.Fatalf("fresh inspection incomplete=%v err=%v", incomplete, err)
	}
	if !reflect.DeepEqual(got, []string{"valid-root"}) {
		t.Fatalf("fresh paths=%v, want only valid root", got)
	}
}

func TestRegistryFreshRegisteredPathsIsSortedAndImmutable(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	for _, path := range []string{`E:\z-repo`, `E:\a-repo`, `E:\m-repo`} {
		if _, err := registry.Add(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}

	got, incomplete, err := registry.FreshRegisteredPathsWithStatus(context.Background())
	if err != nil || incomplete {
		t.Fatalf("fresh inspection incomplete=%v err=%v", incomplete, err)
	}
	want := []string{`E:\a-repo`, `E:\m-repo`, `E:\z-repo`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh paths=%v, want %v", got, want)
	}
	got[0] = `E:\mutated`
	if next, nextIncomplete, nextErr := registry.FreshRegisteredPathsWithStatus(context.Background()); nextErr != nil || nextIncomplete || !reflect.DeepEqual(next, want) {
		t.Fatalf("fresh path snapshot was mutable: %v", next)
	}
}

func TestRegistryFreshRegisteredPathsBoundsWorkersAndSortsResults(t *testing.T) {
	inspector := &slowRegistryInspector{delays: map[string]time.Duration{
		`E:\z-repo`: 5 * time.Millisecond,
		`E:\a-repo`: 2 * time.Millisecond,
		`E:\m-repo`: 4 * time.Millisecond,
		`E:\b-repo`: 1 * time.Millisecond,
	}, started: make(chan string, 16)}
	registry := NewRegistry(inspector, "")
	for path := range inspector.delays {
		if _, err := registry.Add(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	got, incomplete, err := registry.freshRegisteredPaths(context.Background(), 100*time.Millisecond, 250*time.Millisecond)
	if err != nil || incomplete {
		t.Fatalf("paths=%v incomplete=%v err=%v", got, incomplete, err)
	}
	want := []string{`E:\a-repo`, `E:\b-repo`, `E:\m-repo`, `E:\z-repo`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths=%v, want %v", got, want)
	}
	if maximum := inspector.maximum.Load(); maximum > freshRegisteredWorkers {
		t.Fatalf("maximum concurrent inspections=%d, want <=%d", maximum, freshRegisteredWorkers)
	}
}

func TestRegistryFreshRegisteredPathsCursorWrapKeepsEveryRoot(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	for _, path := range []string{"root-a", "root-b", "root-c"} {
		registry.paths[registryKey(path)] = path
	}
	registry.probeCursor.Store(^uint64(0) - 1)

	paths, incomplete, err := registry.freshRegisteredPaths(context.Background(), 20*time.Millisecond, 100*time.Millisecond)
	if err != nil || incomplete {
		t.Fatalf("paths=%v incomplete=%v err=%v", paths, incomplete, err)
	}
	want := []string{"root-a", "root-b", "root-c"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths=%v, want %v", paths, want)
	}
}

func TestRegistryFreshRegisteredPathsDeadlineWinsOverLateSuccessfulInspect(t *testing.T) {
	registry := NewRegistry(deadlineSuccessInspector{}, "")
	registry.paths[registryKey("late")] = "late"
	paths, incomplete, err := registry.freshRegisteredPaths(context.Background(), time.Millisecond, 50*time.Millisecond)
	if err != nil || !incomplete || len(paths) != 0 {
		t.Fatalf("paths=%v incomplete=%v err=%v, want incomplete without late success", paths, incomplete, err)
	}
}

func TestRegistryFreshRegisteredPathsTimeoutOmitsOnlySlowRoot(t *testing.T) {
	inspector := &slowRegistryInspector{delays: map[string]time.Duration{
		"slow": 30 * time.Millisecond,
		"fast": 0,
	}}
	registry := NewRegistry(inspector, "")
	for path := range inspector.delays {
		if _, err := registry.Add(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	got, incomplete, err := registry.freshRegisteredPaths(context.Background(), 5*time.Millisecond, 100*time.Millisecond)
	if err != nil || !incomplete || !reflect.DeepEqual(got, []string{"fast"}) {
		t.Fatalf("paths=%v incomplete=%v err=%v, want fast/incomplete", got, incomplete, err)
	}
}

func TestRegistryFreshRegisteredPathsParentCancellationJoinsWorkers(t *testing.T) {
	inspector := &slowRegistryInspector{delays: map[string]time.Duration{"blocked": time.Second}, started: make(chan string, 1)}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), "blocked"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := registry.freshRegisteredPaths(ctx, time.Second, time.Second)
		done <- err
	}()
	select {
	case <-inspector.started:
	case <-time.After(time.Second):
		t.Fatal("inspection did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("fresh inspection did not join canceled worker")
	}
}

func TestRegistryFreshRegisteredPathsNonCooperativeProbeReturnsBoundedIncomplete(t *testing.T) {
	inspector := &blockingRegistryInspector{started: make(chan string, 2), release: make(chan struct{})}
	t.Cleanup(inspector.Release)
	registry := NewRegistry(inspector, "")
	registry.paths[registryKey("blocked")] = "blocked"

	done := make(chan struct {
		paths      []string
		incomplete bool
		err        error
	}, 1)
	started := time.Now()
	go func() {
		paths, incomplete, err := registry.freshRegisteredPaths(context.Background(), time.Millisecond, 20*time.Millisecond)
		done <- struct {
			paths      []string
			incomplete bool
			err        error
		}{paths, incomplete, err}
	}()
	select {
	case <-inspector.started:
	case <-time.After(time.Second):
		t.Fatal("blocking inspection did not start")
	}
	select {
	case result := <-done:
		if result.err != nil || !result.incomplete || len(result.paths) != 0 {
			t.Fatalf("paths=%v incomplete=%v err=%v", result.paths, result.incomplete, result.err)
		}
		if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
			t.Fatalf("phase did not return boundedly: %v", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("non-cooperative probe blocked the caller past phase timeout")
	}
	inspector.Release()
	deadline := time.After(time.Second)
	for inspector.active.Load() != 0 {
		select {
		case <-deadline:
			t.Fatal("probe did not release after cleanup")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	paths, incomplete, err := registry.freshRegisteredPaths(context.Background(), 20*time.Millisecond, 100*time.Millisecond)
	if err != nil || incomplete || !reflect.DeepEqual(paths, []string{"blocked"}) {
		t.Fatalf("recovered paths=%v incomplete=%v err=%v, want complete blocked root", paths, incomplete, err)
	}
}

func TestRegistryFreshRegisteredPathsNonCooperativeParentCancellationReturnsBounded(t *testing.T) {
	inspector := &blockingRegistryInspector{started: make(chan string, 2), release: make(chan struct{})}
	t.Cleanup(inspector.Release)
	registry := NewRegistry(inspector, "")
	registry.paths[registryKey("blocked")] = "blocked"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := registry.freshRegisteredPaths(ctx, time.Second, time.Second)
		done <- err
	}()
	select {
	case <-inspector.started:
	case <-time.After(time.Second):
		t.Fatal("blocking inspection did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v, want context canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("parent cancellation did not return boundedly")
	}
}

func TestRegistryFreshRegisteredPathsRotatingAdmissionEventuallyCoversRoots(t *testing.T) {
	inspector := &delayedRegistryInspector{delay: 5 * time.Millisecond, calls: make(map[string]int)}
	registry := NewRegistry(inspector, "")
	roots := []string{"root-a", "root-b", "root-c", "root-d", "root-e", "root-f", "root-g", "root-h"}
	for _, path := range roots {
		registry.paths[registryKey(path)] = path
	}
	waitIdle := func() {
		deadline := time.Now().Add(time.Second)
		for {
			registry.probeMu.Lock()
			active := registry.probeActive
			registry.probeMu.Unlock()
			if active == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("delayed probes did not release")
			}
			time.Sleep(time.Millisecond)
		}
	}

	first, incomplete, err := registry.freshRegisteredPaths(context.Background(), 50*time.Millisecond, time.Millisecond)
	if err != nil || !incomplete || len(first) != 0 || inspector.Calls("root-h") != 0 {
		t.Fatalf("first paths=%v incomplete=%v err=%v calls(final)=%d", first, incomplete, err, inspector.Calls("root-h"))
	}
	waitIdle()
	for retry := 0; retry < 6; retry++ {
		_, incomplete, err = registry.freshRegisteredPaths(context.Background(), 50*time.Millisecond, time.Millisecond)
		if err != nil || !incomplete {
			t.Fatalf("retry %d incomplete=%v err=%v, want phase-limited incomplete", retry, incomplete, err)
		}
		waitIdle()
	}
	for _, path := range roots {
		if inspector.Calls(path) == 0 {
			t.Fatalf("rotating admission never probed %s", path)
		}
	}
	paths, incomplete, err := registry.freshRegisteredPaths(context.Background(), 50*time.Millisecond, 100*time.Millisecond)
	if err != nil || incomplete || !reflect.DeepEqual(paths, roots) {
		t.Fatalf("final paths=%v incomplete=%v err=%v, want all roots", paths, incomplete, err)
	}
}

func TestRegistryFreshRegisteredPathsGlobalProbeSlotsAndShutdown(t *testing.T) {
	inspector := &blockingRegistryInspector{started: make(chan string, 8), release: make(chan struct{})}
	t.Cleanup(inspector.Release)
	registry := NewRegistry(inspector, "")
	for _, path := range []string{"a", "b", "c", "d"} {
		registry.paths[registryKey(path)] = path
	}

	const requests = 20
	results := make(chan struct{}, requests)
	for i := 0; i < requests; i++ {
		go func() {
			_, _, _ = registry.freshRegisteredPaths(context.Background(), time.Millisecond, 15*time.Millisecond)
			results <- struct{}{}
		}()
	}
	for i := 0; i < requests; i++ {
		select {
		case <-results:
		case <-time.After(time.Second):
			t.Fatal("concurrent fresh request did not return")
		}
	}
	if maximum := inspector.maximum.Load(); maximum > freshRegisteredWorkers {
		t.Fatalf("global maximum concurrent probes=%d, want <=%d", maximum, freshRegisteredWorkers)
	}
	if calls := inspector.calls.Load(); calls > freshRegisteredWorkers {
		t.Fatalf("slot exhaustion launched %d probes, want <=%d", calls, freshRegisteredWorkers)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := registry.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error=%v", err)
	}
	inspector.Release()
	for inspector.active.Load() != 0 {
		time.Sleep(time.Millisecond)
	}
	if _, incomplete, err := registry.freshRegisteredPaths(context.Background(), 10*time.Millisecond, 20*time.Millisecond); err != nil || !incomplete {
		t.Fatalf("shutdown admission paths incomplete=%v err=%v, want rejected incomplete", incomplete, err)
	}
}

func TestRegistryIdentityUsesSelectedAndPlatformKey(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\Workspace\Repo`); err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity("")
	if err != nil {
		t.Fatal(err)
	}
	if identity != gitx.RepositoryKey(`E:\Workspace\Repo`) {
		t.Fatalf("identity = %q", identity)
	}
}

func TestRegistrySelectedLeaseSnapshotsIdentityAndReleasesOnce(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo-a`); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(context.Background(), `E:\repo-b`); err != nil {
		t.Fatal(err)
	}
	lease, err := registry.acquireSelected(gitx.RepositoryKey(`E:\repo-a`))
	if err != nil {
		t.Fatal(err)
	}
	if lease.path != `E:\repo-a` || lease.identity != gitx.RepositoryKey(`E:\repo-a`) {
		t.Fatalf("lease snapshot = path %q identity %q", lease.path, lease.identity)
	}
	lease.Release()
	lease.Release()
	if err := registry.Select(`E:\repo-b`); err != nil {
		t.Fatalf("selection remained locked after once-safe release: %v", err)
	}
	if _, err := registry.acquireSelected(gitx.RepositoryKey(`E:\repo-a`)); !errors.Is(err, errSelectedRepositoryChanged) {
		t.Fatalf("stale lease acquisition err=%v", err)
	}
}

func TestRegistryOrdinaryReadsAreNotBlockedByPendingSelectionWriter(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo-a`); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(context.Background(), `E:\repo-b`); err != nil {
		t.Fatal(err)
	}
	lease, err := registry.acquireSelected(gitx.RepositoryKey(`E:\repo-a`))
	if err != nil {
		t.Fatal(err)
	}
	selected := make(chan error, 1)
	go func() { selected <- registry.Select(`E:\repo-b`) }()
	// Wait until the writer is observably pending. TryRLock fails for new
	// readers once a writer is queued, while the existing health lease remains
	// held. This avoids relying on a fixed scheduler-dependent sleep.
	writerPending := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !registry.selectionMu.TryRLock() {
			writerPending = true
			break
		}
		registry.selectionMu.RUnlock()
		runtime.Gosched()
	}
	if !writerPending {
		lease.Release()
		select {
		case <-selected:
		default:
		}
		t.Fatal("selection writer did not become pending")
	}
	resolved := make(chan error, 1)
	go func() {
		_, resolveErr := registry.Resolve("")
		resolved <- resolveErr
	}()
	select {
	case err := <-resolved:
		if err != nil {
			t.Fatalf("ordinary Resolve failed while selection writer waited: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ordinary Resolve was blocked by selection writer")
	}
	panel := make(chan error, 1)
	go func() {
		_, _, panelErr := registry.PanelSelection("")
		panel <- panelErr
	}()
	select {
	case err := <-panel:
		if err != nil {
			t.Fatalf("ordinary PanelSelection failed while selection writer waited: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ordinary PanelSelection was blocked by selection writer")
	}
	lease.Release()
	select {
	case err := <-selected:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("selection did not complete after lease release")
	}
}

func waitForPendingSelectionWriter(t *testing.T, registry *Registry) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !registry.selectionMu.TryRLock() {
			return
		}
		registry.selectionMu.RUnlock()
		runtime.Gosched()
	}
	t.Fatal("selection writer did not become pending")
}

func TestRegistrySelectContextCancellationDoesNotCommitQueuedSelection(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.json")
	registry := NewRegistry(fakeInspector{}, configFile)
	if _, err := registry.Add(context.Background(), `E:\repo-a`); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(context.Background(), `E:\repo-b`); err != nil {
		t.Fatal(err)
	}
	if err := registry.Select(`E:\repo-a`); err != nil {
		t.Fatal(err)
	}
	lease, err := registry.acquireSelected(gitx.RepositoryKey(`E:\repo-a`))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	selected := make(chan error, 1)
	go func() { selected <- registry.SelectContext(ctx, `E:\repo-b`) }()
	waitForPendingSelectionWriter(t, registry)
	cancel()
	lease.Release()
	select {
	case err := <-selected:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SelectContext error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled selection did not return")
	}
	if got := registry.Selected(); got != `E:\repo-a` {
		t.Fatalf("selected repository changed after cancellation: %q", got)
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	var cfg registryConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.LastSelected != `E:\repo-a` {
		t.Fatalf("persisted selection changed after cancellation: %q", cfg.LastSelected)
	}
	if err := registry.Select(`E:\repo-b`); err != nil {
		t.Fatalf("subsequent selection failed after canceled request: %v", err)
	}
}

func TestRegistrySelectContextCommitsAfterQueuedLeaseRelease(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo-a`); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(context.Background(), `E:\repo-b`); err != nil {
		t.Fatal(err)
	}
	lease, err := registry.acquireSelected(gitx.RepositoryKey(`E:\repo-a`))
	if err != nil {
		t.Fatal(err)
	}
	selected := make(chan error, 1)
	go func() { selected <- registry.SelectContext(context.Background(), `E:\repo-b`) }()
	waitForPendingSelectionWriter(t, registry)
	lease.Release()
	select {
	case err := <-selected:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("selection did not return after lease release")
	}
	if got := registry.Selected(); got != `E:\repo-b` {
		t.Fatalf("selected repository = %q, want repo-b", got)
	}
}

func TestRegistrySelectContextAlreadyCanceledHasNoSideEffects(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo-a`); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(context.Background(), `E:\repo-b`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := registry.SelectContext(ctx, `E:\repo-b`); !errors.Is(err, context.Canceled) {
		t.Fatalf("SelectContext error = %v, want context.Canceled", err)
	}
	if got := registry.Selected(); got != `E:\repo-a` {
		t.Fatalf("already-canceled selection changed selected repository: %q", got)
	}
	if err := registry.Select(`E:\repo-b`); err != nil {
		t.Fatalf("normal selection failed after already-canceled request: %v", err)
	}
}

func writeRegistryConfig(t *testing.T, recent []string, selected string) string {
	t.Helper()
	configFile := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(registryConfig{Recent: recent, LastSelected: selected})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return configFile
}

func TestRegistryLoadRejectsPersistedPathWhoseCanonicalTargetDrifted(t *testing.T) {
	configFile := writeRegistryConfig(t, []string{"approved-root"}, "approved-root")
	registry := NewRegistry(mappingInspector{repositories: map[string]gitx.Repository{
		"approved-root": {Path: "secret-root"},
	}}, configFile)
	// A reused Registry must not retain state from an earlier successful load.
	registry.paths[registryKey("approved-root")] = "approved-root"
	registry.selected = "approved-root"

	registry.Load(context.Background())
	if registry.Contains("approved-root") || registry.Contains("secret-root") {
		t.Fatal("drifted persisted path authorized an approved or replacement alias")
	}
	if _, err := registry.Resolve("approved-root"); err == nil {
		t.Fatal("approved alias resolved after canonical target drift")
	}
	if _, err := registry.Resolve("secret-root"); err == nil {
		t.Fatal("replacement alias resolved after canonical target drift")
	}
	if got := registry.Selected(); got != "" {
		t.Fatalf("drift-only load retained selection %q", got)
	}
}

func TestRegistryLoadAcceptsPersistedRepositoryWithSameIdentity(t *testing.T) {
	configFile := writeRegistryConfig(t, []string{"approved-root"}, "approved-root")
	registry := NewRegistry(mappingInspector{repositories: map[string]gitx.Repository{
		"approved-root": {Path: "approved-root", Head: "fresh-head"},
	}}, configFile)
	registry.Load(context.Background())

	resolved, err := registry.Resolve("approved-root")
	if err != nil || resolved != "approved-root" {
		t.Fatalf("same-identity load failed: resolved=%q err=%v", resolved, err)
	}
	if got := registry.Selected(); got != "approved-root" {
		t.Fatalf("selected = %q", got)
	}
}

func TestRegistryLoadFallsBackToFirstValidRepositoryWhenSelectedDrifted(t *testing.T) {
	configFile := writeRegistryConfig(t, []string{"drifted-root", "valid-root"}, "drifted-root")
	registry := NewRegistry(mappingInspector{repositories: map[string]gitx.Repository{
		"drifted-root": {Path: "replacement-root"},
		"valid-root":   {Path: "valid-root"},
	}}, configFile)
	registry.Load(context.Background())

	if registry.Contains("drifted-root") || registry.Contains("replacement-root") {
		t.Fatal("drifted selected repository remained authorized")
	}
	if got := registry.Selected(); got != "valid-root" {
		t.Fatalf("fallback selected = %q, want valid-root", got)
	}
	if resolved, err := registry.Resolve(""); err != nil || resolved != "valid-root" {
		t.Fatalf("selected resolve = %q, %v", resolved, err)
	}
}

func TestRegistryLoadCaseOnlyIdentityFollowsPlatform(t *testing.T) {
	approved := `E:\Workspace\Repo`
	inspected := strings.ToLower(approved)
	configFile := writeRegistryConfig(t, []string{approved}, approved)
	registry := NewRegistry(mappingInspector{repositories: map[string]gitx.Repository{
		approved: {Path: inspected},
	}}, configFile)
	registry.Load(context.Background())

	if runtime.GOOS == "windows" {
		if resolved, err := registry.Resolve(approved); err != nil || !gitx.SameRepositoryPath(resolved, approved) {
			t.Fatalf("Windows-equivalent persisted path rejected: %q, %v", resolved, err)
		}
		if registry.Selected() == "" {
			t.Fatal("Windows-equivalent repository was not selected")
		}
		return
	}
	if registry.Contains(approved) || registry.Selected() != "" {
		t.Fatal("case-sensitive platform accepted case-only canonical target change")
	}
}

func TestRegistryPanelSelectionPersistsAndIgnoresStaleRepositories(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.json")
	registry := NewRegistry(fakeInspector{}, configFile)
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPanelSelection(`E:\repo`, "team-panel"); err != nil {
		t.Fatal(err)
	}

	var cfg registryConfig
	data, err := os.ReadFile(configFile)
	if err != nil || json.Unmarshal(data, &cfg) != nil {
		t.Fatalf("read config: %v", err)
	}
	if cfg.PanelSelections == nil {
		cfg.PanelSelections = make(map[string]string)
	}
	cfg.PanelSelections[registryKey(`E:\stale`)] = "stale-panel"
	data, _ = json.Marshal(cfg)
	if err := os.WriteFile(configFile, data, 0o600); err != nil {
		t.Fatal(err)
	}

	reloaded := NewRegistry(fakeInspector{}, configFile)
	reloaded.Load(context.Background())
	if panel, ok, err := reloaded.PanelSelection(`E:\repo`); err != nil || !ok || panel != "team-panel" {
		t.Fatalf("selection = %q, %v, %v", panel, ok, err)
	}
	if _, _, err := reloaded.PanelSelection(`E:\stale`); err == nil {
		t.Fatal("stale panel preference became authorization")
	}
}

func TestRegistryPanelSelectionPersistenceFailureRollsBack(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), "repo"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPanelSelection("repo", "before"); err != nil {
		t.Fatal(err)
	}
	registry.configFile = failingRegistryConfig(t)
	if err := registry.SetPanelSelection("repo", "after"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if panel, ok, err := registry.PanelSelection("repo"); err != nil || !ok || panel != "before" {
		t.Fatalf("rollback selection = %q, %v, %v", panel, ok, err)
	}
}

func TestRegistryPanelSelectionUsesPlatformIdentity(t *testing.T) {
	registered := `E:\Workspace\Repo`
	alias := strings.ToLower(registered)
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), registered); err != nil {
		t.Fatal(err)
	}
	err := registry.SetPanelSelection(alias, "panel")
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("Windows identity alias rejected: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatal("case-sensitive platform accepted different path identity")
	}
}
