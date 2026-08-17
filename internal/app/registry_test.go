package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

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
