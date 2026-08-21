package fixoperation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

type fakeRegistry struct {
	mu         sync.Mutex
	repo       gitx.Repository
	identity   string
	err        error
	resolveErr error
	selected   string
}

func (r *fakeRegistry) ResolveIdentity(string) (string, error) {
	if r.resolveErr != nil {
		return "", r.resolveErr
	}
	return r.repo.Path, nil
}
func (r *fakeRegistry) Identity(path string) (string, error) {
	if r.identity != "" && path == r.repo.Path {
		return r.identity, nil
	}
	return path, nil
}
func (r *fakeRegistry) Selected() string {
	if r.selected != "" {
		return r.selected
	}
	return r.repo.Path
}
func (r *fakeRegistry) InspectIdentity(context.Context, string) (gitx.Repository, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return gitx.Repository{}, r.err
	}
	return r.repo, nil
}

type fakeService struct {
	registry    *fakeRegistry
	job         roborev.JobMetadata
	started     chan struct{}
	block       bool
	install     roborev.Installation
	metadataErr error
	fixErr      error
	fixMode     string
	returnedID  int64
}

func (s *fakeService) Installation(context.Context) roborev.Installation {
	if s.install.Version != "" || s.install.Installed || s.install.Capabilities.FixCommand || s.install.Capabilities.JobListAPI {
		return s.install
	}
	return roborev.Installation{Installed: true, Version: roborevcompat.Manifest064().Version, Binary: `C:\tools\roborev.exe`, Capabilities: roborev.Capabilities{FixCommand: true, JobListAPI: true}}
}
func (s *fakeService) TerminalJobMetadata(context.Context, int64) (roborev.JobMetadata, error) {
	if s.metadataErr != nil {
		return roborev.JobMetadata{}, s.metadataErr
	}
	job := s.job
	if s.returnedID != 0 {
		job.ID = s.returnedID
	}
	return job, nil
}
func (s *fakeService) Fix(ctx context.Context, _ roborev.FixRequest) error {
	if s.started != nil {
		close(s.started)
		s.started = nil
	}
	if s.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if s.fixErr != nil {
		return s.fixErr
	}
	s.registry.mu.Lock()
	switch s.fixMode {
	case "unchanged":
		s.registry.mu.Unlock()
		return nil
	case "dirty":
		s.registry.repo.Head = "new-head"
		s.registry.repo.Dirty = true
	case "branch":
		s.registry.repo.Head = "new-head"
		s.registry.repo.Branch = "feature"
	case "path":
		s.registry.repo.Head = "new-head"
		s.registry.repo.Path = `C:\other`
	case "identity":
		s.registry.repo.Head = "new-head"
		s.registry.identity = "other"
	case "inspect-unavailable":
		s.registry.repo.Head = "new-head"
		s.registry.err = errors.New("repository unavailable")
	default:
		s.registry.repo.Head = "new-head"
		s.registry.repo.Dirty = false
	}
	s.registry.mu.Unlock()
	return nil
}

func validDeps() (*fakeRegistry, *fakeService, *Manager) {
	closed := false
	registry := &fakeRegistry{repo: gitx.Repository{Path: `C:\repo`, Branch: "main", Head: "old-head"}}
	service := &fakeService{registry: registry, job: roborev.JobMetadata{ID: 42, RepoPath: registry.repo.Path, GitRef: "old-head", Branch: "main", JobType: "review", Status: "done", Verdict: "F", Closed: &closed}}
	manager, _ := NewManager(Dependencies{Registry: registry, RoboRev: service, Binary: `C:\tools\roborev.exe`, Gate: operation.NewGate()})
	return registry, service, manager
}

func runToTerminal(t *testing.T, manager *Manager, service *fakeService, registry *fakeRegistry) *Operation {
	t.Helper()
	preview, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	item, err := manager.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"}, PreviewToken: preview.PreviewToken})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, _ := manager.Get(item.ID)
		if current != nil && current.CompletedAt != nil {
			return current
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("operation did not finish; service=%#v", service)
	return nil
}

func TestPrepareUsesExactArgsAndFailsClosedOnMissingClosed(t *testing.T) {
	_, service, manager := validDeps()
	preview, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: `C:\repo`, TargetJobID: 42, Agent: "codex"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := preview.Command.Args; len(got) != 5 || got[0] != "fix" || got[1] != "--agent" || got[2] != "codex" || got[3] != "--quiet" || got[4] != "42" {
		t.Fatalf("args = %#v", got)
	}
	service.job.Closed = nil
	if _, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: `C:\repo`, TargetJobID: 42, Agent: "codex"}); err == nil {
		t.Fatal("missing closed state was accepted")
	}
}

func TestPrepareEligibilityMatrix(t *testing.T) {
	closed := false
	tests := []struct {
		name   string
		mutate func(*fakeRegistry, *fakeService)
		kind   ErrorKind
	}{
		{name: "single valid"},
		{name: "pass", mutate: func(_ *fakeRegistry, s *fakeService) { s.job.Verdict = "P" }, kind: ErrorConflict},
		{name: "running", mutate: func(_ *fakeRegistry, s *fakeService) { s.job.Status = "running" }, kind: ErrorConflict},
		{name: "member", mutate: func(_ *fakeRegistry, s *fakeService) { s.job.PanelRole, s.job.JobType = "member", "synthesis" }, kind: ErrorConflict},
		{name: "single with panel name", mutate: func(_ *fakeRegistry, s *fakeService) { s.job.PanelName = "cross" }, kind: ErrorConflict},
		{name: "single with panel run", mutate: func(_ *fakeRegistry, s *fakeService) { s.job.PanelRunUUID = "run-1" }, kind: ErrorConflict},
		{name: "synthesis valid", mutate: func(_ *fakeRegistry, s *fakeService) {
			s.job.PanelRole, s.job.JobType, s.job.PanelName, s.job.PanelRunUUID = "synthesis", "synthesis", "cross", "run-1"
		}},
		{name: "synthesis malformed", mutate: func(_ *fakeRegistry, s *fakeService) {
			s.job.PanelRole, s.job.JobType, s.job.PanelName, s.job.PanelRunUUID = "synthesis", "synthesis", "", "run-1"
		}, kind: ErrorConflict},
		{name: "closed", mutate: func(_ *fakeRegistry, s *fakeService) { value := true; s.job.Closed = &value }, kind: ErrorConflict},
		{name: "closed missing", mutate: func(_ *fakeRegistry, s *fakeService) { s.job.Closed = nil }, kind: ErrorConflict},
		{name: "foreign repo", mutate: func(_ *fakeRegistry, s *fakeService) { s.job.RepoPath = `C:\other` }, kind: ErrorConflict},
		{name: "skipped", mutate: func(_ *fakeRegistry, s *fakeService) { s.job.Skipped = true }, kind: ErrorConflict},
		{name: "wrong returned job ID", mutate: func(_ *fakeRegistry, s *fakeService) { s.returnedID = 99 }, kind: ErrorConflict},
		{name: "identity resolve failure", mutate: func(r *fakeRegistry, _ *fakeService) { r.resolveErr = errors.New("repository is not registered") }, kind: ErrorForbidden},
		{name: "selected repository mismatch", mutate: func(r *fakeRegistry, _ *fakeService) { r.selected = `C:\other` }, kind: ErrorConflict},
		{name: "stale head", mutate: func(_ *fakeRegistry, s *fakeService) { s.job.GitRef = "old-head-2" }, kind: ErrorConflict},
		{name: "stale branch", mutate: func(_ *fakeRegistry, s *fakeService) { s.job.Branch = "feature" }, kind: ErrorConflict},
		{name: "dirty", mutate: func(r *fakeRegistry, _ *fakeService) { r.repo.Dirty = true }, kind: ErrorConflict},
		{name: "unsupported version", mutate: func(_ *fakeRegistry, s *fakeService) {
			s.install = roborev.Installation{Installed: true, Version: "v0.63.0", Capabilities: roborev.Capabilities{FixCommand: true, JobListAPI: true}}
		}, kind: ErrorUnavailable},
		{name: "missing fix capability", mutate: func(_ *fakeRegistry, s *fakeService) {
			s.install = roborev.Installation{Installed: true, Version: roborevcompat.Manifest064().Version, Capabilities: roborev.Capabilities{JobListAPI: true}}
		}, kind: ErrorUnavailable},
		{name: "missing job list capability", mutate: func(_ *fakeRegistry, s *fakeService) {
			s.install = roborev.Installation{Installed: true, Version: roborevcompat.Manifest064().Version, Capabilities: roborev.Capabilities{FixCommand: true}}
		}, kind: ErrorUnavailable},
		{name: "metadata unavailable", mutate: func(_ *fakeRegistry, s *fakeService) { s.metadataErr = errors.New("daemon connection refused") }, kind: ErrorUnavailable},
		{name: "metadata not found", mutate: func(_ *fakeRegistry, s *fakeService) { s.metadataErr = errors.New("job not found") }, kind: ErrorNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, service, manager := validDeps()
			service.job.Closed = &closed
			if test.mutate != nil {
				test.mutate(registry, service)
			}
			_, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"})
			if test.kind == "" {
				if err != nil {
					t.Fatalf("Prepare: %v", err)
				}
				return
			}
			var typedErr *Error
			if !errors.As(err, &typedErr) || typedErr.Kind != test.kind {
				t.Fatalf("error = %v, kind=%v; want %v", err, typedErr, test.kind)
			}
		})
	}
}

func TestPreviewAndOperationUseStableBrowserProjection(t *testing.T) {
	registry, _, manager := validDeps()
	preview, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	data, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	target, ok := object["target"].(map[string]any)
	if !ok || target["jobId"] != float64(42) || target["gitRef"] != "old-head" {
		t.Fatalf("target projection = %#v", object["target"])
	}
	if object["startingHead"] != "old-head" || object["startingBranch"] != "main" {
		t.Fatalf("starting state = %#v", object)
	}
}

func TestStartFreshRejectsEligibilityChangesWithoutProcessOrLeaseLeak(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeRegistry, *fakeService)
	}{
		{name: "verdict changed", mutate: func(_ *fakeRegistry, s *fakeService) { s.job.Verdict = "P" }},
		{name: "skipped", mutate: func(_ *fakeRegistry, s *fakeService) { s.job.Skipped = true }},
		{name: "returned ID changed", mutate: func(_ *fakeRegistry, s *fakeService) { s.returnedID = 99 }},
		{name: "identity unregistered", mutate: func(r *fakeRegistry, _ *fakeService) { r.resolveErr = errors.New("repository is not registered") }},
		{name: "selected repository changed", mutate: func(r *fakeRegistry, _ *fakeService) { r.selected = `C:\other` }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, service, manager := validDeps()
			preview, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"})
			if err != nil {
				t.Fatal(err)
			}
			service.started = make(chan struct{})
			test.mutate(registry, service)
			if _, err := manager.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"}, PreviewToken: preview.PreviewToken}); err == nil {
				t.Fatal("fresh eligibility change was accepted")
			}
			if service.started == nil || len(manager.List(registry.repo.Path).Operations) != 0 {
				t.Fatal("rejected Start launched a process or recorded an operation")
			}
			if snapshot := manager.gate.SnapshotDetailed(); snapshot.Fixes != 0 || snapshot.RepositoryLeases != 0 {
				t.Fatalf("leases after rejected Start = %#v", snapshot)
			}
		})
	}
}

func TestPreviewTokenCoversConfirmationScalars(t *testing.T) {
	registry, _, manager := validDeps()
	base, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Preview)
	}{
		{name: "repository identity", mutate: func(value *Preview) { value.RepositoryIdentity += "-changed" }},
		{name: "repository path", mutate: func(value *Preview) { value.RepositoryPath += "-changed" }},
		{name: "starting head", mutate: func(value *Preview) { value.StartingHead = "different-head" }},
		{name: "starting branch", mutate: func(value *Preview) { value.StartingBranch = "different-branch" }},
		{name: "target job id", mutate: func(value *Preview) { value.Target.JobID++ }},
		{name: "target git ref", mutate: func(value *Preview) { value.Target.GitRef = "different-ref" }},
		{name: "target branch", mutate: func(value *Preview) { value.Target.Branch = "different-branch" }},
		{name: "target job type", mutate: func(value *Preview) { value.Target.JobType = "synthesis" }},
		{name: "target panel role", mutate: func(value *Preview) { value.Target.PanelRole = "synthesis" }},
		{name: "target panel name", mutate: func(value *Preview) { value.Target.PanelName = "different-panel" }},
		{name: "agent", mutate: func(value *Preview) { value.Agent = "different-agent" }},
		{name: "executable", mutate: func(value *Preview) { value.Command.Executable = "different-executable" }},
		{name: "argument", mutate: func(value *Preview) { value.Command.Args[0] = "different-arg" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := *base
			value.Command.Args = append([]string(nil), base.Command.Args...)
			test.mutate(&value)
			if got := previewToken(&value); got == base.PreviewToken {
				t.Fatalf("token did not change for %s: %s", test.name, got)
			}
		})
	}
}

func TestStartRevalidatesPreviewAndVerifiesNewCleanHead(t *testing.T) {
	registry, _, manager := validDeps()
	preview, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	item, err := manager.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"}, PreviewToken: preview.PreviewToken})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, _ := manager.Get(item.ID)
		if current != nil && current.CompletedAt != nil {
			if current.Status != StatusSucceeded || current.NewHead != "new-head" {
				t.Fatalf("terminal operation = %#v", current)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation did not finish")
}

func TestPostconditionAndCommandFailureCodes(t *testing.T) {
	tests := []struct {
		name  string
		mode  string
		err   error
		code  string
		phase Status
	}{
		{name: "process nonzero", err: errors.New(strings.Repeat("x", 3000)), code: ErrorCodeFixCommandFailed, phase: StatusFailed},
		{name: "exit0 unchanged", mode: "unchanged", code: ErrorCodeFixDidNotCommit, phase: StatusFailed},
		{name: "exit0 dirty", mode: "dirty", code: ErrorCodeFixLeftDirty, phase: StatusFailed},
		{name: "branch changed", mode: "branch", code: ErrorCodeRepositoryChanged, phase: StatusFailed},
		{name: "path changed", mode: "path", code: ErrorCodeRepositoryChanged, phase: StatusFailed},
		{name: "identity changed", mode: "identity", code: ErrorCodeRepositoryChanged, phase: StatusFailed},
		{name: "inspect unavailable", mode: "inspect-unavailable", code: ErrorCodeRepositoryUnavailable, phase: StatusFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, service, manager := validDeps()
			service.fixMode, service.fixErr = test.mode, test.err
			item := runToTerminal(t, manager, service, registry)
			if item.Phase != test.phase || item.ErrorCode != test.code {
				t.Fatalf("terminal = %#v; want phase=%s code=%s", item, test.phase, test.code)
			}
			if len(item.Error) > 1024 {
				t.Fatalf("error exceeded bounded limit: %d", len(item.Error))
			}
		})
	}
}

func TestCancelKeepsProcessScopedOperationAndWaits(t *testing.T) {
	registry, service, manager := validDeps()
	service.block = true
	service.started = make(chan struct{})
	preview, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	item, err := manager.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"}, PreviewToken: preview.PreviewToken})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-service.started:
	case <-time.After(time.Second):
		t.Fatal("fix process did not start")
	}
	if err := manager.Cancel(item.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown: %v", err)
	}
	current, _ := manager.Get(item.ID)
	if current == nil || current.Status != StatusCanceled {
		t.Fatalf("canceled operation = %#v", current)
	}
}

func TestStartCanceledBeforeAcceptanceCreatesNoOperation(t *testing.T) {
	registry, _, manager := validDeps()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := manager.Start(ctx, StartRequest{Request: Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"}, PreviewToken: "token"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v; want context.Canceled", err)
	}
	if got := manager.List(registry.repo.Path); len(got.Operations) != 0 || got.Operation != nil {
		t.Fatalf("canceled acceptance created operation: %#v", got)
	}
	if snapshot := manager.gate.SnapshotDetailed(); snapshot.Fixes != 0 || snapshot.RepositoryLeases != 0 {
		t.Fatalf("leases after canceled acceptance = %#v", snapshot)
	}
}

func TestParentCancellationAfterAcceptanceDoesNotCancelOperation(t *testing.T) {
	registry, service, manager := validDeps()
	service.block = true
	started := make(chan struct{})
	service.started = started
	preview, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	parent, cancelParent := context.WithCancel(context.Background())
	item, err := manager.Start(parent, StartRequest{Request: Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"}, PreviewToken: preview.PreviewToken})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fix process did not start")
	}
	cancelParent()
	time.Sleep(20 * time.Millisecond)
	current, _ := manager.Get(item.ID)
	if current == nil || current.CompletedAt != nil {
		t.Fatalf("parent cancellation canceled accepted operation: %#v", current)
	}
	if err := manager.Cancel(item.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestStartRejectsPreviewMismatchWithoutLaunchingFix(t *testing.T) {
	registry, service, manager := validDeps()
	preview, err := manager.Prepare(context.Background(), Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: registry.repo.Path, TargetJobID: 42, Agent: "codex"}, PreviewToken: preview.PreviewToken + "stale"})
	if err == nil {
		t.Fatal("stale preview was accepted")
	}
	if service.started != nil || len(manager.List(registry.repo.Path).Operations) != 0 {
		t.Fatal("stale preview launched a process or operation")
	}
	if snapshot := manager.gate.SnapshotDetailed(); snapshot.Fixes != 0 || snapshot.RepositoryLeases != 0 {
		t.Fatalf("leases after stale preview = %#v", snapshot)
	}
}
