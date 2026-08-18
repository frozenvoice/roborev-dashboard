package roborev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeGitMetadataResolver struct {
	linked bool
	err    error
}

func (f fakeGitMetadataResolver) IsLinkedWorktree(context.Context, string) (bool, error) {
	return f.linked, f.err
}

type countingGitMetadataResolver struct {
	calls  int
	linked bool
	err    error
}

func (f *countingGitMetadataResolver) IsLinkedWorktree(context.Context, string) (bool, error) {
	f.calls++
	return f.linked, f.err
}

func writePanelConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPanelResolverMergesGlobalAndLocalByWholeKey(t *testing.T) {
	root := t.TempDir()
	globalPath := filepath.Join(t.TempDir(), "config.toml")
	writePanelConfig(t, globalPath, "[review.subagents.global]\nagent='codex'\n[review.subagents.shared]\nagent='global-agent'\n[review.panels.global_only]\nmembers=['global']\nsynthesis_agent='codex'\n[review.panels.shared]\nmembers=['global']\n")
	writePanelConfig(t, filepath.Join(root, ".roborev.toml"), "[review.subagents.local]\nagent='claude'\n[review.subagents.shared]\nagent='local-agent'\n[review.panels.local_only]\nmembers=['local']\n[review.panels.shared]\nmembers=['shared']\nsynthesis_backup_agent='backup'\n")
	catalog, err := (&PanelResolver{GlobalConfigPath: globalPath, Metadata: fakeGitMetadataResolver{linked: true}}).Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"global_only", "local_only", "none", "shared"}
	if len(catalog.Panels) != len(wantNames) {
		t.Fatalf("panels = %#v", catalog.Panels)
	}
	for index, name := range wantNames {
		if catalog.Panels[index].Name != name {
			t.Fatalf("panel order = %#v", catalog.Panels)
		}
	}
	shared, _ := catalog.Find("shared")
	if shared.Origin != "local" || len(shared.Members) != 1 || shared.Members[0] != "shared" || shared.SynthesisBackupAgent != "backup" {
		t.Fatalf("local whole-key override failed: %#v", shared)
	}
	global, _ := catalog.Find("global_only")
	if global.Origin != "global" || global.SynthesisAgent != "codex" {
		t.Fatalf("global panel metadata = %#v", global)
	}
}

func TestPanelResolverSafeAgentConfigurationMergesAliasesAndGlobalCommands(t *testing.T) {
	root := t.TempDir()
	globalPath := filepath.Join(t.TempDir(), "config.toml")
	writePanelConfig(t, globalPath, "codex_cmd='C:/tools/codex proxy.exe'\nclaude_code_cmd='claude-global'\n[review.subagents.shared]\nagent='claude'\nmodel='global-model'\nreasoning='high'\ninstructions='SECRET_GLOBAL_INSTRUCTIONS'\n[review.subagents.dynamic]\nagent='custom-acp'\n[review.subagents.global]\nagent='codex'\n")
	writePanelConfig(t, filepath.Join(root, ".roborev.toml"), "codex_cmd='local-must-be-ignored'\n[review.subagents.shared]\nagent='claude-code'\nreasoning='fast'\nprovider='SECRET_PROVIDER'\n[review.subagents.local]\nagent='grok-build'\nmodel='local-model'\n")
	configuration, err := (&PanelResolver{GlobalConfigPath: globalPath}).ResolveAgentConfiguration(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Commands["codex_cmd"] != "C:/tools/codex proxy.exe" || configuration.Commands["codex_cmd"] == "local-must-be-ignored" {
		t.Fatalf("global command override was not preserved: %#v", configuration.Commands)
	}
	if configuration.Commands["claude_code_cmd"] != "claude-global" {
		t.Fatalf("global Claude command override missing: %#v", configuration.Commands)
	}
	if len(configuration.Subagents) != 3 {
		t.Fatalf("safe subagents = %#v", configuration.Subagents)
	}
	shared := configuration.Subagents[2]
	if configuration.Subagents[1].Name != "local" || shared.Name != "shared" {
		t.Fatalf("subagent order = %#v", configuration.Subagents)
	}
	if shared.Agent != "claude-code" || shared.CanonicalAgent != "claude-code" || shared.Model != "" || shared.Reasoning != "fast" || shared.Origin != "local" {
		t.Fatalf("whole-entry local override failed: %#v", shared)
	}
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SECRET_GLOBAL_INSTRUCTIONS", "SECRET_PROVIDER", "custom-acp", "local-must-be-ignored"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("safe config leaked %q: %s", secret, data)
		}
	}
}

func TestPanelResolverPreservesInheritedSubagentAsDetectionUnavailable(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(t.TempDir(), "config.toml")
	writePanelConfig(t, filepath.Join(root, ".roborev.toml"), "[review.subagents.inherited]\nmodel='workflow-default'\nreasoning='medium'\ninstructions='SECRET'\n[review.subagents.dynamic]\nagent='custom-acp'\n")
	got, err := (&PanelResolver{GlobalConfigPath: config}).ResolveAgentConfiguration(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Subagents) != 1 || got.Subagents[0].Name != "inherited" || !got.Subagents[0].DetectionUnavailable || got.Subagents[0].CanonicalAgent != "" || got.Subagents[0].Model != "workflow-default" {
		t.Fatalf("inherited projection = %#v", got.Subagents)
	}
}

func TestPanelResolverNeverExposesSecretConfigFields(t *testing.T) {
	root := t.TempDir()
	config := "[review.subagents.audit]\nagent='codex'\nmodel='secret-model'\nprovider='secret-provider'\ninstructions='SECRET_SENTINEL_INSTRUCTIONS'\nreasoning='high'\ntimeout='2m'\n[review.panels.audit]\nmembers=['audit']\nsynthesis_model='secret-synthesis-model'\nsynthesis_backup_model='secret-backup-model'\n"
	writePanelConfig(t, filepath.Join(root, ".roborev.toml"), config)
	catalog, err := (&PanelResolver{GlobalConfigPath: filepath.Join(t.TempDir(), "missing.toml")}).Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(catalog.Panels)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-model", "secret-provider", "SECRET_SENTINEL_INSTRUCTIONS", "secret-synthesis-model", "secret-backup-model"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("catalog leaked %q: %s", secret, data)
		}
	}
}

func TestPanelResolverFailsClosedOnInvalidConfiguration(t *testing.T) {
	tests := []struct{ name, config string }{
		{"syntax", "[review.panels.bad"},
		{"type", "[review.panels.bad]\nmembers='not-an-array'\n"},
		{"empty", "[review.panels.bad]\nmembers=[]\n"},
		{"undefined", "[review.panels.bad]\nmembers=['missing']\n"},
		{"default-reference", "[review]\ndefault_panel='missing'\n"},
		{"review-type", "[review.subagents.bad]\nreview_type='bogus'\n[review.panels.bad]\nmembers=['bad']\n"},
		{"reasoning", "[review.subagents.bad]\nreasoning='bogus'\n[review.panels.bad]\nmembers=['bad']\n"},
		{"timeout", "[review.subagents.bad]\ntimeout='0s'\n[review.panels.bad]\nmembers=['bad']\n"},
		{"allow-failure-type", "[review.subagents.bad]\nallow_failure='yes'\n[review.panels.bad]\nmembers=['bad']\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writePanelConfig(t, filepath.Join(root, ".roborev.toml"), test.config)
			catalog, err := (&PanelResolver{GlobalConfigPath: filepath.Join(t.TempDir(), "missing.toml")}).Resolve(context.Background(), root)
			if err == nil || len(catalog.Panels) != 0 {
				t.Fatalf("invalid config did not fail closed: catalog=%#v err=%v", catalog, err)
			}
		})
	}
}

func TestPanelResolverParseErrorsDoNotEchoConfigValues(t *testing.T) {
	root := t.TempDir()
	writePanelConfig(t, filepath.Join(root, ".roborev.toml"), "[review.panels.bad]\nmembers='SECRET_CONFIG_VALUE'\n")
	_, err := (&PanelResolver{GlobalConfigPath: filepath.Join(t.TempDir(), "missing.toml")}).Resolve(context.Background(), root)
	if err == nil || strings.Contains(err.Error(), "SECRET_CONFIG_VALUE") {
		t.Fatalf("unsafe parser error: %v", err)
	}
}

func TestLoadPanelConfigAllowsSmallAndNearLimitRegularFiles(t *testing.T) {
	for _, size := range []int{1, maxPanelConfigBytes - 1, maxPanelConfigBytes} {
		t.Run(fmt.Sprintf("%d-bytes", size), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, bytes.Repeat([]byte{'#'}, size), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := loadPanelConfig(path); err != nil {
				t.Fatalf("size %d rejected: %v", size, err)
			}
		})
	}
}

func TestLoadPanelConfigRejectsOversizedAndNonRegularFiles(t *testing.T) {
	over := filepath.Join(t.TempDir(), "oversized.toml")
	if err := os.WriteFile(over, bytes.Repeat([]byte{'#'}, maxPanelConfigBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadPanelConfig(over); err == nil {
		t.Fatal("oversized config was accepted")
	}
	directory := t.TempDir()
	if _, _, err := loadPanelConfig(directory); err == nil {
		t.Fatal("directory config was accepted")
	}
	target := filepath.Join(t.TempDir(), "target.toml")
	if err := os.WriteFile(target, []byte("# target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(target), "link.toml")
	if err := os.Symlink(target, link); err == nil {
		if _, _, err := loadPanelConfig(link); err == nil {
			t.Fatal("symlink config was accepted")
		}
	}
}

func TestLoadPanelConfigReadsOpenedHandleAfterPathReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	want := []byte("# original panel config\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	handle, err := openPanelConfigNoFollow(path)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	replacement := path + ".moved"
	if err := os.Rename(path, replacement); err != nil {
		t.Skipf("path replacement unavailable: %v", err)
	}
	defer os.Remove(replacement)
	if err := os.WriteFile(path, []byte("# replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(handle)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("opened handle changed after pathname replacement: got %q want %q", got, want)
	}
}

func TestPanelResolverOversizedLocalConfigDoesNotExposeGlobalPanels(t *testing.T) {
	root := t.TempDir()
	globalPath := filepath.Join(t.TempDir(), "config.toml")
	writePanelConfig(t, globalPath, "[review.subagents.global]\nagent='codex'\n[review.panels.global]\nmembers=['global']\n")
	writePanelConfig(t, filepath.Join(root, ".roborev.toml"), strings.Repeat("#", maxPanelConfigBytes+1))
	catalog, err := (&PanelResolver{GlobalConfigPath: globalPath}).Resolve(context.Background(), root)
	if err == nil || len(catalog.Panels) != 0 {
		t.Fatalf("oversized local config leaked global catalog: catalog=%#v err=%v", catalog, err)
	}
}

func TestPanelResolverReservesBuiltinNone(t *testing.T) {
	root := t.TempDir()
	writePanelConfig(t, filepath.Join(root, ".roborev.toml"), "[review.panels.none]\nmembers=[]\n")
	catalog, err := (&PanelResolver{GlobalConfigPath: filepath.Join(t.TempDir(), "missing.toml")}).Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Panels) != 1 || catalog.Panels[0].Name != PanelNone || catalog.Panels[0].Kind != "single" || catalog.Panels[0].Origin != "builtin" {
		t.Fatalf("reserved none was shadowed: %#v", catalog.Panels)
	}
}

func TestPanelResolverUsesDirectRegisteredRootOnly(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "linked-worktree")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writePanelConfig(t, filepath.Join(base, "main-worktree", ".roborev.toml"), "[review.panels.parent]\nmembers=[]\n")
	catalog, err := (&PanelResolver{GlobalConfigPath: filepath.Join(t.TempDir(), "missing.toml")}).Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Panels) != 1 || catalog.Panels[0].Name != PanelNone {
		t.Fatalf("resolver searched outside direct root: %#v", catalog.Panels)
	}
}

func TestPanelResolverUsesRoboRevDataDirGlobalConfig(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ROBOREV_DATA_DIR", dataDir)
	writePanelConfig(t, filepath.Join(dataDir, "config.toml"), "[review.subagents.a]\nagent='codex'\n[review.panels.global]\nmembers=['a']\n")
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewPanelResolver().Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	panel, ok := catalog.Find("global")
	if !ok || panel.Origin != "global" {
		t.Fatalf("global data-dir panel missing: %#v", catalog.Panels)
	}
}

func TestPanelResolverSuppressesGlobalNamedPanelsForLinkedWorktreeWithoutDirectConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ../main/.git/worktrees/linked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(t.TempDir(), "config.toml")
	writePanelConfig(t, globalPath, "[review.subagents.a]\nagent='codex'\n[review.panels.shared]\nmembers=['a']\n")
	catalog, err := (&PanelResolver{GlobalConfigPath: globalPath, Metadata: fakeGitMetadataResolver{linked: true}}).Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Panels) != 1 || catalog.Panels[0].Name != PanelNone || catalog.Panels[0].Kind != "single" {
		t.Fatalf("linked worktree exposed ambiguous global panels: %#v", catalog.Panels)
	}
}

func TestPanelResolverLinkedWorktreeStillFailsClosedOnMalformedGlobalConfig(t *testing.T) {
	for _, config := range []string{
		"[review.panels.bad",
		"[review.panels.bad]\nmembers=['missing']\n",
	} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ../main/.git/worktrees/linked\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		globalPath := filepath.Join(t.TempDir(), "config.toml")
		writePanelConfig(t, globalPath, config)
		catalog, err := (&PanelResolver{GlobalConfigPath: globalPath, Metadata: fakeGitMetadataResolver{linked: true}}).Resolve(context.Background(), root)
		if err == nil || len(catalog.Panels) != 0 {
			t.Fatalf("linked worktree bypassed malformed global config: catalog=%#v err=%v", catalog, err)
		}
	}
}

func TestPanelResolverUsesDirectConfigInLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ../main/.git/worktrees/linked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writePanelConfig(t, filepath.Join(root, ".roborev.toml"), "[review.subagents.local]\nagent='codex'\n[review.panels.shared]\nmembers=['local']\n")
	globalPath := filepath.Join(t.TempDir(), "config.toml")
	writePanelConfig(t, globalPath, "[review.subagents.global]\nagent='codex'\n[review.panels.shared]\nmembers=['global']\n")
	catalog, err := (&PanelResolver{GlobalConfigPath: globalPath}).Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	shared, ok := catalog.Find("shared")
	if !ok || shared.Origin != "local" || len(shared.Members) != 1 || shared.Members[0] != "local" {
		t.Fatalf("direct linked-worktree config was not applied: %#v", catalog.Panels)
	}
}

func TestPanelResolverDirectEmptyConfigBypassesAmbiguousGitMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: not parsed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".roborev.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(t.TempDir(), "config.toml")
	writePanelConfig(t, globalPath, "[review.subagents.global]\nagent='codex'\n[review.panels.global]\nmembers=['global']\n")
	metadata := &countingGitMetadataResolver{err: errors.New("metadata failure")}
	catalog, err := (&PanelResolver{GlobalConfigPath: globalPath, Metadata: metadata}).Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.calls != 0 {
		t.Fatalf("metadata calls = %d, want 0", metadata.calls)
	}
	if _, ok := catalog.Find("global"); !ok {
		t.Fatalf("global panel missing: %#v", catalog.Panels)
	}
}

func TestPanelResolverUnambiguousGitLayoutsDoNotCallMetadata(t *testing.T) {
	for _, layout := range []string{"absent", "directory"} {
		t.Run(layout, func(t *testing.T) {
			root := t.TempDir()
			if layout == "directory" {
				if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			globalPath := filepath.Join(t.TempDir(), "config.toml")
			writePanelConfig(t, globalPath, "[review.subagents.global]\nagent='codex'\n[review.panels.global]\nmembers=['global']\n")
			metadata := &countingGitMetadataResolver{err: errors.New("metadata failure")}
			catalog, err := (&PanelResolver{GlobalConfigPath: globalPath, Metadata: metadata}).Resolve(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if metadata.calls != 0 {
				t.Fatalf("metadata calls = %d, want 0", metadata.calls)
			}
			if _, ok := catalog.Find("global"); !ok {
				t.Fatalf("global panel missing: %#v", catalog.Panels)
			}
		})
	}
}

func TestPanelResolverAmbiguousGitMetadataFailureFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata GitMetadataResolver
	}{
		{name: "nil", metadata: nil},
		{name: "error", metadata: fakeGitMetadataResolver{err: errors.New("SECRET_METADATA_ERROR")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: not parsed\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			globalPath := filepath.Join(t.TempDir(), "config.toml")
			writePanelConfig(t, globalPath, "[review.subagents.global]\nagent='codex'\n[review.panels.global]\nmembers=['global']\n")
			catalog, err := (&PanelResolver{GlobalConfigPath: globalPath, Metadata: test.metadata}).Resolve(context.Background(), root)
			if err == nil || len(catalog.Panels) != 0 || strings.Contains(err.Error(), "SECRET_METADATA_ERROR") {
				t.Fatalf("ambiguous metadata did not fail closed: catalog=%#v err=%v", catalog, err)
			}
		})
	}
}
