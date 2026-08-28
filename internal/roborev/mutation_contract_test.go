package roborev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type versionRunner struct {
	version string
}

func (r versionRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	if command.Executable != "roborev" || len(command.Args) != 2 || command.Args[0] != "version" || command.Args[1] != "--json" {
		return proc.Result{}, errors.New("unexpected native command")
	}
	data, _ := json.Marshal(map[string]string{"name": "roborev", "version": r.version})
	return proc.Result{Stdout: string(data)}, nil
}

func newMutationClient(t *testing.T, version, daemonURL string) *Client {
	t.Helper()
	client, err := NewClient("roborev", versionRunner{version: version}, daemonURL)
	if err != nil {
		t.Fatal(err)
	}
	client.versionMu.Lock()
	_, _, generation := client.snapshot()
	client.version = version
	client.versionGeneration = generation
	client.versionAt = time.Now()
	client.versionMu.Unlock()
	return client
}

func TestReviewPostsPinnedTargetToEnqueue(t *testing.T) {
	var method, path string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true,"version":"v0.64.0"}`))
		case "/api/enqueue":
			method, path = r.Method, r.URL.Path
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"job_id":49}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newMutationClient(t, "v0.64.0", server.URL)
	request := ReviewRequest{RepoPath: `E:\한글 project\repo`, GitRef: "pinned-sha-123", Branch: "feature/review", Panel: "dashboard_cross"}
	receipt, err := client.Review(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/api/enqueue" {
		t.Fatalf("request = %s %s", method, path)
	}
	want := map[string]string{
		"repo_path": request.RepoPath,
		"git_ref":   request.GitRef,
		"branch":    request.Branch,
		"panel":     request.Panel,
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("body[%q] = %#v, want %q", key, body[key], value)
		}
	}
	if string(receipt) != `{"job_id":49}` {
		t.Fatalf("receipt = %s", receipt)
	}
}

func TestRerunPostsFrozenJobID(t *testing.T) {
	var body map[string]int64
	var method, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = w.Write([]byte(`{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true,"version":"v0.64.0"}`))
			return
		}
		method, path = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	client := newMutationClient(t, "v0.64.0", server.URL)
	if err := client.Rerun(context.Background(), 49); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/api/job/rerun" || body["job_id"] != 49 {
		t.Fatalf("rerun request = %s %s %#v", method, path, body)
	}
}

func TestUnsupportedVersionDoesNotCallDaemonMutationEndpoints(t *testing.T) {
	mutationCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutationCalls++
		http.Error(w, "must not be called", http.StatusInternalServerError)
	}))
	defer server.Close()
	client := newMutationClient(t, "v0.63.9", server.URL)
	if _, err := client.Review(context.Background(), ReviewRequest{RepoPath: "repo", GitRef: "sha", Panel: "dashboard_cross"}); err == nil {
		t.Fatal("expected unsupported review error")
	}
	if err := client.Rerun(context.Background(), 49); err == nil {
		t.Fatal("expected unsupported rerun error")
	}
	if mutationCalls != 0 {
		t.Fatalf("unsupported version made %d daemon calls", mutationCalls)
	}
}

func TestReviewRejectsMalformedAndNon2xxDaemonResponses(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		response    string
		wantInError string
	}{
		{name: "malformed", status: http.StatusCreated, response: "not-json", wantInError: "malformed JSON"},
		{name: "non-2xx", status: http.StatusServiceUnavailable, response: `{"error":"offline"}`, wantInError: "returned 503"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/health" {
					_, _ = w.Write([]byte(`{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true,"version":"v0.64.0"}`))
					return
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			client := newMutationClient(t, "v0.64.0", server.URL)
			_, err := client.Review(context.Background(), ReviewRequest{RepoPath: "repo", GitRef: "sha", Panel: "dashboard_cross"})
			if err == nil || !strings.Contains(err.Error(), test.wantInError) {
				t.Fatalf("error = %v, want containing %q", err, test.wantInError)
			}
		})
	}
}
