package roborev

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

// semanticUpstreamRunner is a deterministic test-only stand-in for the v0.64
// writer. It records semantic hook ownership and intentionally preserves
// unrelated values, allowing this adapter test to verify the transport scope
// without reproducing the production JSON writer.
type semanticUpstreamRunner struct {
	mu       sync.Mutex
	config   map[string]any
	hooks    map[string]string
	path     map[string]string
	commands []proc.Command
}

func (r *semanticUpstreamRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	if len(command.Args) == 2 && command.Args[0] == "version" {
		r.mu.Unlock()
		return proc.Result{Started: true, ExitCode: 0, Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
	}
	if len(command.Args) < 6 || command.Args[0] != "agent-hook" || command.Args[1] != "install" {
		r.mu.Unlock()
		return proc.Result{Started: true, ExitCode: 1}, fmt.Errorf("unexpected semantic command")
	}
	dryRun := command.Args[2] == "--dry-run"
	agentIndex := 4
	if !dryRun {
		agentIndex = 3
	}
	agent := command.Args[agentIndex]
	current := r.hooks[agent] == semanticRunnerFor(agent)
	path := r.path[agent]
	if !dryRun {
		r.hooks[agent] = semanticRunnerFor(agent)
	}
	r.mu.Unlock()
	if dryRun {
		label := map[string]string{"codex": "Codex", "claude": "Claude", "grok": "Grok Build", "droid": "Factory Droid"}[agent]
		if current {
			return proc.Result{Started: true, ExitCode: 0, Stdout: label + " agent hooks already installed in " + path + "\n"}, nil
		}
		return proc.Result{Started: true, ExitCode: 0, Stdout: "would update " + label + " agent hooks in " + path + "\n"}, nil
	}
	return proc.Result{Started: true, ExitCode: 0}, nil
}

func semanticRunnerFor(agent string) string {
	switch agent {
	case "grok":
		return "grok-runner"
	case "droid":
		return "droid-runner"
	default:
		return "plain-runner"
	}
}

func TestAgentHookManagementPreservesUnrelatedUpstreamSemantics(t *testing.T) {
	runner := &semanticUpstreamRunner{
		config: map[string]any{"unrelated": "preserve-me", "number": float64(7)},
		hooks:  map[string]string{"codex": "old", "claude": "plain-runner", "grok": "old", "droid": "droid-runner"},
		path: map[string]string{
			"codex":  `C:\fake-home\.codex\hooks.json`,
			"claude": `C:\fake-home\.claude\settings.json`,
			"grok":   `C:\fake-home\.grok\hooks\roborev.json`,
			"droid":  `C:\fake-home\.factory\hooks.json`,
		},
	}
	client, _ := readinessTestClient(t, runner)
	binding, err := client.ProbeAgentHookBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"codex", "claude", "grok", "droid"} {
		if _, err := client.RunAgentHookInstall(context.Background(), agent, binding); err != nil {
			t.Fatalf("install %s: %v", agent, err)
		}
	}
	if runner.config["unrelated"] != "preserve-me" || runner.config["number"] != float64(7) {
		t.Fatalf("unrelated top-level config changed: %#v", runner.config)
	}
	if runner.hooks["codex"] != "plain-runner" || runner.hooks["claude"] != "plain-runner" || runner.hooks["grok"] != "grok-runner" || runner.hooks["droid"] != "droid-runner" {
		t.Fatalf("agent runners=%#v", runner.hooks)
	}
	if runner.hooks["droid"] == runner.hooks["codex"] || runner.hooks["grok"] == runner.hooks["codex"] {
		t.Fatalf("agent-specific runners collapsed=%#v", runner.hooks)
	}
	raw, err := json.Marshal(runner.config)
	if err != nil || string(raw) == "" {
		t.Fatalf("semantic config marshal=%s err=%v", raw, err)
	}
}
