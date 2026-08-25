package roborev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type diagnosticsRunner struct {
	mu       sync.Mutex
	commands []proc.Command
	version  string
}

func (r *diagnosticsRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	if reflect.DeepEqual(command.Args, []string{"version", "--json"}) {
		return proc.Result{Stdout: `{"name":"roborev","version":"` + r.version + `"}`}, nil
	}
	return proc.Result{}, errors.New("unexpected process command")
}

func diagnosticsStatusBody(version string) string {
	return `{"version":"` + version + `","queued_jobs":2,"running_jobs":1,"completed_jobs":100,"failed_jobs":3,"canceled_jobs":4,"applied_jobs":5,"rebased_jobs":6,"skipped_jobs":7,"active_workers":1,"max_workers":4,"queue_paused":false,"network":"tcp","address":"127.0.0.1","port":7373,"machine_id":"machine","config_reloaded_at":"2026-08-25T00:00:00Z","config_reload_counter":1,"auto_design":null}`
}

func diagnosticsHealthBody(healthy bool, version string) string {
	return `{"healthy":` + map[bool]string{true: "true", false: "false"}[healthy] + `,"uptime":"1h2m3s","version":"` + version + `","components":[{"name":"database","healthy":true},{"name":"workers","healthy":` + map[bool]string{true: "true", false: "false"}[healthy] + `}],"recent_errors":null,"error_count_24h":0}`
}

const diagnosticsAutoDesignObject = `{"enabled":true,"triggered_heuristic":1,"skipped_heuristic":2,"triggered_classifier":3,"skipped_classifier":4,"classifier_failed":5}`

type diagnosticsErrorTransport struct{ err error }

func (t diagnosticsErrorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

type diagnosticsTrackingBody struct {
	data   []byte
	reads  int
	closed bool
}

func (b *diagnosticsTrackingBody) Read(p []byte) (int, error) {
	b.reads++
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

func (b *diagnosticsTrackingBody) Close() error {
	b.closed = true
	return nil
}

type diagnosticsRedirectTransport struct {
	body             *diagnosticsTrackingBody
	destinationCalls int
}

func (t *diagnosticsRedirectTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path == "/api/health" {
		t.body = &diagnosticsTrackingBody{data: []byte("sensitive redirect body")}
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://127.0.0.1:7373/redirected-health"}},
			Body:       t.body,
		}, nil
	}
	if request.URL.Path == "/redirected-health" {
		t.destinationCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(diagnosticsHealthBody(true, "v0.64.0"))),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader("not found")),
	}, nil
}

func TestRuntimeDiagnosticsOfflineIsTypedAndHasNoLifecycleCommand(t *testing.T) {
	runner := &diagnosticsRunner{version: "v0.64.0"}
	client, err := NewClient("roborev", runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: diagnosticsErrorTransport{err: fmt.Errorf("offline: %w", syscall.Errno(10061))}}
	if _, err := client.RuntimeDiagnostics(context.Background()); !errors.Is(err, ErrRuntimeDiagnosticsOffline) {
		t.Fatalf("offline error=%v, want ErrRuntimeDiagnosticsOffline", err)
	}
	if len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0].Args, []string{"version", "--json"}) {
		t.Fatalf("offline command ledger=%#v, want only version --json", runner.commands)
	}
}

func TestRuntimeDiagnosticsRejectsRedirectWithoutFollowingOrReadingBody(t *testing.T) {
	runner := &diagnosticsRunner{version: "v0.64.0"}
	client, err := NewClient("roborev", runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	transport := &diagnosticsRedirectTransport{}
	client.HTTP = &http.Client{Transport: transport}
	if _, err := client.RuntimeDiagnostics(context.Background()); !errors.Is(err, ErrMalformedRuntimeDiagnostics) {
		t.Fatalf("redirect error=%v, want malformed diagnostics", err)
	}
	if transport.destinationCalls != 0 {
		t.Fatalf("redirect destination calls=%d, want 0", transport.destinationCalls)
	}
	if transport.body == nil || transport.body.reads != 0 || !transport.body.closed {
		t.Fatalf("redirect body=%+v, want unread and closed", transport.body)
	}
}

func TestRuntimeDiagnosticsUnsupportedCLIReadsNoDaemon(t *testing.T) {
	runner := &diagnosticsRunner{version: "v0.63.0"}
	var paths []string
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		http.Error(w, "unexpected daemon read", http.StatusInternalServerError)
	}))
	defer daemon.Close()
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RuntimeDiagnostics(context.Background()); !errors.Is(err, ErrUnsupportedRuntimeDiagnostics) {
		t.Fatalf("unsupported CLI error=%v, want ErrUnsupportedRuntimeDiagnostics", err)
	}
	if len(paths) != 0 {
		t.Fatalf("unsupported CLI daemon paths=%v, want none", paths)
	}
}

func TestRuntimeDiagnosticsUnsupportedHealthVersionReadsNoStatus(t *testing.T) {
	runner := &diagnosticsRunner{version: "v0.64.0"}
	var paths []string
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, diagnosticsHealthBody(true, "v0.63.0"))
		default:
			http.Error(w, "unexpected daemon read", http.StatusInternalServerError)
		}
	}))
	defer daemon.Close()
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RuntimeDiagnostics(context.Background()); !errors.Is(err, ErrUnsupportedRuntimeDiagnostics) {
		t.Fatalf("unsupported daemon error=%v, want ErrUnsupportedRuntimeDiagnostics", err)
	}
	if !reflect.DeepEqual(paths, []string{"/api/health"}) {
		t.Fatalf("daemon paths=%v, want health only", paths)
	}
}

func TestRuntimeDiagnosticsRejectsUnsupportedStatusVersion(t *testing.T) {
	runner := &diagnosticsRunner{version: "v0.64.0"}
	var paths []string
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/health" {
			_, _ = io.WriteString(w, diagnosticsHealthBody(true, "v0.64.0"))
			return
		}
		_, _ = io.WriteString(w, diagnosticsStatusBody("v0.63.0"))
	}))
	defer daemon.Close()
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RuntimeDiagnostics(context.Background()); !errors.Is(err, ErrUnsupportedRuntimeDiagnostics) {
		t.Fatalf("unsupported status error=%v, want ErrUnsupportedRuntimeDiagnostics", err)
	}
	if !reflect.DeepEqual(paths, []string{"/api/health", "/api/status"}) {
		t.Fatalf("daemon paths=%v, want health then status", paths)
	}
}

func TestRuntimeDiagnosticsRejectsMalformedAndOversizedStatus(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "malformed", body: "{"},
		{name: "oversized", body: strings.Repeat("x", int(runtimeDiagnosticsResponseLimit)+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &diagnosticsRunner{version: "v0.64.0"}
			var paths []string
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/health" {
					_, _ = io.WriteString(w, diagnosticsHealthBody(true, "v0.64.0"))
					return
				}
				_, _ = io.WriteString(w, test.body)
			}))
			defer daemon.Close()
			client, err := NewClient("roborev", runner, daemon.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.RuntimeDiagnostics(context.Background()); !errors.Is(err, ErrMalformedRuntimeDiagnostics) {
				t.Fatalf("%s status error=%v, want malformed", test.name, err)
			}
			if !reflect.DeepEqual(paths, []string{"/api/health", "/api/status"}) {
				t.Fatalf("daemon paths=%v, want health then status", paths)
			}
		})
	}
}

func TestRuntimeDiagnosticsRejectsNegativeAndNonsensicalStatusCounters(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
	}{
		{name: "negative completed", status: strings.Replace(diagnosticsStatusBody("v0.64.0"), `"completed_jobs":100`, `"completed_jobs":-1`, 1)},
		{name: "active exceeds max", status: strings.Replace(diagnosticsStatusBody("v0.64.0"), `"active_workers":1`, `"active_workers":5`, 1)},
		{name: "zero max", status: strings.Replace(diagnosticsStatusBody("v0.64.0"), `"max_workers":4`, `"max_workers":0`, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &diagnosticsRunner{version: "v0.64.0"}
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/health" {
					_, _ = io.WriteString(w, diagnosticsHealthBody(true, "v0.64.0"))
					return
				}
				_, _ = io.WriteString(w, test.status)
			}))
			defer daemon.Close()
			client, err := NewClient("roborev", runner, daemon.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.RuntimeDiagnostics(context.Background()); !errors.Is(err, ErrMalformedRuntimeDiagnostics) {
				t.Fatalf("%s status error=%v, want malformed", test.name, err)
			}
		})
	}
}

func TestRuntimeDiagnosticsReadsHealthBeforeStatusWithoutStatusCLI(t *testing.T) {
	runner := &diagnosticsRunner{version: "v0.64.0"}
	var paths []string
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, diagnosticsHealthBody(true, "v0.64.0"))
		case "/api/status":
			_, _ = io.WriteString(w, diagnosticsStatusBody("v0.64.0"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.RuntimeDiagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"/api/health", "/api/status"}) {
		t.Fatalf("daemon paths=%v, want health then status", paths)
	}
	if len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0].Args, []string{"version", "--json"}) {
		t.Fatalf("commands=%#v, want one typed version probe", runner.commands)
	}
	if got.Daemon.Version != "v0.64.0" || got.Workers.Active != 1 || got.Workers.Max != 4 || got.Queue.Queued != 2 || got.Jobs.Completed != 100 || !got.Health.Healthy {
		t.Fatalf("diagnostics=%+v", got)
	}
}

func TestRuntimeDiagnosticsAcceptsExactAutoDesignObject(t *testing.T) {
	runner := &diagnosticsRunner{version: "v0.64.0"}
	var paths []string
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/health" {
			_, _ = io.WriteString(w, diagnosticsHealthBody(true, "v0.64.0"))
			return
		}
		status := strings.Replace(diagnosticsStatusBody("v0.64.0"), `"auto_design":null`, `"auto_design":`+diagnosticsAutoDesignObject, 1)
		_, _ = io.WriteString(w, status)
	}))
	defer daemon.Close()
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RuntimeDiagnostics(context.Background()); err != nil {
		t.Fatalf("typed auto_design status rejected: %v", err)
	}
	if !reflect.DeepEqual(paths, []string{"/api/health", "/api/status"}) {
		t.Fatalf("daemon paths=%v, want health then status", paths)
	}
}

func TestRuntimeDiagnosticsRejectsMalformedAutoDesignObjects(t *testing.T) {
	for _, test := range []struct {
		name   string
		object string
	}{
		{name: "missing fields", object: `{}`},
		{name: "missing counter", object: `{"enabled":true,"triggered_heuristic":1,"skipped_heuristic":2,"triggered_classifier":3,"skipped_classifier":4}`},
		{name: "wrong type", object: `{"enabled":"true","triggered_heuristic":1,"skipped_heuristic":2,"triggered_classifier":3,"skipped_classifier":4,"classifier_failed":5}`},
		{name: "unknown field", object: `{"enabled":true,"triggered_heuristic":1,"skipped_heuristic":2,"triggered_classifier":3,"skipped_classifier":4,"classifier_failed":5,"unexpected":0}`},
		{name: "negative counter", object: `{"enabled":true,"triggered_heuristic":-1,"skipped_heuristic":2,"triggered_classifier":3,"skipped_classifier":4,"classifier_failed":5}`},
		{name: "null field", object: `{"enabled":true,"triggered_heuristic":1,"skipped_heuristic":2,"triggered_classifier":3,"skipped_classifier":4,"classifier_failed":null}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &diagnosticsRunner{version: "v0.64.0"}
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/health" {
					_, _ = io.WriteString(w, diagnosticsHealthBody(true, "v0.64.0"))
					return
				}
				status := strings.Replace(diagnosticsStatusBody("v0.64.0"), `"auto_design":null`, `"auto_design":`+test.object, 1)
				_, _ = io.WriteString(w, status)
			}))
			defer daemon.Close()
			client, err := NewClient("roborev", runner, daemon.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.RuntimeDiagnostics(context.Background()); !errors.Is(err, ErrMalformedRuntimeDiagnostics) {
				t.Fatalf("%s auto_design error=%v, want malformed", test.name, err)
			}
		})
	}
}

func TestRuntimeDiagnosticsAcceptsDegradedHealth(t *testing.T) {
	runner := &diagnosticsRunner{version: "v0.64.0"}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/health" {
			_, _ = io.WriteString(w, diagnosticsHealthBody(false, "v0.64.0"))
			return
		}
		_, _ = io.WriteString(w, diagnosticsStatusBody("v0.64.0"))
	}))
	defer daemon.Close()
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.RuntimeDiagnostics(context.Background())
	if err != nil || got.Health.Healthy {
		t.Fatalf("degraded diagnostics=%+v err=%v", got, err)
	}
}

func TestRuntimeDiagnosticsRejectsMalformedTransportContracts(t *testing.T) {
	tests := []struct {
		name string
		body func() string
	}{
		{name: "wrong content type", body: func() string { return diagnosticsHealthBody(true, "v0.64.0") }},
		{name: "trailing JSON", body: func() string { return diagnosticsHealthBody(true, "v0.64.0") + `{}` }},
		{name: "invalid UTF8", body: func() string { return "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &diagnosticsRunner{version: "v0.64.0"}
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/health" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, diagnosticsStatusBody("v0.64.0"))
					return
				}
				if test.name == "wrong content type" {
					w.Header().Set("Content-Type", "text/plain")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				if test.name == "invalid UTF8" {
					_, _ = w.Write([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'})
					return
				}
				_, _ = io.WriteString(w, test.body())
			}))
			defer daemon.Close()
			client, err := NewClient("roborev", runner, daemon.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.RuntimeDiagnostics(context.Background()); !errors.Is(err, ErrMalformedRuntimeDiagnostics) {
				t.Fatalf("err=%v, want malformed diagnostics", err)
			}
		})
	}

	runner := &diagnosticsRunner{version: "v0.64.0"}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, strings.Repeat("x", int(runtimeDiagnosticsResponseLimit)+1))
	}))
	defer daemon.Close()
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RuntimeDiagnostics(context.Background()); !errors.Is(err, ErrMalformedRuntimeDiagnostics) {
		t.Fatalf("oversized error=%v, want malformed diagnostics", err)
	}
}
