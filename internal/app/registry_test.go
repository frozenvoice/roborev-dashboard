package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/froze/roborev-dashboard/internal/gitx"
)

type mappingInspector struct {
	repositories map[string]gitx.Repository
}

func (m mappingInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
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
