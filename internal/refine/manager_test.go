package refine

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type fakeRegistry struct {
	mu           sync.Mutex
	repo         gitx.Repository
	inspectCount int
	inspectHook  func(int, *gitx.Repository)
}

func (r *fakeRegistry) Resolve(path string) (string, error) {
	if path == "" {
		return `C:\repo`, nil
	}
	if path != `C:\repo` {
		return "", errors.New("repository is not registered")
	}
	return path, nil
}
func (r *fakeRegistry) Contains(path string) bool { return path == `C:\repo` }
func (r *fakeRegistry) Inspect(context.Context, string) (gitx.Repository, error) {
	r.mu.Lock()
	r.inspectCount++
	count := r.inspectCount
	r.mu.Unlock()
	if r.inspectHook != nil {
		r.inspectHook(count, &r.repo)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.repo, nil
}
func (r *fakeRegistry) Identity(string) (string, error) { return "repo-key", nil }

type multiRegistry struct {
	mu    sync.Mutex
	repos map[string]gitx.Repository
}

func (r *multiRegistry) Resolve(path string) (string, error) {
	if path == "" {
		return "", errors.New("repository is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.repos[path]; !ok {
		return "", errors.New("repository is not registered")
	}
	return path, nil
}
func (r *multiRegistry) Contains(path string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.repos[path]
	return ok
}
func (r *multiRegistry) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	repo, ok := r.repos[path]
	if !ok {
		return gitx.Repository{}, errors.New("repository is not registered")
	}
	return repo, nil
}
func (r *multiRegistry) Identity(path string) (string, error) {
	if !r.Contains(path) {
		return "", errors.New("repository is not registered")
	}
	return "identity:" + path, nil
}

type fakePanels struct{}

func (fakePanels) Resolve(context.Context, string) (roborev.PanelCatalog, error) {
	return roborev.PanelCatalog{Panels: []roborev.PanelInfo{{Name: "cross", Kind: "panel", Valid: true}, {Name: "deep", Kind: "panel", Valid: true}}}, nil
}

type mutablePanels struct {
	mu        sync.Mutex
	available bool
}

type unsupportedReviewService struct{ ReviewService }

func (unsupportedReviewService) Installation(context.Context) roborev.Installation {
	return roborev.Installation{Installed: true, Version: "v0.63.0", Capabilities: roborev.Capabilities{PanelReview: true}}
}

func (p *mutablePanels) Resolve(context.Context, string) (roborev.PanelCatalog, error) {
	p.mu.Lock()
	available := p.available
	p.mu.Unlock()
	if !available {
		return roborev.PanelCatalog{}, nil
	}
	return roborev.PanelCatalog{Panels: []roborev.PanelInfo{{Name: "cross", Kind: "panel", Valid: true}, {Name: "deep", Kind: "panel", Valid: true}}}, nil
}

type fakeReviewService struct {
	mu                 sync.Mutex
	reviewCount        int
	fixCount           int
	cancelCount        int
	verdicts           []string
	statusError        bool
	statusPending      bool
	reviewErr          error
	installationStatus json.RawMessage
	installationError  string
	fixErr             error
	fixNoCommit        bool
	fixDirty           bool
	fixBranch          string
	fixPath            string
	fixBlock           bool
	fixStarted         chan struct{}
	fixReturned        chan struct{}
	fixRelease         chan struct{}
	statusHook         func()
	receipts           map[int64]roborev.PanelEnqueueResponse
	registry           RepositoryRegistry
}

func (s *fakeReviewService) Installation(context.Context) roborev.Installation {
	status := s.installationStatus
	if status == nil {
		status = json.RawMessage(`{"running":true}`)
	}
	return roborev.Installation{Installed: true, Version: "v0.64.0", Error: s.installationError, Status: status, Capabilities: roborev.Capabilities{PanelReview: true, FixCommand: true}}
}
func (s *fakeReviewService) ReviewPanel(_ context.Context, request roborev.ReviewRequest) (roborev.PanelEnqueueResponse, error) {
	s.mu.Lock()
	if s.reviewErr != nil {
		err := s.reviewErr
		s.mu.Unlock()
		return roborev.PanelEnqueueResponse{}, err
	}
	s.reviewCount++
	id := int64(s.reviewCount)
	receipt := roborev.PanelEnqueueResponse{ID: id, RepoPath: request.RepoPath, GitRef: request.GitRef, Branch: request.Branch, PanelName: request.Panel, PanelRunUUID: "00000000-0000-0000-0000-00000000000" + string(rune('0'+id)), PanelRole: "synthesis", JobType: "synthesis"}
	if s.receipts == nil {
		s.receipts = make(map[int64]roborev.PanelEnqueueResponse)
	}
	s.receipts[id] = receipt
	s.mu.Unlock()
	return receipt, nil
}
func (s *fakeReviewService) SynthesisStatus(context.Context, int64) (roborev.SynthesisStatus, error) {
	s.mu.Lock()
	count := s.reviewCount
	verdict := "P"
	if len(s.verdicts) >= count {
		verdict = s.verdicts[count-1]
	}
	receipt := s.receipts[int64(count)]
	hook := s.statusHook
	s.statusHook = nil
	statusError := s.statusError
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	if statusError {
		return roborev.SynthesisStatus{JobMetadata: roborev.JobMetadata{ID: receipt.ID, RepoPath: receipt.RepoPath, GitRef: receipt.GitRef, Branch: receipt.Branch, PanelName: receipt.PanelName, PanelRunUUID: receipt.PanelRunUUID, PanelRole: receipt.PanelRole, JobType: receipt.JobType, Status: "done", Verdict: "?"}}, nil
	}
	if s.statusPending {
		return roborev.SynthesisStatus{JobMetadata: roborev.JobMetadata{ID: receipt.ID, RepoPath: receipt.RepoPath, GitRef: receipt.GitRef, Branch: receipt.Branch, PanelName: receipt.PanelName, PanelRunUUID: receipt.PanelRunUUID, PanelRole: receipt.PanelRole, JobType: receipt.JobType, Status: "queued"}}, nil
	}
	return roborev.SynthesisStatus{JobMetadata: roborev.JobMetadata{ID: receipt.ID, RepoPath: receipt.RepoPath, GitRef: receipt.GitRef, Branch: receipt.Branch, PanelName: receipt.PanelName, PanelRunUUID: receipt.PanelRunUUID, PanelRole: receipt.PanelRole, JobType: receipt.JobType, Status: "done", Verdict: verdict}}, nil
}
func (s *fakeReviewService) Fix(ctx context.Context, request roborev.FixRequest) error {
	s.mu.Lock()
	if s.fixErr != nil {
		err := s.fixErr
		s.mu.Unlock()
		return err
	}
	s.fixCount++
	noCommit, dirty, branch, path, block := s.fixNoCommit, s.fixDirty, s.fixBranch, s.fixPath, s.fixBlock
	started, returned := s.fixStarted, s.fixReturned
	s.mu.Unlock()
	if block {
		if started != nil {
			select {
			case started <- struct{}{}:
			default:
			}
		}
		<-ctx.Done()
		if returned != nil {
			select {
			case returned <- struct{}{}:
			default:
			}
		}
		if release := s.fixRelease; release != nil {
			<-release
		}
		return ctx.Err()
	}
	if noCommit {
		return nil
	}
	s.updateRepository(request.RepoPath, func(repo *gitx.Repository) {
		repo.Head += "-fixed"
		repo.Dirty = dirty
		if branch != "" {
			repo.Branch = branch
		}
		if path != "" {
			repo.Path = path
		}
	})
	return nil
}
func (s *fakeReviewService) Cancel(context.Context, int64) error { s.cancelCount++; return nil }

func (s *fakeReviewService) updateRepository(path string, fn func(*gitx.Repository)) {
	switch registry := s.registry.(type) {
	case *fakeRegistry:
		registry.mu.Lock()
		defer registry.mu.Unlock()
		fn(&registry.repo)
	case *multiRegistry:
		registry.mu.Lock()
		defer registry.mu.Unlock()
		if repo, ok := registry.repos[path]; ok {
			fn(&repo)
			registry.repos[path] = repo
		}
	}
}

func newTestManager(t *testing.T, service *fakeReviewService) (*Manager, *fakeRegistry) {
	t.Helper()
	registry := &fakeRegistry{repo: gitx.Repository{Path: `C:\repo`, Branch: "main", Head: "head", Dirty: false}}
	service.registry = registry
	manager, err := NewManager(Dependencies{Registry: registry, Panels: fakePanels{}, RoboRev: service, Binary: `C:\roborev.exe`})
	if err != nil {
		t.Fatal(err)
	}
	return manager, registry
}

func waitRun(t *testing.T, manager *Manager, id string) *PanelRefineRun {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, ok := manager.Get(id)
		if ok && isTerminal(run.Phase) {
			return run
		}
		time.Sleep(2 * time.Millisecond)
	}
	run, _ := manager.Get(id)
	t.Fatalf("run did not finish: %#v", run)
	return nil
}

func TestManagerPassesWithoutFix(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"P"}}
	manager, _ := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhasePassed || result.FixAttempts != 0 || len(result.Iterations) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestManagerFixesThenReviewsNewHead(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"F", "P"}}
	manager, registry := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross", MaxFixAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhasePassed || result.FixAttempts != 1 || len(result.Iterations) != 2 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !result.Iterations[0].FixAttempted || result.Iterations[0].FixAgent != "codex" || result.Iterations[0].HeadAfterFix == "" || result.Iterations[1].TargetHead != registry.repo.Head {
		t.Fatalf("fix did not advance review target: %#v", result.Iterations)
	}
}

func TestManagerRejectsConcurrentSameRepository(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"P"}}
	manager, _ := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	first, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"}); err == nil {
		t.Fatal("expected same-repository lease rejection")
	}
	_ = waitRun(t, manager, first.ID)
}

func TestManagerFailsClosedOnMalformedVerdict(t *testing.T) {
	service := &fakeReviewService{statusError: true}
	manager, _ := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	// The malformed status is reached through the normal receipt path and
	// must not be interpreted as a pass or trigger a fix.
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhaseFailed || result.FixAttempts != 0 || len(result.Iterations) != 1 || result.Iterations[0].ReviewJobID != 1 || result.Iterations[0].PanelRunUUID == "" || result.Iterations[0].Error == "" || result.Iterations[0].ReviewResult != "" {
		t.Fatalf("malformed status was not failed closed: %#v", result)
	}
}

type blockingReviewService struct {
	*fakeReviewService
	started chan struct{}
}

func (s *blockingReviewService) SynthesisStatus(ctx context.Context, _ int64) (roborev.SynthesisStatus, error) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return roborev.SynthesisStatus{}, ctx.Err()
}

func TestManagerCancelSignalsWorkerAndCleansReceipt(t *testing.T) {
	service := &blockingReviewService{fakeReviewService: &fakeReviewService{verdicts: []string{"P"}}, started: make(chan struct{}, 1)}
	manager, _ := newTestManager(t, service.fakeReviewService)
	// Rebuild with the blocking service while preserving the same dependencies.
	manager.service = service
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-service.started:
	case <-time.After(time.Second):
		t.Fatal("review status did not start")
	}
	if err := manager.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhaseCancelled || result.CancelState == nil || service.cancelCount != 1 || len(result.Iterations) != 1 || result.Iterations[0].ReviewJobID != 1 || result.Iterations[0].PanelRunUUID == "" || result.Iterations[0].Error == "" {
		t.Fatalf("unexpected cancellation result: %#v, cancels=%d", result, service.cancelCount)
	}
}

func TestListResponseIncludesSafeFixOptions(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"P"}}
	manager, _ := newTestManager(t, service)
	response := manager.ListResponse(`C:\repo`)
	if response.Options.Executable != `C:\roborev.exe` || response.Options.DefaultFixAgent != "codex" || response.Options.MinFixAttempts != 1 || response.Options.MaxFixAttempts != 10 || len(response.Options.Args) != 5 || response.Options.Args[0] != "fix" {
		t.Fatalf("unexpected options: %#v", response.Options)
	}
}

func TestManagerRejectsStalePassAfterReview(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"P"}}
	manager, registry := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	service.mu.Lock()
	service.statusHook = func() {
		registry.mu.Lock()
		registry.repo.Head = "changed-by-user"
		registry.mu.Unlock()
	}
	service.mu.Unlock()
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhaseNeedsAttention || result.StopReason != StopRepositoryChanged || result.FixAttempts != 0 {
		t.Fatalf("stale pass was accepted: %#v", result)
	}
}

func TestManagerFinalPanelGateUsesNamedPanel(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"P", "P"}}
	manager, _ := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross", FinalPanel: "deep"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhasePassed || len(result.Iterations) != 2 || result.Iterations[0].Panel != "cross" || result.Iterations[1].Panel != "deep" {
		t.Fatalf("final panel was not run: %#v", result)
	}
}

func TestManagerRejectsHeadChangeBeforeNextReviewEnqueue(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"F", "P"}}
	manager, registry := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	registry.inspectHook = func(count int, repo *gitx.Repository) {
		if count == 6 {
			repo.Head = "external-commit"
		}
	}
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhaseNeedsAttention || result.StopReason != StopRepositoryChanged || len(result.Iterations) != 1 || service.reviewCount != 1 {
		t.Fatalf("head drift was absorbed as a new target: %#v", result)
	}
}

func TestManagerStopsAtMaximumFixAttempts(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"F", "F"}}
	manager, _ := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross", MaxFixAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhaseNeedsAttention || result.StopReason != StopMaximumFixAttempts || result.FixAttempts != 1 || len(result.Iterations) != 2 {
		t.Fatalf("maximum attempts was not enforced: %#v", result)
	}
}

func TestManagerRejectsDirtyStateBeforeFix(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"F"}}
	manager, registry := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	service.mu.Lock()
	service.statusHook = func() {
		registry.mu.Lock()
		registry.repo.Dirty = true
		registry.mu.Unlock()
	}
	service.mu.Unlock()
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhaseNeedsAttention || result.StopReason != StopRepositoryChanged || result.FixAttempts != 0 {
		t.Fatalf("dirty state was not rejected before fix: %#v", result)
	}
}

func TestManagerRejectsBranchAndIdentityChangesAfterFix(t *testing.T) {
	tests := []struct {
		name   string
		branch string
		path   string
	}{
		{name: "branch", branch: "feature"},
		{name: "identity", path: `C:\other`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeReviewService{verdicts: []string{"F"}, fixBranch: tc.branch, fixPath: tc.path}
			manager, _ := newTestManager(t, service)
			defer manager.Shutdown(context.Background())
			run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
			if err != nil {
				t.Fatal(err)
			}
			result := waitRun(t, manager, run.ID)
			if result.Phase != PhaseNeedsAttention || result.StopReason != StopRepositoryChanged || result.FixAttempts != 1 || !result.Iterations[0].FixAttempted || result.Iterations[0].FixAgent != "codex" {
				t.Fatalf("post-fix drift was not rejected: %#v", result)
			}
		})
	}
}

func TestManagerRejectsFixWithoutCommitOrWithDirtyResult(t *testing.T) {
	tests := []struct {
		name     string
		noCommit bool
		dirty    bool
		expected StopReason
	}{
		{name: "no commit", noCommit: true, expected: StopFixDidNotCommit},
		{name: "dirty", dirty: true, expected: StopFixLeftDirty},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeReviewService{verdicts: []string{"F"}, fixNoCommit: tc.noCommit, fixDirty: tc.dirty}
			manager, _ := newTestManager(t, service)
			defer manager.Shutdown(context.Background())
			run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
			if err != nil {
				t.Fatal(err)
			}
			result := waitRun(t, manager, run.ID)
			if result.Phase != PhaseNeedsAttention || result.StopReason != tc.expected || !result.Iterations[0].FixAttempted || result.Iterations[0].FixAgent != "codex" {
				t.Fatalf("fix postcondition was not enforced: %#v", result)
			}
		})
	}
}

func TestManagerBoundsStatusPollingErrors(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"P"}, statusError: true}
	manager, _ := newTestManager(t, service)
	manager.wait = func(context.Context, time.Duration) error { return nil }
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhaseFailed || result.StopReason != StopMalformedReviewStatus {
		t.Fatalf("polling errors were not bounded: %#v", result)
	}
}

func TestManagerKeepsTerminalStateImmutableAndBoundsHistory(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"P", "P"}}
	registry := &fakeRegistry{repo: gitx.Repository{Path: `C:\repo`, Branch: "main", Head: "head"}}
	service.registry = registry
	manager, err := NewManager(Dependencies{Registry: registry, Panels: fakePanels{}, RoboRev: service, Binary: `C:\roborev.exe`, HistoryLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	first, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	_ = waitRun(t, manager, first.ID)
	second, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	secondResult := waitRun(t, manager, second.ID)
	if _, ok := manager.Get(first.ID); ok {
		t.Fatal("terminal history exceeded configured bound")
	}
	manager.stop(second.ID, PhaseFailed, StopReviewFailed, errors.New("late update"))
	latest, ok := manager.Get(second.ID)
	if !ok || latest.Phase != secondResult.Phase {
		t.Fatalf("terminal state changed after completion: %#v", latest)
	}
}

func TestManagerRunsTwoFixAttemptsBeforePassing(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"F", "F", "P"}}
	manager, _ := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross", MaxFixAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhasePassed || result.FixAttempts != 2 || service.reviewCount != 3 || len(result.Iterations) != 3 || !result.Iterations[0].FixAttempted || !result.Iterations[1].FixAttempted || result.Iterations[0].FixAgent != "codex" || result.Iterations[1].FixAgent != "codex" {
		t.Fatalf("unexpected F-fix-F-fix-P result: %#v reviews=%d", result, service.reviewCount)
	}
}

func TestManagerRejectsBranchDriftAfterReviewBeforeFix(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"F"}}
	manager, registry := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	service.mu.Lock()
	service.statusHook = func() {
		registry.mu.Lock()
		registry.repo.Branch = "user-branch"
		registry.mu.Unlock()
	}
	service.mu.Unlock()
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhaseNeedsAttention || result.StopReason != StopRepositoryChanged || result.FixAttempts != 0 {
		t.Fatalf("branch drift reached fix: %#v", result)
	}
}

func TestManagerRejectsIdentityDriftAfterReviewBeforeFix(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"F"}}
	manager, registry := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	service.mu.Lock()
	service.statusHook = func() {
		registry.mu.Lock()
		registry.repo.Path = `C:\foreign`
		registry.mu.Unlock()
	}
	service.mu.Unlock()
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhaseNeedsAttention || result.StopReason != StopRepositoryChanged || result.FixAttempts != 0 {
		t.Fatalf("identity drift reached fix: %#v", result)
	}
}

func TestManagerStopsOnFixProcessError(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"F"}, fixErr: errors.New("process exited")}
	manager, _ := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhaseNeedsAttention || result.StopReason != StopFixFailed || result.FixAttempts != 1 || !result.Iterations[0].FixAttempted || result.Iterations[0].FixAgent != "codex" {
		t.Fatalf("fix process error was not terminalized: %#v", result)
	}
}

func TestManagerStopsOnReviewEnqueueError(t *testing.T) {
	service := &fakeReviewService{reviewErr: errors.New("daemon unavailable")}
	manager, _ := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhaseFailed || result.StopReason != StopReviewFailed || service.reviewCount != 0 {
		t.Fatalf("review enqueue error was not failed closed: %#v", result)
	}
}

func TestManagerCancelDuringFixWaitsForProcessAndInspectsRepository(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"F"}, fixBlock: true, fixStarted: make(chan struct{}, 1), fixReturned: make(chan struct{}, 1), fixRelease: make(chan struct{})}
	manager, _ := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-service.fixStarted:
	case <-time.After(time.Second):
		t.Fatal("fix process did not start")
	}
	if err := manager.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-service.fixReturned:
	case <-time.After(time.Second):
		t.Fatal("fix process did not observe cancellation")
	}
	if current, ok := manager.Get(run.ID); ok && isTerminal(current.Phase) {
		t.Fatal("cancel became terminal before fix returned")
	}
	close(service.fixRelease)
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhaseCancelled || result.CancelState == nil || !result.CancelState.Available || !result.Iterations[0].FixAttempted || result.Iterations[0].FixAgent != "codex" || result.Iterations[0].Error == "" {
		t.Fatalf("cancel did not inspect repository after fix cleanup: %#v", result)
	}
}

func TestManagerAllowsDifferentRepositoryIdentitiesIndependently(t *testing.T) {
	registry := &multiRegistry{repos: map[string]gitx.Repository{
		`C:\one`: {Path: `C:\one`, Branch: "main", Head: "one-head"},
		`C:\two`: {Path: `C:\two`, Branch: "main", Head: "two-head"},
	}}
	service := &fakeReviewService{statusPending: true}
	service.registry = registry
	manager, err := NewManager(Dependencies{Registry: registry, Panels: fakePanels{}, RoboRev: service, Binary: `C:\roborev.exe`, PollInterval: time.Millisecond, Wait: func(context.Context, time.Duration) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	first, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\one`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\two`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	if first.RepositoryIdentity == second.RepositoryIdentity {
		t.Fatalf("different repositories shared identity: %#v %#v", first, second)
	}
	_ = manager.Cancel(first.ID)
	_ = manager.Cancel(second.ID)
	_ = waitRun(t, manager, first.ID)
	_ = waitRun(t, manager, second.ID)
}

func TestManagerRejectsPanelDisappearanceBeforeNextEnqueue(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"F", "P"}}
	manager, _ := newTestManager(t, service)
	panels := &mutablePanels{available: true}
	manager.panels = panels
	service.mu.Lock()
	service.statusHook = func() {
		panels.mu.Lock()
		panels.available = false
		panels.mu.Unlock()
	}
	service.mu.Unlock()
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhaseFailed || result.StopReason != StopReviewFailed || service.reviewCount != 1 {
		t.Fatalf("panel disappearance was not rejected: %#v", result)
	}
}

func TestManagerRunsCrossThenDeepAfterDeepFinding(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"P", "F", "P", "P"}}
	manager, _ := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross", FinalPanel: "deep"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhasePassed || result.FixAttempts != 1 || service.reviewCount != 4 || len(result.Iterations) != 4 {
		t.Fatalf("cross/deep refinement sequence incorrect: %#v reviews=%d", result, service.reviewCount)
	}
	wantPanels := []string{"cross", "deep", "cross", "deep"}
	for i, want := range wantPanels {
		if result.Iterations[i].Panel != want {
			t.Fatalf("iteration %d panel=%q want %q", i, result.Iterations[i].Panel, want)
		}
	}
}

func TestManagerSameIterationAndFinalPanelRunsOnce(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"P"}}
	manager, _ := newTestManager(t, service)
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross", FinalPanel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhasePassed || service.reviewCount != 1 || len(result.Iterations) != 1 || result.FinalPanel != "" {
		t.Fatalf("same final/iteration panel was run more than once: %#v", result)
	}
}

func TestManagerStartPreflightRejectsDirtyUnregisteredUnsupportedAndInvalidPanels(t *testing.T) {
	tests := []struct {
		name    string
		service *fakeReviewService
		path    string
		request StartRequest
		mutate  func(*fakeRegistry)
	}{
		{name: "dirty", request: StartRequest{RepoPath: `C:\repo`, Panel: "cross"}, mutate: func(registry *fakeRegistry) { registry.repo.Dirty = true }},
		{name: "unregistered", path: `C:\foreign`, request: StartRequest{RepoPath: `C:\foreign`, Panel: "cross"}},
		{name: "unsupported", service: &fakeReviewService{}, request: StartRequest{RepoPath: `C:\repo`, Panel: "cross"}},
		{name: "attempt bounds", request: StartRequest{RepoPath: `C:\repo`, Panel: "cross", MaxFixAttempts: 11}},
		{name: "missing final panel", request: StartRequest{RepoPath: `C:\repo`, Panel: "cross", FinalPanel: "missing"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := tc.service
			if service == nil {
				service = &fakeReviewService{verdicts: []string{"P"}}
			}
			if tc.name == "unsupported" {
				service = &fakeReviewService{verdicts: []string{"P"}}
			}
			manager, registry := newTestManager(t, service)
			defer manager.Shutdown(context.Background())
			if tc.mutate != nil {
				registry.mu.Lock()
				tc.mutate(registry)
				registry.mu.Unlock()
			}
			if tc.name == "unsupported" {
				manager.service = unsupportedReviewService{ReviewService: service}
			}
			request := tc.request
			if tc.path != "" {
				request.RepoPath = tc.path
			}
			if _, err := manager.Start(context.Background(), request); err == nil {
				t.Fatal("invalid preflight was accepted")
			}
		})
	}
}

func TestManagerStartRejectsOfflineOrMalformedInstallationSynchronously(t *testing.T) {
	tests := []struct {
		name   string
		status json.RawMessage
		err    string
	}{
		{name: "empty", status: json.RawMessage{}},
		{name: "malformed", status: json.RawMessage(`{"running":`)},
		{name: "not running", status: json.RawMessage(`{"running":false}`)},
		{name: "installation error", status: json.RawMessage(`{"running":true}`), err: "daemon health check failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeReviewService{installationStatus: tc.status, installationError: tc.err}
			manager, _ := newTestManager(t, service)
			defer manager.Shutdown(context.Background())
			if _, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"}); err == nil {
				t.Fatal("offline or malformed installation was accepted")
			}
			if service.reviewCount != 0 {
				t.Fatalf("ReviewPanel called during failed preflight: %d", service.reviewCount)
			}
		})
	}
}
