package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/froze/roborev-dashboard/internal/gitx"
)

type RepositoryInspector interface {
	Inspect(context.Context, string) (gitx.Repository, error)
}

type repositoryMetadataInspector interface {
	IsLinkedWorktree(context.Context, string) (bool, error)
}

type repositoryHookPathResolver interface {
	HooksPath(context.Context, string) (string, error)
}

type repositoryHookLayoutResolver interface {
	ResolveHookLayout(context.Context, string) (gitx.HookLayout, error)
}

type registryConfig struct {
	Recent          []string          `json:"recentRepositories"`
	LastSelected    string            `json:"lastSelectedRepository"`
	PanelSelections map[string]string `json:"panelSelections,omitempty"`
}

type Registry struct {
	// selectionMu protects the selected repository independently from the
	// broader registry state.  Whenever both locks are needed they are always
	// acquired in selectionMu -> mu order.  This lets a long-running operation
	// hold a selected-repository read lease without blocking unrelated registry
	// reads and writes.
	selectionMu     sync.RWMutex
	mu              sync.RWMutex
	paths           map[string]string
	selected        string
	panelSelections map[string]string
	configFile      string
	inspector       RepositoryInspector

	// probeMu protects the Registry-owned admission gate and lifecycle. A
	// probe slot is held until inspectExpected returns; request cancellation
	// cannot release it early because a non-cooperative inspector may still be
	// executing in its detached goroutine.
	probeMu      sync.Mutex
	probeSlots   chan struct{}
	probeIdle    chan struct{}
	probeCancels map[uint64]context.CancelFunc
	probeActive  int
	probeNextID  uint64
	probeClosing bool
	probeCursor  atomic.Uint64
}

func registryKey(path string) string {
	return gitx.RepositoryKey(path)
}

func NewRegistry(inspector RepositoryInspector, configFile string) *Registry {
	registry := &Registry{paths: make(map[string]string), panelSelections: make(map[string]string), inspector: inspector, configFile: configFile}
	registry.initProbeStateLocked()
	return registry
}

func (r *Registry) initProbeStateLocked() {
	if r.probeSlots == nil {
		r.probeSlots = make(chan struct{}, freshRegisteredWorkers)
	}
	if r.probeIdle == nil {
		r.probeIdle = make(chan struct{})
		close(r.probeIdle)
	}
	if r.probeCancels == nil {
		r.probeCancels = make(map[uint64]context.CancelFunc)
	}
}

// BeginShutdown closes new fresh-inspection admission and cancels all
// cooperative probes. Non-cooperative inspectors continue to hold their
// Registry-owned slots until they return.
func (r *Registry) BeginShutdown() {
	r.probeMu.Lock()
	defer r.probeMu.Unlock()
	r.initProbeStateLocked()
	if r.probeClosing {
		return
	}
	r.probeClosing = true
	for _, cancel := range r.probeCancels {
		cancel()
	}
}

// Shutdown waits for all admitted probes without starting a waiter
// goroutine. A non-cooperative inspector can therefore make this return the
// caller's context error while the Registry still enforces its hard maximum
// of two active probe goroutines.
func (r *Registry) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.BeginShutdown()
	r.probeMu.Lock()
	r.initProbeStateLocked()
	idle := r.probeIdle
	r.probeMu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Registry) Load(ctx context.Context) {
	if r.configFile == "" {
		return
	}
	data, err := os.ReadFile(r.configFile)
	if err != nil {
		return
	}
	var cfg registryConfig
	if json.Unmarshal(data, &cfg) != nil {
		return
	}
	loaded := make(map[string]string)
	loadedPanelSelections := make(map[string]string)
	firstLoaded := ""
	for _, path := range cfg.Recent {
		repo, inspectErr := r.inspectExpected(ctx, path)
		if inspectErr == nil {
			key := registryKey(repo.Path)
			loaded[key] = repo.Path
			if panel, ok := cfg.PanelSelections[key]; ok {
				loadedPanelSelections[key] = panel
			}
			if firstLoaded == "" {
				firstLoaded = repo.Path
			}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = loaded
	r.panelSelections = loadedPanelSelections
	selectedPath := ""
	if selected, ok := loaded[registryKey(cfg.LastSelected)]; ok {
		selectedPath = selected
	}
	if selectedPath == "" {
		selectedPath = firstLoaded
	}
	r.selected = selectedPath
}

func (r *Registry) saveState(pathsByKey map[string]string, selected string, panelSelections map[string]string) error {
	if r.configFile == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.configFile), 0o700); err != nil {
		return err
	}
	paths := make([]string, 0, len(pathsByKey))
	for _, path := range pathsByKey {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	data, err := json.MarshalIndent(registryConfig{Recent: paths, LastSelected: selected, PanelSelections: panelSelections}, "", "  ")
	if err != nil {
		return err
	}
	temp := r.configFile + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, r.configFile)
}

func (r *Registry) Add(ctx context.Context, path string) (gitx.Repository, error) {
	if r.inspector == nil {
		return gitx.Repository{}, errors.New("repository inspector is unavailable")
	}
	repo, err := r.inspector.Inspect(ctx, path)
	if err != nil {
		return gitx.Repository{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	prospectivePaths := make(map[string]string, len(r.paths)+1)
	for key, value := range r.paths {
		prospectivePaths[key] = value
	}
	prospectivePaths[registryKey(repo.Path)] = repo.Path
	prospectivePanelSelections := cloneSelections(r.panelSelections)
	prospectiveSelected := r.selected
	if prospectiveSelected == "" {
		prospectiveSelected = repo.Path
	}
	if err := r.saveState(prospectivePaths, prospectiveSelected, prospectivePanelSelections); err != nil {
		return gitx.Repository{}, err
	}
	r.paths = prospectivePaths
	r.selected = prospectiveSelected
	r.panelSelections = prospectivePanelSelections
	return repo, nil
}

func (r *Registry) Contains(path string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.paths[registryKey(path)]
	return ok
}

const (
	freshRegisteredRootTimeout  = 3 * time.Second
	freshRegisteredPhaseTimeout = 6 * time.Second
	freshRegisteredWorkers      = 2
)

type freshProbeResult struct {
	path       string
	valid      bool
	incomplete bool
}

// FreshRegisteredPathsWithStatus returns completed canonical roots, whether
// one or more probes timed out, and any parent-context error. Membership is
// frozen before probing; only fresh inspections can contribute paths.
func (r *Registry) FreshRegisteredPathsWithStatus(ctx context.Context) ([]string, bool, error) {
	return r.freshRegisteredPaths(ctx, freshRegisteredRootTimeout, freshRegisteredPhaseTimeout)
}

// freshRegisteredPaths is parameterized for deterministic short-timeout
// tests. Production callers use the 3-second per-root and 6-second phase
// ceilings above.
func (r *Registry) freshRegisteredPaths(ctx context.Context, rootTimeout, phaseTimeout time.Duration) ([]string, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.RLock()
	snapshot := make([]string, 0, len(r.paths))
	for _, path := range r.paths {
		snapshot = append(snapshot, path)
	}
	r.mu.RUnlock()

	sort.Strings(snapshot)
	if len(snapshot) == 0 {
		return []string{}, false, nil
	}
	phaseCtx, cancel := context.WithTimeout(ctx, phaseTimeout)
	defer cancel()
	results := make(chan freshProbeResult, len(snapshot))
	start := r.probeCursor.Add(1) - 1
	accepted := 0
	processed := 0
	incomplete := false
	paths := make([]string, 0, len(snapshot))
	consume := func(result freshProbeResult) {
		processed++
		if result.valid {
			paths = append(paths, result.path)
		}
		if result.incomplete {
			incomplete = true
		}
	}
	for offset := uint64(0); offset < uint64(len(snapshot)); {
		if phaseCtx.Err() != nil {
			incomplete = true
			break
		}
		index := (start%uint64(len(snapshot)) + offset) % uint64(len(snapshot))
		if r.startFreshProbe(phaseCtx, snapshot[index], rootTimeout, results) {
			accepted++
			offset++
			continue
		}
		// A concurrent caller may own every slot. If this request still has
		// admitted probes, consume one result before retrying admission; do
		// not create a goroutine while the gate is full. With no in-flight
		// probe of our own, returning incomplete is immediate by contract.
		if processed == accepted {
			incomplete = true
			break
		}
		select {
		case result := <-results:
			consume(result)
		case <-phaseCtx.Done():
			incomplete = true
			goto done
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	// Drain admitted probes while the caller's phase remains alive. There is
	// deliberately no WaitGroup: a non-cooperative inspector may outlive the
	// request, but its result delivery is bounded and non-blocking.
	for processed < accepted {
		select {
		case result := <-results:
			consume(result)
		case <-phaseCtx.Done():
			incomplete = true
			goto done
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
done:
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if phaseCtx.Err() != nil && processed < accepted {
		incomplete = true
	}
	sort.Strings(paths)
	return paths, incomplete, nil
}

func (r *Registry) startFreshProbe(parent context.Context, path string, rootTimeout time.Duration, results chan<- freshProbeResult) bool {
	if parent.Err() != nil {
		return false
	}
	rootCtx, rootCancel := context.WithTimeout(parent, rootTimeout)
	r.probeMu.Lock()
	r.initProbeStateLocked()
	if r.probeClosing || parent.Err() != nil {
		r.probeMu.Unlock()
		rootCancel()
		return false
	}
	select {
	case r.probeSlots <- struct{}{}:
	default:
		r.probeMu.Unlock()
		rootCancel()
		return false
	}
	id := r.probeNextID
	for {
		if _, exists := r.probeCancels[id]; !exists {
			break
		}
		id++
	}
	r.probeNextID = id + 1
	r.probeCancels[id] = rootCancel
	if r.probeActive == 0 {
		r.probeIdle = make(chan struct{})
	}
	r.probeActive++
	r.probeMu.Unlock()
	go func() {
		repo, inspectErr := r.inspectExpected(rootCtx, path)
		result := freshProbeResult{}
		if rootCtx.Err() != nil {
			result.incomplete = true
		} else if inspectErr == nil {
			result.path = repo.Path
			result.valid = true
		} else if rootCtx.Err() != nil || errors.Is(inspectErr, context.Canceled) || errors.Is(inspectErr, context.DeadlineExceeded) {
			result.incomplete = true
		}
		// Release is linearized before delivery so a caller that receives this
		// result can immediately admit the next frozen root without observing
		// a transiently full gate.
		r.finishFreshProbe(id)
		select {
		case results <- result:
		default:
		}
	}()
	return true
}

func (r *Registry) finishFreshProbe(id uint64) {
	r.probeMu.Lock()
	cancel, ok := r.probeCancels[id]
	if !ok {
		r.probeMu.Unlock()
		panic("fresh probe released more than once")
	}
	delete(r.probeCancels, id)
	cancel()
	<-r.probeSlots
	if r.probeActive <= 0 {
		r.probeMu.Unlock()
		panic("fresh probe active count underflow")
	}
	r.probeActive--
	if r.probeActive == 0 {
		close(r.probeIdle)
	}
	r.probeMu.Unlock()
}

func (r *Registry) Resolve(path string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if path == "" {
		path = r.selected
	}
	resolved, ok := r.paths[registryKey(path)]
	if !ok {
		return "", errors.New("repository is not registered")
	}
	return resolved, nil
}

func (r *Registry) Select(path string) error {
	return r.SelectContext(context.Background(), path)
}

// SelectContext selects a registered repository while honoring cancellation
// before the request reaches the acceptance boundary.  Once the final check
// below passes, persistence and the in-memory selection update are accepted as
// one synchronous mutation.
func (r *Registry) SelectContext(ctx context.Context, path string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.selectionMu.Lock()
	defer r.selectionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// This is the selection acceptance boundary: cancellation before it has
	// zero side effects; cancellation after it may complete this mutation.
	if err := ctx.Err(); err != nil {
		return err
	}
	resolved, ok := r.paths[registryKey(path)]
	if !ok {
		return errors.New("repository is not registered")
	}
	if err := r.saveState(r.paths, resolved, r.panelSelections); err != nil {
		return err
	}
	r.selected = resolved
	return nil
}

func cloneSelections(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func validPanelPreference(panel string) bool {
	if strings.TrimSpace(panel) == "" || len(panel) > 128 {
		return false
	}
	for _, char := range panel {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func (r *Registry) SetPanelSelection(path, panel string) error {
	if !validPanelPreference(panel) {
		return errors.New("invalid panel selection")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if path == "" {
		path = r.selected
	}
	key := registryKey(path)
	if _, ok := r.paths[key]; !ok {
		return errors.New("repository is not registered")
	}
	prospective := cloneSelections(r.panelSelections)
	prospective[key] = panel
	if err := r.saveState(r.paths, r.selected, prospective); err != nil {
		return err
	}
	r.panelSelections = prospective
	return nil
}

func (r *Registry) PanelSelection(path string) (string, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if path == "" {
		path = r.selected
	}
	key := registryKey(path)
	if _, ok := r.paths[key]; !ok {
		return "", false, errors.New("repository is not registered")
	}
	panel, ok := r.panelSelections[key]
	return panel, ok, nil
}

// Inspect resolves a path using the registry's platform-aware identity rules
// and returns freshly inspected repository state for that registered root.
func (r *Registry) Inspect(ctx context.Context, path string) (gitx.Repository, error) {
	resolved, err := r.Resolve(path)
	if err != nil {
		return gitx.Repository{}, err
	}
	return r.inspectExpected(ctx, resolved)
}

func (r *Registry) inspectExpected(ctx context.Context, expectedPath string) (gitx.Repository, error) {
	if r.inspector == nil {
		return gitx.Repository{}, errors.New("repository inspector is unavailable")
	}
	repo, err := r.inspector.Inspect(ctx, expectedPath)
	if err != nil {
		return gitx.Repository{}, err
	}
	if !gitx.SameRepositoryPath(repo.Path, expectedPath) {
		return gitx.Repository{}, fmt.Errorf("inspected Git root %q does not match expected repository path %q", repo.Path, expectedPath)
	}
	return repo, nil
}

func (r *Registry) InspectAll(ctx context.Context) []gitx.Repository {
	r.mu.RLock()
	paths := make([]string, 0, len(r.paths))
	for _, path := range r.paths {
		paths = append(paths, path)
	}
	r.mu.RUnlock()
	repos := make([]gitx.Repository, 0, len(paths))
	for _, path := range paths {
		if repo, err := r.inspectExpected(ctx, path); err == nil {
			repos = append(repos, repo)
		}
	}
	return repos
}

// IsLinkedWorktree delegates checkout classification to the same Git service
// used for repository inspection. A custom inspector that cannot provide Git
// metadata reports an error; panel discovery then follows the conservative
// non-suppression fallthrough for unknown layouts.
func (r *Registry) IsLinkedWorktree(ctx context.Context, path string) (bool, error) {
	r.mu.RLock()
	inspector := r.inspector
	r.mu.RUnlock()
	metadata, ok := inspector.(repositoryMetadataInspector)
	if !ok {
		return false, errors.New("Git metadata inspection is unavailable")
	}
	return metadata.IsLinkedWorktree(ctx, path)
}

// HooksPath delegates read-only effective Git hook-path resolution to the
// canonical repository inspector. The browser never supplies a path; the
// endpoint first resolves an opaque registered identity and freshly inspects
// that root before calling this narrow adapter.
func (r *Registry) HooksPath(ctx context.Context, path string) (string, error) {
	r.mu.RLock()
	inspector := r.inspector
	r.mu.RUnlock()
	resolver, ok := inspector.(repositoryHookPathResolver)
	if !ok {
		return "", errors.New("Git hook path inspection is unavailable")
	}
	return resolver.HooksPath(ctx, path)
}

// ResolveHookLayout exposes the server-only complete Git hook layout to the
// management service. The HTTP layer still accepts only opaque identities.
func (r *Registry) ResolveHookLayout(ctx context.Context, path string) (gitx.HookLayout, error) {
	r.mu.RLock()
	inspector := r.inspector
	r.mu.RUnlock()
	resolver, ok := inspector.(repositoryHookLayoutResolver)
	if !ok {
		return gitx.HookLayout{}, errors.New("Git hook layout inspection is unavailable")
	}
	return resolver.ResolveHookLayout(ctx, path)
}

// Identity returns a stable, platform-aware correlation key for a registered
// repository. It does not grant authorization; mutations continue to resolve
// and authorize repository paths through the registry.
func (r *Registry) Identity(path string) (string, error) {
	resolved, err := r.Resolve(path)
	if err != nil {
		return "", err
	}
	return registryKey(resolved), nil
}

// ResolveIdentity authorizes an opaque repository identity against the
// registered canonical-path map.  Browser callers never provide a path to a
// stream or mutation endpoint.
func (r *Registry) ResolveIdentity(identity string) (string, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return "", errors.New("repository identity is required")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	path, ok := r.paths[identity]
	if !ok {
		return "", errors.New("repository is not registered")
	}
	return path, nil
}

// InspectIdentity combines identity authorization with a fresh Git inspection
// and canonical-root verification for long-lived repository-scoped sources.
func (r *Registry) InspectIdentity(ctx context.Context, identity string) (gitx.Repository, error) {
	path, err := r.ResolveIdentity(identity)
	if err != nil {
		return gitx.Repository{}, err
	}
	return r.inspectExpected(ctx, path)
}

func (r *Registry) Selected() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.selected
}

var errSelectedRepositoryChanged = errors.New("selected repository changed; refresh agent status and retry")

// selectedRepositoryLease keeps the selected repository stable while an
// operation that was explicitly accepted for that repository is running.
// Release is once-safe so all cancellation/error paths can defer it without
// risking an unlock panic.
type selectedRepositoryLease struct {
	path     string
	identity string
	once     sync.Once
	release  func()
}

func (l *selectedRepositoryLease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.release != nil {
			l.release()
		}
	})
}

// acquireSelected obtains a read lease for the currently selected repository
// and snapshots its path and platform-aware identity atomically.  The expected
// identity is checked while the lease is held, so callers can safely dispatch
// a repository-scoped operation without a selection/dispatch race.
func (r *Registry) acquireSelected(expectedIdentity string) (*selectedRepositoryLease, error) {
	r.selectionMu.RLock()
	r.mu.RLock()
	path := r.selected
	identity := registryKey(path)
	if path == "" || (strings.TrimSpace(expectedIdentity) != "" && identity != expectedIdentity) {
		r.mu.RUnlock()
		r.selectionMu.RUnlock()
		return nil, errSelectedRepositoryChanged
	}
	r.mu.RUnlock()
	return &selectedRepositoryLease{path: path, identity: identity, release: r.selectionMu.RUnlock}, nil
}

// AcquireSelected holds the selected-repository read lease through a
// repository-scoped acceptance/launch boundary. It is intentionally narrow:
// callers receive only an idempotent release function and the canonical path;
// ordinary repository switching semantics remain unchanged outside the
// bounded lease.
func (r *Registry) AcquireSelected(expectedIdentity string) (func(), string, error) {
	lease, err := r.acquireSelected(strings.TrimSpace(expectedIdentity))
	if err != nil {
		return nil, "", err
	}
	return lease.Release, lease.path, nil
}
