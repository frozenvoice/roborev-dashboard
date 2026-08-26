package roborev

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type hookLedgerRunner struct {
	commands   []proc.Command
	version    string
	versionErr error
}

func (r *hookLedgerRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	if len(command.Args) == 2 && command.Args[0] == "version" && command.Args[1] == "--json" {
		if r.versionErr != nil {
			return proc.Result{ExitCode: 1}, r.versionErr
		}
		return proc.Result{Started: true, ExitCode: 0, Stdout: `{"name":"roborev","version":"` + r.version + `"}`}, nil
	}
	return proc.Result{Started: true, ExitCode: 0}, nil
}

func TestGitHookTransportUsesFreshBindingAndExactCommandLedger(t *testing.T) {
	t.Setenv("GIT_DIR", `C:\attacker\git`)
	t.Setenv("GIT_WORK_TREE", `C:\attacker\worktree`)
	t.Setenv("GIT_CONFIG_GLOBAL", `C:\attacker\config`)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", `C:\attacker\hooks`)
	dir := t.TempDir()
	binary := filepath.Join(dir, "roborev.exe")
	if err := os.WriteFile(binary, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &hookLedgerRunner{version: "v0.64.0"}
	client, err := NewClient(binary, runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := client.ProbeGitHookBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || !equalStrings(runner.commands[0].Args, []string{"version", "--json"}) || runner.commands[0].Executable != binary {
		t.Fatalf("version ledger=%#v", runner.commands)
	}
	root := filepath.Join(dir, "repo")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		action GitHookAction
		args   []string
	}{
		{GitHookInstall, []string{"install-hook", "--binary", binary}},
		{GitHookRepair, []string{"install-hook", "repair", "--binary", binary}},
		{GitHookRemove, []string{"uninstall-hook"}},
	}
	for _, tc := range cases {
		before := len(runner.commands)
		if _, err := client.RunGitHook(context.Background(), root, tc.action, binding); err != nil {
			t.Fatalf("action %s: %v", tc.action, err)
		}
		if len(runner.commands) != before+2 {
			t.Fatalf("action %s ledger delta=%d commands=%#v", tc.action, len(runner.commands)-before, runner.commands)
		}
		versionCommand := runner.commands[before]
		mutationCommand := runner.commands[before+1]
		if !equalStrings(versionCommand.Args, []string{"version", "--json"}) || mutationCommand.Executable != binary || mutationCommand.Dir != root || !equalStrings(mutationCommand.Args, tc.args) {
			t.Fatalf("action %s commands=%#v", tc.action, runner.commands[before:before+2])
		}
	}
	for _, command := range runner.commands {
		for _, entry := range command.Env {
			if strings.HasPrefix(strings.ToUpper(entry), "GIT_") {
				t.Fatalf("hostile Git environment leaked into command %#v: %q", command, entry)
			}
		}
		joined := strings.Join(command.Args, " ")
		forbidden := []string{"--force", "--registered", "init", "agent-hook", "status"}
		for _, token := range forbidden {
			if strings.Contains(joined, token) {
				t.Fatalf("forbidden token %q in command %#v", token, command)
			}
		}
	}
}

func TestGitHookTransportRejectsBindingChangeBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "roborev")
	if err := os.WriteFile(binary, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &hookLedgerRunner{version: "v0.64.0"}
	client, err := NewClient(binary, runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := client.ProbeGitHookBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	client.bindingMu.Lock()
	client.binding.generation++
	client.bindingMu.Unlock()
	before := len(runner.commands)
	if _, err := client.RunGitHook(context.Background(), dir, GitHookInstall, binding); !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("binding change error=%v", err)
	}
	if len(runner.commands) != before+1 {
		t.Fatalf("mutation launched after binding change: %#v", runner.commands[before:])
	}
}
