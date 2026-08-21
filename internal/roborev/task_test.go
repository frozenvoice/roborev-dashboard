package roborev

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

func TestTaskArgsIsTheExactCodexOnlyContract(t *testing.T) {
	got, err := TaskArgs("codex")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"run", "--agent", "codex", "--json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%#v want=%#v", got, want)
	}
	forbidden := []string{"--agentic", "--yolo", "--wait", "--quiet", "--model", "--reasoning", "--no-context", "--label"}
	for _, arg := range got {
		for _, flag := range forbidden {
			if arg == flag {
				t.Fatalf("forbidden flag %q appeared in exact task args %#v", flag, got)
			}
		}
	}
	for _, agent := range []string{"", "claude", "grok", "codex --agentic", "$(whoami)"} {
		if _, err := TaskArgs(agent); !errors.Is(err, ErrTaskAgentNotAllowed) {
			t.Fatalf("agent %q accepted: %v", agent, err)
		}
	}
}

func TestValidateTaskTextPreservesPromptCharactersAndBoundsBytes(t *testing.T) {
	for _, value := range []string{
		"한글\nquotes \" ' & | ; $(literal) `literal`\tmarkdown",
		"  inspect this task exactly  \n",
	} {
		if err := ValidateTaskText(value); err != nil {
			t.Fatalf("task rejected: %v", err)
		}
	}
	for _, value := range []string{"", " \t\r\n", "bad\x00task", "bad\x01task", strings.Repeat("x", TaskMaxBytes+1)} {
		if err := ValidateTaskText(value); err == nil {
			t.Fatalf("invalid task accepted: %q", value)
		}
	}
}

func TestDecodeTaskReceiptStrictCreatedSkippedAndInvalidShapes(t *testing.T) {
	created := []byte(`{"job_id":42,"job_uuid":"123e4567-e89b-12d3-a456-426614174000","git_ref":"run","status":"queued"}`)
	receipt, err := DecodeTaskReceipt(created)
	if err != nil || !receipt.Created() || receipt.JobID != 42 {
		t.Fatalf("created receipt=%#v err=%v", receipt, err)
	}
	skipped, err := DecodeTaskReceipt([]byte(`{"skipped":true,"reason":"already queued"}`))
	if err != nil || !skipped.Skipped || skipped.Reason != "already queued" {
		t.Fatalf("skipped receipt=%#v err=%v", skipped, err)
	}
	invalid := []string{
		"",
		`{"job_id":42,"job_uuid":"123e4567-e89b-12d3-a456-426614174000","git_ref":"run","status":"queued"} {}`,
		`{"job_id":0,"job_uuid":"123e4567-e89b-12d3-a456-426614174000","git_ref":"run","status":"queued"}`,
		`{"job_id":42,"job_uuid":"not-a-uuid","git_ref":"run","status":"queued"}`,
		`{"job_id":42,"job_uuid":"123e4567-e89b-12d3-a456-426614174000","git_ref":"HEAD","status":"queued"}`,
		`{"job_id":42,"job_uuid":"123e4567-e89b-12d3-a456-426614174000","git_ref":"run","status":"running"}`,
		`{"job_id":42,"job_id":43,"job_uuid":"123e4567-e89b-12d3-a456-426614174000","git_ref":"run","status":"queued"}`,
		`{"skipped":true,"reason":"ok","unknown":1}`,
		`{"skipped":false,"reason":"no"}`,
	}
	for _, raw := range invalid {
		if _, err := DecodeTaskReceipt([]byte(raw)); err == nil {
			t.Fatalf("invalid receipt accepted: %q", raw)
		}
	}
	if _, err := DecodeTaskReceipt([]byte(strings.Repeat("x", TaskOutputLimit+1))); err == nil {
		t.Fatal("oversized receipt accepted")
	}
}

func TestLoadTaskSafetyUsesBoundedGlobalConfigAndSafeDefaults(t *testing.T) {
	missing, err := LoadTaskSafety(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || !missing.Safe() {
		t.Fatalf("missing config safety=%#v err=%v", missing, err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("allow_unsafe_agents = true\ndisable_codex_sandbox = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsafe, err := LoadTaskSafety(path)
	if err != nil || unsafe.Safe() || !unsafe.AllowUnsafeAgents || unsafe.DisableCodexSandbox {
		t.Fatalf("unsafe config safety=%#v err=%v", unsafe, err)
	}
	if err := os.WriteFile(path, []byte("allow_unsafe_agents = false\ndisable_codex_sandbox = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	explicitSafe, err := LoadTaskSafety(path)
	if err != nil || !explicitSafe.Safe() || explicitSafe.AllowUnsafeAgents || explicitSafe.DisableCodexSandbox || !explicitSafe.Known {
		t.Fatalf("explicit safe config safety=%#v err=%v", explicitSafe, err)
	}
	if err := os.WriteFile(path, []byte("allow_unsafe_agents = \"true\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTaskSafety(path); err == nil {
		t.Fatal("malformed safety config accepted")
	}
}

type taskCaptureRunner struct {
	mu            sync.Mutex
	commands      []proc.Command
	result        proc.Result
	err           error
	versionOutput string
}

func (r *taskCaptureRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	if command.Args[0] == "version" {
		if r.versionOutput != "" {
			return proc.Result{Stdout: r.versionOutput}, nil
		}
		return proc.Result{Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
	}
	if r.err != nil && !r.result.Started {
		r.result.Started = true
	}
	if r.result.Stdout != "" || r.result.StdoutTruncated || r.err != nil {
		return r.result, r.err
	}
	return proc.Result{Stdout: `{"job_id":42,"job_uuid":"123e4567-e89b-12d3-a456-426614174000","git_ref":"run","status":"queued"}`}, nil
}

func TestFreshTaskVersionRejectsUnknownJSONFields(t *testing.T) {
	runner := &taskCaptureRunner{versionOutput: `{"name":"roborev","version":"v0.64.0","unexpected":"value"}`}
	client, err := NewClient("roborev", runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.freshTaskVersion(context.Background(), "roborev"); err == nil {
		t.Fatal("version response with unknown field was accepted")
	}
}

func taskHealthServer(t *testing.T, version string, healthy bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"healthy":` + map[bool]string{true: "true", false: "false"}[healthy] + `,"version":"` + version + `"}`))
	}))
}

func TestRunTaskFreshPreflightAndExactStdinCommand(t *testing.T) {
	server := taskHealthServer(t, "v0.64.0", true)
	defer server.Close()
	runner := &taskCaptureRunner{}
	client, err := NewClient(`C:\trusted\roborev.exe`, runner, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	client.TaskConfigPath = config
	task := "한글\nquotes \" ' & | ; $(literal)"
	receipt, err := client.RunTask(context.Background(), TaskRequest{RepoPath: `E:\registered repo`, Task: task, Agent: "codex"})
	if err != nil || receipt.JobID != 42 {
		t.Fatalf("receipt=%#v err=%v", receipt, err)
	}
	runner.mu.Lock()
	commands := append([]proc.Command(nil), runner.commands...)
	runner.mu.Unlock()
	if len(commands) != 2 {
		t.Fatalf("commands=%#v", commands)
	}
	command := commands[1]
	wantArgs := []string{"run", "--agent", "codex", "--json"}
	if command.Executable != `C:\trusted\roborev.exe` || command.Dir != `E:\registered repo` || !reflect.DeepEqual(command.Args, wantArgs) || command.Stdin != task || command.StdinLimit != TaskMaxBytes || command.Timeout != TaskTimeout || command.OutputLimit != TaskOutputLimit {
		t.Fatalf("task command=%#v", command)
	}
}

func TestRunTaskProcessFailureIsTypedAmbiguous(t *testing.T) {
	server := taskHealthServer(t, "v0.64.0", true)
	defer server.Close()
	runner := &taskCaptureRunner{err: errors.New("child failed")}
	client, err := NewClient("roborev", runner, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.TaskConfigPath = filepath.Join(t.TempDir(), "missing.toml")
	_, err = client.RunTask(context.Background(), TaskRequest{RepoPath: "repo", Task: "inspect", Agent: "codex"})
	if err == nil || !errors.Is(err, ErrTaskEnqueueAmbiguous) {
		t.Fatalf("error=%v, want typed ambiguous", err)
	}
	var typed *TaskError
	if !errors.As(err, &typed) || typed.Kind != TaskErrorAmbiguous {
		t.Fatalf("error type=%#v", err)
	}
}

type taskStartFailureRunner struct{}

func (taskStartFailureRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	if command.Args[0] == "version" {
		return proc.Result{Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
	}
	return proc.Result{}, errors.New("process could not start")
}

func TestRunTaskBeforeStartFailureIsNotAmbiguous(t *testing.T) {
	server := taskHealthServer(t, "v0.64.0", true)
	defer server.Close()
	client, err := NewClient("roborev", taskStartFailureRunner{}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.TaskConfigPath = filepath.Join(t.TempDir(), "missing.toml")
	_, err = client.RunTask(context.Background(), TaskRequest{RepoPath: "repo", Task: "inspect", Agent: "codex"})
	if err == nil || !errors.Is(err, ErrTaskProcessStart) || errors.Is(err, ErrTaskEnqueueAmbiguous) {
		t.Fatalf("error=%v, want definite pre-start failure", err)
	}
}

func TestTaskPreflightRejectsUnsafeAndUnhealthyDaemon(t *testing.T) {
	for _, test := range []struct {
		name    string
		config  string
		healthy bool
		want    error
	}{
		{name: "unsafe", config: "allow_unsafe_agents = true\n", healthy: true, want: ErrTaskUnsafeConfig},
		{name: "sandbox disabled", config: "disable_codex_sandbox = true\n", healthy: true, want: ErrTaskUnsafeConfig},
		{name: "unhealthy", config: "", healthy: false, want: ErrDaemonUnhealthy},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := taskHealthServer(t, "v0.64.0", test.healthy)
			defer server.Close()
			runner := &taskCaptureRunner{}
			client, err := NewClient("roborev", runner, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(test.config), 0o600); err != nil {
				t.Fatal(err)
			}
			client.TaskConfigPath = path
			_, err = client.TaskPreflight(context.Background())
			if err == nil || !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
			if len(runner.commands) != 1 {
				t.Fatalf("task command launched during failed preflight: %#v", runner.commands)
			}
		})
	}
}
