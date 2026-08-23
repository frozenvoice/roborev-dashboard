package roborev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/repomove"
)

type repositoryMoveRunner struct {
	versionCalls int
	moveCalls    int
	started      bool
	exitErr      error
	last         proc.Command
	versions     []string
	versionIndex int
}

func (r *repositoryMoveRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.last = command
	if len(command.Args) == 2 && command.Args[0] == "version" && command.Args[1] == "--json" {
		r.versionCalls++
		version := "v0.64.0"
		if len(r.versions) > 0 {
			version = r.versions[r.versionIndex]
			if r.versionIndex+1 < len(r.versions) {
				r.versionIndex++
			}
		}
		return proc.Result{Started: true, Stdout: fmt.Sprintf(`{"name":"roborev","version":%q}`, version)}, nil
	}
	if len(command.Args) == 4 && command.Args[0] == "repo" && command.Args[1] == "move" {
		r.moveCalls++
		return proc.Result{Started: r.started, ExitCode: 1}, r.exitErr
	}
	return proc.Result{}, errors.New("unexpected command")
}

type moveAbsentFS struct{}

func (moveAbsentFS) Lstat(string) (os.FileInfo, error) { return nil, os.ErrNotExist }

type moveSequenceFS struct {
	mu     sync.Mutex
	calls  int
	absent int
	info   os.FileInfo
}

func (f *moveSequenceFS) Lstat(string) (os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.absent {
		return nil, os.ErrNotExist
	}
	return f.info, nil
}

func TestRepositoryMovePreProcessDriftMatrixDoesNotLaunch(t *testing.T) {
	source, _ := filepath.Abs(filepath.Join("stale", "drift-source"))
	destination, _ := filepath.Abs(filepath.Join("new", "drift-destination"))
	base := func() string {
		return fmt.Sprintf(`{"repos":[{"name":"old","root_path":%q,"count":0}],"total_count":0}`, source)
	}
	tests := []struct {
		name             string
		inventory        func() string
		versions         []string
		healthVersions   []string
		fs               repomove.FileSystem
		bindingDrift     bool
		wantPreviewError bool
	}{
		{name: "source missing", inventory: func() string { return `{"repos":[],"total_count":0}` }, wantPreviewError: true},
		{name: "source name changed", inventory: func() string {
			return fmt.Sprintf(`{"repos":[{"name":"changed","root_path":%q,"count":0}],"total_count":0}`, source)
		}, wantPreviewError: true},
		{name: "destination conflict", inventory: func() string {
			return fmt.Sprintf(`{"repos":[{"name":"old","root_path":%q,"count":0},{"name":"new","root_path":%q,"count":0}],"total_count":0}`, source, destination)
		}, wantPreviewError: true},
		{name: "fallback collision", inventory: func() string {
			key, _ := repomove.SourceFallbackKey(source)
			return fmt.Sprintf(`{"repos":[{"name":"old","root_path":%q,"count":0},{"name":%q,"root_path":%q,"count":0}],"total_count":0}`, source, key, filepath.Join(filepath.Dir(destination), "other"))
		}, wantPreviewError: true},
		{name: "source reappears", inventory: func() string { return base() }, fs: &moveSequenceFS{absent: 2, info: moveExistsInfo{}}, wantPreviewError: false},
		{name: "binding changed", inventory: func() string { return base() }, bindingDrift: true, wantPreviewError: true},
		{name: "CLI version drift", inventory: func() string { return base() }, versions: []string{"v0.64.0", "v0.63.0"}, wantPreviewError: false},
		{name: "daemon version drift", inventory: func() string { return base() }, healthVersions: []string{"v0.64.0", "v0.63.0"}, wantPreviewError: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			healthCalls, inventoryCalls := 0, 0
			var client *Client
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/health":
					healthCalls++
					version := "v0.64.0"
					if len(test.healthVersions) > 0 {
						index := healthCalls - 1
						if index >= len(test.healthVersions) {
							index = len(test.healthVersions) - 1
						}
						version = test.healthVersions[index]
					}
					_, _ = io.WriteString(w, fmt.Sprintf(`{"healthy":true,"version":%q}`, version))
				case "/api/repos":
					inventoryCalls++
					_, _ = io.WriteString(w, test.inventory())
					if test.bindingDrift && client != nil {
						client.bindingMu.Lock()
						client.binding.generation++
						client.bindingMu.Unlock()
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer daemon.Close()
			runner := &repositoryMoveRunner{started: true, versions: test.versions}
			var err error
			client, err = NewClient("roborev", runner, daemon.URL)
			if err != nil {
				t.Fatal(err)
			}
			if test.fs != nil {
				client.repositoryMoveFS = test.fs
			} else {
				client.repositoryMoveFS = moveAbsentFS{}
			}
			request := repomove.Request{SourcePath: source, SourceName: "old", DestinationPath: destination}
			preflight, preflightErr := client.RepositoryMovePreflight(context.Background(), request)
			if test.wantPreviewError {
				if preflightErr == nil || runner.moveCalls != 0 {
					t.Fatalf("preview err=%v move=%d", preflightErr, runner.moveCalls)
				}
				return
			}
			if preflightErr != nil {
				t.Fatal(preflightErr)
			}
			result, moveErr := client.RepositoryMove(context.Background(), request, preflight)
			if result.Started || moveErr == nil || runner.moveCalls != 0 {
				t.Fatalf("result=%#v err=%v move=%d health=%d inventory=%d", result, moveErr, runner.moveCalls, healthCalls, inventoryCalls)
			}
		})
	}
}

// These cases deliberately complete a successful preview preflight first.
// The second, confirm-owned preflight then observes the drift and must fail
// before the move process is accepted.
func TestRepositoryMoveConfirmFreshPreflightCatchesDriftBeforeProcess(t *testing.T) {
	source, _ := filepath.Abs(filepath.Join("stale", "confirm-drift-source"))
	destination, _ := filepath.Abs(filepath.Join("new", "confirm-drift-destination"))
	base := fmt.Sprintf(`{"repos":[{"name":"old","root_path":%q,"count":0}],"total_count":0}`, source)
	fallbackKey, err := repomove.SourceFallbackKey(source)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		drift     string
		want      error
		bindDrift bool
	}{
		{name: "source missing", drift: `{"repos":[],"total_count":0}`, want: ErrRepositoryMoveStale},
		{name: "source name changed", drift: fmt.Sprintf(`{"repos":[{"name":"changed","root_path":%q,"count":0}],"total_count":0}`, source), want: ErrRepositoryMoveStale},
		{name: "destination newly tracked", drift: fmt.Sprintf(`{"repos":[{"name":"old","root_path":%q,"count":0},{"name":"new","root_path":%q,"count":0}],"total_count":0}`, source, destination), want: ErrRepositoryMoveConflict},
		{name: "exact fallback collision", drift: fmt.Sprintf(`{"repos":[{"name":"old","root_path":%q,"count":0},{"name":%q,"root_path":%q,"count":0}],"total_count":0}`, source, fallbackKey, filepath.Join(filepath.Dir(destination), "other")), want: ErrRepositoryMoveAmbiguous},
		{name: "binding generation changed", drift: base, want: ErrBindingChanged, bindDrift: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inventoryCalls := 0
			var client *Client
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/health":
					_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
				case "/api/repos":
					inventoryCalls++
					body := base
					if inventoryCalls >= 2 {
						body = test.drift
					}
					_, _ = io.WriteString(w, body)
					if test.bindDrift && inventoryCalls >= 2 {
						client.bindingMu.Lock()
						client.binding.generation++
						client.bindingMu.Unlock()
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer daemon.Close()
			runner := &repositoryMoveRunner{started: true}
			client, err = NewClient("roborev", runner, daemon.URL)
			if err != nil {
				t.Fatal(err)
			}
			client.repositoryMoveFS = moveAbsentFS{}
			request := repomove.Request{SourcePath: source, SourceName: "old", DestinationPath: destination}
			preview, err := client.RepositoryMovePreflight(context.Background(), request)
			if err != nil {
				t.Fatalf("preview preflight failed: %v", err)
			}
			result, confirmErr := client.RepositoryMove(context.Background(), request, preview)
			if !errors.Is(confirmErr, test.want) || result.Started || runner.moveCalls != 0 {
				t.Fatalf("result=%#v err=%v want errors.Is(...,%v), process=%d inventory=%d", result, confirmErr, test.want, runner.moveCalls, inventoryCalls)
			}
		})
	}
}

type moveExistsInfo struct{}

func (moveExistsInfo) Name() string       { return "source" }
func (moveExistsInfo) Size() int64        { return 0 }
func (moveExistsInfo) Mode() os.FileMode  { return 0 }
func (moveExistsInfo) ModTime() time.Time { return time.Time{} }
func (moveExistsInfo) IsDir() bool        { return false }
func (moveExistsInfo) Sys() any           { return nil }

func TestRepositoryMoveCapabilityIsExactV064(t *testing.T) {
	cap := capabilities("v0.64.0")
	if !cap.RepositoryInventory || !cap.RepositoryRename || !cap.RepositoryMove {
		t.Fatalf("v0.64.0 repository capabilities=%#v; want inventory/rename/move all true", cap)
	}
	for _, version := range []string{"v0.63.0", "v0.64.1", "v0.65.0"} {
		cap = capabilities(version)
		if cap.RepositoryInventory || cap.RepositoryRename || cap.RepositoryMove {
			t.Fatalf("repository capabilities escaped exact v0.64 gate for %s: %#v", version, cap)
		}
	}
}

func TestRepositoryMoveConfirmCallBudgetAndReadback(t *testing.T) {
	source, _ := filepath.Abs(filepath.Join("stale", "project"))
	destination, _ := filepath.Abs(filepath.Join("new", "project"))
	healthCalls, inventoryCalls := 0, 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/health":
			healthCalls++
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			inventoryCalls++
			if inventoryCalls == 1 {
				_, _ = io.WriteString(w, fmt.Sprintf(`{"repos":[{"name":"old","root_path":%q,"count":4}],"total_count":4}`, source))
			} else {
				_, _ = io.WriteString(w, fmt.Sprintf(`{"repos":[{"name":"old","root_path":%q,"count":4}],"total_count":4}`, destination))
			}
		default:
			http.NotFound(w, request)
		}
	}))
	defer daemon.Close()
	runner := &repositoryMoveRunner{started: true, exitErr: errors.New("exit 1")}
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.repositoryMoveFS = moveAbsentFS{}
	request := repomove.Request{SourcePath: source, SourceName: "old", DestinationPath: destination}
	preflight, err := client.RepositoryMovePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	runner.versionCalls, runner.moveCalls, healthCalls, inventoryCalls = 0, 0, 0, 0
	result, err := client.RepositoryMove(context.Background(), request, preflight)
	if err != nil || result != (repomove.ExecutionResult{Started: true, Verified: true}) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if runner.versionCalls != 1 || healthCalls != 1 || inventoryCalls != 2 || runner.moveCalls != 1 {
		t.Fatalf("version=%d health=%d repos=%d move=%d; want 1/1/2/1", runner.versionCalls, healthCalls, inventoryCalls, runner.moveCalls)
	}
	if runner.last.Dir != destination || runner.last.Timeout != repositoryMoveTimeout || runner.last.OutputLimit != repositoryMoveOutputLimit || fmt.Sprint(runner.last.Args) != fmt.Sprint([]string{"repo", "move", source, "."}) {
		t.Fatalf("command=%#v", runner.last)
	}
}

func TestRepositoryMoveStartedFalseDoesNotReadback(t *testing.T) {
	source, _ := filepath.Abs(filepath.Join("stale", "project"))
	destination, _ := filepath.Abs(filepath.Join("new", "project"))
	inventoryCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
			return
		}
		inventoryCalls++
		_, _ = io.WriteString(w, fmt.Sprintf(`{"repos":[{"name":"old","root_path":%q,"count":0}],"total_count":0}`, source))
	}))
	defer server.Close()
	runner := &repositoryMoveRunner{started: false}
	client, err := NewClient("roborev", runner, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.repositoryMoveFS = moveAbsentFS{}
	request := repomove.Request{SourcePath: source, SourceName: "old", DestinationPath: destination}
	preview, err := client.RepositoryMovePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	inventoryCalls = 0
	if result, err := client.RepositoryMove(context.Background(), request, preview); result.Started || err == nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if inventoryCalls != 1 {
		t.Fatalf("started=false performed post-write readback: inventory calls=%d", inventoryCalls)
	}
}

func TestRepositoryMoveReadbackOutcomeMatrix(t *testing.T) {
	source, _ := filepath.Abs(filepath.Join("stale", "matrix-source"))
	destination, _ := filepath.Abs(filepath.Join("new", "matrix-destination"))
	before := fmt.Sprintf(`{"repos":[{"name":"old","root_path":%q,"count":0}],"total_count":0}`, source)
	valid := fmt.Sprintf(`{"repos":[{"name":"old","root_path":%q,"count":0}],"total_count":0}`, destination)
	tests := []struct {
		name       string
		after      string
		status     int
		exitErr    error
		wantVerify bool
	}{
		{name: "exit zero moved", after: valid, wantVerify: true},
		{name: "exit nonzero moved", after: valid, exitErr: errors.New("exit 1"), wantVerify: true},
		{name: "readback unavailable", status: http.StatusServiceUnavailable},
		{name: "destination missing", after: `{"repos":[],"total_count":0}`},
		{name: "unexpected name", after: fmt.Sprintf(`{"repos":[{"name":"other","root_path":%q,"count":0}],"total_count":0}`, destination)},
		{name: "source present", after: before},
		{name: "both present", after: fmt.Sprintf(`{"repos":[{"name":"old","root_path":%q,"count":0},{"name":"old","root_path":%q,"count":0}],"total_count":0}`, source, destination)},
		{name: "duplicate destination", after: fmt.Sprintf(`{"repos":[{"name":"old","root_path":%q,"count":0},{"name":"old","root_path":%q,"count":0}],"total_count":0}`, destination, destination)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inventoryCalls := 0
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/health":
					_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
				case "/api/repos":
					inventoryCalls++
					if inventoryCalls == 1 {
						_, _ = io.WriteString(w, before)
						return
					}
					if test.status != 0 {
						w.WriteHeader(test.status)
						return
					}
					_, _ = io.WriteString(w, test.after)
				default:
					http.NotFound(w, r)
				}
			}))
			defer daemon.Close()
			runner := &repositoryMoveRunner{started: true, exitErr: test.exitErr}
			client, err := NewClient("roborev", runner, daemon.URL)
			if err != nil {
				t.Fatal(err)
			}
			client.repositoryMoveFS = moveAbsentFS{}
			request := repomove.Request{SourcePath: source, SourceName: "old", DestinationPath: destination}
			preflight, err := client.RepositoryMovePreflight(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			inventoryCalls = 0
			runner.versionCalls, runner.moveCalls = 0, 0
			result, err := client.RepositoryMove(context.Background(), request, preflight)
			if err != nil || result.Started != true || result.Verified != test.wantVerify {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if inventoryCalls != 2 || runner.moveCalls != 1 {
				t.Fatalf("inventory=%d move=%d; want 2/1", inventoryCalls, runner.moveCalls)
			}
		})
	}
}
