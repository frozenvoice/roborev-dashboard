package roborev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/insights"
	proc "github.com/froze/roborev-dashboard/internal/process"
)

type insightsRunner struct {
	commands []proc.Command
	output   string
	started  bool
}

func (r *insightsRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	if strings.Contains(strings.Join(command.Args, "\x00"), "version") {
		return proc.Result{Started: true, Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
	}
	return proc.Result{Started: r.started, Stdout: r.output}, nil
}

func TestInsightsLaunchUsesExactTypedArgvAndFreshPreflight(t *testing.T) {
	binary, err := os.CreateTemp(t.TempDir(), "roborev")
	if err != nil {
		t.Fatal(err)
	}
	_ = binary.Close()
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"healthy":true,"version":"v0.64.0"}`))
	}))
	defer health.Close()
	runner := &insightsRunner{output: `{"job_id":42,"agent":"codex","since":"2026-08-23T12:00:00Z"}`, started: true}
	client, err := NewClient(binary.Name(), runner, health.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := insights.Request{RepoPath: `E:\repo`, Window: insights.Window90d, BranchScope: insights.BranchCurrent, Branch: "main"}
	preflight, err := client.InsightsPreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Insights(context.Background(), request, preflight)
	if err != nil || result.Unverified || result.Receipt == nil || result.Receipt.JobID != 42 {
		t.Fatalf("launch=%#v err=%v", result, err)
	}
	var launch proc.Command
	for _, command := range runner.commands {
		if len(command.Args) > 0 && command.Args[0] == "insights" {
			launch = command
		}
	}
	if got, want := strings.Join(launch.Args, "\x00"), strings.Join([]string{"insights", "--repo", `E:\repo`, "--branch", "main", "--since", "90d", "--json"}, "\x00"); got != want {
		t.Fatalf("argv=%q want=%q", got, want)
	}
	if launch.Dir != `E:\repo` || launch.Timeout.Seconds() != 35 || launch.OutputLimit != insights.MaxReceiptBytes {
		t.Fatalf("launch command=%#v", launch)
	}
}

func TestInsightsAmbiguousLaunchIsUnverified(t *testing.T) {
	binary, err := os.CreateTemp(t.TempDir(), "roborev")
	if err != nil {
		t.Fatal(err)
	}
	_ = binary.Close()
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"healthy":true,"version":"v0.64.0"}`))
	}))
	defer health.Close()
	runner := &insightsRunner{output: "not-json", started: true}
	client, err := NewClient(binary.Name(), runner, health.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := insights.Request{RepoPath: `E:\repo`, Window: insights.Window30d, BranchScope: insights.BranchAll}
	preflight, err := client.InsightsPreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Insights(context.Background(), request, preflight)
	if err != nil || !result.Unverified || result.Receipt != nil {
		t.Fatalf("malformed launch=%#v err=%v", result, err)
	}
}

func TestInsightsLaunchWithoutStartedProcessIsUnverified(t *testing.T) {
	binary, err := os.CreateTemp(t.TempDir(), "roborev")
	if err != nil {
		t.Fatal(err)
	}
	_ = binary.Close()
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"healthy":true,"version":"v0.64.0"}`))
	}))
	defer health.Close()
	runner := &insightsRunner{output: `{"job_id":42,"agent":"codex","since":"2026-08-23T12:00:00Z"}`, started: false}
	client, err := NewClient(binary.Name(), runner, health.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := insights.Request{RepoPath: `E:\repo`, Window: insights.Window30d, BranchScope: insights.BranchAll}
	preflight, err := client.InsightsPreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Insights(context.Background(), request, preflight)
	if err != nil || !result.Unverified || result.Receipt != nil {
		t.Fatalf("not-started launch=%#v err=%v", result, err)
	}
}
