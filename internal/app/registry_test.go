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
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
)

type mappingInspector struct {
	repositories map[string]gitx.Repository
	errors       map[string]error
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

func TestRegistryRegisteredPathsReturnsSortedSnapshot(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	for _, path := range []string{`E:\z-repo`, `E:\a-repo`, `E:\m-repo`} {
		if _, err := registry.Add(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	got := registry.RegisteredPaths()
	want := []string{`E:\a-repo`, `E:\m-repo`, `E:\z-repo`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registered paths=%v, want %v", got, want)
	}
	got[0] = `E:\mutated`
	if registry.RegisteredPaths()[0] != want[0] {
		t.Fatal("registered path snapshot aliases registry state")
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

	got := registry.FreshRegisteredPaths(context.Background())
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

	got := registry.FreshRegisteredPaths(context.Background())
	want := []string{`E:\a-repo`, `E:\m-repo`, `E:\z-repo`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh paths=%v, want %v", got, want)
	}
	got[0] = `E:\mutated`
	if next := registry.FreshRegisteredPaths(context.Background()); !reflect.DeepEqual(next, want) {
		t.Fatalf("fresh path snapshot was mutable: %v", next)
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
