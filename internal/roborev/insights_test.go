package roborev

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/froze/roborev-dashboard/internal/insights"
	proc "github.com/froze/roborev-dashboard/internal/process"
)

type insightsRunner struct {
	mu       sync.Mutex
	commands []proc.Command
}

func (r *insightsRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	if len(command.Args) > 0 && command.Args[0] == "version" {
		return proc.Result{Started: true, Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
	}
	return proc.Result{}, nil
}

func makeInsightsClient(t *testing.T, handler http.Handler) (*Client, *insightsRunner, func()) {
	t.Helper()
	binary, err := os.CreateTemp(t.TempDir(), "roborev")
	if err != nil {
		t.Fatal(err)
	}
	_ = binary.Close()
	server := httptest.NewServer(handler)
	runner := &insightsRunner{}
	client, err := NewClient(binary.Name(), runner, server.URL)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return client, runner, server.Close
}

func TestInsightsLaunchUsesStrictDaemonHTTPAndExactBody(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var method string
	var body map[string]any
	client, runner, closeServer := makeInsightsClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		method = r.Method
		if r.URL.Path == "/api/enqueue" {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"healthy":true,"version":"v0.64.0"}`))
		case "/api/enqueue":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":42,"agent":"codex","job_type":"insights","repo_path":"E:\\repo","branch":"main","prompt":"PRIVATE","error":"PRIVATE"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer closeServer()
	request := insights.Request{RepoPath: `E:\repo`, Window: insights.Window90d, BranchScope: insights.BranchCurrent, Branch: "main"}
	preflight, err := client.InsightsPreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Insights(context.Background(), request, preflight)
	if err != nil || result.Unverified || result.Receipt == nil || result.Receipt.JobID != 42 {
		t.Fatalf("launch=%#v err=%v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if method != http.MethodPost || len(paths) != 3 || paths[0] != "/api/health" || paths[1] != "/api/health" || !strings.HasPrefix(paths[2], "/api/enqueue?") && paths[2] != "/api/enqueue" {
		t.Fatalf("requests=%#v method=%s", paths, method)
	}
	if body["repo_path"] != `E:\repo` || body["git_ref"] != "insights" || body["branch"] != "main" || body["job_type"] != "insights" {
		t.Fatalf("request body=%#v", body)
	}
	if _, ok := body["prompt"]; ok {
		t.Fatal("request body unexpectedly included prompt")
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.commands) != 2 || runner.commands[0].Args[0] != "version" || runner.commands[1].Args[0] != "version" {
		t.Fatalf("runner commands=%#v; insights CLI must not run", runner.commands)
	}
}

func TestInsightsAcceptsExactSkippedResponse(t *testing.T) {
	client, _, closeServer := makeInsightsClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/health" {
			_, _ = w.Write([]byte(`{"healthy":true,"version":"v0.64.0"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"skipped":true,"reason":"no failing reviews"}`))
	}))
	defer closeServer()
	request := insights.Request{RepoPath: `E:\repo`, Window: insights.Window30d, BranchScope: insights.BranchAll}
	preflight, err := client.InsightsPreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Insights(context.Background(), request, preflight)
	if err != nil || result.Unverified || result.Receipt == nil || !result.Receipt.Skipped {
		t.Fatalf("skipped launch=%#v err=%v", result, err)
	}
}

func TestInsightsAmbiguousHTTPOutcomeIsUnverifiedWithoutRetry(t *testing.T) {
	var enqueues int
	client, _, closeServer := makeInsightsClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = w.Write([]byte(`{"healthy":true,"version":"v0.64.0"}`))
			return
		}
		enqueues++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`PRIVATE daemon error`))
	}))
	defer closeServer()
	request := insights.Request{RepoPath: `E:\repo`, Window: insights.Window30d, BranchScope: insights.BranchAll}
	preflight, err := client.InsightsPreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Insights(context.Background(), request, preflight)
	if err != nil || !result.Unverified || result.Receipt != nil || strings.Contains(result.Message, "PRIVATE") || enqueues != 1 {
		t.Fatalf("ambiguous launch=%#v err=%v enqueues=%d", result, err, enqueues)
	}
}

func TestInsightsRedirectIsNotFollowed(t *testing.T) {
	var enqueueCount, destinationCount int
	client, _, closeServer := makeInsightsClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = w.Write([]byte(`{"healthy":true,"version":"v0.64.0"}`))
			return
		}
		if r.URL.Path == "/api/enqueue" {
			enqueueCount++
			http.Redirect(w, r, "/destination", http.StatusTemporaryRedirect)
			return
		}
		destinationCount++
	}))
	defer closeServer()
	request := insights.Request{RepoPath: `E:\repo`, Window: insights.Window30d, BranchScope: insights.BranchAll}
	preflight, err := client.InsightsPreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Insights(context.Background(), request, preflight)
	if err == nil || result.Unverified || enqueueCount != 1 || destinationCount != 0 {
		t.Fatalf("redirect launch=%#v err=%v counts=%d/%d", result, err, enqueueCount, destinationCount)
	}
}

func TestInsightsAcceptedResponseRequiresSafeReceipt(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   string
		header string
	}{
		{name: "malformed JSON", body: `{"id":42,"agent":"codex"`},
		{name: "duplicate key", body: `{"id":42,"id":43,"agent":"codex"}`},
		{name: "private trailing data", body: `{"id":42,"agent":"codex"} PRIVATE_MARKER`},
		{name: "oversized", body: strings.Repeat("x", insights.MaxReceiptBytes+1)},
		{name: "wrong media type", body: `PRIVATE_MARKER`, header: "text/plain"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, _, closeServer := makeInsightsClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/health" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"healthy":true,"version":"v0.64.0"}`))
					return
				}
				if test.header == "" {
					w.Header().Set("Content-Type", "application/json")
				} else {
					w.Header().Set("Content-Type", test.header)
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(test.body))
			}))
			defer closeServer()
			request := insights.Request{RepoPath: `E:\repo`, Window: insights.Window30d, BranchScope: insights.BranchAll}
			preflight, err := client.InsightsPreflight(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Insights(context.Background(), request, preflight)
			if err != nil || !result.Unverified || result.Receipt != nil || strings.Contains(result.Message, "PRIVATE_MARKER") {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestInsightsBindingDriftAfterEnqueueIsUnverified(t *testing.T) {
	var client *Client
	client, _, closeServer := makeInsightsClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"healthy":true,"version":"v0.64.0"}`))
			return
		}
		if err := client.Rebind(context.Background(), client.BinaryPath()); err != nil {
			t.Errorf("rebind: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":42,"agent":"codex"}`))
	}))
	defer closeServer()
	request := insights.Request{RepoPath: `E:\repo`, Window: insights.Window30d, BranchScope: insights.BranchAll}
	preflight, err := client.InsightsPreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Insights(context.Background(), request, preflight)
	if err != nil || !result.Unverified || result.Receipt != nil || !strings.Contains(result.Message, "could not be verified") {
		t.Fatalf("binding drift result=%#v err=%v", result, err)
	}
}
