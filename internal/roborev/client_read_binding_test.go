package roborev

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type readBindingRunner struct {
	mu      sync.Mutex
	version string
	calls   int
}

func (r *readBindingRunner) Run(_ context.Context, _ proc.Command) (proc.Result, error) {
	r.mu.Lock()
	r.calls++
	version := r.version
	r.mu.Unlock()
	return proc.Result{Stdout: `{"name":"roborev","version":"` + version + `"}`}, nil
}

func (r *readBindingRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func newReadBindingClient(t *testing.T, version, daemonURL string) (*Client, *readBindingRunner) {
	t.Helper()
	runner := &readBindingRunner{version: version}
	client, err := NewClient("roborev", runner, daemonURL)
	if err != nil {
		t.Fatal(err)
	}
	client.versionMu.Lock()
	_, _, generation := client.snapshot()
	client.version = version
	client.versionGeneration = generation
	client.versionAt = nowForLifecycleTest()
	client.versionMu.Unlock()
	return client, runner
}

func TestReadBindingsRejectDaemonVersionMismatchBeforeDownstream(t *testing.T) {
	var mu sync.Mutex
	healthCalls := 0
	jobsCalls := 0
	outputCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Errorf("%s %s Accept-Encoding=%q, want identity", r.Method, r.URL.Path, r.Header.Get("Accept-Encoding"))
		}
		switch r.URL.Path {
		case "/api/health":
			mu.Lock()
			healthCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.65.0"}`)
		case "/api/jobs":
			mu.Lock()
			jobsCalls++
			mu.Unlock()
			http.Error(w, "PRIVATE_DOWNSTREAM_SHOULD_NOT_RUN", http.StatusBadGateway)
		case "/api/job/output":
			mu.Lock()
			outputCalls++
			mu.Unlock()
			http.Error(w, "PRIVATE_DOWNSTREAM_SHOULD_NOT_RUN", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, runner := newReadBindingClient(t, "v0.64.0", server.URL)
	ctx := context.Background()
	if _, err := client.List(ctx, `C:\repo`, 10); !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("List error=%v, want binding mismatch", err)
	}
	if _, err := client.PanelMembers(ctx, "986d3bfa-05da-4621-aeca-66f493a19ea3"); !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("PanelMembers error=%v, want binding mismatch", err)
	}
	if _, err := client.TerminalJobMetadata(ctx, 42); !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("TerminalJobMetadata error=%v, want binding mismatch", err)
	}
	if _, err := client.JobOutputStatus(ctx, 42); !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("JobOutputStatus error=%v, want binding mismatch", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if healthCalls != 4 || jobsCalls != 0 || outputCalls != 0 {
		t.Fatalf("health=%d jobs=%d output=%d, want 4/0/0", healthCalls, jobsCalls, outputCalls)
	}
	if got := runner.count(); got != 0 {
		t.Fatalf("lifecycle/process calls=%d, want zero", got)
	}
}

func TestReadBindingsAcceptMatchingDaemonVersion(t *testing.T) {
	var mu sync.Mutex
	healthCalls := 0
	jobsCalls := 0
	outputCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Errorf("%s %s Accept-Encoding=%q, want identity", r.Method, r.URL.Path, r.Header.Get("Accept-Encoding"))
		}
		switch r.URL.Path {
		case "/api/health":
			mu.Lock()
			healthCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/jobs":
			mu.Lock()
			jobsCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"jobs":[{"id":42,"status":"running"}]}`)
		case "/api/job/output":
			mu.Lock()
			outputCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"job_id":42,"status":"running","lines":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, runner := newReadBindingClient(t, "v0.64.0", server.URL)
	ctx := context.Background()
	if raw, err := client.List(ctx, `C:\repo`, 10); err != nil || len(raw) == 0 {
		t.Fatalf("List raw=%s err=%v", raw, err)
	}
	if raw, err := client.PanelMembers(ctx, "986d3bfa-05da-4621-aeca-66f493a19ea3"); err != nil || len(raw) == 0 {
		t.Fatalf("PanelMembers raw=%s err=%v", raw, err)
	}
	if metadata, err := client.TerminalJobMetadata(ctx, 42); err != nil || metadata.ID != 42 {
		t.Fatalf("TerminalJobMetadata=%#v err=%v", metadata, err)
	}
	if status, err := client.JobOutputStatus(ctx, 42); err != nil || status.JobID != 42 || status.Status != "running" {
		t.Fatalf("JobOutputStatus=%#v err=%v", status, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if healthCalls != 4 || jobsCalls != 3 || outputCalls != 1 {
		t.Fatalf("health=%d jobs=%d output=%d, want 4/3/1", healthCalls, jobsCalls, outputCalls)
	}
	if got := runner.count(); got != 0 {
		t.Fatalf("lifecycle/process calls=%d, want zero", got)
	}
}

func TestReadBindingRejectsGenerationDriftBeforeListAdoption(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
			return
		}
		if r.URL.Path != "/api/jobs" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jobs":[{"id":42,"status":"running"}]}`)
	}))
	defer server.Close()
	client, runner := newReadBindingClient(t, "v0.64.0", server.URL)

	// Mutating the generation immediately after the response is received is
	// deterministic with a custom RoundTripper. The test below uses the
	// handler's response only for the happy request, while the binding drift is
	// injected by wrapping the configured transport after server setup.
	base := client.HTTP.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(r)
		if err == nil && r.URL.Path == "/api/jobs" {
			client.bindingMu.Lock()
			client.binding.generation++
			client.bindingMu.Unlock()
		}
		return response, err
	})
	if _, err := client.List(context.Background(), `C:\repo`, 10); !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("List error=%v, want generation drift rejection", err)
	}
	if got := runner.count(); got != 0 {
		t.Fatalf("lifecycle/process calls=%d, want zero", got)
	}
}
