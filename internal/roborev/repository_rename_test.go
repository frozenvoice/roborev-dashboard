package roborev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"syscall"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/reporename"
)

type repositoryRenameRunner struct {
	renameCalls    int
	versionCalls   int
	started        bool
	exitErr        error
	versionOutputs []string
	versionIndex   int
}

func (r *repositoryRenameRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	if len(command.Args) == 2 && command.Args[0] == "version" {
		r.versionCalls++
		version := "v0.64.0"
		if len(r.versionOutputs) > 0 {
			version = r.versionOutputs[r.versionIndex]
			if r.versionIndex+1 < len(r.versionOutputs) {
				r.versionIndex++
			}
		}
		return proc.Result{Started: true, Stdout: fmt.Sprintf(`{"name":"roborev","version":%q}`, version)}, nil
	}
	if len(command.Args) == 4 && command.Args[0] == "repo" && command.Args[1] == "rename" {
		r.renameCalls++
		if command.Dir != "" || command.Timeout != repositoryRenameTimeout || command.OutputLimit != repositoryRenameOutputLimit {
			return proc.Result{Started: r.started}, nil
		}
		return proc.Result{Started: r.started, ExitCode: 1}, r.exitErr
	}
	return proc.Result{}, io.ErrUnexpectedEOF
}

func TestRepositoryRenamePreflightAndExecuteUseExactBoundedCommand(t *testing.T) {
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/repos" {
			_, _ = io.WriteString(w, `{"repos":[{"name":"old","root_path":"E:\\tracked repo","count":0}],"total_count":0}`)
			return
		}
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
	}))
	defer daemon.Close()
	runner := &repositoryRenameRunner{started: true}
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := reporename.Request{Path: `E:\tracked repo`, CurrentName: "old", NewName: "새 이름"}
	preflight, err := client.RepositoryRenamePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if preflight.Command.Dir != "" || preflight.Command.Executable != "roborev" {
		t.Fatalf("preflight command=%#v", preflight.Command)
	}
	want := []string{"repo", "rename", request.Path, request.NewName}
	for i := range want {
		if preflight.Command.Args[i] != want[i] {
			t.Fatalf("args=%#v", preflight.Command.Args)
		}
	}
	result, err := client.RepositoryRename(context.Background(), request, preflight)
	if err != nil || !result.Started || runner.renameCalls != 1 {
		t.Fatalf("result=%#v err=%v calls=%d", result, err, runner.renameCalls)
	}
}

func TestRepositoryRenameLeadingDashIsRejected(t *testing.T) {
	if _, err := reporename.BuildCommand("roborev", reporename.Request{Path: `E:\repo`, NewName: "-flag"}); err == nil {
		t.Fatal("leading dash name accepted")
	}
}

func TestRepositoryRenameCapabilityIsExactV064(t *testing.T) {
	if !capabilities("v0.64.0").RepositoryRename {
		t.Fatal("v0.64.0 repositoryRename capability is false")
	}
	if capabilities("v0.63.9").RepositoryRename || capabilities("v0.64.1").RepositoryRename || capabilities("v0.65.0").RepositoryRename {
		t.Fatal("repositoryRename capability escaped exact v0.64 gate")
	}
}

type repositoryRenameBoundedRunner struct{}

func (repositoryRenameBoundedRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	if len(command.Args) == 2 && command.Args[0] == "version" {
		return proc.Result{Started: true, Stdout: `{"name":"roborev","version":"v0.64.0"}`, StdoutTruncated: true}, nil
	}
	return proc.Result{}, io.ErrUnexpectedEOF
}

func TestRepositoryRenameRejectsBoundedVersionProbe(t *testing.T) {
	client, err := NewClient("roborev", repositoryRenameBoundedRunner{}, "http://127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RepositoryRenamePreflight(context.Background(), reporename.Request{Path: `E:\repo`, NewName: "new"}); err == nil {
		t.Fatal("accepted truncated version probe")
	}
}

func TestRepositoryRenameRevalidatesInventoryImmediatelyBeforeProcess(t *testing.T) {
	inventoryCalls := 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			inventoryCalls++
			_, _ = io.WriteString(w, `{"repos":[{"name":"old","root_path":"E:\\repo","count":0},{"name":"E:\\repo","root_path":"E:\\other","count":0}],"total_count":0}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	runner := &repositoryRenameRunner{started: true}
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := reporename.Request{Path: `E:\repo`, CurrentName: "old", NewName: "new"}
	preflight, err := client.RepositoryRenamePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RepositoryRename(context.Background(), request, preflight); err == nil {
		t.Fatal("accepted final fallback collision")
	}
	if runner.renameCalls != 0 || inventoryCalls == 0 {
		t.Fatalf("rename calls=%d inventory calls=%d", runner.renameCalls, inventoryCalls)
	}
}

func TestRepositoryRenameCurrentNameBindingIncludesEmptyNames(t *testing.T) {
	request := reporename.Request{Path: `E:\repo`, CurrentName: "", NewName: "new"}
	if err := validateRenameInventory(repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "", RootPath: request.Path}}}, request); err != nil {
		t.Fatalf("empty current name rejected: %v", err)
	}
	if err := validateRenameInventory(repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "external", RootPath: request.Path}}}, request); !errors.Is(err, ErrRepositoryRenameStale) {
		t.Fatalf("empty current-name binding drift error=%v", err)
	}
}

func TestRepositoryRenameConfirmCallBudgetAndReadback(t *testing.T) {
	healthCalls, inventoryCalls := 0, 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			healthCalls++
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			inventoryCalls++
			if inventoryCalls == 1 {
				_, _ = io.WriteString(w, `{"repos":[{"name":"old","root_path":"E:\\repo","count":0}],"total_count":0}`)
			} else {
				_, _ = io.WriteString(w, `{"repos":[{"name":"new","root_path":"E:\\repo","count":0}],"total_count":0}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	runner := &repositoryRenameRunner{started: true}
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := reporename.Request{Path: `E:\repo`, CurrentName: "old", NewName: "new"}
	preview, err := client.RepositoryRenamePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	runner.versionCalls, runner.renameCalls, healthCalls, inventoryCalls = 0, 0, 0, 0
	result, err := client.RepositoryRename(context.Background(), request, preview)
	if err != nil || result != (reporename.ExecutionResult{Started: true, Verified: true}) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if runner.versionCalls != 1 || healthCalls != 1 || inventoryCalls != 2 || runner.renameCalls != 1 {
		t.Fatalf("version=%d health=%d repos=%d rename=%d; want 1/1/2/1", runner.versionCalls, healthCalls, inventoryCalls, runner.renameCalls)
	}
}

func TestRepositoryRenameReadbackBindingDriftIsUnverified(t *testing.T) {
	var client *Client
	inventoryCalls := 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			inventoryCalls++
			if inventoryCalls == 2 {
				client.bindingMu.Lock()
				client.binding.generation++
				client.bindingMu.Unlock()
			}
			name := "old"
			if inventoryCalls == 2 {
				name = "new"
			}
			_, _ = io.WriteString(w, fmt.Sprintf(`{"repos":[{"name":%q,"root_path":"E:\\repo","count":0}],"total_count":0}`, name))
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	runner := &repositoryRenameRunner{started: true}
	var err error
	client, err = NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := reporename.Request{Path: `E:\repo`, CurrentName: "old", NewName: "new"}
	preview, err := client.RepositoryRenamePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.RepositoryRename(context.Background(), request, preview)
	if err != nil || result != (reporename.ExecutionResult{Started: true}) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestRepositoryRenameReadbackOutcomeMatrix(t *testing.T) {
	base := `{"repos":[{"name":"old","root_path":"E:\\repo","count":0}],"total_count":0}`
	tests := []struct {
		name       string
		after      string
		status     int
		exitErr    error
		wantVerify bool
	}{
		{name: "exit zero new", after: `{"repos":[{"name":"new","root_path":"E:\\repo","count":0}],"total_count":0}`, wantVerify: true},
		{name: "exit nonzero new", after: `{"repos":[{"name":"new","root_path":"E:\\repo","count":0}],"total_count":0}`, exitErr: errors.New("exit 1"), wantVerify: true},
		{name: "unavailable", status: http.StatusServiceUnavailable},
		{name: "malformed", after: `{bad`, wantVerify: false},
		{name: "missing", after: `{"repos":[],"total_count":0}`},
		{name: "duplicate", after: `{"repos":[{"name":"new","root_path":"E:\\repo","count":0},{"name":"new","root_path":"E:\\repo","count":0}],"total_count":0}`},
		{name: "old", after: base},
		{name: "third", after: `{"repos":[{"name":"third","root_path":"E:\\repo","count":0}],"total_count":0}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			healthCalls, inventoryCalls := 0, 0
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/health":
					healthCalls++
					_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
				case "/api/repos":
					inventoryCalls++
					if inventoryCalls == 1 {
						_, _ = io.WriteString(w, base)
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
			runner := &repositoryRenameRunner{started: true, exitErr: test.exitErr}
			client, err := NewClient("roborev", runner, daemon.URL)
			if err != nil {
				t.Fatal(err)
			}
			request := reporename.Request{Path: `E:\repo`, CurrentName: "old", NewName: "new"}
			preview, err := client.RepositoryRenamePreflight(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			runner.versionCalls, runner.renameCalls, healthCalls, inventoryCalls = 0, 0, 0, 0
			result, err := client.RepositoryRename(context.Background(), request, preview)
			if err != nil || result.Started != true || result.Verified != test.wantVerify {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if runner.versionCalls != 1 || healthCalls != 1 || inventoryCalls != 2 || runner.renameCalls != 1 {
				t.Fatalf("version=%d health=%d repos=%d rename=%d; want 1/1/2/1", runner.versionCalls, healthCalls, inventoryCalls, runner.renameCalls)
			}
		})
	}
}

func TestRepositoryRenamePreProcessTargetDriftDoesNotLaunch(t *testing.T) {
	tests := []struct {
		name      string
		inventory string
		want      error
	}{
		{
			name:      "current name drift",
			inventory: `{"repos":[{"name":"renamed elsewhere","root_path":"E:\\repo","count":0}],"total_count":0}`,
			want:      ErrRepositoryRenameStale,
		},
		{
			name:      "target missing",
			inventory: `{"repos":[{"name":"other","root_path":"E:\\other","count":0}],"total_count":0}`,
			want:      ErrRepositoryRenameStale,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/health":
					_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
				case "/api/repos":
					_, _ = io.WriteString(w, test.inventory)
				default:
					http.NotFound(w, r)
				}
			}))
			defer daemon.Close()
			runner := &repositoryRenameRunner{started: true}
			client, err := NewClient("roborev", runner, daemon.URL)
			if err != nil {
				t.Fatal(err)
			}
			request := reporename.Request{Path: `E:\repo`, CurrentName: "old", NewName: "new"}
			preview, err := client.RepositoryRenamePreflight(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			runner.versionCalls = 0
			result, err := client.RepositoryRename(context.Background(), request, preview)
			if !errors.Is(err, test.want) || result.Started || runner.renameCalls != 0 {
				t.Fatalf("result=%#v err=%v renameCalls=%d", result, err, runner.renameCalls)
			}
		})
	}
}

func TestRepositoryRenamePreProcessInventoryUnavailableDoesNotLaunch(t *testing.T) {
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			_, _ = io.WriteString(w, `{"repos":[{"name":"old","root_path":"E:\\repo","count":0}],"total_count":0}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	runner := &repositoryRenameRunner{started: true}
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP.Transport = repositoryRenameFailingInventoryTransport{base: http.DefaultTransport}
	request := reporename.Request{Path: `E:\repo`, CurrentName: "old", NewName: "new"}
	preview, err := client.RepositoryRenamePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.RepositoryRename(context.Background(), request, preview)
	if !errors.Is(err, ErrRepositoryRenameOffline) || result.Started || runner.renameCalls != 0 {
		t.Fatalf("result=%#v err=%v renameCalls=%d", result, err, runner.renameCalls)
	}
}

type repositoryRenameFailingInventoryTransport struct {
	base http.RoundTripper
}

func (t repositoryRenameFailingInventoryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path == "/api/repos" {
		return nil, &url.Error{Op: request.Method, URL: request.URL.String(), Err: syscall.ECONNREFUSED}
	}
	return t.base.RoundTrip(request)
}

func TestRepositoryRenameCLIAndDaemonVersionDriftDoNotLaunch(t *testing.T) {
	tests := []struct {
		name          string
		runner        *repositoryRenameRunner
		healthVersion func(int) string
		want          error
	}{
		{
			name:   "CLI version drift",
			runner: &repositoryRenameRunner{started: true, versionOutputs: []string{"v0.64.0", "v0.63.0"}},
			healthVersion: func(_ int) string {
				return "v0.64.0"
			},
			want: ErrUnsupportedRepositoryRename,
		},
		{
			name:   "daemon version drift",
			runner: &repositoryRenameRunner{started: true},
			healthVersion: func(call int) string {
				if call == 1 {
					return "v0.64.0"
				}
				return "v0.63.0"
			},
			want: ErrUnsupportedRepositoryRename,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			healthCalls := 0
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/health":
					healthCalls++
					_, _ = io.WriteString(w, fmt.Sprintf(`{"healthy":true,"version":%q}`, test.healthVersion(healthCalls)))
				case "/api/repos":
					_, _ = io.WriteString(w, `{"repos":[{"name":"old","root_path":"E:\\repo","count":0}],"total_count":0}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer daemon.Close()
			client, err := NewClient("roborev", test.runner, daemon.URL)
			if err != nil {
				t.Fatal(err)
			}
			request := reporename.Request{Path: `E:\repo`, CurrentName: "old", NewName: "new"}
			preview, err := client.RepositoryRenamePreflight(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			test.runner.versionCalls = 0
			result, err := client.RepositoryRename(context.Background(), request, preview)
			if !errors.Is(err, test.want) || result.Started || test.runner.renameCalls != 0 {
				t.Fatalf("result=%#v err=%v renameCalls=%d", result, err, test.runner.renameCalls)
			}
		})
	}
}

func TestRepositoryRenamePreProcessBindingGenerationDriftDoesNotLaunch(t *testing.T) {
	var client *Client
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			client.bindingMu.Lock()
			client.binding.generation++
			client.bindingMu.Unlock()
			_, _ = io.WriteString(w, `{"repos":[{"name":"old","root_path":"E:\\repo","count":0}],"total_count":0}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	runner := &repositoryRenameRunner{started: true}
	var err error
	client, err = NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := reporename.Request{Path: `E:\repo`, CurrentName: "old", NewName: "new"}
	preview, err := client.RepositoryRenamePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.RepositoryRename(context.Background(), request, preview)
	if !errors.Is(err, ErrBindingChanged) || result.Started || runner.renameCalls != 0 {
		t.Fatalf("result=%#v err=%v renameCalls=%d", result, err, runner.renameCalls)
	}
}

func TestRepositoryRenamePreProcessMismatchesDoNotLaunch(t *testing.T) {
	// The explicit command/binding mismatch is the important process-boundary
	// guard; current-name/target drift and fallback ambiguity are covered by
	// the inventory revalidation tests above.
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			_, _ = io.WriteString(w, `{"repos":[{"name":"old","root_path":"E:\\repo","count":0}],"total_count":0}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	runner := &repositoryRenameRunner{started: true}
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := reporename.Request{Path: `E:\repo`, CurrentName: "old", NewName: "new"}
	preview, err := client.RepositoryRenamePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	preview.Binding = "stale-binding"
	if result, err := client.RepositoryRename(context.Background(), request, preview); !errors.Is(err, ErrBindingChanged) || result.Started || runner.renameCalls != 0 {
		t.Fatalf("result=%#v err=%v renameCalls=%d", result, err, runner.renameCalls)
	}
}
