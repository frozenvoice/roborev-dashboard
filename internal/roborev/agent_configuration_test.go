package roborev

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type agentConfigurationRunner struct {
	commands []proc.Command
	results  []proc.Result
	errors   []error
}

func (r *agentConfigurationRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	i := len(r.commands) - 1
	if i >= len(r.results) {
		return proc.Result{Started: true}, nil
	}
	var err error
	if i < len(r.errors) {
		err = r.errors[i]
	}
	return r.results[i], err
}

func newAgentConfigurationTestClient(t *testing.T, runner proc.Runner) (*Client, string) {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "roborev")
	if err := os.WriteFile(binary, []byte("test executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(binary, runner, "")
	if err != nil {
		t.Fatal(err)
	}
	return client, binary
}

func agentVersionResult() proc.Result {
	return proc.Result{Started: true, ExitCode: 0, Stdout: `{"name":"roborev","version":"v0.64.0"}`}
}

func agentConfigOKResult(stdout string) proc.Result {
	return proc.Result{Started: true, ExitCode: 0, Stdout: stdout}
}

const agentConfigListFixture = "default  max_workers  4\nglobal   default_agent  codex\nlocal    agent           gemini\n"

func TestRepositoryAgentConfigurationPreflightIsBoundedAndUsesExactCommands(t *testing.T) {
	t.Setenv("ROBOREV_DATA_DIR", t.TempDir())
	t.Setenv("GIT_DIR", `C:\private\redirected.git`)
	t.Setenv("PRIVATE_CONFIG_CONTENT", "secret")
	runner := &agentConfigurationRunner{results: []proc.Result{
		agentVersionResult(), agentConfigOKResult(agentConfigListFixture), agentConfigOKResult("codex\n"), agentVersionResult(),
	}}
	client, binary := newAgentConfigurationTestClient(t, runner)
	preflight, err := client.RepositoryAgentConfigurationPreflight(context.Background(), t.TempDir(), ConfigScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if preflight.Binding.Version != "v0.64.0" || !preflight.Value.Set || preflight.Value.Value != "codex" || len(preflight.Rows) != 3 {
		t.Fatalf("preflight=%#v", preflight)
	}
	if len(runner.commands) != 4 {
		t.Fatalf("commands=%d, want 4", len(runner.commands))
	}
	if !reflect.DeepEqual(runner.commands[0].Args, []string{"version", "--json"}) || runner.commands[0].Executable != binary || runner.commands[0].Timeout != 5*time.Second {
		t.Fatalf("version command=%#v", runner.commands[0])
	}
	if runner.commands[0].OutputLimit != 64<<10 || runner.commands[0].Dir != "" {
		t.Fatalf("version bounds/dir=%#v", runner.commands[0])
	}
	if !reflect.DeepEqual(runner.commands[1].Args, []string{"config", "list", "--show-origin"}) || runner.commands[1].Timeout != 10*time.Second || runner.commands[1].OutputLimit != agentConfigurationOutputLimit {
		t.Fatalf("list command=%#v", runner.commands[1])
	}
	if !reflect.DeepEqual(runner.commands[2].Args, []string{"config", "get", "default_agent", "--global"}) || runner.commands[2].Timeout != 10*time.Second || runner.commands[2].OutputLimit != agentConfigurationValueLimit {
		t.Fatalf("get command=%#v", runner.commands[2])
	}
	if !reflect.DeepEqual(runner.commands[3].Args, []string{"version", "--json"}) || runner.commands[3].Timeout != 5*time.Second || runner.commands[3].OutputLimit != 64<<10 || runner.commands[3].Dir != "" {
		t.Fatalf("final version command=%#v", runner.commands[3])
	}
	for _, command := range runner.commands {
		if (len(command.Args) > 0 && command.Args[0] == "config" && command.Dir != filepath.Clean(runner.commands[1].Dir)) || len(command.Env) == 0 {
			t.Fatalf("missing cwd/env: %#v", command)
		}
		joined := strings.Join(command.Env, "\n")
		if !strings.Contains(joined, "ROBOREV_DATA_DIR=") || strings.Contains(joined, "GIT_DIR=") || strings.Contains(joined, "PRIVATE_CONFIG_CONTENT=") {
			t.Fatalf("unsafe env=%#v", command.Env)
		}
	}
}

func TestRepositoryAgentConfigurationStatusUsesOneBoundSnapshot(t *testing.T) {
	runner := &agentConfigurationRunner{results: []proc.Result{
		agentVersionResult(),
		agentConfigOKResult(agentConfigListFixture),
		agentConfigOKResult("codex\n"),
		agentConfigOKResult("gemini\n"),
		agentVersionResult(),
	}}
	client, binary := newAgentConfigurationTestClient(t, runner)
	repoPath := t.TempDir()
	status, err := client.RepositoryAgentConfigurationStatus(context.Background(), repoPath)
	if err != nil {
		t.Fatal(err)
	}
	if status.Binding.Version != "v0.64.0" || len(status.Rows) != 3 || !status.Global.Set || status.Global.Value != "codex" || !status.Repository.Set || status.Repository.Value != "gemini" {
		t.Fatalf("status=%#v", status)
	}
	wantArgs := [][]string{
		{"version", "--json"},
		{"config", "list", "--show-origin"},
		{"config", "get", "default_agent", "--global"},
		{"config", "get", "agent", "--local"},
		{"version", "--json"},
	}
	wantTimeouts := []time.Duration{5 * time.Second, 10 * time.Second, 10 * time.Second, 10 * time.Second, 5 * time.Second}
	wantLimits := []int{64 << 10, agentConfigurationOutputLimit, agentConfigurationValueLimit, agentConfigurationValueLimit, 64 << 10}
	if len(runner.commands) != len(wantArgs) {
		t.Fatalf("commands=%d, want %d", len(runner.commands), len(wantArgs))
	}
	for index, command := range runner.commands {
		if command.Executable != binary || !reflect.DeepEqual(command.Args, wantArgs[index]) || command.Timeout != wantTimeouts[index] || command.OutputLimit != wantLimits[index] {
			t.Fatalf("command[%d]=%#v", index, command)
		}
		wantDir := ""
		if index > 0 && index < len(wantArgs)-1 {
			wantDir = filepath.Clean(repoPath)
		}
		if command.Dir != wantDir || len(command.Env) == 0 {
			t.Fatalf("command[%d] cwd/env=%#v", index, command)
		}
	}
}

func TestRepositoryAgentConfigurationStatusFailsClosedAtEverySnapshotStage(t *testing.T) {
	cases := []struct {
		name         string
		results      []proc.Result
		wantCommands int
	}{
		{
			name: "list",
			results: []proc.Result{
				agentVersionResult(),
				{Started: true, ExitCode: 0, Stdout: agentConfigListFixture, Stderr: "unexpected"},
			},
			wantCommands: 2,
		},
		{
			name: "local-get",
			results: []proc.Result{
				agentVersionResult(), agentConfigOKResult(agentConfigListFixture), agentConfigOKResult("codex\n"), agentConfigOKResult("gemini\nextra\n"),
			},
			wantCommands: 4,
		},
		{
			name: "final-binding",
			results: []proc.Result{
				agentVersionResult(), agentConfigOKResult(agentConfigListFixture), agentConfigOKResult("codex\n"), agentConfigOKResult("gemini\n"),
				{Started: true, ExitCode: 0, Stdout: `{"name":"roborev","version":"v0.63.0"}`},
			},
			wantCommands: 5,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runner := &agentConfigurationRunner{results: test.results}
			client, _ := newAgentConfigurationTestClient(t, runner)
			if status, err := client.RepositoryAgentConfigurationStatus(context.Background(), t.TempDir()); err == nil || status.Binding.Valid() {
				t.Fatalf("status=%#v err=%v", status, err)
			}
			if len(runner.commands) != test.wantCommands {
				t.Fatalf("commands=%d, want %d", len(runner.commands), test.wantCommands)
			}
		})
	}
}

func TestRepositoryAgentConfigurationGetDistinguishesUnsetAndExplicitEmpty(t *testing.T) {
	runner := &agentConfigurationRunner{results: []proc.Result{
		{Started: true, ExitCode: 1, Stderr: `Error: key "agent" is not set in local config`},
		{Started: true, ExitCode: 0, Stdout: "\n"},
	}, errors: []error{errors.New("exit status 1"), nil}}
	client, _ := newAgentConfigurationTestClient(t, runner)
	unset, err := client.agentConfigurationGet(context.Background(), filepath.Dir(client.BinaryPath()), ConfigScopeLocal)
	if err != nil || unset.Set {
		t.Fatalf("unset=%#v err=%v", unset, err)
	}
	empty, err := client.agentConfigurationGet(context.Background(), filepath.Dir(client.BinaryPath()), ConfigScopeLocal)
	if err != nil || !empty.Set || empty.Value != "" {
		t.Fatalf("empty=%#v err=%v", empty, err)
	}
	for _, stderr := range []string{` Error: key "agent" is not set in local config`, `Error: key "agent" is not set in local config\n\n`, `Error: key "agent" is not set in local config `} {
		runner := &agentConfigurationRunner{results: []proc.Result{{Started: true, ExitCode: 1, Stderr: stderr}}, errors: []error{errors.New("exit status 1")}}
		client, _ := newAgentConfigurationTestClient(t, runner)
		if _, err := client.agentConfigurationGet(context.Background(), filepath.Dir(client.BinaryPath()), ConfigScopeLocal); err == nil {
			t.Fatalf("accepted malformed unset stderr %q", stderr)
		}
	}
}

func TestRepositoryAgentConfigurationSetUsesExpectedBinaryAndFinalGuard(t *testing.T) {
	runner := &agentConfigurationRunner{results: []proc.Result{agentVersionResult(), agentVersionResult(), {Started: true, ExitCode: 0}}}
	client, binary := newAgentConfigurationTestClient(t, runner)
	binding, err := client.ProbeRepositoryAgentConfigurationBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	guardCalled := false
	result, err := client.RepositoryAgentConfigurationSet(context.Background(), filepath.Dir(binary), ConfigScopeGlobal, "codex", binding, func(context.Context) error {
		guardCalled = true
		return nil
	})
	if err != nil || !result.Started || !guardCalled {
		t.Fatalf("result=%#v err=%v guard=%v", result, err, guardCalled)
	}
	if len(runner.commands) != 3 || runner.commands[2].Executable != binding.Binary || runner.commands[2].Dir != filepath.Dir(binary) || !reflect.DeepEqual(runner.commands[2].Args, []string{"config", "set", "default_agent", "codex", "--global"}) || runner.commands[2].Timeout != 15*time.Second || runner.commands[2].OutputLimit != agentConfigurationValueLimit {
		t.Fatalf("commands=%#v", runner.commands)
	}
}

func TestRepositoryAgentConfigurationReadbackRequiresExactScopedRow(t *testing.T) {
	runner := &agentConfigurationRunner{results: []proc.Result{
		agentVersionResult(), agentVersionResult(), {Started: true, ExitCode: 0, Stdout: "codex\n"}, agentConfigOKResult("local  agent  codex\n"), agentVersionResult(),
	}}
	client, binary := newAgentConfigurationTestClient(t, runner)
	binding, err := client.ProbeRepositoryAgentConfigurationBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The initial binding probe above is intentionally separate from the
	// readback queue; this catches accidental reuse of a stale process result.
	if err := client.RepositoryAgentConfigurationReadback(context.Background(), filepath.Dir(binary), ConfigScopeLocal, "codex", binding); err != nil {
		t.Fatal(err)
	}
	if got := len(runner.commands); got != 5 {
		t.Fatalf("commands=%d, want initial probe + readback probes", got)
	}
}

func TestRepositoryAgentConfigurationListRejectsMalformedOutput(t *testing.T) {
	for _, result := range []proc.Result{
		{Started: true, ExitCode: 0, Stderr: "warning"},
		{Started: true, ExitCode: 0, StdoutTruncated: true, Stdout: agentConfigListFixture},
		{Started: true, ExitCode: 0, Stdout: string([]byte{'l', 'o', 'c', 'a', 'l', ' ', ' ', 'a', 'g', 'e', 'n', 't', ' ', ' ', 0xff})},
		{Started: true, ExitCode: 0, Stdout: "bogus agent value"},
		{Started: true, ExitCode: 0, Stdout: ""},
	} {
		runner := &agentConfigurationRunner{results: []proc.Result{result}}
		client, _ := newAgentConfigurationTestClient(t, runner)
		if _, err := client.agentConfigurationList(context.Background(), filepath.Dir(client.BinaryPath())); err == nil {
			t.Fatalf("accepted malformed list result %#v", result)
		}
	}
}

func TestRepositoryAgentConfigurationBindingRejectsMalformedVersionContract(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		stderr string
	}{
		{"unknown-field", `{"name":"roborev","version":"v0.64.0","private":"x"}`, ""},
		{"trailing", `{"name":"roborev","version":"v0.64.0"}{}`, ""},
		{"wrong-name", `{"name":"other","version":"v0.64.0"}`, ""},
		{"wrong-version", `{"name":"roborev","version":"v0.63.0"}`, ""},
		{"stderr", `{"name":"roborev","version":"v0.64.0"}`, "warning"},
		{"invalid-utf8", string([]byte(`{"name":"roborev","version":"v0.64.0"}`)) + string([]byte{0xff}), ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runner := &agentConfigurationRunner{results: []proc.Result{{Started: true, ExitCode: 0, Stdout: test.stdout, Stderr: test.stderr}}}
			client, _ := newAgentConfigurationTestClient(t, runner)
			if _, err := client.ProbeRepositoryAgentConfigurationBinding(context.Background()); err == nil {
				t.Fatalf("accepted malformed version response")
			}
		})
	}
}

func TestRepositoryAgentConfigurationGetRejectsNearMissResponses(t *testing.T) {
	cases := []struct {
		name   string
		result proc.Result
		err    error
	}{
		{"multiple-eol", proc.Result{Started: true, ExitCode: 0, Stdout: "codex\n\n"}, nil},
		{"leading-space", proc.Result{Started: true, ExitCode: 0, Stdout: " codex\n"}, nil},
		{"control", proc.Result{Started: true, ExitCode: 0, Stdout: "codex\x00\n"}, nil},
		{"near-unset", proc.Result{Started: true, ExitCode: 1, Stderr: `Error: key "agent" is not set in local config `}, errors.New("exit status 1")},
		{"missing-start", proc.Result{Started: false, ExitCode: 1, Stderr: `Error: key "agent" is not set in local config`}, errors.New("start process")},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runner := &agentConfigurationRunner{results: []proc.Result{test.result}, errors: []error{test.err}}
			client, _ := newAgentConfigurationTestClient(t, runner)
			if _, err := client.agentConfigurationGet(context.Background(), filepath.Dir(client.BinaryPath()), ConfigScopeLocal); err == nil {
				t.Fatalf("accepted malformed get response")
			}
		})
	}
}

func TestRepositoryAgentConfigurationSetGuardAndValidationBoundMutation(t *testing.T) {
	// A guard failure still requires exactly one fresh binding probe but never
	// enters the config-set process boundary.
	runner := &agentConfigurationRunner{results: []proc.Result{agentVersionResult(), agentVersionResult()}}
	client, binary := newAgentConfigurationTestClient(t, runner)
	binding, err := client.ProbeRepositoryAgentConfigurationBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RepositoryAgentConfigurationSet(context.Background(), filepath.Dir(binary), ConfigScopeGlobal, "codex", binding, func(context.Context) error { return errors.New("root changed") }); err == nil {
		t.Fatal("guard failure was accepted")
	}
	if len(runner.commands) != 2 {
		t.Fatalf("guard failure entered mutation: commands=%#v", runner.commands)
	}
	for _, test := range []struct {
		name  string
		scope ConfigScope
		value string
	}{
		{"bad-scope", ConfigScope("global "), "codex"},
		{"bad-value", ConfigScopeGlobal, "claude"},
		{"whitespace-value", ConfigScopeGlobal, " codex"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &agentConfigurationRunner{}
			client, binary := newAgentConfigurationTestClient(t, runner)
			binding := RepositoryAgentConfigurationBinding{Binary: binary, Version: "v0.64.0", Generation: 1, FileID: "file"}
			if _, err := client.RepositoryAgentConfigurationSet(context.Background(), filepath.Dir(binary), test.scope, test.value, binding, func(context.Context) error { return nil }); err == nil {
				t.Fatal("invalid set input was accepted")
			}
			if len(runner.commands) != 0 {
				t.Fatalf("invalid set launched process: %#v", runner.commands)
			}
		})
	}
}

func TestRepositoryAgentConfigurationSetStartedEvidenceIsSingleMutation(t *testing.T) {
	cases := []struct {
		name   string
		result proc.Result
		runErr error
	}{
		{"started-success", proc.Result{Started: true, ExitCode: 0}, nil},
		{"started-nonzero", proc.Result{Started: true, ExitCode: 2}, errors.New("exit status 2")},
		{"not-started", proc.Result{Started: false, ExitCode: -1}, errors.New("start process")},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runner := &agentConfigurationRunner{results: []proc.Result{agentVersionResult(), agentVersionResult(), test.result}, errors: []error{nil, nil, test.runErr}}
			client, binary := newAgentConfigurationTestClient(t, runner)
			binding, err := client.ProbeRepositoryAgentConfigurationBinding(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			result, _ := client.RepositoryAgentConfigurationSet(context.Background(), filepath.Dir(binary), ConfigScopeGlobal, "codex", binding, func(context.Context) error { return nil })
			if result.Started != test.result.Started {
				t.Fatalf("Started=%v, want %v", result.Started, test.result.Started)
			}
			if len(runner.commands) != 3 {
				t.Fatalf("mutation process count=%d, want one set plus two probes", len(runner.commands))
			}
			if got := runner.commands[2].Args; !reflect.DeepEqual(got, []string{"config", "set", "default_agent", "codex", "--global"}) {
				t.Fatalf("set args=%#v", got)
			}
			if runner.commands[2].Timeout != agentConfigurationSetTimeout || runner.commands[2].OutputLimit != agentConfigurationValueLimit || runner.commands[2].Dir != filepath.Dir(binary) {
				t.Fatalf("set bounds/dir=%#v", runner.commands[2])
			}
		})
	}
}

func TestRepositoryAgentConfigurationReadbackRejectsWrongScopedRowAndBindingDrift(t *testing.T) {
	for _, test := range []struct {
		name string
		get  string
		list string
	}{
		{"missing-row", "codex\n", "local  other_key  value\n"},
		{"wrong-origin", "codex\n", "default  agent  codex\n"},
		{"wrong-value", "gemini\n", "local  agent  gemini\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &agentConfigurationRunner{results: []proc.Result{agentVersionResult(), agentVersionResult(), agentConfigOKResult(test.get), agentConfigOKResult(test.list), agentVersionResult()}}
			client, binary := newAgentConfigurationTestClient(t, runner)
			binding, err := client.ProbeRepositoryAgentConfigurationBinding(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := client.RepositoryAgentConfigurationReadback(context.Background(), filepath.Dir(binary), ConfigScopeLocal, "codex", binding); err == nil {
				t.Fatal("accepted incorrect scoped readback")
			}
		})
	}
	runner := &agentConfigurationRunner{results: []proc.Result{agentVersionResult()}}
	client, binary := newAgentConfigurationTestClient(t, runner)
	expected, err := client.ProbeRepositoryAgentConfigurationBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	client.bindingMu.Lock()
	client.binding.generation++
	client.bindingMu.Unlock()
	runner.results = append(runner.results, agentVersionResult())
	if err := client.RepositoryAgentConfigurationReadback(context.Background(), filepath.Dir(binary), ConfigScopeGlobal, "codex", expected); err == nil {
		t.Fatal("accepted binding drift")
	}
}
