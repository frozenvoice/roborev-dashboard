package refine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	return roborev.Installation{Installed: true, Version: "v0.64.0", Capabilities: roborev.Capabilities{PanelReview: true, FixCommand: true}}
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
	statusEntered      chan int64
	statusEnteredSeen  map[int64]bool
	receipts           map[int64]roborev.PanelEnqueueResponse
	registry           RepositoryRegistry
}

type lightweightReviewService struct {
	*fakeReviewService
	mu            sync.Mutex
	statuses      []string
	statusCalls   int
	metadataCalls int
	metadata      roborev.JobMetadata
	outputErrors  int
	outputError   error
	metadataError error
}

func (s *lightweightReviewService) JobOutputStatus(context.Context, int64) (roborev.JobOutputStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusCalls++
	if s.outputErrors > 0 {
		s.outputErrors--
		if s.outputError == nil {
			s.outputError = errors.New("temporary status transport")
		}
		return roborev.JobOutputStatus{}, s.outputError
	}
	status := "done"
	if len(s.statuses) > 0 {
		index := s.statusCalls - 1
		if index >= len(s.statuses) {
			index = len(s.statuses) - 1
		}
		status = s.statuses[index]
	}
	return roborev.JobOutputStatus{JobID: 1, Status: status}, nil
}

func (s *lightweightReviewService) JobMetadata(context.Context, int64) (roborev.JobMetadata, error) {
	s.mu.Lock()
	s.metadataCalls++
	metadata := s.metadata
	s.mu.Unlock()
	return metadata, nil
}
func (s *lightweightReviewService) TerminalJobMetadata(context.Context, int64) (roborev.JobMetadata, error) {
	s.mu.Lock()
	s.metadataCalls++
	metadata := s.metadata
	metadataError := s.metadataError
	s.mu.Unlock()
	if metadataError != nil {
		return roborev.JobMetadata{}, metadataError
	}
	return metadata, nil
}

func (s *fakeReviewService) Installation(context.Context) roborev.Installation {
	status := s.installationStatus
	if status == nil {
		status = json.RawMessage(`{"running":true}`)
	}
	return roborev.Installation{Installed: true, Version: "v0.64.0", Error: s.installationError, Status: status, Capabilities: roborev.Capabilities{PanelReview: true, FixCommand: true, JobOutputStatusAPI: true}}
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
func (s *fakeReviewService) JobOutputStatus(ctx context.Context, id int64) (roborev.JobOutputStatus, error) {
	if err := ctx.Err(); err != nil {
		return roborev.JobOutputStatus{}, err
	}
	s.mu.Lock()
	statusError := s.statusError
	pending := s.statusPending
	receipt := s.receipts[id]
	hook := s.statusHook
	s.statusHook = nil
	statusEntered := s.statusEntered
	entered := statusEntered != nil && !s.statusEnteredSeen[id]
	if entered {
		if s.statusEnteredSeen == nil {
			s.statusEnteredSeen = make(map[int64]bool)
		}
		s.statusEnteredSeen[id] = true
	}
	s.mu.Unlock()
	if entered {
		select {
		case statusEntered <- id:
		default:
		}
	}
	if hook != nil {
		hook()
	}
	if statusError {
		return roborev.JobOutputStatus{JobID: receipt.ID, Status: "done"}, nil
	}
	if pending {
		return roborev.JobOutputStatus{JobID: receipt.ID, Status: "queued"}, nil
	}
	return roborev.JobOutputStatus{JobID: receipt.ID, Status: "done"}, nil
}
func (s *fakeReviewService) TerminalJobMetadata(_ context.Context, id int64) (roborev.JobMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt := s.receipts[id]
	verdict := "P"
	if len(s.verdicts) >= int(id) {
		verdict = s.verdicts[id-1]
	}
	if s.statusError {
		verdict = "?"
	}
	return roborev.JobMetadata{ID: receipt.ID, RepoPath: receipt.RepoPath, GitRef: receipt.GitRef, Branch: receipt.Branch, PanelName: receipt.PanelName, PanelRunUUID: receipt.PanelRunUUID, PanelRole: receipt.PanelRole, JobType: receipt.JobType, Status: "done", Verdict: verdict}, nil
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
func (s *fakeReviewService) Cancel(context.Context, int64) error {
	s.mu.Lock()
	s.cancelCount++
	s.mu.Unlock()
	return nil
}

func (s *fakeReviewService) cancelCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelCount
}

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

func immediateWait(ctx context.Context, _ time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
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

func (s *blockingReviewService) JobOutputStatus(ctx context.Context, _ int64) (roborev.JobOutputStatus, error) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return roborev.JobOutputStatus{}, ctx.Err()
}

type cancellationIgnoringStatusService struct {
	*fakeReviewService
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (s *cancellationIgnoringStatusService) JobOutputStatus(_ context.Context, id int64) (roborev.JobOutputStatus, error) {
	s.mu.Lock()
	s.calls++
	calls := s.calls
	s.mu.Unlock()
	if calls == 1 {
		select {
		case s.entered <- struct{}{}:
		default:
		}
		<-s.release
	}
	s.fakeReviewService.mu.Lock()
	receipt := s.fakeReviewService.receipts[id]
	s.fakeReviewService.mu.Unlock()
	return roborev.JobOutputStatus{JobID: receipt.ID, Status: "queued"}, nil
}

func (s *cancellationIgnoringStatusService) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
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
	cancelCalls := service.cancelCalls()
	if result.Phase != PhaseCancelled || result.CancelState == nil || cancelCalls != 1 || len(result.Iterations) != 1 || result.Iterations[0].ReviewJobID != 1 || result.Iterations[0].PanelRunUUID == "" || result.Iterations[0].Error == "" {
		t.Fatalf("unexpected cancellation result: %#v, cancels=%d", result, cancelCalls)
	}
}

func TestManagerPollGuardStopsAfterCancellationIgnoringStatusService(t *testing.T) {
	service := &cancellationIgnoringStatusService{
		fakeReviewService: &fakeReviewService{verdicts: []string{"P"}},
		entered:           make(chan struct{}, 1),
		release:           make(chan struct{}),
	}
	manager, _ := newTestManager(t, service.fakeReviewService)
	manager.service = service
	manager.wait = func(context.Context, time.Duration) error { return nil }
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(service.release) }) }
	defer release()
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-service.entered:
	case <-time.After(time.Second):
		t.Fatal("status polling did not start")
	}
	if err := manager.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	release()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown did not complete after cancellation: %v", err)
	}
	result, ok := manager.Get(run.ID)
	if !ok || result.Phase != PhaseCancelled {
		t.Fatalf("canceled run was not terminalized: ok=%v result=%#v", ok, result)
	}
	if calls := service.callCount(); calls != 1 {
		t.Fatalf("cancellation-ignoring status service calls=%d, want 1", calls)
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
	manager.wait = immediateWait
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

func TestManagerMapsMalformedStatusAndTerminalMetadataToMalformedStop(t *testing.T) {
	tests := []struct {
		name          string
		outputError   error
		metadataError error
	}{
		{name: "status", outputError: roborev.ErrMalformedJobOutputStatus},
		{name: "terminal metadata", metadataError: roborev.ErrMalformedJobMetadata},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := &fakeReviewService{verdicts: []string{"P"}}
			service := &lightweightReviewService{fakeReviewService: base, statuses: []string{"done"}, outputErrors: 1, outputError: test.outputError, metadataError: test.metadataError}
			service.metadata = roborev.JobMetadata{ID: 1, RepoPath: `C:\repo`, GitRef: "head", Branch: "main", PanelName: "cross", PanelRunUUID: "00000000-0000-0000-0000-000000000001", PanelRole: "synthesis", JobType: "synthesis", Status: "done", Verdict: "P"}
			if test.metadataError != nil {
				service.outputErrors = 0
			}
			manager, _ := newTestManager(t, base)
			manager.service = service
			manager.wait = immediateWait
			defer manager.Shutdown(context.Background())
			run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
			if err != nil {
				t.Fatal(err)
			}
			result := waitRun(t, manager, run.ID)
			if result.StopReason != StopMalformedReviewStatus {
				t.Fatalf("stop reason=%s, want %s; result=%#v", result.StopReason, StopMalformedReviewStatus, result)
			}
		})
	}
}

func TestManagerUsesBoundedOutputStatusAndFetchesTerminalMetadataOnce(t *testing.T) {
	base := &fakeReviewService{verdicts: []string{"P"}}
	service := &lightweightReviewService{fakeReviewService: base, statuses: []string{"running", "done"}}
	service.metadata = roborev.JobMetadata{ID: 1, RepoPath: `C:\repo`, GitRef: "head", Branch: "main", PanelName: "cross", PanelRunUUID: "00000000-0000-0000-0000-000000000001", PanelRole: "synthesis", JobType: "synthesis", Status: "done", Verdict: "P"}
	manager, _ := newTestManager(t, base)
	manager.service = service
	manager.wait = immediateWait
	defer manager.Shutdown(context.Background())
	run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, manager, run.ID)
	if result.Phase != PhasePassed || service.statusCalls < 2 || service.metadataCalls != 1 {
		t.Fatalf("bounded status flow = phase=%s statusCalls=%d metadataCalls=%d", result.Phase, service.statusCalls, service.metadataCalls)
	}
}

func TestTerminalPanelResultRejectsEveryReceiptIdentityAndTerminalMismatch(t *testing.T) {
	request := roborev.ReviewRequest{RepoPath: `C:\repo`, GitRef: "head", Branch: "main", Panel: "cross"}
	receipt := roborev.PanelEnqueueResponse{ID: 1, RepoPath: `C:\repo`, GitRef: "head", Branch: "main", PanelName: "cross", PanelRunUUID: "run-1", PanelRole: "synthesis", JobType: "synthesis"}
	base := roborev.JobMetadata{ID: 1, RepoPath: `C:\repo`, GitRef: "head", Branch: "main", PanelName: "cross", PanelRunUUID: "run-1", PanelRole: "synthesis", JobType: "synthesis", Status: "done", Verdict: "P"}
	cases := []struct {
		name   string
		mutate func(*roborev.JobMetadata)
	}{
		{name: "repository", mutate: func(value *roborev.JobMetadata) { value.RepoPath = `C:\other` }},
		{name: "ref", mutate: func(value *roborev.JobMetadata) { value.GitRef = "other" }},
		{name: "branch", mutate: func(value *roborev.JobMetadata) { value.Branch = "other" }},
		{name: "panel", mutate: func(value *roborev.JobMetadata) { value.PanelName = "other" }},
		{name: "run uuid", mutate: func(value *roborev.JobMetadata) { value.PanelRunUUID = "run-2" }},
		{name: "role", mutate: func(value *roborev.JobMetadata) { value.PanelRole = "member" }},
		{name: "job type", mutate: func(value *roborev.JobMetadata) { value.JobType = "review" }},
		{name: "skipped", mutate: func(value *roborev.JobMetadata) { value.Skipped = true }},
		{name: "status", mutate: func(value *roborev.JobMetadata) { value.Status = "failed" }},
		{name: "verdict", mutate: func(value *roborev.JobMetadata) { value.Verdict = "?" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			if _, err := terminalPanelResult(value, request, receipt); err == nil {
				t.Fatal("terminal metadata mismatch was accepted")
			}
		})
	}
}

func TestManagerRetriesGenericStatusTransportButNotPermanentContractErrors(t *testing.T) {
	for _, test := range []struct {
		name         string
		outputErrors int
		outputError  error
		wantCalls    int
	}{
		{name: "transient transport", outputErrors: 2, wantCalls: 3},
		{name: "malformed contract", outputErrors: 1, outputError: roborev.ErrMalformedJobOutputStatus, wantCalls: 1},
		{name: "unsupported contract", outputErrors: 1, outputError: roborev.ErrUnsupportedJobOutputStatus, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := &fakeReviewService{verdicts: []string{"P"}}
			service := &lightweightReviewService{fakeReviewService: base, outputErrors: test.outputErrors, outputError: test.outputError}
			service.metadata = roborev.JobMetadata{ID: 1, RepoPath: `C:\repo`, GitRef: "head", Branch: "main", PanelName: "cross", PanelRunUUID: "00000000-0000-0000-0000-000000000001", PanelRole: "synthesis", JobType: "synthesis", Status: "done", Verdict: "P"}
			manager, _ := newTestManager(t, base)
			manager.service = service
			manager.wait = immediateWait
			defer manager.Shutdown(context.Background())
			run, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
			if err != nil {
				t.Fatal(err)
			}
			result := waitRun(t, manager, run.ID)
			if service.statusCalls != test.wantCalls {
				t.Fatalf("status calls=%d, want %d; result=%#v", service.statusCalls, test.wantCalls, result)
			}
		})
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
	service := &fakeReviewService{statusPending: true, statusEntered: make(chan int64, 2)}
	service.registry = registry
	manager, err := NewManager(Dependencies{Registry: registry, Panels: fakePanels{}, RoboRev: service, Binary: `C:\roborev.exe`, PollInterval: time.Millisecond, Wait: immediateWait})
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
	seen := make(map[int64]bool)
	for i := 0; i < 2; i++ {
		select {
		case id := <-service.statusEntered:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatal("both repository runs did not enter polling")
		}
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("polling did not enter for both repository receipts: seen=%v", seen)
	}
	if err := manager.Cancel(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Cancel(second.ID); err != nil {
		t.Fatal(err)
	}
	firstResult := waitRun(t, manager, first.ID)
	secondResult := waitRun(t, manager, second.ID)
	if firstResult.Phase != PhaseCancelled || secondResult.Phase != PhaseCancelled {
		t.Fatalf("independent repository cancellation = first=%s second=%s", firstResult.Phase, secondResult.Phase)
	}
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
			} else {
				var typed *StartError
				if !errors.As(err, &typed) {
					t.Fatalf("start error was not typed: %v", err)
				}
				want := StartInvalid
				switch tc.name {
				case "dirty":
					want = StartConflict
				case "unregistered":
					want = StartForbidden
				case "unsupported":
					want = StartUnavailable
				}
				if typed.Kind != want {
					t.Fatalf("start error kind=%s, want %s", typed.Kind, want)
				}
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

// blockingInspectRegistry blocks the first Inspect and reports whether the
// manager canceled the preflight context while it was blocked.
type blockingInspectRegistry struct {
	fakeRegistry
	entered     chan struct{}
	release     chan struct{}
	cancelled   chan struct{}
	cancelClose sync.Once
	once        sync.Once
}

func (r *blockingInspectRegistry) Inspect(ctx context.Context, path string) (gitx.Repository, error) {
	r.once.Do(func() { close(r.entered) })
	select {
	case <-r.release:
	case <-ctx.Done():
		r.cancelClose.Do(func() { close(r.cancelled) })
		// A canceled preflight still takes time to unwind; keep blocking
		// until the test releases it so Shutdown's wait is observable.
		<-r.release
		return gitx.Repository{}, ctx.Err()
	}
	return r.fakeRegistry.Inspect(ctx, path)
}

// An accepted Start that is still inside its blocking preflight is tracked by
// Shutdown: Shutdown stays waiting, cancels the manager-owned preflight
// context, the Start terminates without enqueueing any review, and only then
// does Shutdown return. Later Starts are rejected before any validation work.
func TestShutdownWaitsForAcceptedPreflightCancelsItAndRejectsLaterStarts(t *testing.T) {
	registry := &blockingInspectRegistry{
		fakeRegistry: fakeRegistry{repo: gitx.Repository{Path: `C:\repo`, Branch: "main", Head: "head"}},
		entered:      make(chan struct{}),
		release:      make(chan struct{}),
		cancelled:    make(chan struct{}),
	}
	service := &fakeReviewService{verdicts: []string{"P"}, registry: registry}
	manager, err := NewManager(Dependencies{Registry: registry, Panels: fakePanels{}, RoboRev: service, Binary: `C:\roborev.exe`})
	if err != nil {
		t.Fatal(err)
	}
	startErr := make(chan error, 1)
	go func() {
		_, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"})
		startErr <- err
	}()
	select {
	case <-registry.entered:
	case <-time.After(time.Second):
		t.Fatal("Start never entered repository validation")
	}
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		shutdownDone <- manager.Shutdown(shutdownCtx)
	}()
	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned while an accepted preflight was blocked: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-registry.cancelled:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not cancel the manager-owned preflight context")
	}
	close(registry.release)
	select {
	case err := <-startErr:
		// A shutdown-canceled preflight must be classified as a shutdown
		// conflict, not an unrelated repository-state error.
		var typedErr *StartError
		if !errors.As(err, &typedErr) || typedErr.Kind != StartConflict || !strings.Contains(err.Error(), "shutting down") {
			t.Fatalf("canceled preflight Start = %v; want shutdown conflict", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start never returned after its preflight was canceled")
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not return after the accepted Start terminated")
	}
	service.mu.Lock()
	reviewCount := service.reviewCount
	service.mu.Unlock()
	if reviewCount != 0 {
		t.Fatalf("ReviewPanel calls after shutdown = %d", reviewCount)
	}
	refineLease, err := manager.gate.AcquireRefine()
	if err != nil {
		t.Fatalf("gate lease was not released after rejected Start: %v", err)
	}
	refineLease()
	if _, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"}); err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("Start after shutdown = %v; want shutdown conflict", err)
	}
}

// An accepted review lease is part of the shutdown lifecycle: Shutdown stays
// waiting until the lease is released, and its repository gate is freed
// before Shutdown returns.
func TestReviewLeaseIsTrackedByShutdownUntilRelease(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"P"}}
	manager, _ := newTestManager(t, service)
	release, err := manager.AcquireReviewLease(`C:\repo`)
	if err != nil {
		t.Fatalf("AcquireReviewLease: %v", err)
	}
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- manager.Shutdown(context.Background()) }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned while a review lease was active: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not return after the review lease was released")
	}
	gateRelease, err := manager.gate.AcquireRepository("repo-key")
	if err != nil {
		t.Fatalf("repository gate was not released after shutdown: %v", err)
	}
	gateRelease()
}

// Once Shutdown has closed acceptance, no new review lease can be granted.
func TestReviewLeaseIsRejectedAfterShutdownBegan(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"P"}}
	manager, _ := newTestManager(t, service)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if _, err := manager.AcquireReviewLease(`C:\repo`); err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("lease after shutdown = %v; want shutdown rejection", err)
	}
}

// Double release must neither panic nor corrupt the shutdown accounting.
func TestReviewLeaseReleaseIsExactlyOnce(t *testing.T) {
	service := &fakeReviewService{verdicts: []string{"P"}}
	manager, _ := newTestManager(t, service)
	release, err := manager.AcquireReviewLease(`C:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown after double release: %v", err)
	}
}

// The pre-existing mutual exclusion between an active Panel Refine and a
// review lease on the same repository is preserved.
func TestReviewLeaseStillExcludesActivePanelRefine(t *testing.T) {
	service := &blockingReviewService{fakeReviewService: &fakeReviewService{verdicts: []string{"P"}}, started: make(chan struct{}, 1)}
	manager, _ := newTestManager(t, service.fakeReviewService)
	manager.service = service
	if _, err := manager.Start(context.Background(), StartRequest{RepoPath: `C:\repo`, Panel: "cross"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-service.started:
	case <-time.After(time.Second):
		t.Fatal("panel refine never reached status polling")
	}
	if _, err := manager.AcquireReviewLease(`C:\repo`); err == nil || !strings.Contains(err.Error(), "active Panel Refine") {
		t.Fatalf("lease during active refine = %v; want Panel Refine conflict", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
