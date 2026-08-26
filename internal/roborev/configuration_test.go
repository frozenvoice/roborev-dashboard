package roborev

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/froze/roborev-dashboard/internal/process"
)

func TestConfigSetArgsUseTheV064Contract(t *testing.T) {
	got, err := ConfigSetArgs("review_reasoning", "thorough", ConfigScopeLocal)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"config", "set", "review_reasoning", "thorough", "--local"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch\n got: %#v\nwant: %#v", got, want)
	}
	global, err := ConfigSetArgs("max_workers", "8", ConfigScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if global[len(global)-1] != "--global" {
		t.Fatalf("global set must end with --global: %#v", global)
	}
	for _, key := range []string{"", "Max Workers", "review.reasoning; rm -rf", "key\nnewline"} {
		if _, err := ConfigSetArgs(key, "x", ConfigScopeLocal); err == nil {
			t.Fatalf("invalid config key %q was accepted", key)
		}
	}
	for _, scope := range []ConfigScope{"", "merged", "GLOBAL"} {
		if _, err := ConfigSetArgs("max_workers", "1", scope); err == nil {
			t.Fatalf("invalid scope %q was accepted", scope)
		}
	}
	if _, err := ConfigSetArgs("review_reasoning", "bad\nvalue", ConfigScopeLocal); err == nil {
		t.Fatal("control character in value was accepted")
	}
}

func TestConfigGetArgsUseTheV064Contract(t *testing.T) {
	got, err := ConfigGetArgs("max_workers", ConfigScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"config", "get", "max_workers", "--global"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestConfigKeyArgsAcceptV064DottedSegments(t *testing.T) {
	setArgs, err := ConfigSetArgs("agent_hook.turn_threshold", "7", ConfigScopeGlobal)
	if err != nil {
		t.Fatalf("dotted config key rejected by set args: %v", err)
	}
	if want := []string{"config", "set", "agent_hook.turn_threshold", "7", "--global"}; !reflect.DeepEqual(setArgs, want) {
		t.Fatalf("set args mismatch\n got: %#v\nwant: %#v", setArgs, want)
	}
	getArgs, err := ConfigGetArgs("cost.timeout", ConfigScopeGlobal)
	if err != nil {
		t.Fatalf("dotted config key rejected by get args: %v", err)
	}
	if want := []string{"config", "get", "cost.timeout", "--global"}; !reflect.DeepEqual(getArgs, want) {
		t.Fatalf("get args mismatch\n got: %#v\nwant: %#v", getArgs, want)
	}
}

func TestParseConfigListAcceptsValidRowsAndPreservesValueSpacing(t *testing.T) {
	output := strings.Join([]string{
		"default  max_workers               4",
		"local    review_guidelines          Keep answers short.  Be kind.",
		"global   review_context_count      5",
		"default  review_min_severity",
		"",
	}, "\n")
	rows, err := ParseConfigList(output)
	if err != nil {
		t.Fatal(err)
	}
	want := []ConfigRow{
		{Origin: "default", Key: "max_workers", Value: "4"},
		{Origin: "local", Key: "review_guidelines", Value: "Keep answers short.  Be kind."},
		{Origin: "global", Key: "review_context_count", Value: "5"},
		{Origin: "default", Key: "review_min_severity", Value: ""},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows mismatch\n got: %#v\nwant: %#v", rows, want)
	}
}

func TestParseConfigListFailsClosed(t *testing.T) {
	cases := map[string]string{
		"unknown origin":    "machine  max_workers  4",
		"missing origin":    "max_workers  4",
		"duplicate key":     "global  max_workers  1\nlocal  max_workers  2",
		"leading dot key":   "global  .cost.timeout  10s",
		"trailing dot key":  "global  cost.timeout.  10s",
		"empty key segment": "global  cost..timeout  10s",
		"uppercase key":     "global  Cost.timeout  10s",
		"hyphenated key":    "global  cost-timeout  10s",
		"control character": "global  max_workers  1\x00",
		"oversized line":    "global  review_guidelines  " + strings.Repeat("x", 5000),
	}
	for name, output := range cases {
		if _, err := ParseConfigList(output); err == nil {
			t.Fatalf("%s: expected parse failure", name)
		}
	}
	tooManyRows := make([]string, 600)
	for i := range tooManyRows {
		tooManyRows[i] = "global  max_workers  1"
	}
	if _, err := ParseConfigList(strings.Join(tooManyRows, "\n")); err == nil {
		t.Fatal("expected row-count bound failure")
	}
}

const v064CIWorkflowConfigListFixture = `default  default_agent                 codex
default  cost.timeout                 10s
default  agent_hook.turn_threshold    5
global   agent_hook.instruction        Invoke the $roborev-fix skill now.
local    ci.include_costs              true
`

func TestConfigListAcceptsV064CIWorkflowDottedFixture(t *testing.T) {
	client, runner := newRecordingClient(t, []process.Result{{Stdout: v064CIWorkflowConfigListFixture}}, []error{nil})
	rows, err := client.ConfigList(context.Background(), `E:\repo`)
	if err != nil {
		t.Fatalf("v0.64 dotted config list fixture rejected: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %d, want version probe + config list", len(runner.calls))
	}
	want := []ConfigRow{
		{Origin: "default", Key: "default_agent", Value: "codex"},
		{Origin: "default", Key: "cost.timeout", Value: "10s"},
		{Origin: "default", Key: "agent_hook.turn_threshold", Value: "5"},
		{Origin: "global", Key: "agent_hook.instruction", Value: "Invoke the $roborev-fix skill now."},
		{Origin: "local", Key: "ci.include_costs", Value: "true"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows mismatch\n got: %#v\nwant: %#v", rows, want)
	}
}

type recordingRunner struct {
	mu          sync.Mutex
	calls       []process.Command
	configCalls int
	results     []process.Result
	errors      []error
}

const versionResponse = `{"name":"roborev","version":"v0.64.0"}`

func (r *recordingRunner) run(command process.Command) (process.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, command)
	if strings.Join(command.Args, " ") == "version --json" {
		return process.Result{Stdout: versionResponse}, nil
	}
	index := r.configCalls
	r.configCalls++
	if index < len(r.results) {
		return r.results[index], r.errors[index]
	}
	return process.Result{}, nil
}

func newRecordingClient(t *testing.T, results []process.Result, errors []error) (*Client, *recordingRunner) {
	t.Helper()
	runner := &recordingRunner{results: results, errors: errors}
	client, err := NewClient("roborev", runnerStub{runner}, "")
	if err != nil {
		t.Fatal(err)
	}
	return client, runner
}

type runnerStub struct{ inner *recordingRunner }

func (s runnerStub) Run(ctx context.Context, command process.Command) (process.Result, error) {
	return s.inner.run(command)
}

func TestConfigListRunsInRepositoryDirectoryAndParsesRows(t *testing.T) {
	client, runner := newRecordingClient(t, []process.Result{{Stdout: "default  max_workers  4\n"}}, []error{nil})
	rows, err := client.ConfigList(context.Background(), `E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %d, want version probe + config list", len(runner.calls))
	}
	command := runner.calls[1]
	wantArgs := []string{"config", "list", "--show-origin"}
	if !reflect.DeepEqual(command.Args, wantArgs) || command.Dir != `E:\repo` {
		t.Fatalf("command mismatch: %#v", command)
	}
	if len(rows) != 1 || rows[0].Key != "max_workers" || rows[0].Value != "4" || rows[0].Origin != "default" {
		t.Fatalf("rows mismatch: %#v", rows)
	}
}

type processExitError struct{}

func (e *processExitError) Error() string { return "exit status 1" }

func TestConfigGetTreatsExplicitNotSetAsEmpty(t *testing.T) {
	client, runner := newRecordingClient(t,
		[]process.Result{
			{Stderr: `Error: key "review_reasoning" is not set in local config`},
			{Stdout: "thorough\n"},
		},
		[]error{
			&processExitError{},
			nil,
		})
	value, err := client.ConfigGet(context.Background(), `E:\repo`, "review_reasoning", ConfigScopeLocal)
	if err != nil || value != "" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	value, err = client.ConfigGet(context.Background(), `E:\repo`, "review_reasoning", ConfigScopeLocal)
	if err != nil || value != "thorough" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("calls = %d, want version probe + two gets (version cached)", len(runner.calls))
	}
	wantArgs := []string{"config", "get", "review_reasoning", "--local"}
	if !reflect.DeepEqual(runner.calls[1].Args, wantArgs) || !reflect.DeepEqual(runner.calls[2].Args, wantArgs) {
		t.Fatalf("args mismatch: %#v", runner.calls[1:])
	}
}

func TestCapabilitiesGateConfigurationByVersion(t *testing.T) {
	if capabilities("v0.63.9").ConfigIntrospection || capabilities("v0.63.9").ConfigMutation {
		t.Fatal("pre-v0.64 must not expose configuration contracts")
	}
	if !capabilities("v0.64.0").ConfigIntrospection || !capabilities("v0.64.0").ConfigMutation {
		t.Fatal("v0.64 must expose the bounded configuration contracts")
	}
	if capabilities("v0.65.0").ConfigIntrospection || capabilities("v0.65.0").ConfigMutation {
		t.Fatal("configuration contracts must be gated to exact v0.64.0")
	}
}
