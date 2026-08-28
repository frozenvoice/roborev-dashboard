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
	"time"

	"github.com/froze/roborev-dashboard/internal/process"
)

type runtimeCommandRunner struct {
	mu       sync.Mutex
	commands []process.Command
}

func (r *runtimeCommandRunner) Run(_ context.Context, command process.Command) (process.Result, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	return process.Result{}, nil
}

func (r *runtimeCommandRunner) last() process.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commands[len(r.commands)-1]
}

func TestDaemonArgsWhitelistExactContract(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		got, err := DaemonArgs(action)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"daemon", action}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s args = %#v, want %#v", action, got, want)
		}
	}
	if _, err := DaemonArgs("status"); err == nil {
		t.Fatal("status was accepted as a lifecycle action")
	}
}

func TestDaemonLifecycleUsesTypedArgsAndDetachPolicy(t *testing.T) {
	runner := &runtimeCommandRunner{}
	client, err := NewClient(`C:\Program Files\RoboRev\roborev.exe`, runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.StartDaemon(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := runner.last(); !reflect.DeepEqual(got.Args, []string{"daemon", "start"}) || !got.AllowDetachedChildren {
		t.Fatalf("start command = %#v", got)
	}
	if err := client.StopDaemon(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := runner.last(); !reflect.DeepEqual(got.Args, []string{"daemon", "stop"}) || got.AllowDetachedChildren {
		t.Fatalf("stop command = %#v", got)
	}
	if err := client.RestartDaemon(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := runner.last(); !reflect.DeepEqual(got.Args, []string{"daemon", "restart"}) || !got.AllowDetachedChildren {
		t.Fatalf("restart command = %#v", got)
	}
}

func TestQueueControlUsesExactHTTPContractsAndVerifiesResponse(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/status":
			if r.Method != http.MethodGet {
				t.Fatalf("status method = %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"queue_paused":false}`))
		case "/api/queue/pause":
			if r.Method != http.MethodPost {
				t.Fatalf("pause method = %s", r.Method)
			}
			if r.ContentLength != 0 {
				t.Fatalf("pause body length = %d", r.ContentLength)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"queue_paused":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client, err := NewClient("roborev", &runtimeCommandRunner{}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetQueuePaused(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET /api/status", "POST /api/queue/pause"}; !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
}

type runtimeTrackingBody struct {
	reader *strings.Reader
	reads  int
	closed bool
}

func (b *runtimeTrackingBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.reads += n
	return n, err
}

func (b *runtimeTrackingBody) Close() error {
	b.closed = true
	return nil
}

func TestQueuePostUsesNoRedirectIdentityAndDoesNotExposeErrorBody(t *testing.T) {
	destinationRequests := 0
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		destinationRequests++
	}))
	defer destination.Close()

	var sourceRequests int
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceRequests++
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Errorf("Accept-Encoding = %q, want identity", r.Header.Get("Accept-Encoding"))
		}
		w.Header().Set("Location", destination.URL+"/followed")
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = io.WriteString(w, "queue-secret-marker")
	}))
	defer source.Close()
	client, err := NewClient("roborev", &runtimeCommandRunner{}, source.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.postEmptyJSON(context.Background(), "/api/queue/pause")
	if err == nil || strings.Contains(err.Error(), "queue-secret-marker") {
		t.Fatalf("redirect error = %v, want sanitized non-body error", err)
	}
	if sourceRequests != 1 || destinationRequests != 0 {
		t.Fatalf("source requests = %d, destination requests = %d; want 1/0", sourceRequests, destinationRequests)
	}
}

func TestQueuePostClosesNonSuccessBodyWithoutReadingOrRetrying(t *testing.T) {
	body := &runtimeTrackingBody{reader: strings.NewReader("queue-secret-marker")}
	transportCalls := 0
	client, err := NewClient("roborev", &runtimeCommandRunner{}, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		transportCalls++
		if req.Header.Get("Accept-Encoding") != "identity" {
			t.Errorf("Accept-Encoding = %q, want identity", req.Header.Get("Accept-Encoding"))
		}
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Status:     "502 Bad Gateway",
			Header:     make(http.Header),
			Body:       body,
			Request:    req,
		}, nil
	})}
	_, err = client.postEmptyJSON(context.Background(), "/api/queue/pause")
	if err == nil || strings.Contains(err.Error(), "queue-secret-marker") {
		t.Fatalf("error = %v, want sanitized non-body error", err)
	}
	if transportCalls != 1 || body.reads != 0 || !body.closed {
		t.Fatalf("transport calls = %d, body reads = %d, closed = %v; want 1/0/true", transportCalls, body.reads, body.closed)
	}
}

func TestQueuePostAcceptsExactlyBoundedBodyAndRejectsOneByteOver(t *testing.T) {
	makeBody := func(size int64) []byte {
		prefix := `{"queue_paused":true,"padding":"`
		suffix := `"}`
		padding := int(size) - len(prefix) - len(suffix)
		if padding < 0 {
			t.Fatalf("test body target is too small")
		}
		return []byte(prefix + strings.Repeat("x", padding) + suffix)
	}
	for _, test := range []struct {
		name string
		size int64
		want bool
	}{
		{name: "exact limit", size: runtimeControlResponseLimit, want: true},
		{name: "one byte over", size: runtimeControlResponseLimit + 1, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(makeBody(test.size))
			}))
			defer server.Close()
			client, err := NewClient("roborev", &runtimeCommandRunner{}, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.postEmptyJSON(context.Background(), "/api/queue/pause")
			if (err == nil) != test.want {
				t.Fatalf("post error = %v, accepted = %v; want accepted = %v", err, err == nil, test.want)
			}
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestQueueControlRejectsMismatchAndMalformedState(t *testing.T) {
	for _, response := range []string{`{"queue_paused":false}`, `{"queue_paused":"true"}`, `{}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/status" {
				_, _ = w.Write([]byte(`{"queue_paused":false}`))
				return
			}
			_, _ = w.Write([]byte(response))
		}))
		client, err := NewClient("roborev", &runtimeCommandRunner{}, srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.SetQueuePaused(context.Background(), true)
		srv.Close()
		if response == `{"queue_paused":false}` {
			if !errors.Is(err, ErrQueueStateMismatch) {
				t.Fatalf("mismatch error = %v", err)
			}
		} else if err == nil {
			t.Fatalf("response %s was accepted", response)
		}
	}
}

func TestQueueStatusOffline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"queue_paused":false}`))
	}))
	address := srv.URL
	srv.Close()
	client, err := NewClient("roborev", &runtimeCommandRunner{}, address)
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.QueueStatus(context.Background())
	if !errors.Is(err, ErrDaemonOffline) || state.Known {
		t.Fatalf("offline result = %#v, %v", state, err)
	}
}

func TestQueueStatusDoesNotMisclassifyReachableOrTimedOutFailuresAsOffline(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "server error", status: http.StatusInternalServerError, body: `daemon exploded`},
		{name: "malformed response", status: http.StatusOK, body: `{`},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer srv.Close()
			client, err := NewClient("roborev", &runtimeCommandRunner{}, srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.QueueStatus(context.Background())
			if err == nil || errors.Is(err, ErrDaemonOffline) {
				t.Fatalf("queue status error = %v, want non-offline error", err)
			}
		})
	}

	client, err := NewClient("roborev", &runtimeCommandRunner{}, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: timeoutRoundTripper{}}
	_, err = client.QueueStatus(context.Background())
	if err == nil || errors.Is(err, ErrDaemonOffline) {
		t.Fatalf("timed out queue status error = %v, want non-offline error", err)
	}
}

type timeoutRoundTripper struct{}

func (timeoutRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("synthetic timeout: %w", context.DeadlineExceeded)
}

type runtimeErrorRoundTripper struct{ err error }

func (r runtimeErrorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, r.err
}

type genericTimeoutError struct{}

func (genericTimeoutError) Error() string   { return "synthetic generic timeout" }
func (genericTimeoutError) Timeout() bool   { return true }
func (genericTimeoutError) Temporary() bool { return true }

type blockingRoundTripper struct{}

func (blockingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestDaemonHealthTransportClassificationIsErrnoOnly(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		offline bool
	}{
		{name: "wrapped connection refused", err: fmt.Errorf("outer: %w", syscall.Errno(10061)), offline: true},
		{name: "wrapped host unreachable", err: fmt.Errorf("outer: %w", syscall.EHOSTUNREACH), offline: true},
		{name: "generic net timeout", err: genericTimeoutError{}, offline: false},
		{name: "wrapped context deadline", err: fmt.Errorf("outer: %w", context.DeadlineExceeded), offline: false},
		{name: "wrapped context canceled", err: fmt.Errorf("outer: %w", context.Canceled), offline: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient("roborev", &runtimeCommandRunner{}, "127.0.0.1:7373")
			if err != nil {
				t.Fatal(err)
			}
			client.HTTP = &http.Client{Transport: runtimeErrorRoundTripper{err: test.err}}
			online, healthErr := client.DaemonHealth(context.Background())
			if test.offline {
				if online || healthErr != nil {
					t.Fatalf("health = online:%v err:%v, want confirmed offline", online, healthErr)
				}
				return
			}
			if !online || healthErr == nil {
				t.Fatalf("health = online:%v err:%v, want reachable/unknown error", online, healthErr)
			}
		})
	}

	client, err := NewClient("roborev", &runtimeCommandRunner{}, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Timeout: 20 * time.Millisecond, Transport: blockingRoundTripper{}}
	online, healthErr := client.DaemonHealth(context.Background())
	if !online || healthErr == nil || errors.Is(healthErr, ErrDaemonOffline) {
		t.Fatalf("client timeout health = online:%v err:%v, want non-offline error", online, healthErr)
	}

	for _, errno := range []error{syscall.Errno(10061), syscall.EHOSTUNREACH} {
		client.HTTP = &http.Client{Transport: runtimeErrorRoundTripper{err: fmt.Errorf("wrapped: %w", errno)}}
		state, queueErr := client.QueueStatus(context.Background())
		if !errors.Is(queueErr, ErrDaemonOffline) || state.Known {
			t.Fatalf("queue transport error %v = state:%#v err:%v, want confirmed offline", errno, state, queueErr)
		}
	}
}

func TestDaemonHealthContextCancellationIsNotOffline(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		want error
	}{
		{name: "canceled", ctx: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}, want: context.Canceled},
		{name: "deadline", ctx: func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		}, want: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient("roborev", &runtimeCommandRunner{}, "127.0.0.1:7373")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := test.ctx()
			defer cancel()
			online, healthErr := client.DaemonHealth(ctx)
			if online || !errors.Is(healthErr, test.want) || errors.Is(healthErr, ErrDaemonOffline) {
				t.Fatalf("health = online:%v err:%v, want context error", online, healthErr)
			}
		})
	}
}

func TestDaemonHealthDistinguishesUnreachableFromReachableInvalidResponses(t *testing.T) {
	const realV064HealthPayload = `{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true,"uptime":"1h2m3s","version":"v0.64.0","components":[{"name":"database","healthy":true},{"name":"workers","healthy":true}],"recent_errors":null,"error_count_24h":0}`
	tests := []struct {
		name       string
		status     int
		body       string
		wantOnline bool
		wantErr    bool
	}{
		{name: "server error is reachable", status: http.StatusInternalServerError, body: `server error`, wantOnline: true, wantErr: true},
		{name: "malformed JSON is reachable", status: http.StatusOK, body: `{`, wantOnline: true, wantErr: true},
		{name: "trailing JSON is rejected", status: http.StatusOK, body: `{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true}{}`, wantOnline: true, wantErr: true},
		{name: "unknown field is rejected", status: http.StatusOK, body: `{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true,"unknown":true}`, wantOnline: true, wantErr: true},
		{name: "top-level array is rejected", status: http.StatusOK, body: `[{"healthy":true}]`, wantOnline: true, wantErr: true},
		{name: "top-level scalar is rejected", status: http.StatusOK, body: `7`, wantOnline: true, wantErr: true},
		{name: "top-level null is rejected", status: http.StatusOK, body: `null`, wantOnline: true, wantErr: true},
		{name: "empty object is rejected", status: http.StatusOK, body: `{}`, wantOnline: true, wantErr: true},
		{name: "missing healthy is rejected", status: http.StatusOK, body: `{"version":"v0.64.0"}`, wantOnline: true, wantErr: true},
		{name: "healthy null is rejected", status: http.StatusOK, body: `{"healthy":null}`, wantOnline: true, wantErr: true},
		{name: "healthy string is rejected", status: http.StatusOK, body: `{"healthy":"true"}`, wantOnline: true, wantErr: true},
		{name: "healthy number is rejected", status: http.StatusOK, body: `{"healthy":1}`, wantOnline: true, wantErr: true},
		{name: "healthy object is rejected", status: http.StatusOK, body: `{"healthy":{}}`, wantOnline: true, wantErr: true},
		{name: "healthy array is rejected", status: http.StatusOK, body: `{"healthy":[]}`, wantOnline: true, wantErr: true},
		{name: "schema missing", status: http.StatusOK, body: `{"healthy":true}`, wantOnline: true, wantErr: false},
		{name: "schema null", status: http.StatusOK, body: `{"$schema":null,"healthy":true}`, wantOnline: true, wantErr: true},
		{name: "schema number", status: http.StatusOK, body: `{"$schema":7,"healthy":true}`, wantOnline: true, wantErr: true},
		{name: "schema boolean", status: http.StatusOK, body: `{"$schema":true,"healthy":true}`, wantOnline: true, wantErr: true},
		{name: "schema object", status: http.StatusOK, body: `{"$schema":{},"healthy":true}`, wantOnline: true, wantErr: true},
		{name: "schema array", status: http.StatusOK, body: `{"$schema":[],"healthy":true}`, wantOnline: true, wantErr: true},
		{name: "schema arbitrary string", status: http.StatusOK, body: `{"$schema":"custom","healthy":true}`, wantOnline: true, wantErr: false},
		{name: "oversized body is rejected", status: http.StatusOK, body: strings.Repeat("x", int(runtimeControlResponseLimit)+1), wantOnline: true, wantErr: true},
		{name: "healthy response is online", status: http.StatusOK, body: `{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true}`, wantOnline: true, wantErr: false},
		{name: "real v0.64.0 shape with schema is online", status: http.StatusOK, body: realV064HealthPayload, wantOnline: true, wantErr: false},
		{name: "unhealthy response is online", status: http.StatusOK, body: `{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":false}`, wantOnline: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer srv.Close()
			client, err := NewClient("roborev", &runtimeCommandRunner{}, srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			online, healthErr := client.DaemonHealth(context.Background())
			if online != test.wantOnline || (healthErr != nil) != test.wantErr {
				t.Fatalf("health = online:%v err:%v", online, healthErr)
			}
		})
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	address := srv.URL
	srv.Close()
	client, err := NewClient("roborev", &runtimeCommandRunner{}, address)
	if err != nil {
		t.Fatal(err)
	}
	online, healthErr := client.DaemonHealth(context.Background())
	if online || healthErr != nil {
		t.Fatalf("closed server health = online:%v err:%v", online, healthErr)
	}
}

func TestDaemonHealthReportsUnhealthyDaemonSeparately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":false,"uptime":"1h2m3s","version":"v0.64.0","components":[{"name":"database","healthy":true},{"name":"workers","healthy":true}],"recent_errors":null,"error_count_24h":0}`))
	}))
	defer srv.Close()
	client, err := NewClient("roborev", &runtimeCommandRunner{}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	online, healthErr := client.DaemonHealth(context.Background())
	if !online || !errors.Is(healthErr, ErrDaemonUnhealthy) {
		t.Fatalf("health = online:%v err:%v, want reachable unhealthy daemon", online, healthErr)
	}
}
