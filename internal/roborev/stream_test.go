package roborev

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type streamGateRunner struct {
	version     string
	mu          sync.Mutex
	streamCalls int
}

func (r *streamGateRunner) Run(context.Context, proc.Command) (proc.Result, error) {
	version := r.version
	if version == "" {
		version = "v0.64.0"
	}
	return proc.Result{Stdout: `{"name":"roborev","version":"` + version + `"}`}, nil
}

type captureStreamRunner struct {
	mu      sync.Mutex
	command proc.StreamCommand
	stream  proc.Stream
	calls   int
}

func (r *captureStreamRunner) StartStream(_ context.Context, command proc.StreamCommand) (proc.Stream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.command = command
	r.calls++
	return r.stream, nil
}

func TestStreamArgsExactContract(t *testing.T) {
	args, err := StreamArgs(`C:\registered repo`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"stream", "--repo", `C:\registered repo`}
	if !equalStrings(args, want) {
		t.Fatalf("args=%#v want=%#v", args, want)
	}
	if _, err := StreamArgs("C:\\registered" + string([]byte{0}) + "repo"); err == nil {
		t.Fatal("control path accepted")
	}
}

func TestOpenStreamCapabilityAndHealthyDaemonGate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true,"uptime":"1h2m3s","version":"v0.64.0","components":[{"name":"database","healthy":true},{"name":"workers","healthy":true}],"recent_errors":null,"error_count_24h":0}`))
	}))
	defer server.Close()
	commands := &streamGateRunner{}
	streams := &captureStreamRunner{stream: noopStream{}}
	client, err := NewClient(`C:\trusted\roborev.exe`, commands, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.StreamRunner = streams
	if _, err := client.OpenStream(context.Background(), `C:\registered repo`); err != nil {
		t.Fatal(err)
	}
	streams.mu.Lock()
	command := streams.command
	streams.mu.Unlock()
	if !equalStrings(command.Args, []string{"stream", "--repo", `C:\registered repo`}) || command.Executable != `C:\trusted\roborev.exe` || command.Dir != "" {
		t.Fatalf("command=%#v", command)
	}
	if streams.calls != 1 {
		t.Fatalf("stream calls=%d", streams.calls)
	}
}

func TestOpenStreamUnsupportedAndOfflineNeverSpawns(t *testing.T) {
	unsupportedRunner := &streamGateRunner{version: "v0.65.0"}
	streamRunner := &captureStreamRunner{stream: noopStream{}}
	client, err := NewClient(`C:\trusted\roborev.exe`, unsupportedRunner, "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	client.StreamRunner = streamRunner
	if _, err := client.OpenStream(context.Background(), `C:\repo`); !errors.Is(err, ErrStreamUnsupported) {
		t.Fatalf("unsupported err=%v", err)
	}
	if streamRunner.calls != 0 {
		t.Fatal("unsupported version spawned stream")
	}

	offlineClient, err := NewClient(`C:\trusted\roborev.exe`, &streamGateRunner{}, "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	offlineClient.StreamRunner = streamRunner
	if _, err := offlineClient.OpenStream(context.Background(), `C:\repo`); !errors.Is(err, ErrStreamOffline) {
		t.Fatalf("offline err=%v", err)
	}
	if streamRunner.calls != 0 {
		t.Fatal("offline daemon spawned stream")
	}
}

func TestOpenStreamRequiresExactMatchingSupportedDaemonVersion(t *testing.T) {
	cases := []struct {
		name          string
		cliVersion    string
		daemonVersion string
		healthy       bool
		want          error
	}{
		{name: "daemon mismatch", cliVersion: "v0.64.0", daemonVersion: "v0.65.0", healthy: true, want: ErrStreamUnsupported},
		{name: "daemon empty", cliVersion: "v0.64.0", daemonVersion: "", healthy: true, want: ErrStreamUnsupported},
		{name: "daemon malformed", cliVersion: "v0.64.0", daemonVersion: "64", healthy: true, want: ErrStreamUnsupported},
		{name: "daemon unhealthy", cliVersion: "v0.64.0", daemonVersion: "v0.64.0", healthy: false, want: ErrStreamUnhealthy},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":%t,"uptime":"1h2m3s","version":%q,"components":[],"recent_errors":null,"error_count_24h":0}`, testCase.healthy, testCase.daemonVersion)
			}))
			defer server.Close()
			runner := &streamGateRunner{version: testCase.cliVersion}
			streams := &captureStreamRunner{stream: noopStream{}}
			client, err := NewClient(`C:\trusted\roborev.exe`, runner, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client.StreamRunner = streams
			_, err = client.OpenStream(context.Background(), `C:\repo`)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("error=%v, want %v", err, testCase.want)
			}
			if streams.calls != 0 {
				t.Fatalf("stream calls=%d, want zero", streams.calls)
			}
		})
	}
}

type noopStream struct{}

func (noopStream) Lines() <-chan []byte { return make(chan []byte) }
func (noopStream) Wait() error          { return nil }
func (noopStream) Close() error         { return nil }

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
