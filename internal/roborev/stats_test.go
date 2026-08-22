package roborev

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/stats"
)

const statsSummaryFixture = `{"since":"2026-08-22T00:00:00Z","repo_path":"E:\\한글 프로젝트\\repo","branch":"main","overview":{"total":0,"queued":0,"running":0,"done":0,"failed":0,"canceled":0,"applied":0,"rebased":0},"verdicts":{"total":0,"passed":0,"failed":0,"addressed":0,"pass_rate":0,"resolution_rate":0},"agents":null,"duration":{"review_p50_secs":0,"review_p90_secs":0,"review_p99_secs":0,"queue_p50_secs":0,"queue_p90_secs":0,"queue_p99_secs":0},"job_types":null,"failures":{"total":0,"retries":0,"errors":{}},"cost":{"total_usd":0,"jobs_with_cost":0,"jobs_total":0,"complete":false}}`

type statsRecordingRunner struct {
	mu              sync.Mutex
	commands        []proc.Command
	version         string
	summaryOutput   string
	costOutput      string
	stdoutTruncated bool
	stderrTruncated bool
	onSummary       func()
}

func (r *statsRecordingRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.mu.Lock()
	r.commands = append(r.commands, proc.Command{Executable: command.Executable, Args: append([]string(nil), command.Args...), Dir: command.Dir, Timeout: command.Timeout, OutputLimit: command.OutputLimit})
	r.mu.Unlock()
	if len(command.Args) > 0 && command.Args[0] == "version" {
		return proc.Result{Stdout: `{"name":"roborev","version":"` + r.version + `"}`}, nil
	}
	if len(command.Args) > 0 && command.Args[0] == "summary" {
		if r.onSummary != nil {
			r.onSummary()
		}
		output := r.summaryOutput
		if output == "" {
			output = statsSummaryFixture
		}
		return proc.Result{Stdout: output, StdoutTruncated: r.stdoutTruncated, StderrTruncated: r.stderrTruncated}, nil
	}
	output := r.costOutput
	if output == "" {
		output = `{"total_usd":0,"jobs_with_cost":0,"jobs_total":0,"complete":false}`
	}
	return proc.Result{Stdout: output, StdoutTruncated: r.stdoutTruncated, StderrTruncated: r.stderrTruncated}, nil
}

func statsHealthServer(healthy bool, version string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"healthy":` + map[bool]string{true: "true", false: "false"}[healthy] + `,"version":"` + version + `"}`))
	}))
}

func statsHealthBodyServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func (r *statsRecordingRunner) snapshot() []proc.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]proc.Command(nil), r.commands...)
}

func TestStatsArgsUseExactV064OrderAndAllowlist(t *testing.T) {
	if got, err := SummaryArgs(`E:\한글 프로젝트\repo`, "", "7d"); err != nil || !reflect.DeepEqual(got, []string{"summary", "--repo", `E:\한글 프로젝트\repo`, "--since", "7d", "--json"}) {
		t.Fatalf("summary all args=%#v err=%v", got, err)
	}
	if got, err := SummaryArgs(`E:\한글 프로젝트\repo`, "main", "30d"); err != nil || !reflect.DeepEqual(got, []string{"summary", "--repo", `E:\한글 프로젝트\repo`, "--branch", "main", "--since", "30d", "--json"}) {
		t.Fatalf("summary current args=%#v err=%v", got, err)
	}
	if got, err := CostArgs(`E:\한글 프로젝트\repo`, "main"); err != nil || !reflect.DeepEqual(got, []string{"cost", "--repo", `E:\한글 프로젝트\repo`, "--branch", "main", "--json"}) {
		t.Fatalf("cost args=%#v err=%v", got, err)
	}
	for _, window := range []string{"1 year ago", "../../secret", ""} {
		if _, err := SummaryArgs("repo", "", window); err == nil {
			t.Fatalf("window %q was accepted", window)
		}
	}
}

func TestClientStatsPreflightsAndUsesCanonicalDir(t *testing.T) {
	health := statsHealthServer(true, "v0.64.0")
	defer health.Close()
	runner := &statsRecordingRunner{version: "v0.64.0"}
	client, err := NewClient(`C:\tools\roborev.exe`, runner, health.URL)
	if err != nil {
		t.Fatal(err)
	}
	const repo = `E:\한글 프로젝트\repo`
	if _, err := client.Summary(context.Background(), repo, "main", "30d"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Cost(context.Background(), repo, ""); err != nil {
		t.Fatal(err)
	}
	commands := runner.snapshot()
	if len(commands) != 3 { // version, summary, cost; health is HTTP, not a process.
		t.Fatalf("commands=%d: %#v", len(commands), commands)
	}
	wantSummary := []string{"summary", "--repo", repo, "--branch", "main", "--since", "30d", "--json"}
	if !reflect.DeepEqual(commands[1].Args, wantSummary) || commands[1].Dir != repo || commands[1].OutputLimit != stats.SummaryOutputLimit || commands[1].Timeout.Seconds() != 15 {
		t.Fatalf("summary command=%#v", commands[1])
	}
	wantCost := []string{"cost", "--repo", repo, "--json"}
	if !reflect.DeepEqual(commands[2].Args, wantCost) || commands[2].Dir != repo || commands[2].OutputLimit != stats.CostOutputLimit {
		t.Fatalf("cost command=%#v", commands[2])
	}
}

func TestClientStatsRejectsUnsupportedAndOfflineBeforeStatsCommand(t *testing.T) {
	unsupportedHealth := statsHealthServer(true, "v0.65.0")
	defer unsupportedHealth.Close()
	unsupportedRunner := &statsRecordingRunner{version: "v0.65.0"}
	unsupported, err := NewClient("roborev", unsupportedRunner, unsupportedHealth.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unsupported.Summary(context.Background(), "repo", "", "7d"); err == nil {
		t.Fatal("unsupported version was accepted")
	}
	if commands := unsupportedRunner.snapshot(); len(commands) != 1 || commands[0].Args[0] != "version" {
		t.Fatalf("unsupported commands=%#v", commands)
	}
	offline := &statsRecordingRunner{version: "v0.64.0"}
	client, err := NewClient("roborev", offline, "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Cost(context.Background(), "repo", ""); err == nil {
		t.Fatal("offline daemon was accepted")
	}
	if _, err := client.Cost(context.Background(), "repo", ""); !errors.Is(err, ErrStatsOffline) {
		t.Fatalf("offline error=%v, want ErrStatsOffline", err)
	}
	for _, command := range offline.snapshot() {
		if len(command.Args) > 0 && command.Args[0] == "cost" {
			t.Fatal("cost command ran while daemon was offline")
		}
	}
}

func TestClientStatsPreservesCancellationAndHealthProtocolErrors(t *testing.T) {
	malformedHealth := statsHealthBodyServer(`{"healthy":true,"version":"v0.64.0","unexpected":true}`)
	defer malformedHealth.Close()
	malformedRunner := &statsRecordingRunner{version: "v0.64.0"}
	malformedClient, err := NewClient("roborev", malformedRunner, malformedHealth.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := malformedClient.Summary(context.Background(), "repo", "", "7d"); err == nil || errors.Is(err, ErrStatsOffline) {
		t.Fatalf("malformed health error=%v, wanted non-offline protocol error", err)
	}
	for _, command := range malformedRunner.snapshot() {
		if len(command.Args) > 0 && command.Args[0] == "summary" {
			t.Fatal("summary command ran after malformed health")
		}
	}
	canceledRunner := &statsRecordingRunner{version: "v0.64.0"}
	canceledClient, err := NewClient("roborev", canceledRunner, "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := canceledClient.Summary(ctx, "repo", "", "7d"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error=%v, want context.Canceled", err)
	}
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 0)
	defer deadlineCancel()
	if _, err := canceledClient.Summary(deadlineCtx, "repo", "", "7d"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error=%v, want context.DeadlineExceeded", err)
	}
}

func TestClientStatsRejectsHealthFailuresOutputBoundsBindingAndIdentity(t *testing.T) {
	cases := []struct {
		name      string
		health    bool
		healthVer string
		want      error
		wantStats bool
		mutate    func(*statsRecordingRunner, *Client)
	}{
		{name: "unhealthy daemon", health: false, healthVer: "v0.64.0", want: ErrStatsUnhealthy},
		{name: "daemon version mismatch", health: true, healthVer: "v0.65.0", want: ErrUnsupportedStats},
		{name: "stdout truncated", health: true, healthVer: "v0.64.0", want: stats.ErrMalformedSummary, wantStats: true, mutate: func(r *statsRecordingRunner, _ *Client) { r.stdoutTruncated = true }},
		{name: "stderr truncated", health: true, healthVer: "v0.64.0", want: stats.ErrMalformedSummary, wantStats: true, mutate: func(r *statsRecordingRunner, _ *Client) { r.stderrTruncated = true }},
		{name: "response repository mismatch", health: true, healthVer: "v0.64.0", want: stats.ErrMalformedSummary, wantStats: true, mutate: func(r *statsRecordingRunner, _ *Client) {
			r.summaryOutput = strings.Replace(statsSummaryFixture, `E:\\한글 프로젝트\\repo`, `E:\\other`, 1)
		}},
		{name: "response branch mismatch", health: true, healthVer: "v0.64.0", want: stats.ErrMalformedSummary, wantStats: true, mutate: func(r *statsRecordingRunner, _ *Client) {
			r.summaryOutput = strings.Replace(statsSummaryFixture, `"branch":"main"`, `"branch":"other"`, 1)
		}},
		{name: "binding changed", health: true, healthVer: "v0.64.0", want: ErrBindingChanged, wantStats: true, mutate: func(r *statsRecordingRunner, client *Client) {
			r.onSummary = func() { client.bindingMu.Lock(); client.binding.generation++; client.bindingMu.Unlock() }
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			health := statsHealthServer(test.health, test.healthVer)
			defer health.Close()
			runner := &statsRecordingRunner{version: "v0.64.0"}
			client, err := NewClient("roborev", runner, health.URL)
			if err != nil {
				t.Fatal(err)
			}
			if test.mutate != nil {
				test.mutate(runner, client)
			}
			_, err = client.Summary(context.Background(), `E:\한글 프로젝트\repo`, "main", "7d")
			if err == nil || !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
			statsCommands := 0
			for _, command := range runner.snapshot() {
				if len(command.Args) > 0 && command.Args[0] == "summary" {
					statsCommands++
				}
			}
			if test.wantStats && statsCommands != 1 {
				t.Fatalf("stats commands=%d want one", statsCommands)
			}
			if !test.wantStats && statsCommands != 0 {
				t.Fatalf("stats command ran before gate: %d", statsCommands)
			}
		})
	}
}

func TestClientInstallationClassifiesStrictHealthProbe(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		daemonURL string
		transport http.RoundTripper
		want      DaemonHealthProbeState
		running   bool
	}{
		{name: "valid reachable", status: http.StatusOK, body: `{"healthy":true}`, want: DaemonHealthOnline, running: true},
		{name: "valid unhealthy", status: http.StatusOK, body: `{"healthy":false}`, want: DaemonHealthOnline, running: true},
		{name: "HTTP error", status: http.StatusInternalServerError, body: `server error`, want: DaemonHealthInvalid},
		{name: "malformed JSON", status: http.StatusOK, body: `{`, want: DaemonHealthInvalid},
		{name: "trailing JSON", status: http.StatusOK, body: `{"healthy":true}{}`, want: DaemonHealthInvalid},
		{name: "healthy null", status: http.StatusOK, body: `{"healthy":null}`, want: DaemonHealthInvalid},
		{name: "missing healthy", status: http.StatusOK, body: `{"version":"v0.64.0"}`, want: DaemonHealthInvalid},
		{name: "unknown field", status: http.StatusOK, body: `{"healthy":true,"unexpected":true}`, want: DaemonHealthInvalid},
		{name: "invalid schema", status: http.StatusOK, body: `{"$schema":null,"healthy":true}`, want: DaemonHealthInvalid},
		{name: "oversized response", status: http.StatusOK, body: strings.Repeat("x", int(runtimeControlResponseLimit)+1), want: DaemonHealthInvalid},
		{name: "generic transport error", daemonURL: "127.0.0.1:7373", transport: runtimeErrorRoundTripper{err: errors.New("banana")}, want: DaemonHealthInvalid},
		{name: "generic timeout transport error", daemonURL: "127.0.0.1:7373", transport: runtimeErrorRoundTripper{err: genericTimeoutError{}}, want: DaemonHealthInvalid},
		{name: "wrapped deadline transport error", daemonURL: "127.0.0.1:7373", transport: runtimeErrorRoundTripper{err: fmt.Errorf("wrapped transport failure: %w", context.DeadlineExceeded)}, want: DaemonHealthInvalid},
		{name: "connection refused", daemonURL: "127.0.0.1:7373", transport: runtimeErrorRoundTripper{err: fmt.Errorf("wrapped transport failure: %w", syscall.Errno(10061))}, want: DaemonHealthOffline},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var health *httptest.Server
			daemonURL := test.daemonURL
			if daemonURL == "" {
				health = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(test.status)
					_, _ = w.Write([]byte(test.body))
				}))
				defer health.Close()
				daemonURL = health.URL
			}
			runner := &statsRecordingRunner{version: "v0.64.0"}
			client, err := NewClient("roborev", runner, daemonURL)
			if err != nil {
				t.Fatal(err)
			}
			if test.transport != nil {
				client.HTTP = &http.Client{Transport: test.transport}
			}
			installation := client.Installation(context.Background())
			if installation.DaemonHealth != test.want {
				t.Fatalf("health state=%q want %q error=%q", installation.DaemonHealth, test.want, installation.Error)
			}
			status := string(installation.Status)
			wantRunning := `"running":false`
			if test.running {
				wantRunning = `"running":true`
			}
			if !strings.Contains(status, wantRunning) {
				t.Fatalf("status=%s want substring %s", status, wantRunning)
			}
		})
	}
}

func TestClientStatsRejectsMalformedHealthBeforeEitherStatsCommand(t *testing.T) {
	for _, body := range []string{
		`{"healthy":null}`,
		`{}`,
		`[{"healthy":true}]`,
	} {
		t.Run(body, func(t *testing.T) {
			health := statsHealthBodyServer(body)
			defer health.Close()
			runner := &statsRecordingRunner{version: "v0.64.0"}
			client, err := NewClient("roborev", runner, health.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Summary(context.Background(), "repo", "", "7d"); err == nil || errors.Is(err, ErrStatsOffline) || errors.Is(err, ErrStatsUnhealthy) {
				t.Fatalf("summary error=%v, want protocol error", err)
			}
			if _, err := client.Cost(context.Background(), "repo", ""); err == nil || errors.Is(err, ErrStatsOffline) || errors.Is(err, ErrStatsUnhealthy) {
				t.Fatalf("cost error=%v, want protocol error", err)
			}
			for _, command := range runner.snapshot() {
				if len(command.Args) > 0 && (command.Args[0] == "summary" || command.Args[0] == "cost") {
					t.Fatalf("stats command ran after malformed health: %#v", command)
				}
			}
		})
	}
}

var _ StatsService = (*Client)(nil)
var _ = stats.Cost{}
