package roborev

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type readinessProbeResponse struct {
	result proc.Result
	err    error
	wait   bool
}

type readinessRunner struct {
	mu                   sync.Mutex
	commands             []proc.Command
	responses            map[string]readinessProbeResponse
	filesystemWrites     int
	daemonLifecycleCalls int
	sessionCalls         int
	version              string
	versionCalls         int
	onVersion            func(call int)
	versionErr           error
	versionResult        *proc.Result
}

func (r *readinessRunner) Run(ctx context.Context, command proc.Command) (proc.Result, error) {
	r.mu.Lock()
	copyCommand := command
	copyCommand.Args = append([]string(nil), command.Args...)
	copyCommand.Env = append([]string(nil), command.Env...)
	r.commands = append(r.commands, copyCommand)
	for _, arg := range command.Args {
		if arg == "daemon" {
			r.daemonLifecycleCalls++
		}
		if arg == "session" {
			r.sessionCalls++
		}
	}
	if containsReadinessArg(command.Args, "agent-hook") && containsReadinessArg(command.Args, "install") && !containsReadinessArg(command.Args, "--dry-run") {
		r.filesystemWrites++
	}
	if len(command.Args) == 2 && command.Args[0] == "version" && command.Args[1] == "--json" {
		r.versionCalls++
		call := r.versionCalls
		onVersion := r.onVersion
		versionErr := r.versionErr
		versionResult := r.versionResult
		version := r.version
		r.mu.Unlock()
		if onVersion != nil {
			onVersion(call)
		}
		if versionErr != nil {
			return proc.Result{Started: true, ExitCode: 1}, versionErr
		}
		if versionResult != nil {
			return *versionResult, nil
		}
		return proc.Result{Started: true, ExitCode: 0, Stdout: `{"name":"roborev","version":"` + version + `"}`}, nil
	}
	if len(command.Args) >= 6 && command.Args[0] == "agent-hook" && command.Args[1] == "install" {
		agent := command.Args[4]
		r.mu.Unlock()
		response := r.responses[agent]
		if response.wait {
			<-ctx.Done()
			return proc.Result{Started: true, ExitCode: -1}, ctx.Err()
		}
		if response.err != nil {
			return response.result, response.err
		}
		return response.result, nil
	}
	r.mu.Unlock()
	return proc.Result{Started: true, ExitCode: 0}, nil
}

func (r *readinessRunner) snapshotCommands() []proc.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	commands := make([]proc.Command, len(r.commands))
	copy(commands, r.commands)
	return commands
}

func (r *readinessRunner) sideEffectCounts() (filesystemWrites, daemonLifecycleCalls, sessionCalls int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.filesystemWrites, r.daemonLifecycleCalls, r.sessionCalls
}

func containsReadinessArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func readinessTestClient(t *testing.T, runner *readinessRunner) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "very-secret-roborev.exe")
	if err := os.WriteFile(binary, []byte("fake executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(binary, runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	return client, binary
}

func readinessOutput(display string, current bool) string {
	if current {
		return display + ` agent hooks already installed in C:\fixture\config.json` + "\r\n"
	}
	return `would update ` + display + ` agent hooks in C:\fixture\config.json` + "\n"
}

func TestAgentHookReadinessReturnsFixedOrderAndStatuses(t *testing.T) {
	runner := &readinessRunner{version: agentHookReadinessVersion, responses: map[string]readinessProbeResponse{
		"codex":  {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Codex", true)}},
		"claude": {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Claude", false)}},
		"grok":   {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Grok Build", true)}},
		"droid":  {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Factory Droid", false)}},
	}}
	client, _ := readinessTestClient(t, runner)
	got, err := client.AgentHookReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []AgentHookReadinessAgent{
		{Agent: "codex", Status: "current"},
		{Agent: "claude", Status: "needs-update"},
		{Agent: "grok", Status: "current"},
		{Agent: "droid", Status: "needs-update"},
	}
	if got.Version != agentHookReadinessVersion || len(got.Agents) != len(want) {
		t.Fatalf("readiness=%#v", got)
	}
	for i := range want {
		if got.Agents[i] != want[i] {
			t.Fatalf("agent[%d]=%#v, want %#v", i, got.Agents[i], want[i])
		}
	}
}

func TestAgentHookReadinessAllCurrentAndCodexMissingConfigNeedsUpdate(t *testing.T) {
	allCurrent := &readinessRunner{version: agentHookReadinessVersion, responses: map[string]readinessProbeResponse{}}
	for _, spec := range agentHookReadinessSpecs {
		allCurrent.responses[spec.agent] = readinessProbeResponse{result: proc.Result{Started: true, ExitCode: 0, Stdout: spec.display + ` agent hooks already installed in C:\private\all-current\hooks.json` + "\n"}}
	}
	client, _ := readinessTestClient(t, allCurrent)
	got, err := client.AgentHookReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range got.Agents {
		if agent.Status != string(AgentHookReadinessCurrent) {
			t.Fatalf("all-current agent=%#v", got.Agents)
		}
	}

	codexUpdate := &readinessRunner{version: agentHookReadinessVersion, responses: map[string]readinessProbeResponse{}}
	for _, spec := range agentHookReadinessSpecs {
		output := spec.display + ` agent hooks already installed in C:\private\configured\hooks.json` + "\n"
		if spec.agent == "codex" {
			output = `would update Codex agent hooks in C:\private\missing-config\hooks.json` + "\n"
		}
		codexUpdate.responses[spec.agent] = readinessProbeResponse{result: proc.Result{Started: true, ExitCode: 0, Stdout: output}}
	}
	client, _ = readinessTestClient(t, codexUpdate)
	got, err = client.AgentHookReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Agents[0].Status != string(AgentHookReadinessNeedsUpdate) {
		t.Fatalf("Codex missing-config status=%#v", got.Agents[0])
	}
	for _, agent := range got.Agents[1:] {
		if agent.Status != string(AgentHookReadinessCurrent) {
			t.Fatalf("configured agent=%#v", agent)
		}
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `C:\private`) {
		t.Fatal("config path escaped the sanitized DTO")
	}
}

func TestAgentHookReadinessMalformedGrokOnlyBecomesUnavailable(t *testing.T) {
	runner := &readinessRunner{version: agentHookReadinessVersion, responses: map[string]readinessProbeResponse{
		"codex":  {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Codex", true)}},
		"claude": {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Claude", true)}},
		"grok":   {result: proc.Result{Started: true, ExitCode: 0, Stdout: "unexpected Grok output\n"}},
		"droid":  {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Factory Droid", true)}},
	}}
	client, _ := readinessTestClient(t, runner)
	got, err := client.AgentHookReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []AgentHookReadinessStatus{AgentHookReadinessCurrent, AgentHookReadinessCurrent, AgentHookReadinessUnavailable, AgentHookReadinessCurrent}
	for index, agent := range got.Agents {
		if agent.Status != string(want[index]) {
			t.Fatalf("agent[%d]=%#v, want %q", index, agent, want[index])
		}
	}
}

func TestAgentHookReadinessDroidTimeoutOnlyBecomesUnavailable(t *testing.T) {
	runner := &readinessRunner{version: agentHookReadinessVersion, responses: map[string]readinessProbeResponse{
		"codex":  {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Codex", true)}},
		"claude": {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Claude", true)}},
		"grok":   {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Grok Build", true)}},
		"droid":  {result: proc.Result{Started: true, ExitCode: -1}, err: context.DeadlineExceeded},
	}}
	client, _ := readinessTestClient(t, runner)
	got, err := client.AgentHookReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for index, agent := range got.Agents {
		want := AgentHookReadinessCurrent
		if index == 3 {
			want = AgentHookReadinessUnavailable
		}
		if agent.Status != string(want) {
			t.Fatalf("agent[%d]=%#v, want %q", index, agent, want)
		}
	}
}

func TestAgentHookReadinessIsolatesProbeFailures(t *testing.T) {
	runner := &readinessRunner{version: agentHookReadinessVersion, responses: map[string]readinessProbeResponse{
		"codex":  {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Codex", true)}},
		"claude": {result: proc.Result{Started: true, ExitCode: 1}, err: errors.New("process failed")},
		"grok":   {result: proc.Result{Started: true, ExitCode: 0, Stdout: strings.Repeat("x", agentHookReadinessOutputLimit), StdoutTruncated: true}},
		"droid":  {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Factory Droid", false), Stderr: "unexpected"}},
	}}
	client, _ := readinessTestClient(t, runner)
	got, err := client.AgentHookReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range got.Agents {
		if agent.Status != string(AgentHookReadinessUnavailable) && agent.Agent != "codex" {
			// The droid output is valid but stderr makes it unavailable; only
			// codex is expected to survive this failure set.
			t.Fatalf("agent %s unexpectedly survived as %s", agent.Agent, agent.Status)
		}
	}
	if got.Agents[0].Status != string(AgentHookReadinessCurrent) || got.Agents[1].Status != string(AgentHookReadinessUnavailable) || got.Agents[2].Status != string(AgentHookReadinessUnavailable) || got.Agents[3].Status != string(AgentHookReadinessUnavailable) {
		t.Fatalf("isolated statuses=%#v", got.Agents)
	}
}

func TestAgentHookReadinessUsesExactLedgerAndSanitizedEnvironment(t *testing.T) {
	runner := &readinessRunner{version: agentHookReadinessVersion, responses: map[string]readinessProbeResponse{
		"codex":  {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Codex", true)}},
		"claude": {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Claude", true)}},
		"grok":   {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Grok Build", true)}},
		"droid":  {result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput("Factory Droid", true)}},
	}}
	client, binary := readinessTestClient(t, runner)
	t.Setenv("GIT_DIR", `C:\attacker\git`)
	t.Setenv("GIT_CONFIG_GLOBAL", `C:\attacker\config`)
	t.Setenv("ROBOREV_DAEMON_URL", `http://secret.invalid`)
	t.Setenv("CODEX_HOME", `C:\fixture\codex`)
	t.Setenv("CLAUDE_CONFIG_DIR", `C:\fixture\claude`)
	t.Setenv("GROK_HOME", `C:\fixture\grok`)
	t.Setenv("HOME", `C:\fixture\home`)
	t.Setenv("USERPROFILE", `C:\fixture\profile`)
	if _, err := client.AgentHookReadiness(context.Background()); err != nil {
		t.Fatal(err)
	}
	commands := runner.snapshotCommands()
	if len(commands) != 6 {
		t.Fatalf("command count=%d, commands=%#v", len(commands), commands)
	}
	versionCount := 0
	agentCount := 0
	for _, command := range commands {
		if command.Executable != binary || command.Dir != "" || command.AllowDetachedChildren || command.Timeout != agentHookReadinessProbeTimeout || command.OutputLimit != agentHookReadinessOutputLimit {
			t.Fatalf("unsafe command metadata=%#v", command)
		}
		if len(command.Args) == 2 && command.Args[0] == "version" {
			versionCount++
			if !equalStrings(command.Args, []string{"version", "--json"}) {
				t.Fatalf("version args=%#v", command.Args)
			}
		} else {
			agentCount++
			if len(command.Args) != 7 || command.Args[0] != "agent-hook" || command.Args[1] != "install" || command.Args[2] != "--dry-run" || command.Args[3] != "--agent" || command.Args[5] != "--binary" || command.Args[6] != binary {
				t.Fatalf("probe args=%#v", command.Args)
			}
			for _, forbidden := range []string{"--codex-config", "--claude-config", "--grok-config", "--config", "--command", "status", "dump", "reset", "run", "daemon"} {
				if containsReadinessArg(command.Args, forbidden) {
					t.Fatalf("forbidden %q in args=%#v", forbidden, command.Args)
				}
			}
		}
		for _, entry := range command.Env {
			upper := strings.ToUpper(entry)
			if strings.HasPrefix(upper, "GIT_") || strings.Contains(upper, "SECRET") || strings.HasPrefix(upper, "ROBOREV_") || strings.HasPrefix(upper, "HOMEDRIVE=") || strings.HasPrefix(upper, "HOMEPATH=") || strings.HasPrefix(upper, "APPDATA=") || strings.HasPrefix(upper, "LOCALAPPDATA=") || strings.HasPrefix(upper, "XDG_CONFIG_HOME=") {
				t.Fatalf("forbidden environment entry %q in %#v", entry, command)
			}
		}
	}
	if versionCount != 2 || agentCount != 4 {
		t.Fatalf("ledger version=%d probes=%d commands=%#v", versionCount, agentCount, commands)
	}
	filesystemWrites, daemonLifecycleCalls, sessionCalls := runner.sideEffectCounts()
	if filesystemWrites != 0 || daemonLifecycleCalls != 0 || sessionCalls != 0 {
		t.Fatalf("readiness used side effects: filesystemWrites=%d daemonLifecycleCalls=%d sessionCalls=%d", filesystemWrites, daemonLifecycleCalls, sessionCalls)
	}
}

func TestAgentHookReadinessUnsupportedVersionUsesZeroAgentHookProbes(t *testing.T) {
	runner := &readinessRunner{version: "v0.63.0", responses: map[string]readinessProbeResponse{}}
	client, _ := readinessTestClient(t, runner)
	if _, err := client.AgentHookReadiness(context.Background()); !errors.Is(err, ErrAgentHookReadinessBinding) {
		t.Fatalf("unsupported version error=%v", err)
	}
	commands := runner.snapshotCommands()
	if len(commands) != 1 || !equalStrings(commands[0].Args, []string{"version", "--json"}) {
		t.Fatalf("unsupported version ledger=%#v", commands)
	}
	if filesystemWrites, daemonLifecycleCalls, sessionCalls := runner.sideEffectCounts(); filesystemWrites != 0 || daemonLifecycleCalls != 0 || sessionCalls != 0 {
		t.Fatalf("unsupported version used side effects: filesystemWrites=%d daemonLifecycleCalls=%d sessionCalls=%d", filesystemWrites, daemonLifecycleCalls, sessionCalls)
	}
}

func TestAgentHookReadinessDoesNotDependOnDaemonAvailability(t *testing.T) {
	runner := &readinessRunner{version: agentHookReadinessVersion, responses: map[string]readinessProbeResponse{}}
	for _, spec := range agentHookReadinessSpecs {
		runner.responses[spec.agent] = readinessProbeResponse{result: proc.Result{Started: true, ExitCode: 0, Stdout: spec.display + ` agent hooks already installed in C:\private\daemon-offline\hooks.json` + "\n"}}
	}
	client, _ := readinessTestClient(t, runner)
	got, err := client.AgentHookReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range got.Agents {
		if agent.Status != string(AgentHookReadinessCurrent) {
			t.Fatalf("daemon-offline readiness=%#v", got.Agents)
		}
	}
	if filesystemWrites, daemonLifecycleCalls, sessionCalls := runner.sideEffectCounts(); filesystemWrites != 0 || daemonLifecycleCalls != 0 || sessionCalls != 0 {
		t.Fatalf("daemon-offline readiness used side effects: filesystemWrites=%d daemonLifecycleCalls=%d sessionCalls=%d", filesystemWrites, daemonLifecycleCalls, sessionCalls)
	}
}

func TestAgentHookReadinessPrivacyDiscardsCPathsAndRawOutput(t *testing.T) {
	const privatePath = `C:\very-secret\SUPER_SECRET_STDOUT\hooks.json`
	runner := &readinessRunner{version: agentHookReadinessVersion, responses: map[string]readinessProbeResponse{}}
	for _, spec := range agentHookReadinessSpecs {
		runner.responses[spec.agent] = readinessProbeResponse{result: proc.Result{Started: true, ExitCode: 0, Stdout: spec.display + ` agent hooks already installed in ` + privatePath + "\n"}}
	}
	client, binary := readinessTestClient(t, runner)
	if !strings.Contains(strings.ToLower(binary), "very-secret") {
		t.Fatalf("test binary does not carry the private fixture marker: %q", binary)
	}
	got, err := client.AgentHookReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "very-secret") || strings.Contains(string(raw), "SUPER_SECRET_STDOUT") || strings.Contains(string(raw), "hooks.json") {
		t.Fatalf("private path/raw output escaped DTO: %s", raw)
	}
}

func TestAgentHookReadinessRejectsBindingFailureAndDrift(t *testing.T) {
	runner := &readinessRunner{version: "v0.63.0", responses: map[string]readinessProbeResponse{}}
	client, _ := readinessTestClient(t, runner)
	if _, err := client.AgentHookReadiness(context.Background()); !errors.Is(err, ErrAgentHookReadinessBinding) {
		t.Fatalf("unsupported version error=%v", err)
	}

	runner = &readinessRunner{version: agentHookReadinessVersion, responses: map[string]readinessProbeResponse{}}
	for _, spec := range agentHookReadinessSpecs {
		runner.responses[spec.agent] = readinessProbeResponse{result: proc.Result{Started: true, ExitCode: 0, Stdout: readinessOutput(spec.display, true)}}
	}
	client, binary := readinessTestClient(t, runner)
	runner.onVersion = func(call int) {
		if call == 1 {
			_ = os.Chtimes(binary, time.Now().Add(time.Second), time.Now().Add(time.Second))
		}
	}
	if _, err := client.AgentHookReadiness(context.Background()); !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("binding drift error=%v", err)
	}
}

func TestAgentHookReadinessParserIsStrict(t *testing.T) {
	tests := []struct {
		name   string
		output string
		status AgentHookReadinessStatus
		valid  bool
	}{
		{"current lf", "Codex agent hooks already installed in C:\\x\\hooks.json\n", AgentHookReadinessCurrent, true},
		{"update crlf", "would update Codex agent hooks in C:\\x\\hooks.json\r\n", AgentHookReadinessNeedsUpdate, true},
		{"missing newline", "Codex agent hooks already installed in C:\\x", "", false},
		{"extra line", "Codex agent hooks already installed in C:\\x\nextra\n", "", false},
		{"wrong display", "Claude agent hooks already installed in C:\\x\n", "", false},
		{"empty path", "Codex agent hooks already installed in \n", "", false},
		{"invalid utf8", string([]byte{'C', 0xff, '\n'}), "", false},
		{"trailing line", "would update Codex agent hooks in C:\\x\ntrailing\n", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, valid := parseAgentHookReadinessOutput(test.output, "Codex")
			if valid != test.valid || status != test.status {
				t.Fatalf("parse=(%q,%v), want (%q,%v)", status, valid, test.status, test.valid)
			}
		})
	}
}

func TestReadinessProbeEnvOnlyAddsRequiredLocationVariables(t *testing.T) {
	got := readinessProbeEnv([]string{
		"PATH=C:\\Windows\\System32",
		"SystemRoot=C:\\Windows",
		"CODEX_HOME=C:\\fixture\\codex",
		"CLAUDE_CONFIG_DIR=C:\\fixture\\claude",
		"GROK_HOME=C:\\fixture\\grok",
		"HOME=C:\\fixture\\home",
		"USERPROFILE=C:\\fixture\\profile",
		"GIT_DIR=C:\\attacker",
		"TOKEN=SECRET_MARKER",
		"HOMEDRIVE=C:",
		"HOMEPATH=\\Users\\secret",
		"APPDATA=C:\\secret",
		"LOCALAPPDATA=C:\\secret",
		"XDG_CONFIG_HOME=C:\\secret",
		"ROBOREV_DAEMON_URL=http://secret",
	})
	joined := strings.Join(got, "\n")
	for _, allowed := range []string{"PATH=", "SystemRoot=", "CODEX_HOME=", "CLAUDE_CONFIG_DIR=", "GROK_HOME=", "HOME=", "USERPROFILE="} {
		if !strings.Contains(joined, allowed) {
			t.Fatalf("missing %q in %#v", allowed, got)
		}
	}
	for _, forbidden := range []string{"GIT_", "SECRET", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "ROBOREV_"} {
		if strings.Contains(strings.ToUpper(joined), strings.ToUpper(forbidden)) {
			t.Fatalf("forbidden %q in %#v", forbidden, got)
		}
	}
}
