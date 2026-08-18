package roborev

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

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
	if err := client.SetQueuePaused(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET /api/status", "POST /api/queue/pause"}; !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
}

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
		err = client.SetQueuePaused(context.Background(), true)
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
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	client, err := NewClient("roborev", &runtimeCommandRunner{}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.QueueStatus(context.Background())
	if !errors.Is(err, ErrDaemonOffline) || state.Known {
		t.Fatalf("offline result = %#v, %v", state, err)
	}
	srv.Close()
}

func TestDaemonHealthDistinguishesUnreachableFromReachableInvalidResponses(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantOnline bool
		wantErr    bool
	}{
		{name: "server error is reachable", status: http.StatusInternalServerError, body: `server error`, wantOnline: true, wantErr: true},
		{name: "malformed JSON is reachable", status: http.StatusOK, body: `{`, wantOnline: true, wantErr: true},
		{name: "unknown field is rejected", status: http.StatusOK, body: `{"healthy":true,"unknown":true}`, wantOnline: true, wantErr: true},
		{name: "oversized body is rejected", status: http.StatusOK, body: strings.Repeat("x", int(runtimeControlResponseLimit)+1), wantOnline: true, wantErr: true},
		{name: "healthy response is online", status: http.StatusOK, body: `{"healthy":true}`, wantOnline: true, wantErr: false},
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
