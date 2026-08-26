package roborev

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

func TestAgentHookManagementRevalidatesBindingBeforeInstall(t *testing.T) {
	runner := &readinessRunner{version: agentHookReadinessVersion, responses: map[string]readinessProbeResponse{
		"codex": {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Codex", false)}},
	}}
	client, binary := readinessTestClient(t, runner)
	binding, err := client.ProbeAgentHookBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(binary, time.Now().Add(time.Second), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	before := len(runner.snapshotCommands())
	if _, err := client.RunAgentHookInstall(context.Background(), "codex", binding); !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("binding drift error=%v", err)
	}
	commands := runner.snapshotCommands()[before:]
	if len(commands) != 1 || !equalStrings(commands[0].Args, []string{"version", "--json"}) {
		t.Fatalf("binding drift launched install: %#v", commands)
	}
}

func TestAgentHookManagementTransportUsesExactSingleAgentCommandsForAllAgents(t *testing.T) {
	runner := &readinessRunner{version: agentHookReadinessVersion, responses: map[string]readinessProbeResponse{}}
	for _, spec := range agentHookReadinessSpecs {
		runner.responses[spec.agent] = readinessProbeResponse{result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput(spec.display, false)}}
	}
	client, binary := readinessTestClient(t, runner)
	binding, err := client.ProbeAgentHookBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range agentHookReadinessSpecs {
		if dryRun, err := client.AgentHookDryRun(context.Background(), spec.agent, binding); err != nil || dryRun.Agent != spec.agent || dryRun.Status != AgentHookReadinessNeedsUpdate {
			t.Fatalf("%s dry-run=%#v err=%v", spec.agent, dryRun, err)
		}
		if _, err := client.RunAgentHookInstall(context.Background(), spec.agent, binding); err != nil {
			t.Fatalf("%s install: %v", spec.agent, err)
		}
	}
	for _, command := range runner.snapshotCommands() {
		if len(command.Args) == 2 && command.Args[0] == "version" {
			continue
		}
		validDryRun := len(command.Args) == 7 && command.Args[2] == "--dry-run" && command.Args[3] == "--agent" && command.Args[5] == "--binary" && command.Args[6] == binary
		validInstall := len(command.Args) == 6 && command.Args[2] == "--agent" && command.Args[4] == "--binary" && command.Args[5] == binary
		if command.Args[0] != "agent-hook" || command.Args[1] != "install" || (!validDryRun && !validInstall) {
			t.Fatalf("unexpected command=%#v", command.Args)
		}
		agentIndex := 3
		if validDryRun {
			agentIndex = 4
		}
		if command.Args[agentIndex] == "all" {
			t.Fatalf("multi-agent command=%#v", command.Args)
		}
		for _, forbidden := range []string{"--config", "--codex-config", "--claude-config", "--grok-config", "--command", "--scope", "status", "dump", "reset", "run", "daemon", "uninstall"} {
			for _, arg := range command.Args {
				if arg == forbidden {
					t.Fatalf("forbidden %q in command=%#v", forbidden, command.Args)
				}
			}
		}
	}
}
