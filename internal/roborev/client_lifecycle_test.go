package roborev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type lifecycleCountingRunner struct {
	mu    sync.Mutex
	calls int
}

func (r *lifecycleCountingRunner) Run(context.Context, proc.Command) (proc.Result, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return proc.Result{}, errors.New("unexpected process use")
}

func (r *lifecycleCountingRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func seedLifecycleVersion(client *Client) {
	_, _, generation := client.snapshot()
	client.versionMu.Lock()
	client.version = "v0.64.0"
	client.versionGeneration = generation
	client.versionAt = nowForLifecycleTest()
	client.versionMu.Unlock()
}

// Kept behind a helper so test setup documents that the cached version is
// intentionally already verified; lifecycle-free calls must not probe it.
func nowForLifecycleTest() time.Time { return time.Now() }

func TestLifecycleHTTPUsesExactContractsWithoutRunner(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
			return
		}
		mu.Lock()
		methods = append(methods, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		if got := r.Header.Get("Accept-Encoding"); got != "identity" {
			t.Errorf("Accept-Encoding=%q, want identity", got)
		}
		if r.Method == http.MethodPost {
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type=%q, want application/json", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			mu.Lock()
			bodies = append(bodies, body)
			mu.Unlock()
		}
		switch r.URL.Path {
		case "/api/review":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"job_id":42,"job":{"id":42,"repo_path":"C:\\repo","status":"done","agent":"codex","session_id":"PRIVATE_SESSION"},"review":{"output":"finding","raw_log":"PRIVATE_LOG"},"prompt":"PRIVATE_PROMPT"}`)
		case "/api/comment":
			w.WriteHeader(http.StatusCreated)
		case "/api/review/close", "/api/job/cancel":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runner := &lifecycleCountingRunner{}
	client, err := NewClient("roborev", runner, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	seedLifecycleVersion(client)
	show, err := client.Show(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(show), "finding") || strings.Contains(string(show), "PRIVATE_") {
		t.Fatalf("show payload was not sanitized: %s", show)
	}
	if err := client.Comment(context.Background(), 42, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(context.Background(), 42, false); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(context.Background(), 42, true); err != nil {
		t.Fatal(err)
	}
	if err := client.Cancel(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if got := runner.count(); got != 0 {
		t.Fatalf("runner calls=%d, want zero", got)
	}
	mu.Lock()
	defer mu.Unlock()
	wantMethods := []string{
		"GET /api/review?job_id=42",
		"POST /api/comment",
		"POST /api/review/close",
		"POST /api/review/close",
		"POST /api/job/cancel",
	}
	if !equalStrings(methods, wantMethods) {
		t.Fatalf("requests=%#v, want %#v", methods, wantMethods)
	}
	if len(bodies) != 4 || bodies[0]["commenter"] != "roborev-dashboard" || bodies[0]["comment"] != "hello" || bodies[1]["closed"] != true || bodies[2]["closed"] != false || bodies[3]["job_id"] != float64(42) {
		t.Fatalf("mutation bodies=%#v", bodies)
	}
}

func TestLifecycleHTTPRejectsRedirectsAndSanitizesErrors(t *testing.T) {
	destinationCalls := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationCalls++
		_, _ = io.WriteString(w, `{"job_id":42}`)
	}))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
			return
		}
		if r.URL.Path == "/api/review" {
			http.Redirect(w, r, destination.URL, http.StatusFound)
			return
		}
		http.Error(w, "PRIVATE_DAEMON_MARKER", http.StatusBadGateway)
	}))
	defer server.Close()
	runner := &lifecycleCountingRunner{}
	client, err := NewClient("roborev", runner, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	seedLifecycleVersion(client)
	if _, err := client.Show(context.Background(), 42); err == nil || strings.Contains(err.Error(), "PRIVATE_DAEMON_MARKER") {
		t.Fatalf("redirect/error was not sanitized: %v", err)
	}
	if destinationCalls != 0 || runner.count() != 0 {
		t.Fatalf("redirect calls=%d runner=%d", destinationCalls, runner.count())
	}
}

func TestLifecycleHTTPBoundsShowBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
			return
		}
		_, _ = io.WriteString(w, strings.Repeat("x", int(daemonHTTPShowLimit)+1))
	}))
	defer server.Close()
	client, err := NewClient("roborev", &lifecycleCountingRunner{}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	seedLifecycleVersion(client)
	if _, err := client.Show(context.Background(), 42); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized show error=%v", err)
	}
}

func TestLifecycleHTTPRejectsMalformedJSONContracts(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        []byte
	}{
		{name: "missing media type", body: []byte(`{"job_id":42}`)},
		{name: "wrong media type", contentType: "text/plain", body: []byte(`{"job_id":42}`)},
		{name: "duplicate field", contentType: "application/json; charset=utf-8", body: []byte(`{"job_id":42,"job_id":43}`)},
		{name: "trailing value", contentType: "application/json", body: []byte(`{"job_id":42}{}`)},
		{name: "invalid UTF-8", contentType: "application/json", body: []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/health" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
					return
				}
				if test.contentType != "" {
					w.Header().Set("Content-Type", test.contentType)
				}
				_, _ = w.Write(test.body)
			}))
			defer server.Close()
			client, err := NewClient("roborev", &lifecycleCountingRunner{}, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			seedLifecycleVersion(client)
			if _, err := client.Show(context.Background(), 42); err == nil || strings.Contains(err.Error(), string(test.body)) {
				t.Fatalf("malformed contract error=%v", err)
			}
		})
	}
}
