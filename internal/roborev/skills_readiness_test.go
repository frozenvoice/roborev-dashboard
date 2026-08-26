package roborev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type skillsReadinessRunner struct {
	mu             sync.Mutex
	commands       []proc.Command
	version        string
	versionCalls   int
	skillsCalls    int
	skillsResult   proc.Result
	skillsErr      error
	onVersion      func(int)
	rejectUnknown  bool
	waitForContext bool
}

func (r *skillsReadinessRunner) Run(ctx context.Context, command proc.Command) (proc.Result, error) {
	r.mu.Lock()
	copyCommand := command
	copyCommand.Args = append([]string(nil), command.Args...)
	copyCommand.Env = append([]string(nil), command.Env...)
	r.commands = append(r.commands, copyCommand)
	if len(command.Args) == 2 && command.Args[0] == "version" && command.Args[1] == "--json" {
		r.versionCalls++
		call := r.versionCalls
		onVersion := r.onVersion
		version := r.version
		r.mu.Unlock()
		if onVersion != nil {
			onVersion(call)
		}
		return proc.Result{Started: true, ExitCode: 0, Stdout: fmt.Sprintf(`{"name":"roborev","version":%q}`, version)}, nil
	}
	if len(command.Args) == 1 && command.Args[0] == "skills" {
		r.skillsCalls++
		result, runErr := r.skillsResult, r.skillsErr
		waitForContext := r.waitForContext
		r.mu.Unlock()
		if waitForContext {
			<-ctx.Done()
			return proc.Result{Started: true, ExitCode: -1}, ctx.Err()
		}
		return result, runErr
	}
	r.mu.Unlock()
	if r.rejectUnknown {
		return proc.Result{Started: true, ExitCode: 1}, errors.New("unexpected command")
	}
	return proc.Result{Started: true, ExitCode: 0}, nil
}

func (r *skillsReadinessRunner) snapshot() ([]proc.Command, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	commands := make([]proc.Command, len(r.commands))
	copy(commands, r.commands)
	return commands, r.versionCalls, r.skillsCalls
}

func skillsReadinessTestClient(t *testing.T, runner proc.Runner) *Client {
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
	return client
}

func skillsReadinessFixture(states map[string]map[string]string, guidance bool) string {
	var builder strings.Builder
	builder.WriteString("Skills:\n\n")
	for skillIndex, skillName := range skillsReadinessCatalog {
		fmt.Fprintf(&builder, "  %s\n", skillName)
		builder.WriteString("  opaque fixture description\n")
		for _, agent := range skillsReadinessOutputAgents {
			badge := states[agent.agent][skillName]
			if badge == "" {
				badge = "installed"
			}
			fmt.Fprintf(&builder, "    %s %-12s  %s%s\n", agent.label, "("+badge+")", agent.prefix, skillName)
		}
		if skillIndex < len(skillsReadinessCatalog)-1 {
			builder.WriteByte('\n')
		}
	}
	if guidance {
		builder.WriteString("\n")
		builder.WriteString(skillsReadinessGuidance)
		builder.WriteString("\n")
	}
	return builder.String()
}

func allInstalledSkillsReadinessFixture() string {
	return skillsReadinessFixture(nil, false)
}

func TestSkillsReadinessUsesExactSingleReadCommandAndDashboardOrder(t *testing.T) {
	t.Setenv("CODEX_HOME", `C:\Users\private\.codex`)
	t.Setenv("CLAUDE_CONFIG_DIR", `C:\Users\private\.claude`)
	t.Setenv("GROK_HOME", `C:\Users\private\.grok`)
	t.Setenv("HOME", `C:\Users\private`)
	t.Setenv("USERPROFILE", `C:\Users\private`)
	t.Setenv("GIT_DIR", `C:\attacker`)
	t.Setenv("GIT_CONFIG_GLOBAL", `C:\attacker\config`)
	t.Setenv("ROBOREV_DAEMON_URL", "http://private-daemon")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "AWS_SECRET_MARKER")
	t.Setenv("SECRET_TOKEN", "SECRET_MARKER")

	output := strings.Replace(allInstalledSkillsReadinessFixture(), "opaque fixture description", "PRIVATE_DESCRIPTION_MARKER", 1)
	runner := &skillsReadinessRunner{version: skillsReadinessVersion, skillsResult: proc.Result{Started: true, ExitCode: 0, Stdout: output}, rejectUnknown: true}
	client := skillsReadinessTestClient(t, runner)
	readiness, err := client.SkillsReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSkillsReadiness(readiness); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(readiness)
	if err != nil {
		t.Fatal(err)
	}
	jsonBody := string(encoded)
	for _, private := range []string{client.BinaryPath(), `C:\Users\private`, "PRIVATE_DESCRIPTION_MARKER", "SECRET_MARKER", "AWS_SECRET_MARKER", "http://private-daemon"} {
		if strings.Contains(jsonBody, private) {
			t.Fatalf("DTO leaked %q: %s", private, jsonBody)
		}
	}
	wantAgents := []string{"codex", "claude", "grok", "droid"}
	for index, agent := range readiness.Agents {
		if agent.Agent != wantAgents[index] || agent.Status != string(SkillsReadinessCurrent) || agent.Current != 10 || agent.Outdated != 0 || agent.Missing != 0 || agent.Total != 10 {
			t.Fatalf("agent[%d]=%+v", index, agent)
		}
	}
	commands, versionCalls, skillsCalls := runner.snapshot()
	if versionCalls != 2 || skillsCalls != 1 || len(commands) != 3 {
		t.Fatalf("commands=%d version=%d skills=%d", len(commands), versionCalls, skillsCalls)
	}
	if got := commands[0].Args; len(got) != 2 || got[0] != "version" || got[1] != "--json" {
		t.Fatalf("initial version args=%#v", got)
	}
	if got := commands[1].Args; len(got) != 1 || got[0] != "skills" {
		t.Fatalf("skills args=%#v", got)
	}
	if got := commands[2].Args; len(got) != 2 || got[0] != "version" || got[1] != "--json" {
		t.Fatalf("final version args=%#v", got)
	}
	for index, command := range commands {
		if command.Executable != client.BinaryPath() {
			t.Fatalf("command[%d] executable=%q want %q", index, command.Executable, client.BinaryPath())
		}
		if command.Dir != "" || command.AllowDetachedChildren {
			t.Fatalf("command[%d] cwd=%q detached=%v", index, command.Dir, command.AllowDetachedChildren)
		}
		if index == 1 && (command.Timeout != skillsReadinessProbeTimeout || command.OutputLimit != skillsReadinessOutputLimit) {
			t.Fatalf("skills command bounds timeout=%v output=%d", command.Timeout, command.OutputLimit)
		}
		if index != 1 && (command.Timeout != skillsReadinessProbeTimeout || command.OutputLimit != agentHookReadinessOutputLimit) {
			t.Fatalf("version command bounds timeout=%v output=%d", command.Timeout, command.OutputLimit)
		}
	}
	env := strings.Join(commands[1].Env, "\n")
	for _, required := range []string{"CODEX_HOME=C:\\Users\\private\\.codex", "CLAUDE_CONFIG_DIR=C:\\Users\\private\\.claude", "GROK_HOME=C:\\Users\\private\\.grok", "HOME=C:\\Users\\private", "USERPROFILE=C:\\Users\\private"} {
		if !strings.Contains(env, required) {
			t.Fatalf("skills env missing %q: %s", required, env)
		}
	}
	for index, command := range commands {
		commandText := strings.Join(command.Args, " ") + "\n" + strings.Join(command.Env, "\n")
		for _, forbidden := range []string{"skills install", "skills update", "agent-hook", "install-hook", "uninstall-hook", "review", "wait", "daemon", "GIT_DIR=", "GIT_CONFIG_GLOBAL=", "ROBOREV_DAEMON_URL=", "AWS_SECRET_ACCESS_KEY=", "SECRET_TOKEN="} {
			if strings.Contains(commandText, forbidden) {
				t.Fatalf("command[%d] leaked forbidden %q", index, forbidden)
			}
		}
	}
}

func TestSkillsReadinessAggregatesExactMixedStatuses(t *testing.T) {
	states := map[string]map[string]string{
		"codex":  {skillsReadinessCatalog[0]: "outdated"},
		"claude": {skillsReadinessCatalog[1]: "not installed"},
		"grok":   {},
		"droid":  {skillsReadinessCatalog[0]: "outdated", skillsReadinessCatalog[1]: "not installed"},
	}
	for _, skill := range skillsReadinessCatalog {
		for _, agent := range skillsReadinessOutputAgents {
			if states[agent.agent] == nil {
				states[agent.agent] = map[string]string{}
			}
			if _, ok := states[agent.agent][skill]; !ok {
				states[agent.agent][skill] = "installed"
			}
		}
	}
	for _, skill := range skillsReadinessCatalog {
		states["grok"][skill] = "no agent"
	}
	runner := &skillsReadinessRunner{version: skillsReadinessVersion, skillsResult: proc.Result{Started: true, ExitCode: 0, Stdout: skillsReadinessFixture(states, true)}}
	readiness, err := skillsReadinessTestClient(t, runner).SkillsReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]SkillsReadinessAgent{
		"codex":  {Agent: "codex", Status: string(SkillsReadinessNeedsInstallUpdate), Current: 9, Outdated: 1, Missing: 0, Total: 10},
		"claude": {Agent: "claude", Status: string(SkillsReadinessNeedsInstallUpdate), Current: 9, Outdated: 0, Missing: 1, Total: 10},
		"grok":   {Agent: "grok", Status: string(SkillsReadinessConfigUnavailable), Current: 0, Outdated: 0, Missing: 0, Total: 10},
		"droid":  {Agent: "droid", Status: string(SkillsReadinessNeedsInstallUpdate), Current: 8, Outdated: 1, Missing: 1, Total: 10},
	}
	for _, agent := range readiness.Agents {
		if agent != want[agent.Agent] {
			t.Errorf("agent=%+v want=%+v", agent, want[agent.Agent])
		}
	}
}

func TestSkillsReadinessParserRejectsStructuralDrift(t *testing.T) {
	valid := allInstalledSkillsReadinessFixture()
	cases := []struct {
		name string
		data string
	}{
		{name: "unknown skill", data: strings.Replace(valid, "  roborev-design-review\n", "  roborev-unknown\n", 1)},
		{name: "missing skill", data: strings.Replace(valid, "  roborev-design-review\n  opaque fixture description\n", "", 1)},
		{name: "duplicate skill", data: strings.Replace(valid, "  roborev-design-review-branch\n", "  roborev-design-review\n", 1)},
		{name: "unknown agent", data: strings.Replace(valid, "    Codex (installed)", "    Unknown (installed)", 1)},
		{name: "duplicate agent row", data: strings.Replace(valid, "    Codex (installed)   $roborev-design-review\n", "    Codex (installed)   $roborev-design-review\n    Codex (installed)   $roborev-design-review\n", 1)},
		{name: "wrong prefix", data: strings.Replace(valid, "    Codex (installed)   $roborev-design-review\n", "    Codex (installed)   /roborev-design-review\n", 1)},
		{name: "wrong invocation name", data: strings.Replace(valid, "$roborev-design-review\n", "$wrong-skill\n", 1)},
		{name: "unknown badge", data: strings.Replace(valid, "Codex (installed)", "Codex (broken)", 1)},
		{name: "unexpected status line", data: strings.Replace(valid, "  opaque fixture description\n", "  opaque fixture description\n    Codex (installed)  $unexpected\n", 1)},
		{name: "three-space description indent", data: strings.Replace(valid, "  opaque fixture description\n", "   opaque fixture description\n", 1)},
		{name: "description trailing whitespace", data: strings.Replace(valid, "  opaque fixture description\n", "  opaque fixture description \n", 1)},
		{name: "description control character", data: strings.Replace(valid, "  opaque fixture description\n", "  opaque fixture description\t\n", 1)},
		{name: "truncated", data: strings.TrimSuffix(valid, "\n")},
		{name: "invalid utf8", data: valid + string([]byte{0xff})},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseSkillsReadinessOutput(test.data); err == nil {
				t.Fatal("parser accepted malformed output")
			}
		})
	}
}

func TestSkillsReadinessRejectsMixedNoAgentAndProcessFailures(t *testing.T) {
	states := map[string]map[string]string{"codex": {skillsReadinessCatalog[0]: "no agent"}}
	validMixed := skillsReadinessFixture(states, false)
	if _, err := parseSkillsReadinessOutput(validMixed); err == nil {
		t.Fatal("mixed no-agent output accepted")
	}
	cases := []struct {
		name   string
		result proc.Result
		err    error
	}{
		{name: "stderr", result: proc.Result{Started: true, ExitCode: 0, Stdout: allInstalledSkillsReadinessFixture(), Stderr: "private marker"}},
		{name: "stderr truncated", result: proc.Result{Started: true, ExitCode: 0, Stdout: allInstalledSkillsReadinessFixture(), StderrTruncated: true}},
		{name: "nonzero", result: proc.Result{Started: true, ExitCode: 2, Stdout: allInstalledSkillsReadinessFixture()}},
		{name: "not started", result: proc.Result{Started: false, ExitCode: 0, Stdout: allInstalledSkillsReadinessFixture()}},
		{name: "stdout truncated", result: proc.Result{Started: true, ExitCode: 0, Stdout: allInstalledSkillsReadinessFixture(), StdoutTruncated: true}},
		{name: "run error", result: proc.Result{Started: true, ExitCode: -1}, err: errors.New("timeout")},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runner := &skillsReadinessRunner{version: skillsReadinessVersion, skillsResult: test.result, skillsErr: test.err}
			if _, err := skillsReadinessTestClient(t, runner).SkillsReadiness(context.Background()); err == nil {
				t.Fatal("process failure accepted")
			}
		})
	}
}

func TestSkillsReadinessRejectsUnsupportedVersionBeforeSkills(t *testing.T) {
	runner := &skillsReadinessRunner{version: "v0.63.0", skillsResult: proc.Result{Started: true, ExitCode: 0, Stdout: allInstalledSkillsReadinessFixture()}}
	_, err := skillsReadinessTestClient(t, runner).SkillsReadiness(context.Background())
	if err == nil {
		t.Fatal("unsupported version accepted")
	}
	_, _, skillsCalls := runner.snapshot()
	if skillsCalls != 0 {
		t.Fatalf("skills calls=%d", skillsCalls)
	}
}

func TestSkillsReadinessRejectsBindingDriftAfterSkillsRead(t *testing.T) {
	runner := &skillsReadinessRunner{version: skillsReadinessVersion, skillsResult: proc.Result{Started: true, ExitCode: 0, Stdout: allInstalledSkillsReadinessFixture()}}
	client := skillsReadinessTestClient(t, runner)
	runner.onVersion = func(call int) {
		if call == 1 {
			client.bindingMu.Lock()
			client.binding.generation++
			client.bindingMu.Unlock()
		}
	}
	_, err := client.SkillsReadiness(context.Background())
	if !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("err=%v, want binding drift", err)
	}
}

func TestSkillsReadinessPreservesOpaqueDescriptionWithoutReturningIt(t *testing.T) {
	privateMarker := "PRIVATE_DESCRIPTION_MARKER"
	output := strings.Replace(allInstalledSkillsReadinessFixture(), "opaque fixture description", privateMarker, 1)
	runner := &skillsReadinessRunner{version: skillsReadinessVersion, skillsResult: proc.Result{Started: true, ExitCode: 0, Stdout: output}}
	got, err := skillsReadinessTestClient(t, runner).SkillsReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%+v", got), privateMarker) {
		t.Fatalf("description crossed DTO boundary: %+v", got)
	}
}

func TestValidateSkillsReadinessInvariants(t *testing.T) {
	valid := SkillsReadiness{Version: skillsReadinessVersion, Agents: []SkillsReadinessAgent{
		{Agent: "codex", Status: string(SkillsReadinessCurrent), Current: 10, Total: 10},
		{Agent: "claude", Status: string(SkillsReadinessNeedsInstallUpdate), Current: 8, Outdated: 1, Missing: 1, Total: 10},
		{Agent: "grok", Status: string(SkillsReadinessConfigUnavailable), Total: 10},
		{Agent: "droid", Status: string(SkillsReadinessCurrent), Current: 10, Total: 10},
	}}
	if err := ValidateSkillsReadiness(valid); err != nil {
		t.Fatal(err)
	}
	cases := []SkillsReadiness{
		{Version: "v0.63.0", Agents: valid.Agents},
		{Version: skillsReadinessVersion, Agents: valid.Agents[:3]},
		{Version: skillsReadinessVersion, Agents: append([]SkillsReadinessAgent(nil), valid.Agents...)},
	}
	cases[2].Agents[0].Agent = "unknown"
	for _, candidate := range cases {
		if err := ValidateSkillsReadiness(candidate); err == nil {
			t.Fatalf("invalid DTO accepted: %+v", candidate)
		}
	}
	invalidCounts := valid
	invalidCounts.Agents = append([]SkillsReadinessAgent(nil), valid.Agents...)
	invalidCounts.Agents[0].Current = 9
	if err := ValidateSkillsReadiness(invalidCounts); err == nil {
		t.Fatal("inconsistent current counts accepted")
	}
	invalidCounts = valid
	invalidCounts.Agents = append([]SkillsReadinessAgent(nil), valid.Agents...)
	invalidCounts.Agents[1].Current = 10
	if err := ValidateSkillsReadiness(invalidCounts); err == nil {
		t.Fatal("inconsistent mixed counts accepted")
	}
	invalidCounts = valid
	invalidCounts.Agents = append([]SkillsReadinessAgent(nil), valid.Agents...)
	invalidCounts.Agents[1].Current = int(^uint(0) >> 1)
	if err := ValidateSkillsReadiness(invalidCounts); err == nil {
		t.Fatal("huge current count accepted")
	}
}

func TestSkillsReadinessPropagatesParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &skillsReadinessRunner{version: skillsReadinessVersion}
	_, err := skillsReadinessTestClient(t, runner).SkillsReadiness(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestSkillsReadinessOutputLimitAndTimeoutAreBounded(t *testing.T) {
	runner := &skillsReadinessRunner{version: skillsReadinessVersion, skillsResult: proc.Result{Started: true, ExitCode: 0, Stdout: allInstalledSkillsReadinessFixture(), StdoutTruncated: true}}
	client := skillsReadinessTestClient(t, runner)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, _ = client.SkillsReadiness(ctx)
	_, _, skillsCalls := runner.snapshot()
	if skillsCalls > 1 {
		t.Fatalf("skills calls=%d", skillsCalls)
	}
}

func TestSkillsReadinessRealRunnerCancellationIsReportedAsTimeout(t *testing.T) {
	runner := &skillsReadinessRunner{version: skillsReadinessVersion, waitForContext: true}
	client := skillsReadinessTestClient(t, runner)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := client.SkillsReadiness(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want deadline exceeded", err)
	}
	_, _, skillsCalls := runner.snapshot()
	if skillsCalls != 1 {
		t.Fatalf("skills calls=%d, want one canceled skills process", skillsCalls)
	}
}
