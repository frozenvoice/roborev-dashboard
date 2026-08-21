package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

type fakeRegistry struct {
	repository gitx.Repository
	selected   string
}

func (r *fakeRegistry) ResolveIdentity(identity string) (string, error) {
	if identity != "repo-key" {
		return "", errors.New("repository is not registered")
	}
	return r.repository.Path, nil
}

func (r *fakeRegistry) InspectIdentity(context.Context, string) (gitx.Repository, error) {
	return r.repository, nil
}

func (r *fakeRegistry) Identity(path string) (string, error) {
	if path != r.repository.Path {
		return "", errors.New("unknown repository")
	}
	return "repo-key", nil
}

func (r *fakeRegistry) Selected() string { return r.selected }

func (r *fakeRegistry) AcquireSelected(identity string) (func(), error) {
	if identity != "repo-key" || r.selected != r.repository.Path {
		return nil, errors.New("selected repository changed")
	}
	return func() {}, nil
}

type fakeService struct {
	installation roborev.Installation
	binding      string
	types        []roborev.AnalysisType
	prompt       string
	receipt      roborev.AnalysisReceipt
	request      roborev.AnalyzeRequest
	analyzeCalls int
	analyzeStart chan struct{}
	analyzeWait  chan struct{}
	analyzeErr   error
}

func (f *fakeService) Installation(context.Context) roborev.Installation { return f.installation }
func (f *fakeService) BindingIdentity() string                           { return f.binding }
func (f *fakeService) AnalysisTypes(context.Context) ([]roborev.AnalysisType, error) {
	return append([]roborev.AnalysisType(nil), f.types...), nil
}
func (f *fakeService) AnalysisPrompt(context.Context, string) (string, error) { return f.prompt, nil }
func (f *fakeService) Analyze(ctx context.Context, request roborev.AnalyzeRequest) (roborev.AnalysisReceipt, error) {
	f.analyzeCalls++
	f.request = request
	if f.analyzeStart != nil {
		close(f.analyzeStart)
	}
	if f.analyzeWait != nil {
		select {
		case <-f.analyzeWait:
		case <-ctx.Done():
			return roborev.AnalysisReceipt{}, ctx.Err()
		}
	}
	if f.analyzeErr != nil {
		return roborev.AnalysisReceipt{}, f.analyzeErr
	}
	return f.receipt, nil
}

func testManager(t *testing.T) (*Manager, *fakeRegistry, *fakeService) {
	t.Helper()
	registry := &fakeRegistry{repository: gitx.Repository{Path: `C:\repo`, Head: "head-1", Branch: "main"}, selected: `C:\repo`}
	service := &fakeService{binding: "binding-1", installation: roborev.Installation{Installed: true, Binary: `C:\tools\roborev.exe`, Version: roborevcompat.Manifest064().Version, Status: json.RawMessage(`{"running":true,"known":true}`), Capabilities: roborev.Capabilities{AnalyzeCommand: true}}, types: []roborev.AnalysisType{{Name: "refactor", Description: "Suggest refactoring opportunities"}}, prompt: "prompt", receipt: roborev.AnalysisReceipt{Jobs: []roborev.AnalysisJob{{ID: 17, Agent: "codex"}}, AnalysisType: "refactor", Files: []string{"./..."}}}
	manager, err := NewManager(Dependencies{Registry: registry, RoboRev: service, Gate: operation.NewGate()})
	if err != nil {
		t.Fatal(err)
	}
	return manager, registry, service
}

func TestPrepareBuildsExactBranchAndRepositoryCommands(t *testing.T) {
	manager, _, _ := testManager(t)
	branch, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: "repo-key", AnalysisType: "refactor", Scope: roborev.AnalysisScopeBranch})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := branch.Command.Args, []string{"analyze", "refactor", "--branch=HEAD", "--json"}; !equalStrings(got, want) {
		t.Fatalf("branch args = %#v, want %#v", got, want)
	}
	repository, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: "repo-key", AnalysisType: "refactor", Scope: roborev.AnalysisScopeRepository})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := repository.Command.Args, []string{"analyze", "refactor", "./...", "--json"}; !equalStrings(got, want) {
		t.Fatalf("repository args = %#v, want %#v", got, want)
	}
	for _, arg := range repository.Command.Args {
		for _, forbidden := range []string{"--fix", "--per-file", "--agent", "--model", "--reasoning", "--wait"} {
			if arg == forbidden {
				t.Fatalf("forbidden argument %q in %#v", forbidden, repository.Command.Args)
			}
		}
	}
}

func TestStartFreshValidatesPreviewAndReleasesLeasesAfterReceipt(t *testing.T) {
	manager, registry, service := testManager(t)
	preview, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: "repo-key", AnalysisType: "refactor", Scope: roborev.AnalysisScopeRepository})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: "repo-key", AnalysisType: "refactor", Scope: roborev.AnalysisScopeRepository}, PreviewToken: preview.PreviewToken})
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.Jobs[0].ID != 17 || service.analyzeCalls != 1 {
		t.Fatalf("result = %#v, calls = %d", result, service.analyzeCalls)
	}
	if service.request.RepositoryPath != registry.repository.Path || service.request.Scope != roborev.AnalysisScopeRepository {
		t.Fatalf("analyze request = %#v", service.request)
	}
	if snapshot := manager.gate.SnapshotDetailed(); snapshot.Analyses != 0 || snapshot.RepositoryLeases != 0 {
		t.Fatalf("leases were not released: %#v", snapshot)
	}
}

func TestStartRejectsStaleHeadAndOfflineDaemon(t *testing.T) {
	manager, registry, service := testManager(t)
	preview, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: "repo-key", AnalysisType: "refactor", Scope: roborev.AnalysisScopeBranch})
	if err != nil {
		t.Fatal(err)
	}
	registry.repository.Head = "head-2"
	var typedErr *Error
	if _, err := manager.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: "repo-key", AnalysisType: "refactor", Scope: roborev.AnalysisScopeBranch}, PreviewToken: preview.PreviewToken}); !errors.As(err, &typedErr) {
		t.Fatalf("stale head error = %v", err)
	}
	if service.analyzeCalls != 0 {
		t.Fatal("stale preview invoked Analyze")
	}
	registry.repository.Head = "head-1"
	service.installation.Status = json.RawMessage(`{"running":false,"known":true}`)
	if _, err := manager.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: "repo-key", AnalysisType: "refactor", Scope: roborev.AnalysisScopeBranch}, PreviewToken: preview.PreviewToken}); err == nil {
		t.Fatal("offline daemon was accepted")
	}
}

func TestPromptRequiresFreshDiscoveredType(t *testing.T) {
	manager, _, service := testManager(t)
	service.types = []roborev.AnalysisType{{Name: "security", Description: "Find risks"}}
	if _, err := manager.Prompt(context.Background(), "refactor"); err == nil {
		t.Fatal("stale/unknown type was accepted")
	}
}

func TestPrepareAndStartRejectInvalidAndForeignInputs(t *testing.T) {
	manager, _, _ := testManager(t)
	for name, request := range map[string]Request{
		"invalid type":  {RepositoryIdentity: "repo-key", AnalysisType: "../secret", Scope: roborev.AnalysisScopeBranch},
		"invalid scope": {RepositoryIdentity: "repo-key", AnalysisType: "refactor", Scope: "files"},
		"foreign repo":  {RepositoryIdentity: "foreign", AnalysisType: "refactor", Scope: roborev.AnalysisScopeBranch},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := manager.Prepare(context.Background(), request); err == nil {
				t.Fatal("invalid request was accepted")
			}
		})
	}
}

func TestStartRejectsChangedBindingVersionAndSelectedRepository(t *testing.T) {
	manager, registry, service := testManager(t)
	request := Request{RepositoryIdentity: "repo-key", AnalysisType: "refactor", Scope: roborev.AnalysisScopeBranch}
	preview, err := manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	service.binding = "binding-2"
	if _, err := manager.Start(context.Background(), StartRequest{Request: request, PreviewToken: preview.PreviewToken}); err == nil {
		t.Fatal("changed binding was accepted")
	}
	service.binding = "binding-1"
	service.installation.Version = "v0.65.0"
	if _, err := manager.Start(context.Background(), StartRequest{Request: request, PreviewToken: preview.PreviewToken}); err == nil {
		t.Fatal("changed version was accepted")
	}
	service.installation.Version = roborevcompat.Manifest064().Version
	registry.selected = `C:\other`
	if _, err := manager.Start(context.Background(), StartRequest{Request: request, PreviewToken: preview.PreviewToken}); err == nil {
		t.Fatal("changed selected repository was accepted")
	}
	if service.analyzeCalls != 0 {
		t.Fatal("stale acceptance invoked Analyze")
	}
}

func TestStartRunsAfterBrowserDisconnectAndReleasesOnProcessError(t *testing.T) {
	manager, _, service := testManager(t)
	request := Request{RepositoryIdentity: "repo-key", AnalysisType: "refactor", Scope: roborev.AnalysisScopeRepository}
	preview, err := manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	service.analyzeStart = make(chan struct{})
	service.analyzeWait = make(chan struct{})
	parent, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, startErr := manager.Start(parent, StartRequest{Request: request, PreviewToken: preview.PreviewToken})
		result <- startErr
	}()
	<-service.analyzeStart
	cancel()
	close(service.analyzeWait)
	if err := <-result; err != nil {
		t.Fatalf("browser disconnect canceled accepted enqueue: %v", err)
	}
	service.analyzeStart = nil
	service.analyzeWait = nil
	service.analyzeErr = fmt.Errorf("%w: runner exited", roborev.ErrAmbiguousAnalysisEnqueue)
	preview, err = manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(context.Background(), StartRequest{Request: request, PreviewToken: preview.PreviewToken}); !errors.Is(err, roborev.ErrAmbiguousAnalysisEnqueue) {
		t.Fatalf("process error = %v, want ambiguous", err)
	}
	if snapshot := manager.gate.SnapshotDetailed(); snapshot.Analyses != 0 || snapshot.RepositoryLeases != 0 {
		t.Fatalf("leases remained after process error: %#v", snapshot)
	}
}

func TestStartKeepsPlainAnalyzePreflightErrorsUnavailable(t *testing.T) {
	manager, _, service := testManager(t)
	request := Request{RepositoryIdentity: "repo-key", AnalysisType: "refactor", Scope: roborev.AnalysisScopeBranch}
	preview, err := manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	service.analyzeErr = errors.New("daemon health schema is invalid")

	_, err = manager.Start(context.Background(), StartRequest{Request: request, PreviewToken: preview.PreviewToken})
	if err == nil {
		t.Fatal("plain Analyze error was accepted")
	}
	if errors.Is(err, roborev.ErrAmbiguousAnalysisEnqueue) {
		t.Fatalf("plain Analyze error was classified as ambiguous: %v", err)
	}
	var typedErr *Error
	if !errors.As(err, &typedErr) || typedErr.Kind != ErrorUnavailable {
		t.Fatalf("plain Analyze error = %v, want unavailable", err)
	}
}

func TestShutdownCancelsAcceptedEnqueueWithBoundedManagerContext(t *testing.T) {
	manager, _, service := testManager(t)
	request := Request{RepositoryIdentity: "repo-key", AnalysisType: "refactor", Scope: roborev.AnalysisScopeBranch}
	preview, err := manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	service.analyzeStart = make(chan struct{})
	service.analyzeWait = make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, startErr := manager.Start(context.Background(), StartRequest{Request: request, PreviewToken: preview.PreviewToken})
		result <- startErr
	}()
	<-service.analyzeStart
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	close(service.analyzeWait)
	if err := <-result; err == nil {
		t.Fatal("shutdown-canceled enqueue was reported successful")
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
