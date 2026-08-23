package roborev

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/reporename"
)

type repositoryRenameRunner struct {
	renameCalls int
	started     bool
	exitErr     error
}

func (r *repositoryRenameRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	if len(command.Args) == 2 && command.Args[0] == "version" {
		return proc.Result{Started: true, Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
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
