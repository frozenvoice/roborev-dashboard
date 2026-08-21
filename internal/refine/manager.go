// Package refine owns the process-scoped Panel Refine state machine.  It does
// not persist runs: a dashboard restart intentionally interrupts active runs.
package refine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type Phase string

const (
	PhaseReviewing      Phase = "reviewing"
	PhaseEvaluating     Phase = "evaluating"
	PhaseFixing         Phase = "fixing"
	PhaseFinalReviewing Phase = "final_reviewing"
	PhasePassed         Phase = "passed"
	PhaseNeedsAttention Phase = "needs_attention"
	PhaseFailed         Phase = "failed"
	PhaseCancelled      Phase = "cancelled"
)

type StopReason string

const (
	StopNone                   StopReason = ""
	StopMaximumFixAttempts     StopReason = "maximum_fix_attempts"
	StopRepositoryChanged      StopReason = "repository_changed"
	StopFixDidNotCommit        StopReason = "fix_did_not_commit"
	StopFixLeftDirty           StopReason = "fix_left_dirty"
	StopReviewFailed           StopReason = "review_failed"
	StopFixFailed              StopReason = "fix_failed"
	StopMalformedReviewReceipt StopReason = "malformed_review_receipt"
	StopMalformedReviewStatus  StopReason = "malformed_review_status"
	StopCancelled              StopReason = "cancelled"
)

type PanelRefineIteration struct {
	Number       int    `json:"number"`
	TargetHead   string `json:"targetHead"`
	Panel        string `json:"panel"`
	ReviewJobID  int64  `json:"reviewJobId"`
	PanelRunUUID string `json:"panelRunUUID"`
	ReviewResult string `json:"reviewResult"`
	FixAttempted bool   `json:"fixAttempted"`
	FixAgent     string `json:"fixAgent,omitempty"`
	HeadAfterFix string `json:"headAfterFix,omitempty"`
	Error        string `json:"error,omitempty"`
}

type PanelRefineRun struct {
	ID                  string                 `json:"id"`
	RepositoryIdentity  string                 `json:"repositoryIdentity"`
	RepositoryPath      string                 `json:"repositoryPath"`
	Branch              string                 `json:"branch"`
	IterationPanel      string                 `json:"iterationPanel"`
	FinalPanel          string                 `json:"finalPanel,omitempty"`
	FixAgent            string                 `json:"fixAgent"`
	MaxFixAttempts      int                    `json:"maxFixAttempts"`
	FixAttempts         int                    `json:"fixAttempts"`
	Phase               Phase                  `json:"phase"`
	StartingHead        string                 `json:"startingHead"`
	CurrentHead         string                 `json:"currentHead"`
	CurrentReviewJobID  int64                  `json:"currentReviewJobId,omitempty"`
	CurrentPanelRunUUID string                 `json:"currentPanelRunUUID,omitempty"`
	StartedAt           time.Time              `json:"startedAt"`
	UpdatedAt           time.Time              `json:"updatedAt"`
	CompletedAt         *time.Time             `json:"completedAt,omitempty"`
	Error               string                 `json:"error,omitempty"`
	StopReason          StopReason             `json:"stopReason,omitempty"`
	Iterations          []PanelRefineIteration `json:"iterations"`
	Command             CommandTemplate        `json:"command"`
	CancelState         *CancelRepositoryState `json:"cancelState,omitempty"`
}

type CommandTemplate struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type Options struct {
	Executable            string   `json:"executable"`
	Args                  []string `json:"args"`
	AllowedFixAgents      []string `json:"allowedFixAgents"`
	DefaultFixAgent       string   `json:"defaultFixAgent"`
	DefaultMaxFixAttempts int      `json:"defaultMaxFixAttempts"`
	MinFixAttempts        int      `json:"minFixAttempts"`
	MaxFixAttempts        int      `json:"maxFixAttempts"`
}

type ListResponse struct {
	Runs    []*PanelRefineRun `json:"runs"`
	Run     *PanelRefineRun   `json:"run,omitempty"`
	Options Options           `json:"options"`
}

type CancelRepositoryState struct {
	Available bool   `json:"available"`
	Head      string `json:"head,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Dirty     bool   `json:"dirty,omitempty"`
	Error     string `json:"error,omitempty"`
}

type StartRequest struct {
	RepoPath       string `json:"repoPath"`
	Panel          string `json:"panel"`
	FixAgent       string `json:"fixAgent"`
	MaxFixAttempts int    `json:"maxFixAttempts"`
	FinalPanel     string `json:"finalPanel"`
}

type StartErrorKind string

const (
	StartInvalid     StartErrorKind = "invalid"
	StartForbidden   StartErrorKind = "forbidden"
	StartUnavailable StartErrorKind = "unavailable"
	StartConflict    StartErrorKind = "conflict"
	StartInternal    StartErrorKind = "internal"
)

// StartError carries stable HTTP classification separately from its user-facing
// detail. Callers must not infer policy from error strings.
type StartError struct {
	Kind StartErrorKind
	Err  error
}

func (e *StartError) Error() string {
	if e == nil || e.Err == nil {
		return string(StartInternal)
	}
	return e.Err.Error()
}
func (e *StartError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
func startError(kind StartErrorKind, err error) error { return &StartError{Kind: kind, Err: err} }

type RepositoryRegistry interface {
	Resolve(string) (string, error)
	Contains(string) bool
	Inspect(context.Context, string) (gitx.Repository, error)
	Identity(string) (string, error)
}

type ReviewService interface {
	Installation(context.Context) roborev.Installation
	JobOutputStatus(context.Context, int64) (roborev.JobOutputStatus, error)
	TerminalJobMetadata(context.Context, int64) (roborev.JobMetadata, error)
	ReviewPanel(context.Context, roborev.ReviewRequest) (roborev.PanelEnqueueResponse, error)
	Fix(context.Context, roborev.FixRequest) error
	Cancel(context.Context, int64) error
}

type Dependencies struct {
	Registry     RepositoryRegistry
	Panels       roborev.PanelCatalogResolver
	RoboRev      ReviewService
	Binary       string
	PollInterval time.Duration
	Wait         func(context.Context, time.Duration) error
	HistoryLimit int
	Gate         *operation.Gate
}

var (
	ErrPanelConfigurationUnavailable = errors.New("panel configuration is unavailable")
	ErrPanelUnavailable              = errors.New("panel is not currently available")
)

const (
	defaultFixAttempts = 3
	runCeiling         = 12 * time.Hour
	reviewTimeout      = 30 * time.Minute
	enqueueTimeout     = 30 * time.Second
	fixTimeout         = 45 * time.Minute
	pollInterval       = 4 * time.Second
)

type Manager struct {
	registry     RepositoryRegistry
	panels       roborev.PanelCatalogResolver
	service      ReviewService
	binary       string
	pollInterval time.Duration
	wait         func(context.Context, time.Duration) error
	historyLimit int
	gate         *operation.Gate

	mu            sync.RWMutex
	runs          map[string]*PanelRefineRun
	locks         map[string]string
	leases        map[string]string
	cancel        map[string]context.CancelFunc
	terminalOrder []string
	gateRelease   map[string]func()
	repoRelease   map[string]func()
	wg            sync.WaitGroup
}

type binaryPathProvider interface{ BinaryPath() string }

func NewManager(deps Dependencies) (*Manager, error) {
	if deps.Registry == nil || deps.Panels == nil || deps.RoboRev == nil {
		return nil, errors.New("panel refine dependencies are required")
	}
	if strings.TrimSpace(deps.Binary) == "" {
		deps.Binary = "roborev"
	}
	if deps.PollInterval <= 0 {
		deps.PollInterval = pollInterval
	}
	if deps.Wait == nil {
		deps.Wait = waitContext
	}
	if deps.HistoryLimit <= 0 {
		deps.HistoryLimit = 100
	}
	if deps.Gate == nil {
		deps.Gate = operation.NewGate()
	}
	return &Manager{registry: deps.Registry, panels: deps.Panels, service: deps.RoboRev, binary: deps.Binary, pollInterval: deps.PollInterval, wait: deps.Wait, historyLimit: deps.HistoryLimit, gate: deps.Gate, runs: make(map[string]*PanelRefineRun), locks: make(map[string]string), leases: make(map[string]string), cancel: make(map[string]context.CancelFunc), gateRelease: make(map[string]func()), repoRelease: make(map[string]func())}, nil
}

func (m *Manager) currentBinary() string {
	if provider, ok := m.service.(binaryPathProvider); ok {
		if path := strings.TrimSpace(provider.BinaryPath()); path != "" {
			return path
		}
	}
	return m.binary
}

func waitContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("refine-%d", time.Now().UnixNano())
	}
	return "refine-" + hex.EncodeToString(bytes[:])
}

func (m *Manager) Start(parent context.Context, request StartRequest) (*PanelRefineRun, error) {
	if m == nil || m.registry == nil || m.panels == nil || m.service == nil {
		return nil, startError(StartUnavailable, errors.New("panel refine manager is unavailable"))
	}
	if strings.TrimSpace(request.FixAgent) == "" {
		request.FixAgent = "codex"
	}
	if request.FixAgent != "codex" {
		return nil, startError(StartInvalid, errors.New("only the codex fix agent is supported"))
	}
	if request.MaxFixAttempts == 0 {
		request.MaxFixAttempts = defaultFixAttempts
	}
	if request.MaxFixAttempts < 1 || request.MaxFixAttempts > 10 {
		return nil, startError(StartInvalid, errors.New("maxFixAttempts must be between 1 and 10"))
	}
	if strings.TrimSpace(request.Panel) == "" {
		return nil, startError(StartInvalid, errors.New("panel is required"))
	}
	installation := m.service.Installation(parent)
	if err := validateInstallation(installation); err != nil {
		return nil, startError(StartUnavailable, err)
	}
	if !installation.Capabilities.PanelReview || !installation.Capabilities.FixCommand || !installation.Capabilities.JobOutputStatusAPI {
		return nil, startError(StartUnavailable, errors.New("RoboRev version does not support Panel Refine"))
	}
	path, err := m.registry.Resolve(request.RepoPath)
	if err != nil {
		if strings.TrimSpace(request.RepoPath) == "" {
			return nil, startError(StartInvalid, err)
		}
		return nil, startError(StartForbidden, err)
	}
	repo, err := m.registry.Inspect(parent, path)
	if err != nil {
		return nil, startError(StartUnavailable, fmt.Errorf("repository state is unavailable: %w", err))
	}
	if repo.Dirty {
		return nil, startError(StartConflict, errors.New("Panel Refine requires a clean working tree"))
	}
	if !m.registry.Contains(repo.Path) {
		return nil, startError(StartForbidden, errors.New("repository is not registered"))
	}
	identity, err := m.registry.Identity(repo.Path)
	if err != nil {
		return nil, startError(StartInternal, err)
	}
	if err := m.validatePanel(parent, repo.Path, request.Panel); err != nil {
		if errors.Is(err, ErrPanelConfigurationUnavailable) {
			return nil, startError(StartUnavailable, err)
		}
		return nil, startError(StartInvalid, err)
	}
	if request.FinalPanel != "" {
		if err := m.validatePanel(parent, repo.Path, request.FinalPanel); err != nil {
			if errors.Is(err, ErrPanelConfigurationUnavailable) {
				return nil, startError(StartUnavailable, err)
			}
			return nil, startError(StartInvalid, err)
		}
		if request.FinalPanel == request.Panel {
			request.FinalPanel = ""
		}
	}
	gateRelease, gateErr := m.gate.AcquireRefine()
	if gateErr != nil {
		return nil, startError(StartConflict, gateErr)
	}
	keepGate := false
	defer func() {
		if !keepGate {
			gateRelease()
		}
	}()

	m.mu.Lock()
	if existing := m.locks[identity]; existing != "" {
		m.mu.Unlock()
		return nil, startError(StartConflict, errors.New("a Panel Refine is already active for this repository"))
	}
	if m.leases[identity] != "" {
		m.mu.Unlock()
		return nil, startError(StartConflict, errors.New("repository has an active review mutation"))
	}
	repoRelease, repoErr := m.gate.AcquireRepository(identity)
	if repoErr != nil {
		m.mu.Unlock()
		return nil, startError(StartConflict, repoErr)
	}
	id := newID()
	now := time.Now().UTC()
	run := &PanelRefineRun{ID: id, RepositoryIdentity: identity, RepositoryPath: repo.Path, Branch: repo.Branch, IterationPanel: request.Panel, FinalPanel: request.FinalPanel, FixAgent: request.FixAgent, MaxFixAttempts: request.MaxFixAttempts, Phase: PhaseReviewing, StartingHead: repo.Head, CurrentHead: repo.Head, StartedAt: now, UpdatedAt: now, Iterations: []PanelRefineIteration{}, Command: CommandTemplate{Executable: m.currentBinary(), Args: []string{"fix", "--agent", request.FixAgent, "--quiet", "<synthesisJobID>"}}}
	m.runs[id] = run
	m.locks[identity] = id
	ctx, cancel := context.WithTimeout(context.Background(), runCeiling)
	m.cancel[id] = cancel
	m.gateRelease[id] = gateRelease
	m.repoRelease[id] = repoRelease
	keepGate = true
	m.wg.Add(1)
	snapshot := cloneRun(run)
	m.mu.Unlock()
	go m.execute(ctx, id)
	return snapshot, nil
}

// AcquireReviewLease serializes short-lived Review HEAD/Rerun mutations with
// Panel Refine start and with each other. The owner must explicitly release
// the lease; HTTP callers use defer and process shutdown drops all leases.
func (m *Manager) AcquireReviewLease(path string) (func(), error) {
	resolved, err := m.registry.Resolve(path)
	if err != nil {
		return nil, err
	}
	identity, err := m.registry.Identity(resolved)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locks[identity] != "" {
		return nil, errors.New("repository has an active Panel Refine")
	}
	if m.leases[identity] != "" {
		return nil, errors.New("repository has an active review mutation")
	}
	repoRelease, err := m.gate.AcquireRepository(identity)
	if err != nil {
		return nil, errors.New("repository has an active operation")
	}
	owner := newID()
	m.leases[identity] = owner
	return func() {
		m.mu.Lock()
		if m.leases[identity] == owner {
			delete(m.leases, identity)
		}
		m.mu.Unlock()
		repoRelease()
	}, nil
}

func (m *Manager) validatePanel(ctx context.Context, path, name string) error {
	catalog, err := m.panels.Resolve(ctx, path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPanelConfigurationUnavailable, err)
	}
	panel, ok := catalog.Find(name)
	if !ok || !panel.Valid || panel.Kind != "panel" {
		return ErrPanelUnavailable
	}
	return nil
}

func (m *Manager) execute(ctx context.Context, id string) {
	defer m.wg.Done()
	defer m.finishContext(id)
	defer func() {
		if recovered := recover(); recovered != nil {
			m.stop(id, PhaseFailed, StopReviewFailed, errors.New("Panel Refine worker failed"))
		}
	}()
	finalGate := false
	for {
		if ctx.Err() != nil {
			m.cancelled(id)
			return
		}
		run, ok := m.Get(id)
		if !ok {
			return
		}
		panel := run.IterationPanel
		if finalGate {
			panel = run.FinalPanel
			m.update(id, func(value *PanelRefineRun) { value.Phase = PhaseFinalReviewing })
		} else {
			m.update(id, func(value *PanelRefineRun) { value.Phase = PhaseReviewing })
		}
		result, iteration, err := m.review(ctx, id, panel)
		if err != nil {
			if ctx.Err() != nil {
				m.cancelled(id)
			} else {
				reason := classifyReviewStop(err)
				phase := PhaseFailed
				if reason == StopRepositoryChanged {
					phase = PhaseNeedsAttention
				}
				m.stop(id, phase, reason, err)
			}
			return
		}
		if ctx.Err() != nil {
			m.updateIterationError(id, iteration.ReviewJobID, ctx.Err())
			m.cancelled(id)
			return
		}
		if err := m.verifyReviewTarget(ctx, id, iteration.TargetHead); err != nil {
			m.updateIterationError(id, iteration.ReviewJobID, err)
			m.stop(id, PhaseNeedsAttention, StopRepositoryChanged, err)
			return
		}
		m.updateIteration(id, iteration.ReviewJobID, func(value *PanelRefineIteration) { value.ReviewResult = result })
		if ctx.Err() != nil {
			m.cancelled(id)
			return
		}
		if result == "P" {
			if finalGate || run.FinalPanel == "" {
				m.complete(id)
				return
			}
			finalGate = true
			continue
		}
		m.update(id, func(value *PanelRefineRun) { value.Phase = PhaseEvaluating })
		run, _ = m.Get(id)
		if run.FixAttempts >= run.MaxFixAttempts {
			m.stop(id, PhaseNeedsAttention, StopMaximumFixAttempts, errors.New("maximum fix attempts reached"))
			return
		}
		if err := m.performFix(ctx, id, iteration.ReviewJobID); err != nil {
			m.updateIterationError(id, iteration.ReviewJobID, err)
			if ctx.Err() != nil {
				m.cancelled(id)
			} else {
				m.stop(id, PhaseNeedsAttention, classifyFixStop(err), err)
			}
			return
		}
		finalGate = false
	}
}

func classifyReviewStop(err error) StopReason {
	if strings.Contains(err.Error(), "malformed panel review receipt") {
		return StopMalformedReviewReceipt
	}
	if errors.Is(err, roborev.ErrMalformedJobOutputStatus) || errors.Is(err, roborev.ErrMalformedJobMetadata) {
		return StopMalformedReviewStatus
	}
	if strings.Contains(err.Error(), "malformed panel status") || strings.Contains(err.Error(), "unknown panel review status") || strings.Contains(err.Error(), "no P/F verdict") || strings.Contains(err.Error(), "status identity") {
		return StopMalformedReviewStatus
	}
	if strings.Contains(err.Error(), "repository changed") || strings.Contains(err.Error(), "branch changed") || strings.Contains(err.Error(), "identity") {
		return StopRepositoryChanged
	}
	return StopReviewFailed
}

func classifyFixStop(err error) StopReason {
	if strings.Contains(err.Error(), "repository changed") || strings.Contains(err.Error(), "branch changed") || strings.Contains(err.Error(), "identity") {
		return StopRepositoryChanged
	}
	if strings.Contains(err.Error(), "without creating a commit") {
		return StopFixDidNotCommit
	}
	if strings.Contains(err.Error(), "uncommitted") {
		return StopFixLeftDirty
	}
	return StopFixFailed
}

func validateInstallation(installation roborev.Installation) error {
	if !installation.Installed {
		if strings.TrimSpace(installation.Error) != "" {
			return fmt.Errorf("RoboRev installation is unavailable: %s", installation.Error)
		}
		return errors.New("RoboRev installation is unavailable")
	}
	if strings.TrimSpace(installation.Error) != "" {
		return fmt.Errorf("RoboRev daemon is unavailable: %s", installation.Error)
	}
	var status struct {
		Running *bool `json:"running"`
	}
	if len(strings.TrimSpace(string(installation.Status))) == 0 || json.Unmarshal(installation.Status, &status) != nil || status.Running == nil || !*status.Running {
		return errors.New("RoboRev daemon is not running")
	}
	return nil
}

func (m *Manager) review(ctx context.Context, id, panel string) (string, PanelRefineIteration, error) {
	run, ok := m.Get(id)
	if !ok {
		return "", PanelRefineIteration{}, errors.New("Panel Refine run not found")
	}
	reviewCtx, cancel := context.WithTimeout(ctx, reviewTimeout)
	defer cancel()
	repo, err := m.registry.Inspect(reviewCtx, run.RepositoryPath)
	if err != nil {
		return "", PanelRefineIteration{}, fmt.Errorf("repository inspection failed: %w", err)
	}
	if repo.Dirty || repo.Head != run.CurrentHead || repo.Branch != run.Branch || !m.sameRegisteredRepository(run, repo) {
		return "", PanelRefineIteration{}, errors.New("repository changed outside Panel Refine")
	}
	if err := m.validatePanel(reviewCtx, repo.Path, panel); err != nil {
		return "", PanelRefineIteration{}, err
	}
	request := roborev.ReviewRequest{RepoPath: repo.Path, GitRef: repo.Head, Branch: repo.Branch, Panel: panel}
	enqueueCtx, enqueueCancel := context.WithTimeout(reviewCtx, enqueueTimeout)
	receipt, err := m.service.ReviewPanel(enqueueCtx, request)
	enqueueCancel()
	if err != nil {
		if errors.Is(err, roborev.ErrMalformedPanelEnqueueResponse) {
			return "", PanelRefineIteration{}, fmt.Errorf("malformed panel review receipt: %w", err)
		}
		return "", PanelRefineIteration{}, err
	}
	iteration := PanelRefineIteration{Number: len(run.Iterations) + 1, TargetHead: repo.Head, Panel: panel, ReviewJobID: receipt.ID, PanelRunUUID: receipt.PanelRunUUID}
	m.recordReviewReceipt(id, iteration, func(value *PanelRefineRun) {
		value.CurrentHead = repo.Head
		value.CurrentReviewJobID = receipt.ID
		value.CurrentPanelRunUUID = receipt.PanelRunUUID
	})
	if reviewCtx.Err() != nil {
		err := reviewCtx.Err()
		m.updateIterationError(id, receipt.ID, err)
		m.bestEffortCancel(receipt.ID)
		return "", iteration, err
	}
	result, err := m.poll(reviewCtx, request, receipt)
	if err != nil {
		m.updateIterationError(id, receipt.ID, err)
		if reviewCtx.Err() != nil {
			m.bestEffortCancel(receipt.ID)
		}
		return "", iteration, err
	}
	return result, iteration, nil
}

func (m *Manager) verifyReviewTarget(ctx context.Context, id, targetHead string) error {
	run, ok := m.Get(id)
	if !ok {
		return errors.New("Panel Refine run not found")
	}
	repo, err := m.registry.Inspect(ctx, run.RepositoryPath)
	if err != nil {
		return fmt.Errorf("repository changed outside Panel Refine: %w", err)
	}
	if repo.Dirty || repo.Head != targetHead || repo.Branch != run.Branch || !m.sameRegisteredRepository(run, repo) {
		return errors.New("repository changed outside Panel Refine")
	}
	return nil
}

func (m *Manager) poll(ctx context.Context, request roborev.ReviewRequest, receipt roborev.PanelEnqueueResponse) (string, error) {
	statusErrors := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		prefix, err := m.service.JobOutputStatus(ctx, receipt.ID)
		if err != nil {
			if errors.Is(err, roborev.ErrMalformedJobOutputStatus) || errors.Is(err, roborev.ErrUnsupportedJobOutputStatus) {
				return "", err
			}
			statusErrors++
			if statusErrors > 3 {
				return "", errors.New("panel status polling failed repeatedly")
			}
			if waitErr := m.wait(ctx, time.Duration(statusErrors)*time.Second); waitErr != nil {
				return "", waitErr
			}
			continue
		}
		statusErrors = 0
		if prefix.JobID != receipt.ID {
			return "", errors.New("panel status identity does not match review receipt")
		}
		switch prefix.Status {
		case "queued", "running":
			// Active output is deliberately not decoded beyond the prefix.
		case "done":
			job, err := m.terminalMetadata(ctx, receipt.ID)
			if err != nil {
				return "", err
			}
			return terminalPanelResult(job, request, receipt)
		default:
			return "", fmt.Errorf("unknown panel review status: %s", prefix.Status)
		}
		if err := m.wait(ctx, m.pollInterval); err != nil {
			return "", err
		}
	}
}

func (m *Manager) terminalMetadata(ctx context.Context, id int64) (roborev.JobMetadata, error) {
	for attempt := 0; attempt < 4; attempt++ {
		job, err := m.service.TerminalJobMetadata(ctx, id)
		if err == nil {
			return job, nil
		}
		if errors.Is(err, roborev.ErrMalformedJobMetadata) || errors.Is(err, roborev.ErrUnsupportedJobMetadata) {
			return roborev.JobMetadata{}, fmt.Errorf("terminal panel metadata lookup failed: %w", err)
		}
		if attempt == 3 {
			return roborev.JobMetadata{}, fmt.Errorf("terminal panel metadata lookup failed repeatedly: %w", err)
		}
		if waitErr := m.wait(ctx, time.Duration(attempt+1)*time.Second); waitErr != nil {
			return roborev.JobMetadata{}, waitErr
		}
	}
	return roborev.JobMetadata{}, errors.New("terminal panel metadata lookup failed repeatedly")
}

func terminalPanelResult(job roborev.JobMetadata, request roborev.ReviewRequest, receipt roborev.PanelEnqueueResponse) (string, error) {
	if job.ID != receipt.ID || !gitx.SameRepositoryPath(job.RepoPath, request.RepoPath) || job.GitRef != request.GitRef || job.Branch != request.Branch || job.PanelName != request.Panel || job.PanelRunUUID != receipt.PanelRunUUID || job.PanelRole != "synthesis" || job.JobType != "synthesis" || job.Skipped {
		return "", errors.New("panel status identity does not match review receipt")
	}
	if job.Status != "done" {
		return "", fmt.Errorf("unknown panel review status: %s", job.Status)
	}
	switch job.Verdict {
	case "P":
		return "P", nil
	case "F":
		return "F", nil
	default:
		return "", errors.New("panel synthesis has no P/F verdict")
	}
}

func (m *Manager) performFix(ctx context.Context, id string, jobID int64) error {
	run, ok := m.Get(id)
	if !ok {
		return errors.New("Panel Refine run not found")
	}
	repo, err := m.registry.Inspect(ctx, run.RepositoryPath)
	if err != nil {
		return fmt.Errorf("repository changed outside Panel Refine: %w", err)
	}
	if repo.Dirty || repo.Head != run.CurrentHead || repo.Branch != run.Branch || !m.sameRegisteredRepository(run, repo) {
		return errors.New("repository changed outside Panel Refine")
	}
	m.update(id, func(value *PanelRefineRun) {
		value.Phase = PhaseFixing
		value.FixAttempts++
		if len(value.Iterations) > 0 {
			iteration := &value.Iterations[len(value.Iterations)-1]
			if iteration.ReviewJobID == jobID {
				iteration.FixAttempted = true
				iteration.FixAgent = value.FixAgent
			}
		}
	})
	fixCtx, fixCancel := context.WithTimeout(ctx, fixTimeout)
	err = m.service.Fix(fixCtx, roborev.FixRequest{RepoPath: repo.Path, JobID: jobID, Agent: run.FixAgent})
	fixCancel()
	if err != nil {
		return err
	}
	after, err := m.registry.Inspect(ctx, run.RepositoryPath)
	if err != nil {
		return fmt.Errorf("repository changed outside Panel Refine: %w", err)
	}
	if !m.sameRegisteredRepository(run, after) || !gitx.SameRepositoryPath(after.Path, repo.Path) {
		return errors.New("repository identity changed during fix")
	}
	if after.Branch != repo.Branch {
		return errors.New("branch changed during fix")
	}
	if after.Dirty {
		return errors.New("fix agent left uncommitted changes")
	}
	if after.Head == repo.Head {
		return errors.New("fix agent completed without creating a commit")
	}
	m.update(id, func(value *PanelRefineRun) {
		value.CurrentHead = after.Head
		value.Branch = after.Branch
		for index := len(value.Iterations) - 1; index >= 0; index-- {
			if value.Iterations[index].ReviewJobID == jobID {
				value.Iterations[index].HeadAfterFix = after.Head
				break
			}
		}
	})
	return nil
}

func (m *Manager) sameRegisteredRepository(run *PanelRefineRun, repo gitx.Repository) bool {
	if run == nil || !m.registry.Contains(repo.Path) || !gitx.SameRepositoryPath(repo.Path, run.RepositoryPath) {
		return false
	}
	identity, err := m.registry.Identity(repo.Path)
	return err == nil && identity == run.RepositoryIdentity
}

func (m *Manager) Cancel(id string) error {
	run, ok := m.Get(id)
	m.mu.RLock()
	cancel := m.cancel[id]
	m.mu.RUnlock()
	if !ok {
		return errors.New("Panel Refine run not found")
	}
	if isTerminal(run.Phase) {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	return nil
}

func (m *Manager) bestEffortCancel(id int64) {
	if id <= 0 {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = m.service.Cancel(cleanupCtx, id)
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.RLock()
	ids := make([]string, 0, len(m.cancel))
	for id := range m.cancel {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		_ = m.Cancel(id)
	}
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) List(repoPath string) []*PanelRefineRun {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*PanelRefineRun, 0, len(m.runs))
	for _, run := range m.runs {
		if repoPath == "" || gitx.SameRepositoryPath(run.RepositoryPath, repoPath) {
			result = append(result, cloneRun(run))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt.Before(result[j].UpdatedAt) })
	return result
}

func (m *Manager) ListResponse(repoPath string) ListResponse {
	runs := m.List(repoPath)
	var latest *PanelRefineRun
	for _, run := range runs {
		if latest == nil || run.UpdatedAt.After(latest.UpdatedAt) {
			latest = run
		}
	}
	return ListResponse{Runs: runs, Run: latest, Options: Options{
		Executable:       m.currentBinary(),
		Args:             append([]string(nil), []string{"fix", "--agent", "codex", "--quiet", "<synthesisJobID>"}...),
		AllowedFixAgents: []string{"codex"}, DefaultFixAgent: "codex", DefaultMaxFixAttempts: defaultFixAttempts, MinFixAttempts: 1, MaxFixAttempts: 10,
	}}
}

func (m *Manager) Get(id string) (*PanelRefineRun, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.runs[id]
	if !ok {
		return nil, false
	}
	return cloneRun(run), true
}

func (m *Manager) update(id string, fn func(*PanelRefineRun)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if run := m.runs[id]; run != nil {
		fn(run)
		run.UpdatedAt = time.Now().UTC()
	}
}

func (m *Manager) recordReviewReceipt(id string, iteration PanelRefineIteration, update func(*PanelRefineRun)) {
	m.update(id, func(run *PanelRefineRun) {
		update(run)
		run.Iterations = append(run.Iterations, iteration)
		if len(run.Iterations) > 100 {
			run.Iterations = append([]PanelRefineIteration(nil), run.Iterations[len(run.Iterations)-100:]...)
		}
	})
}

func (m *Manager) updateIteration(id string, jobID int64, update func(*PanelRefineIteration)) {
	m.update(id, func(run *PanelRefineRun) {
		for index := len(run.Iterations) - 1; index >= 0; index-- {
			if run.Iterations[index].ReviewJobID == jobID {
				update(&run.Iterations[index])
				return
			}
		}
	})
}

func (m *Manager) updateIterationError(id string, jobID int64, err error) {
	if err == nil {
		return
	}
	m.updateIteration(id, jobID, func(iteration *PanelRefineIteration) { iteration.Error = err.Error() })
}
func (m *Manager) complete(id string) {
	m.setTerminal(id, PhasePassed, StopNone, nil, nil)
}
func (m *Manager) setTerminal(id string, phase Phase, reason StopReason, err error, state *CancelRepositoryState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run := m.runs[id]
	if run == nil || isTerminal(run.Phase) {
		return
	}
	now := time.Now().UTC()
	run.Phase = phase
	run.CompletedAt = &now
	run.StopReason = reason
	if err != nil {
		run.Error = err.Error()
	}
	if state != nil {
		copy := *state
		run.CancelState = &copy
	}
	run.UpdatedAt = now
	m.terminalOrder = append(m.terminalOrder, id)
	for len(m.terminalOrder) > m.historyLimit {
		oldest := m.terminalOrder[0]
		m.terminalOrder = m.terminalOrder[1:]
		if oldRun := m.runs[oldest]; oldRun != nil && isTerminal(oldRun.Phase) {
			delete(m.runs, oldest)
		}
	}
}
func (m *Manager) stop(id string, phase Phase, reason StopReason, err error) {
	m.setTerminal(id, phase, reason, err, nil)
}
func (m *Manager) cancelled(id string) {
	state := &CancelRepositoryState{}
	inspectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if run, ok := m.Get(id); ok {
		if repo, err := m.registry.Inspect(inspectCtx, run.RepositoryPath); err == nil {
			state.Available = true
			state.Head, state.Branch, state.Dirty = repo.Head, repo.Branch, repo.Dirty
		} else {
			state.Error = err.Error()
		}
	}
	cancel()
	m.setTerminal(id, PhaseCancelled, StopCancelled, context.Canceled, state)
}
func (m *Manager) finishContext(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if release := m.gateRelease[id]; release != nil {
		delete(m.gateRelease, id)
		release()
	}
	if release := m.repoRelease[id]; release != nil {
		delete(m.repoRelease, id)
		release()
	}
	if run := m.runs[id]; run != nil && isTerminal(run.Phase) {
		delete(m.locks, run.RepositoryIdentity)
		delete(m.cancel, id)
	}
}
func isTerminal(phase Phase) bool {
	return phase == PhasePassed || phase == PhaseNeedsAttention || phase == PhaseFailed || phase == PhaseCancelled
}

func cloneRun(run *PanelRefineRun) *PanelRefineRun {
	if run == nil {
		return nil
	}
	copy := *run
	copy.Command.Args = append([]string(nil), run.Command.Args...)
	copy.Iterations = append([]PanelRefineIteration(nil), run.Iterations...)
	if run.CancelState != nil {
		state := *run.CancelState
		copy.CancelState = &state
	}
	return &copy
}
