package roborev

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type healthCaptureRunner struct {
	command proc.Command
	result  proc.Result
	err     error
}

func (r *healthCaptureRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.command = command
	return r.result, r.err
}

func TestV064AgentDescriptorsAndAliases(t *testing.T) {
	descriptors := AgentDescriptors()
	want := []string{"codex", "claude-code", "gemini", "copilot", "opencode", "cursor", "kiro", "kilo", "droid", "pi", "grok"}
	if len(descriptors) != len(want) {
		t.Fatalf("descriptor count = %d, want %d", len(descriptors), len(want))
	}
	for i, name := range want {
		if descriptors[i].Name != name {
			t.Fatalf("descriptor[%d] = %#v", i, descriptors[i])
		}
	}
	for alias, canonical := range map[string]string{"claude": "claude-code", "agent": "cursor", "grok-build": "grok"} {
		if got, ok := CanonicalAgentName(alias); !ok || got != canonical {
			t.Fatalf("alias %q = %q, %v", alias, got, ok)
		}
	}
	for _, excluded := range []string{"test", "acp", "custom-agent"} {
		if _, ok := CanonicalAgentName(excluded); ok {
			t.Fatalf("unsafe agent %q was allowlisted", excluded)
		}
	}
}

func TestCheckAgentBuildsExactV064CommandAndParsesPass(t *testing.T) {
	runner := &healthCaptureRunner{result: proc.Result{Stdout: "? claude-code claude (/path) ... OK (2 bytes)\n1 passed, 0 failed, 0 skipped\n"}}
	client, err := NewClient(`C:\trusted\roborev.exe`, runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.CheckAgent(context.Background(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent != "claude-code" || got.Status != AgentHealthPassed {
		t.Fatalf("result = %#v", got)
	}
	want := proc.Command{Executable: `C:\trusted\roborev.exe`, Args: []string{"check-agents", "--agent", "claude-code", "--timeout", "60"}, Timeout: 65 * time.Second, OutputLimit: 64 << 10}
	if runner.command.Timeout <= 60*time.Second {
		t.Fatalf("outer health timeout must exceed CLI timeout: %s", runner.command.Timeout)
	}
	if !reflect.DeepEqual(runner.command, want) {
		t.Fatalf("command = %#v, want %#v", runner.command, want)
	}
	if _, err := client.CheckAgentInRepo(context.Background(), `E:\registered\한글 repo`, "claude"); err != nil {
		t.Fatal(err)
	}
	if runner.command.Dir != `E:\registered\한글 repo` {
		t.Fatalf("health command dir = %q", runner.command.Dir)
	}
}

func TestCheckAgentParsesFailAndSkipWithoutLeakingOutput(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
		err    error
		status AgentHealthStatus
	}{
		{name: "failed", output: "? codex codex (/path) ... FAIL\n    provider failure\n0 passed, 1 failed, 0 skipped\n", err: errors.New("exit 1"), status: AgentHealthFailed},
		{name: "skipped", output: "- codex unavailable\n0 passed, 0 failed, 1 skipped\n", status: AgentHealthSkipped},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &healthCaptureRunner{result: proc.Result{Stdout: test.output}, err: test.err}
			client, err := NewClient("roborev", runner, "127.0.0.1:7373")
			if err != nil {
				t.Fatal(err)
			}
			got, checkErr := client.CheckAgent(context.Background(), "codex")
			if checkErr != nil || got.Status != test.status {
				t.Fatalf("result = %#v, err=%v", got, checkErr)
			}
		})
	}
}

func TestCheckAgentFailsClosedOnBoundsMalformedAndCancel(t *testing.T) {
	tooLarge := &healthCaptureRunner{result: proc.Result{StdoutTruncated: true, Stdout: strings.Repeat("x", 64<<10)}}
	client, err := NewClient("roborev", tooLarge, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CheckAgent(context.Background(), "codex"); err == nil {
		t.Fatal("truncated health output was accepted")
	}
	malformed := &healthCaptureRunner{result: proc.Result{Stdout: "OK\n"}}
	client.Runner = malformed
	if _, err := client.CheckAgent(context.Background(), "codex"); err == nil {
		t.Fatal("malformed health output was accepted")
	}
	if _, err := client.CheckAgent(context.Background(), "unknown"); err == nil {
		t.Fatal("unknown health agent was accepted")
	}
	canceled := &healthCaptureRunner{err: context.Canceled}
	client.Runner = canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := client.CheckAgent(ctx, "codex")
	if err != nil || got.Status != AgentHealthCanceled {
		t.Fatalf("cancel result = %#v, err=%v", got, err)
	}
}

func TestParseAgentHealthOutputRequiresMatchingResultRow(t *testing.T) {
	tests := []string{
		"1 passed, 0 failed, 0 skipped\n",
		"? gemini gemini (/path) ... OK (2 bytes)\n1 passed, 0 failed, 0 skipped\n",
		"? codex codex (/path) ... OK (2 bytes)\n? codex duplicate\n1 passed, 0 failed, 0 skipped\n",
		"? codex codex (/path) ... OK (2 bytes)\n0 passed, 1 failed, 0 skipped\n",
	}
	for _, output := range tests {
		if _, err := ParseAgentHealthOutput("codex", output); err == nil {
			t.Fatalf("invalid health output accepted: %q", output)
		}
	}
}
