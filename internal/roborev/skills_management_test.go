package roborev

import (
	"context"
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
}

func (r *skillsInstallRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.mu.Lock()
	copyCommand := command
	copyCommand.Args = append([]string(nil), command.Args...)
	copyCommand.Env = append([]string(nil), command.Env...)
	r.commands = append(r.commands, copyCommand)
	r.mu.Unlock()
	if len(command.Args) == 2 && command.Args[0] == "version" && command.Args[1] == "--json" {
		return proc.Result{Started: true, ExitCode: 0, Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
	}
	if len(command.Args) == 6 && command.Args[0] == "skills" && command.Args[1] == "install" {
		return proc.Result{Started: true, ExitCode: 0}, nil
	}
	return proc.Result{Started: true, ExitCode: 1}, os.ErrInvalid
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
		if execution, runErr := client.RunSkillsInstall(context.Background(), agent, directory, binding); runErr != nil || !execution.Started || execution.ExitCode != 0 {
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
		if execution, runErr := client.RunSkillsInstall(context.Background(), test.agent, test.path, binding); runErr == nil || execution.Started {
			t.Fatalf("agent=%s path=%s execution=%#v err=%v", test.agent, test.path, execution, runErr)
		}
	}
	for _, command := range runner.snapshot() {
		if len(command.Args) > 0 && command.Args[0] == "skills" {
			t.Fatalf("invalid input launched mutation=%#v", command.Args)
		}
	}
}
