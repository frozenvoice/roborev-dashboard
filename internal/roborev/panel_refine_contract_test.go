package roborev

import (
	"context"
	"encoding/json"
	"errors"
	proc "github.com/froze/roborev-dashboard/internal/process"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDecodePanelEnqueueResponseRequiresExactSynthesisShape(t *testing.T) {
	request := ReviewRequest{RepoPath: `C:\repo`, GitRef: "head-1", Branch: "main", Panel: "dashboard_cross"}
	raw := json.RawMessage(`{"id":42,"repo_path":"C:\\repo","git_ref":"head-1","branch":"main","panel_name":"dashboard_cross","panel_run_uuid":"00000000-0000-0000-0000-000000000042","panel_role":"synthesis","job_type":"synthesis"}`)
	receipt, err := DecodePanelEnqueueResponse(raw, request)
	if err != nil || receipt.ID != 42 {
		t.Fatalf("receipt = %#v, err = %v", receipt, err)
	}
	for _, malformed := range []string{
		`{"id":42,"repo_path":"C:\\repo","git_ref":"head-1","branch":"main","panel_name":"dashboard_cross","panel_run_uuid":"u","panel_role":"member","job_type":"synthesis"}`,
		`{"id":42,"repo_path":"C:\\repo","git_ref":"head-1","branch":"main","panel_name":"dashboard_cross","panel_run_uuid":"u","panel_role":"synthesis","job_type":"panel_synthesis"}`,
		`{"id":42,"repo_path":"C:\\repo","git_ref":"head-1","branch":"main","panel_name":"dashboard_cross","panel_run_uuid":"u","panel_role":"synthesis","job_type":"synthesis","skipped":true}`,
	} {
		if _, err := DecodePanelEnqueueResponse(json.RawMessage(malformed), request); err == nil {
			t.Fatalf("malformed receipt accepted: %s", malformed)
		}
	}
}

func TestReviewPanelPostsPinnedTargetAndDecodesReceipt(t *testing.T) {
	request := ReviewRequest{RepoPath: `C:\repo with spaces`, GitRef: "head-1", Branch: "feature/refine", Panel: "dashboard_cross"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = w.Write([]byte(`{"healthy":true}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/enqueue" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["repo_path"] != request.RepoPath || body["git_ref"] != request.GitRef || body["branch"] != request.Branch || body["panel"] != request.Panel {
			t.Fatalf("request body = %#v", body)
		}
		_, _ = w.Write([]byte(`{"id":42,"repo_path":"C:\\repo with spaces","git_ref":"head-1","branch":"feature/refine","panel_name":"dashboard_cross","panel_run_uuid":"00000000-0000-0000-0000-000000000042","panel_role":"synthesis","job_type":"synthesis"}`))
	}))
	defer server.Close()
	client := newMutationClient(t, "v0.64.0", server.URL)
	receipt, err := client.ReviewPanel(context.Background(), request)
	if err != nil || receipt.ID != 42 || receipt.GitRef != request.GitRef {
		t.Fatalf("receipt = %#v, err = %v", receipt, err)
	}
}

func TestSynthesisStatusUsesExactIDEndpointAndExpandedMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = w.Write([]byte(`{"healthy":true}`))
			return
		}
		if r.URL.Path != "/api/jobs" || r.URL.Query().Get("id") != "42" || r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("omit_prompt") != "true" {
			t.Fatalf("unexpected status query: %s", r.URL.String())
		}
		_, _ = w.Write([]byte(`{"jobs":[{"id":42,"repo_path":"C:\\repo","git_ref":"head-1","branch":"main","panel_name":"dashboard_cross","panel_run_uuid":"00000000-0000-0000-0000-000000000042","panel_role":"synthesis","job_type":"synthesis","status":"done","verdict":"P"}]}`))
	}))
	defer server.Close()
	client := newMutationClient(t, "v0.64.0", server.URL)
	status, err := client.SynthesisStatus(context.Background(), 42)
	if err != nil || status.ID != 42 || status.JobType != "synthesis" || status.Verdict != "P" {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}

type fixCaptureRunner struct {
	commands []proc.Command
	fail     bool
}

type blockingFixRunner struct {
	started  chan struct{}
	observed chan struct{}
	command  proc.Command
}

func (r *blockingFixRunner) Run(ctx context.Context, command proc.Command) (proc.Result, error) {
	if command.Args[0] == "version" {
		return proc.Result{Stdout: `{"version":"v0.64.0"}`}, nil
	}
	r.command = command
	close(r.started)
	<-ctx.Done()
	close(r.observed)
	return proc.Result{}, ctx.Err()
}

func (r *fixCaptureRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	if command.Args[0] == "version" {
		return proc.Result{Stdout: `{"version":"v0.64.0"}`}, nil
	}
	if r.fail {
		return proc.Result{Stderr: "secret finding and token"}, errors.New("exit status 7")
	}
	return proc.Result{}, nil
}

func TestFixUsesConfiguredExecutableDirExactArgsAndSanitizedError(t *testing.T) {
	runner := &fixCaptureRunner{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = w.Write([]byte(`{"healthy":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client, err := NewClient(`C:\tools\roborev.exe`, runner, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Fix(context.Background(), FixRequest{RepoPath: `C:\repo with spaces`, JobID: 42, Agent: "codex"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	command := runner.commands[1]
	if command.Executable != `C:\tools\roborev.exe` || command.Dir != `C:\repo with spaces` || len(command.Args) != 5 || command.Args[0] != "fix" || command.Args[1] != "--agent" || command.Args[2] != "codex" || command.Args[3] != "--quiet" || command.Args[4] != "42" {
		t.Fatalf("fix command = %#v", command)
	}
	runner.fail = true
	if err := client.Fix(context.Background(), FixRequest{RepoPath: `C:\repo`, JobID: 42, Agent: "codex"}); err == nil || err.Error() != "RoboRev fix command failed" {
		t.Fatalf("fix error = %v", err)
	}
}

func TestFixCapabilityGateRunsNoFixProcess(t *testing.T) {
	runner := &fixCaptureRunner{}
	client, err := NewClient("roborev", runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	runner.commands = nil
	client.version = "v0.63.9"
	client.versionAt = time.Now()
	if err := client.Fix(context.Background(), FixRequest{RepoPath: `C:\repo`, JobID: 1, Agent: "codex"}); err == nil {
		t.Fatal("unsupported fix capability was accepted")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("fix process was started: %#v", runner.commands)
	}
}

func TestFixPropagatesCancellationToRunnerAndUsesTimeout(t *testing.T) {
	runner := &blockingFixRunner{started: make(chan struct{}), observed: make(chan struct{})}
	client, err := NewClient("roborev", runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.Fix(ctx, FixRequest{RepoPath: `C:\repo`, JobID: 42, Agent: "codex"})
	}()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("fix runner did not start")
	}
	cancel()
	select {
	case <-runner.observed:
	case <-time.After(time.Second):
		t.Fatal("fix runner did not observe cancellation")
	}
	if err := <-done; err == nil || err.Error() != "RoboRev fix command failed" {
		t.Fatalf("fix cancellation error = %v", err)
	}
	if runner.command.Timeout != 45*time.Minute {
		t.Fatalf("fix timeout = %s, want 45m", runner.command.Timeout)
	}
}
