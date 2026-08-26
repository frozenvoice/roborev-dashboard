package roborev

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type skillsInstallRunner struct {
	mu       sync.Mutex
	commands []proc.Command
	events   *[]string
}

func (r *skillsInstallRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.mu.Lock()
	copyCommand := command
	copyCommand.Args = append([]string(nil), command.Args...)
	copyCommand.Env = append([]string(nil), command.Env...)
	r.commands = append(r.commands, copyCommand)
	if r.events != nil {
		switch {
		case len(command.Args) == 2 && command.Args[0] == "version" && command.Args[1] == "--json":
			*r.events = append(*r.events, "VERSION_PROBE")
		case len(command.Args) == 6 && command.Args[0] == "skills" && command.Args[1] == "install":
			*r.events = append(*r.events, "RUN")
		}
	}
	r.mu.Unlock()
	if len(command.Args) == 2 && command.Args[0] == "version" && command.Args[1] == "--json" {
		return proc.Result{Started: true, ExitCode: 0, Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
	}
	if len(command.Args) == 6 && command.Args[0] == "skills" && command.Args[1] == "install" {
		return proc.Result{Started: true, ExitCode: 0}, nil
	}
	return proc.Result{Started: true, ExitCode: 1}, os.ErrInvalid
}

func TestSkillsManagementTransportFinalBindingGuardRunsImmediatelyBeforeMutation(t *testing.T) {
	events := make([]string, 0, 4)
	runner := &skillsInstallRunner{events: &events}
	client := skillsReadinessTestClient(t, runner)
	binding, err := client.ProbeSkillsBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The initial probe binds the preview. The transport must perform one
	// fresh probe, invoke the guard exactly once, then run without another
	// subprocess or command preparation step.
	events = events[:0]
	directory := filepath.Join(t.TempDir(), "skills")
	guardCalls := 0
	execution, runErr := client.RunSkillsInstall(context.Background(), "codex", directory, binding, func(context.Context) error {
		guardCalls++
		events = append(events, "FINAL_TREE_GUARD")
		return nil
	})
	if runErr != nil || !execution.Started || execution.ExitCode != 0 {
		t.Fatalf("execution=%#v err=%v", execution, runErr)
	}
	if guardCalls != 1 || !equalStrings(events, []string{"VERSION_PROBE", "FINAL_TREE_GUARD", "RUN"}) {
		t.Fatalf("guard calls=%d events=%#v", guardCalls, events)
	}
}

func TestSkillsManagementTransportGuardFailureAndNilFailClosed(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "skills")
	for _, test := range []struct {
		name   string
		guard  SkillsPreLaunchGuard
		marker string
	}{
		{name: "callback error", guard: func(context.Context) error { return errors.New(`private path C:\secret\roborev-address`) }, marker: "C:\\secret"},
		{name: "nil", guard: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &skillsInstallRunner{}
			client := skillsReadinessTestClient(t, runner)
			binding, err := client.ProbeSkillsBinding(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			execution, runErr := client.RunSkillsInstall(context.Background(), "codex", directory, binding, test.guard)
			if !errors.Is(runErr, ErrSkillsPreLaunchGuard) || execution.Started {
				t.Fatalf("execution=%#v err=%v", execution, runErr)
			}
			if test.marker != "" && strings.Contains(runErr.Error(), test.marker) {
				t.Fatalf("guard error leaked marker %q: %v", test.marker, runErr)
			}
			for _, command := range runner.snapshot() {
				if len(command.Args) > 0 && command.Args[0] == "skills" {
					t.Fatalf("guard failure launched install=%#v", command.Args)
				}
			}
		})
	}
}

func (r *skillsInstallRunner) snapshot() []proc.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]proc.Command, len(r.commands))
	copy(result, r.commands)
	return result
}

func TestSkillsManagementTransportUsesExactSingleAgentCommandAndMinimalEnv(t *testing.T) {
	t.Setenv("ROBOREV_PRIVATE_TOKEN", "secret")
	t.Setenv("GIT_DIR", `C:\private-git`)
	t.Setenv("CODEX_HOME", `C:\private-codex`)
	t.Setenv("CLAUDE_CONFIG_DIR", `C:\private-claude`)
	t.Setenv("GROK_HOME", `C:\private-grok`)
	t.Setenv("HOME", `C:\private-home`)
	runner := &skillsInstallRunner{}
	client := skillsReadinessTestClient(t, runner)
	binding, err := client.ProbeSkillsBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	directory, err := filepath.Abs(filepath.Join(t.TempDir(), "skills"))
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"codex", "claude", "grok", "droid"} {
		if execution, runErr := client.RunSkillsInstall(context.Background(), agent, directory, binding, func(context.Context) error { return nil }); runErr != nil || !execution.Started || execution.ExitCode != 0 {
			t.Fatalf("agent=%s execution=%#v err=%v", agent, execution, runErr)
		}
	}
	commands := runner.snapshot()
	installCount := 0
	for _, command := range commands {
		if len(command.Args) == 2 && command.Args[0] == "version" {
			continue
		}
		installCount++
		if !equalStrings(command.Args, []string{"skills", "install", "--path", directory, "--agent", command.Args[5]}) {
			t.Fatalf("unexpected install args=%#v", command.Args)
		}
		if command.Dir != "" || command.AllowDetachedChildren {
			t.Fatalf("unsafe process options=%#v", command)
		}
		for _, entry := range command.Env {
			name, _, _ := strings.Cut(entry, "=")
			for _, forbidden := range []string{"HOME", "USERPROFILE", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "GROK_HOME", "GIT_DIR", "ROBOREV_PRIVATE_TOKEN"} {
				if strings.EqualFold(name, forbidden) {
					t.Fatalf("forbidden env %q in %#v", name, command.Env)
				}
			}
		}
		if strings.Contains(strings.Join(command.Args, " "), "skills update") {
			t.Fatalf("broad update command=%#v", command.Args)
		}
	}
	if installCount != 4 {
		t.Fatalf("install count=%d commands=%#v", installCount, commands)
	}
}

func TestSkillsManagementTransportRejectsRelativeOrUnsupportedMutation(t *testing.T) {
	runner := &skillsInstallRunner{}
	client := skillsReadinessTestClient(t, runner)
	binding, err := client.ProbeSkillsBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		agent string
		path  string
	}{
		{agent: "all", path: filepath.Join("relative", "skills")},
		{agent: "codex", path: filepath.Join("relative", "skills")},
	} {
		if execution, runErr := client.RunSkillsInstall(context.Background(), test.agent, test.path, binding, func(context.Context) error { return nil }); runErr == nil || execution.Started {
			t.Fatalf("agent=%s path=%s execution=%#v err=%v", test.agent, test.path, execution, runErr)
		}
	}
	for _, command := range runner.snapshot() {
		if len(command.Args) > 0 && command.Args[0] == "skills" {
			t.Fatalf("invalid input launched mutation=%#v", command.Args)
		}
	}
}
