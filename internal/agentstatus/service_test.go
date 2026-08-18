package agentstatus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type fakeExecutableResolver struct {
	paths map[string]string
	same  map[[2]string]bool
}

func (f fakeExecutableResolver) LookPath(command string) (string, error) {
	path, ok := f.paths[command]
	if !ok {
		return "", os.ErrNotExist
	}
	return path, nil
}

func (f fakeExecutableResolver) SameFile(left, right string) bool {
	if left == right {
		return true
	}
	return f.same[[2]string{left, right}] || f.same[[2]string{right, left}]
}

type fakeProbeRunner struct {
	results map[string]ProbeResult
	calls   [][]string
}

func (f *fakeProbeRunner) Run(_ context.Context, _ string, args []string) ProbeResult {
	f.calls = append(f.calls, append([]string(nil), args...))
	return f.results[args[0]]
}

type fakeHealthChecker struct{ calls int }

func (f *fakeHealthChecker) CheckAgent(_ context.Context, agent string) (roborev.AgentHealthResult, error) {
	f.calls++
	return roborev.AgentHealthResult{Agent: agent, Status: roborev.AgentHealthPassed}, nil
}

type statusHealthChecker struct {
	calls  int
	status roborev.AgentHealthStatus
}

func (f *statusHealthChecker) CheckAgent(_ context.Context, agent string) (roborev.AgentHealthResult, error) {
	f.calls++
	return roborev.AgentHealthResult{Agent: agent, Status: f.status}, nil
}

type captureProcessRunner struct{ command proc.Command }

func (r *captureProcessRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.command = command
	return proc.Result{Stdout: "Cursor 1.0"}, nil
}

func writeAgentConfig(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverUsesDescriptorsCustomGlobalOverridesAndNoAutoHealth(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(t.TempDir(), "config.toml")
	writeAgentConfig(t, global, "codex_cmd='codex-custom'\n[review.subagents.review]\nagent='claude'\nmodel='sonnet'\nreasoning='high'\ninstructions='DO_NOT_EXPOSE'\n[review.subagents.inherited]\nmodel='workflow-default'\n")
	resolver := fakeExecutableResolver{paths: map[string]string{
		"codex-custom": "C:/custom/codex.exe", "claude": "C:/bin/claude.exe",
		"grok": "C:/bin/grok.exe", "agent": "C:/bin/cursor.exe",
	}}
	probe := &fakeProbeRunner{results: map[string]ProbeResult{"--version": {Output: "Cursor 1.0"}}}
	checker := &fakeHealthChecker{}
	service := NewServiceWithSeams(&roborev.PanelResolver{GlobalConfigPath: global}, checker, resolver, probe)
	scope := RepositoryScope{Root: root, Identity: "repo-a"}
	statuses, err := service.Discover(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if checker.calls != 0 {
		t.Fatalf("discovery called health checker %d times", checker.calls)
	}
	var codex, claude, cursor Status
	for _, status := range statuses {
		switch status.Name {
		case "codex":
			codex = status
		case "claude-code":
			claude = status
		case "cursor":
			cursor = status
		}
	}
	if codex.Command != "codex-custom" || !codex.Available || codex.Path != "C:/custom/codex.exe" {
		t.Fatalf("custom codex discovery = %#v", codex)
	}
	if !claude.Configured || len(claude.Subagents) != 1 || claude.Subagents[0].Agent != "claude" || claude.Subagents[0].CanonicalAgent != "claude-code" {
		t.Fatalf("configured alias projection = %#v", claude)
	}
	if !cursor.Available || !reflect.DeepEqual(probe.calls, [][]string{{"--version"}}) {
		t.Fatalf("cursor detection = %#v, probe calls=%#v", cursor, probe.calls)
	}
	if len(statuses[0].UnresolvedSubagents) != 1 || statuses[0].UnresolvedSubagents[0].Name != "inherited" || !statuses[0].UnresolvedSubagents[0].DetectionUnavailable {
		t.Fatalf("inherited projection = %#v", statuses[0].UnresolvedSubagents)
	}
}

func TestCursorDetectionFailsClosedForGrokAmbiguousTimeoutAndOversize(t *testing.T) {
	tests := []struct {
		name    string
		cursor  string
		grok    string
		results map[string]ProbeResult
		want    bool
	}{
		{name: "same grok file", cursor: "same.exe", grok: "same.exe", results: map[string]ProbeResult{"--version": {Output: "Cursor"}}, want: false},
		{name: "grok output", cursor: "cursor.exe", grok: "grok.exe", results: map[string]ProbeResult{"--version": {Output: "grok 0.2"}}, want: false},
		{name: "ambiguous empty", cursor: "cursor.exe", grok: "grok.exe", results: map[string]ProbeResult{"--version": {}, "-v": {}}, want: false},
		{name: "timeout", cursor: "cursor.exe", grok: "grok.exe", results: map[string]ProbeResult{"--version": {Err: context.DeadlineExceeded}}, want: false},
		{name: "oversize", cursor: "cursor.exe", grok: "grok.exe", results: map[string]ProbeResult{"--version": {Truncated: true}}, want: false},
		{name: "non grok", cursor: "cursor.exe", grok: "grok.exe", results: map[string]ProbeResult{"--version": {Output: "Cursor 1.0"}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := fakeExecutableResolver{paths: map[string]string{"agent": test.cursor, "grok": test.grok}}
			if test.cursor == test.grok {
				resolver.same = map[[2]string]bool{{test.cursor, test.grok}: true}
			}
			probe := &fakeProbeRunner{results: test.results}
			service := NewServiceWithSeams(&roborev.PanelResolver{GlobalConfigPath: filepath.Join(t.TempDir(), "missing.toml")}, nil, resolver, probe)
			root := t.TempDir()
			statuses, err := service.Discover(context.Background(), RepositoryScope{Root: root, Identity: test.name})
			if err != nil {
				t.Fatal(err)
			}
			var got Status
			for _, status := range statuses {
				if status.Name == "cursor" {
					got = status
				}
			}
			if got.Available != test.want {
				t.Fatalf("cursor available=%v, want %v; calls=%#v", got.Available, test.want, probe.calls)
			}
		})
	}
}

func TestServiceCheckStoresAliasMappedHealth(t *testing.T) {
	checker := &fakeHealthChecker{}
	global := filepath.Join(t.TempDir(), "config.toml")
	writeAgentConfig(t, global, "[review.subagents.grok]\nagent='grok'\n")
	service := NewServiceWithSeams(&roborev.PanelResolver{GlobalConfigPath: global}, checker, fakeExecutableResolver{paths: map[string]string{"grok": "grok.exe"}}, &fakeProbeRunner{})
	scope := RepositoryScope{Root: t.TempDir(), Identity: "repo-a"}
	result, err := service.Check(context.Background(), scope, "grok-build")
	if err != nil || result.Agent != "grok" || checker.calls != 1 {
		t.Fatalf("check result=%#v err=%v calls=%d", result, err, checker.calls)
	}
	if got, ok := service.Health(scope, "grok"); !ok || got.Status != roborev.AgentHealthPassed {
		t.Fatalf("stored health=%#v ok=%v", got, ok)
	}
	if _, err := service.Check(context.Background(), scope, "test"); err == nil {
		t.Fatal("unsafe health agent accepted")
	}
}

func TestHealthIsScopedByRepositoryIdentity(t *testing.T) {
	global := filepath.Join(t.TempDir(), "config.toml")
	writeAgentConfig(t, global, "[review.subagents.codex]\nagent='codex'\n")
	checker := &statusHealthChecker{status: roborev.AgentHealthPassed}
	service := NewServiceWithSeams(
		&roborev.PanelResolver{GlobalConfigPath: global}, checker,
		fakeExecutableResolver{paths: map[string]string{"codex": "codex.exe"}}, &fakeProbeRunner{},
	)
	scopeA := RepositoryScope{Root: t.TempDir(), Identity: "repo-a"}
	scopeB := RepositoryScope{Root: t.TempDir(), Identity: "repo-b"}
	if _, err := service.Check(context.Background(), scopeA, "codex"); err != nil {
		t.Fatal(err)
	}
	if got, ok := service.Health(scopeA, "codex"); !ok || got.Status != roborev.AgentHealthPassed {
		t.Fatalf("repo A health = %#v, ok=%v", got, ok)
	}
	if got, ok := service.Health(scopeB, "codex"); ok || got.Status != "" {
		t.Fatalf("repo B inherited repo A health = %#v, ok=%v", got, ok)
	}
	checker.status = roborev.AgentHealthFailed
	if _, err := service.Check(context.Background(), scopeB, "codex"); err != nil {
		t.Fatal(err)
	}
	if got, _ := service.Health(scopeA, "codex"); got.Status != roborev.AgentHealthPassed {
		t.Fatalf("repo A health changed after repo B check: %#v", got)
	}
	if got, _ := service.Health(scopeB, "codex"); got.Status != roborev.AgentHealthFailed {
		t.Fatalf("repo B health = %#v", got)
	}
}

func TestCheckRequiresConfiguredAndDetectedAgentBeforeRunner(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(t.TempDir(), "config.toml")
	writeAgentConfig(t, global, "[review.subagents.codex]\nagent='codex'\n")
	checker := &fakeHealthChecker{}
	resolver := fakeExecutableResolver{paths: map[string]string{}}
	service := NewServiceWithSeams(&roborev.PanelResolver{GlobalConfigPath: global}, checker, resolver, &fakeProbeRunner{})
	_, err := service.Check(context.Background(), RepositoryScope{Root: root, Identity: "repo-a"}, "codex")
	if err == nil || checker.calls != 0 {
		t.Fatalf("missing executable check err=%v calls=%d", err, checker.calls)
	}
	globalMissing := filepath.Join(t.TempDir(), "missing.toml")
	service = NewServiceWithSeams(&roborev.PanelResolver{GlobalConfigPath: globalMissing}, checker, fakeExecutableResolver{paths: map[string]string{"codex": "codex.exe"}}, &fakeProbeRunner{})
	_, err = service.Check(context.Background(), RepositoryScope{Root: root, Identity: "repo-a"}, "codex")
	if err == nil || checker.calls != 0 {
		t.Fatalf("unconfigured check err=%v calls=%d", err, checker.calls)
	}
}

func TestCheckGuardStopsSelectionRaceBeforeRunner(t *testing.T) {
	global := filepath.Join(t.TempDir(), "config.toml")
	writeAgentConfig(t, global, "[review.subagents.codex]\nagent='codex'\n")
	checker := &fakeHealthChecker{}
	service := NewServiceWithSeams(&roborev.PanelResolver{GlobalConfigPath: global}, checker, fakeExecutableResolver{paths: map[string]string{"codex": "codex.exe"}}, &fakeProbeRunner{})
	guardCalls := 0
	_, err := service.CheckInRepo(context.Background(), RepositoryScope{Root: t.TempDir(), Identity: "repo-a"}, "codex", func() error {
		guardCalls++
		return errors.New("selected repository changed")
	})
	if err == nil || guardCalls != 1 || checker.calls != 0 {
		t.Fatalf("guard result err=%v guardCalls=%d runnerCalls=%d", err, guardCalls, checker.calls)
	}
}

func TestProductionCursorProbeUsesIsolatedProcessOptions(t *testing.T) {
	t.Setenv("GH_TOKEN", "secret")
	t.Setenv("NODE_OPTIONS", "--require=untrusted.js")
	capture := &captureProcessRunner{}
	probe := processIdentityProbeRunner{runner: capture}
	result := probe.Run(context.Background(), "cursor.exe", []string{"--version"})
	if result.Err != nil || result.Truncated || result.Output != "Cursor 1.0" {
		t.Fatalf("probe result = %#v", result)
	}
	command := capture.command
	if command.Dir != os.TempDir() || command.Timeout != identityProbeTimeout || command.WaitDelay != identityProbeWaitDelay || command.OutputLimit != identityProbeMaxBytes {
		t.Fatalf("probe command options = %#v", command)
	}
	if !reflect.DeepEqual(command.Args, []string{"--version"}) {
		t.Fatalf("probe args = %#v", command.Args)
	}
	for _, entry := range command.Env {
		if strings.HasPrefix(entry, "GH_TOKEN=") || strings.HasPrefix(entry, "NODE_OPTIONS=") {
			t.Fatalf("probe inherited untrusted environment: %q", entry)
		}
	}
}
