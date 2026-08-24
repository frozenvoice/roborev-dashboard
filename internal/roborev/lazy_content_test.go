package roborev

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type lazyContentRunner struct {
	mu       sync.Mutex
	commands []proc.Command
	version  string
}

type lazyOfflineTransport struct{}

func (lazyOfflineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("connect: %w", syscall.Errno(10061))
}

func (r *lazyContentRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	if len(command.Args) == 2 && command.Args[0] == "version" && command.Args[1] == "--json" {
		version := r.version
		if version == "" {
			version = "v0.64.0"
		}
		return proc.Result{Stdout: fmt.Sprintf(`{"name":"roborev","version":%q}`, version)}, nil
	}
	return proc.Result{}, fmt.Errorf("unexpected command: %#v", command.Args)
}

func newLazyContentClient(t *testing.T, handler http.Handler) (*Client, *lazyContentRunner, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	runner := &lazyContentRunner{}
	client, err := NewClient("roborev", runner, server.URL)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return client, runner, server.Close
}

func writeLazyHealth(w http.ResponseWriter, version string, healthy bool) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"healthy":%t,"version":%q}`, healthy, version)
}

func TestLazyPromptAndLogUseDaemonWithoutCLIOutputCommands(t *testing.T) {
	client, runner, closeServer := newLazyContentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			writeLazyHealth(w, "v0.64.0", false)
		case "/api/review":
			if r.URL.Query().Get("job_id") != "42" {
				t.Fatalf("review query = %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(`{"job_id":42,"prompt":"hello" ,"review":{"unknown":true}}`))
		case "/api/job/log":
			if r.URL.Query().Get("job_id") != "42" || r.URL.Query().Get("offset") != "0" {
				t.Fatalf("log query = %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.Header().Set("X-Job-Status", "running")
			w.Header().Set("X-Log-Offset", "12")
			_, _ = w.Write([]byte("한글\nline\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	prompt, err := client.Prompt(context.Background(), 42)
	if err != nil || prompt != "hello" {
		t.Fatalf("prompt = %q, err = %v", prompt, err)
	}
	log, err := client.Log(context.Background(), 42)
	if err != nil || log != "한글\nline\n" {
		t.Fatalf("log = %q, err = %v", log, err)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.commands) != 1 || strings.Join(runner.commands[0].Args, " ") != "version --json" {
		t.Fatalf("lazy content command history = %#v", runner.commands)
	}
}

func TestLazyPromptFallbackIsStrict(t *testing.T) {
	tests := []struct {
		name         string
		jobs         string
		want         string
		wantNotFound bool
	}{
		{name: "queued", jobs: `{"jobs":[{"id":42,"status":"queued","prompt":"queued prompt"}]}`, want: "queued prompt"},
		{name: "running", jobs: `{"jobs":[{"id":42,"status":"running","prompt":"running prompt"}]}`, want: "running prompt"},
		{name: "zero", jobs: `{"jobs":[]}`, wantNotFound: true},
		{name: "malformed JSON", jobs: `{`},
		{name: "trailing JSON", jobs: `{"jobs":[{"id":42,"status":"running","prompt":"a"}]}{}`},
		{name: "multiple", jobs: `{"jobs":[{"id":42,"status":"running","prompt":"a"},{"id":42,"status":"running","prompt":"b"}]}`},
		{name: "terminal", jobs: `{"jobs":[{"id":42,"status":"done","prompt":"done"}]}`},
		{name: "id mismatch", jobs: `{"jobs":[{"id":43,"status":"running","prompt":"wrong"}]}`},
		{name: "empty", jobs: `{"jobs":[{"id":42,"status":"running","prompt":""}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, _, closeServer := newLazyContentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/health":
					writeLazyHealth(w, "v0.64.0", true)
				case "/api/review":
					http.NotFound(w, r)
				case "/api/jobs":
					query := r.URL.Query()
					if query.Get("id") != "42" || query.Get("limit") != "1" || query.Get("omit_prompt") != "true" {
						t.Fatalf("fallback query = %s", r.URL.RawQuery)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(test.jobs))
				default:
					http.NotFound(w, r)
				}
			}))
			defer closeServer()
			got, err := client.Prompt(context.Background(), 42)
			if test.wantNotFound {
				if !errors.Is(err, ErrLazyContentNotFound) {
					t.Fatalf("err = %v, want not found", err)
				}
				return
			}
			if test.want != "" {
				if err != nil || got != test.want {
					t.Fatalf("prompt = %q, err = %v", got, err)
				}
				return
			}
			if !errors.Is(err, ErrLazyContentMalformed) {
				t.Fatalf("err = %v, want malformed", err)
			}
		})
	}
}

func TestLazyLogValidatesBoundHeadersAndUTF8(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		status    string
		offset    string
		wantError error
	}{
		{name: "empty running", content: "", status: "running", offset: "0"},
		{name: "bad media", content: "x", status: "running", offset: "1", wantError: ErrLazyContentMalformed},
		{name: "wrong media", content: "x", status: "running", offset: "1", wantError: ErrLazyContentMalformed},
		{name: "parameterized media", content: "x", status: "running", offset: "1", wantError: ErrLazyContentMalformed},
		{name: "case-variant media", content: "x", status: "running", offset: "1", wantError: ErrLazyContentMalformed},
		{name: "duplicate media", content: "x", status: "running", offset: "1", wantError: ErrLazyContentMalformed},
		{name: "bad status", content: "x", status: "future", offset: "1", wantError: ErrLazyContentMalformed},
		{name: "short offset", content: "x", status: "running", offset: "0", wantError: ErrLazyContentMalformed},
		{name: "long offset", content: "x", status: "running", offset: "2", wantError: ErrLazyContentMalformed},
		{name: "invalid utf8", content: string([]byte{0xff}), status: "done", offset: "1", wantError: ErrLazyContentMalformed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, _, closeServer := newLazyContentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/health" {
					writeLazyHealth(w, "v0.64.0", true)
					return
				}
				if r.URL.Path != "/api/job/log" {
					http.NotFound(w, r)
					return
				}
				switch test.name {
				case "bad media":
				case "wrong media":
					w.Header().Set("Content-Type", "text/plain")
				case "parameterized media":
					w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
				case "case-variant media":
					w.Header().Set("Content-Type", "application/X-NDJSON")
				case "duplicate media":
					w.Header().Add("Content-Type", "application/x-ndjson")
					w.Header().Add("Content-Type", "application/x-ndjson")
				default:
					w.Header().Set("Content-Type", "application/x-ndjson")
				}
				w.Header().Set("X-Job-Status", test.status)
				w.Header().Set("X-Log-Offset", test.offset)
				_, _ = w.Write([]byte(test.content))
			}))
			defer closeServer()
			got, err := client.Log(context.Background(), 42)
			if test.wantError != nil {
				if !errors.Is(err, test.wantError) {
					t.Fatalf("err = %v, want %v", err, test.wantError)
				}
				return
			}
			if err != nil || got != test.content {
				t.Fatalf("log = %q, err = %v", got, err)
			}
		})
	}
}

func TestLazyPromptRejectsInvalidUTF8InCompletedAndFallbackJSON(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "completed"
		if fallback {
			name = "fallback"
		}
		t.Run(name, func(t *testing.T) {
			client, _, closeServer := newLazyContentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/health" {
					writeLazyHealth(w, "v0.64.0", true)
					return
				}
				if fallback && r.URL.Path == "/api/review" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				var body []byte
				if fallback {
					body = append([]byte(`{"jobs":[{"id":42,"status":"running","prompt":"`), 0xff)
					body = append(body, []byte(`"}]}`)...)
				} else {
					body = append([]byte(`{"job_id":42,"prompt":"`), 0xff)
					body = append(body, []byte(`"}`)...)
				}
				_, _ = w.Write(body)
			}))
			defer closeServer()
			got, err := client.Prompt(context.Background(), 42)
			if got != "" || !errors.Is(err, ErrLazyContentMalformed) {
				t.Fatalf("prompt=%q err=%v, want malformed UTF-8 with no content", got, err)
			}
		})
	}
}

func TestLazyContentBoundIsExactAndRejectsLimitPlusOne(t *testing.T) {
	for _, test := range []struct {
		name      string
		bodySize  int
		wantError error
	}{
		{name: "exact", bodySize: int(lazyContentLimit)},
		{name: "plus one", bodySize: int(lazyContentLimit) + 1, wantError: ErrLazyContentOversized},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := strings.Repeat("x", test.bodySize)
			client, _, closeServer := newLazyContentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/health" {
					writeLazyHealth(w, "v0.64.0", true)
					return
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				w.Header().Set("X-Job-Status", "done")
				w.Header().Set("X-Log-Offset", strconv.Itoa(len(body)))
				_, _ = w.Write([]byte(body))
			}))
			defer closeServer()
			got, err := client.Log(context.Background(), 42)
			if test.wantError != nil {
				if !errors.Is(err, test.wantError) {
					t.Fatalf("err = %v, want %v", err, test.wantError)
				}
				return
			}
			if err != nil || len(got) != test.bodySize {
				t.Fatalf("log length = %d, err = %v, want %d", len(got), err, test.bodySize)
			}
		})
	}
}

func TestLazyPromptCompletedResponseValidationAndNoPartialSuccess(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		status    int
		want      string
		wantError error
	}{
		{name: "empty prompt is valid", body: `{"job_id":42,"prompt":""}`, status: http.StatusOK, want: ""},
		{name: "malformed", body: `{`, status: http.StatusOK, wantError: ErrLazyContentMalformed},
		{name: "trailing", body: `{"job_id":42,"prompt":"a"}{}`, status: http.StatusOK, wantError: ErrLazyContentMalformed},
		{name: "ID mismatch", body: `{"job_id":43,"prompt":"wrong"}`, status: http.StatusOK, wantError: ErrLazyContentMalformed},
		{name: "unexpected status", body: `failure details`, status: http.StatusInternalServerError, wantError: ErrLazyContentMalformed},
		{name: "oversized", body: strings.Repeat("x", int(lazyContentLimit)+1), status: http.StatusOK, wantError: ErrLazyContentOversized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, _, closeServer := newLazyContentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/health" {
					writeLazyHealth(w, "v0.64.0", true)
					return
				}
				if r.URL.Path != "/api/review" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer closeServer()
			got, err := client.Prompt(context.Background(), 42)
			if test.wantError != nil {
				if !errors.Is(err, test.wantError) || got != "" {
					t.Fatalf("prompt=%q err=%v, want empty + %v", got, err, test.wantError)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("prompt=%q err=%v, want %q", got, err, test.want)
			}
		})
	}
}

func TestLazyPreflightRejectsUnsupportedOrMismatchedDaemonBeforeContent(t *testing.T) {
	for _, test := range []struct {
		name          string
		runnerVersion string
		daemonVersion string
		wantError     error
	}{
		{name: "unsupported executable", runnerVersion: "v0.63.0", daemonVersion: "v0.64.0", wantError: ErrLazyContentUnsupported},
		{name: "daemon mismatch", runnerVersion: "v0.64.0", daemonVersion: "v0.63.0", wantError: ErrLazyContentUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			contentCalls := 0
			client, runner, closeServer := newLazyContentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/health" {
					writeLazyHealth(w, test.daemonVersion, true)
					return
				}
				contentCalls++
				t.Fatalf("content endpoint called for preflight failure: %s", r.URL.Path)
			}))
			defer closeServer()
			runner.version = test.runnerVersion
			_, err := client.Prompt(context.Background(), 42)
			if !errors.Is(err, test.wantError) || contentCalls != 0 {
				t.Fatalf("err=%v contentCalls=%d, want %v and zero", err, contentCalls, test.wantError)
			}
		})
	}
}

func TestLazyPreflightClassifiesOfflineBeforeContent(t *testing.T) {
	client, _, closeServer := newLazyContentClient(t, http.NotFoundHandler())
	defer closeServer()
	client.HTTP = &http.Client{Transport: lazyOfflineTransport{}}
	if _, err := client.Prompt(context.Background(), 42); !errors.Is(err, ErrLazyContentOffline) {
		t.Fatalf("err = %v, want offline", err)
	}
}

func TestLazyContentBindingDriftRejectsStalePromptAndLog(t *testing.T) {
	for _, path := range []string{"/api/review", "/api/job/log"} {
		t.Run(path, func(t *testing.T) {
			var client *Client
			var closeServer func()
			client, _, closeServer = newLazyContentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/health" {
					writeLazyHealth(w, "v0.64.0", true)
					return
				}
				if r.URL.Path != path {
					http.NotFound(w, r)
					return
				}
				client.bindingMu.Lock()
				client.binding.generation++
				client.bindingMu.Unlock()
				if path == "/api/review" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"job_id":42,"prompt":"stale"}`))
					return
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				w.Header().Set("X-Job-Status", "done")
				w.Header().Set("X-Log-Offset", "5")
				_, _ = w.Write([]byte("stale"))
			}))
			defer closeServer()
			var err error
			if path == "/api/review" {
				_, err = client.Prompt(context.Background(), 42)
			} else {
				_, err = client.Log(context.Background(), 42)
			}
			if !errors.Is(err, ErrBindingChanged) {
				t.Fatalf("err=%v, want binding changed", err)
			}
		})
	}
}

func TestLazyLogStatusAndNotFoundContracts(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		body      string
		wantError error
	}{
		{name: "404", status: http.StatusNotFound, body: "not found", wantError: ErrLazyContentNotFound},
		{name: "non2xx", status: http.StatusBadGateway, body: "secret upstream details", wantError: ErrLazyContentMalformed},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, _, closeServer := newLazyContentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/health" {
					writeLazyHealth(w, "v0.64.0", true)
					return
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer closeServer()
			_, err := client.Log(context.Background(), 42)
			if !errors.Is(err, test.wantError) || strings.Contains(err.Error(), "secret upstream") {
				t.Fatalf("err=%v, want %v without upstream body", err, test.wantError)
			}
		})
	}
}
