// Package fixoperation owns Dashboard's process-scoped, confirmed RoboRev
// fix commands. It deliberately does not persist operations or expose child
// stdout/stderr to the browser.
package fixoperation

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	defaultHistoryLimit = 100
	fixCeiling          = 12 * time.Hour
	inspectTimeout      = 10 * time.Second
)

type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "cancelled"
)

const (
	ErrorCodeFixCommandFailed      = "fix_command_failed"
	ErrorCodeFixDidNotCommit       = "fix_did_not_commit"
	ErrorCodeFixLeftDirty          = "fix_left_dirty"
	ErrorCodeRepositoryChanged     = "repository_changed"
	ErrorCodeRepositoryUnavailable = "repository_unavailable"
	ErrorCodeOperationTimedOut     = "operation_timed_out"
	ErrorCodeCanceled              = "cancelled"
)

type ErrorKind string

const (
	ErrorInvalid     ErrorKind = "invalid"
	ErrorForbidden   ErrorKind = "forbidden"
	ErrorUnavailable ErrorKind = "unavailable"
	ErrorConflict    ErrorKind = "conflict"
	ErrorNotFound    ErrorKind = "not_found"
	ErrorInternal    ErrorKind = "internal"
)

type Error struct {
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string {
	if e == nil || e.Err == nil {
		return string(ErrorInternal)
	}
	return e.Err.Error()
}
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
func typed(kind ErrorKind, err error) error { return &Error{Kind: kind, Err: err} }

type RepositoryRegistry interface {
	ResolveIdentity(string) (string, error)
	InspectIdentity(context.Context, string) (gitx.Repository, error)
	Identity(string) (string, error)
}

type selectedRepositoryRegistry interface {
	Selected() string
}

type Service interface {
	Installation(context.Context) roborev.Installation
	TerminalJobMetadata(context.Context, int64) (roborev.JobMetadata, error)
	Fix(context.Context, roborev.FixRequest) error
}

type Dependencies struct {
	Registry     RepositoryRegistry
	RoboRev      Service
	Binary       string
	Gate         *operation.Gate
	HistoryLimit int
}

type Request struct {
	RepositoryIdentity string `json:"repositoryIdentity"`
	TargetJobID        int64  `json:"targetJobId"`
	Agent              string `json:"agent"`
}

type Command struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

// Target is the intentionally small, stable target projection consumed by
// the browser. Full RoboRev metadata remains internal to validation.
type Target struct {
	JobID     int64  `json:"jobId"`
	GitRef    string `json:"gitRef"`
	Branch    string `json:"branch"`
	JobType   string `json:"jobType"`
	PanelRole string `json:"panelRole"`
	PanelName string `json:"panelName,omitempty"`
}

type Preview struct {
	RepositoryIdentity string  `json:"repositoryIdentity"`
	RepositoryPath     string  `json:"repositoryPath"`
	StartingHead       string  `json:"startingHead"`
	StartingBranch     string  `json:"startingBranch"`
	Target             Target  `json:"target"`
	Agent              string  `json:"agent"`
	Command            Command `json:"command"`
	PreviewToken       string  `json:"previewToken"`
}

type StartRequest struct {
	Request
	PreviewToken string `json:"previewToken"`
}

type Operation struct {
	ID                 string     `json:"id"`
	RepositoryIdentity string     `json:"repositoryIdentity"`
	RepositoryPath     string     `json:"repositoryPath"`
	TargetJobID        int64      `json:"targetJobId,omitempty"`
	Target             Target     `json:"target"`
	Agent              string     `json:"agent"`
	Phase              Status     `json:"phase"`
	Status             Status     `json:"status,omitempty"`
	Command            Command    `json:"command"`
	StartingHead       string     `json:"startingHead"`
	StartingBranch     string     `json:"startingBranch"`
	NewHead            string     `json:"newHead,omitempty"`
	EndingHead         string     `json:"endingHead,omitempty"`
	EndingBranch       string     `json:"endingBranch,omitempty"`
	EndingDirty        bool       `json:"endingDirty,omitempty"`
	StartedAt          time.Time  `json:"startedAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	CompletedAt        *time.Time `json:"completedAt,omitempty"`
	ErrorCode          string     `json:"errorCode,omitempty"`
	Error              string     `json:"error,omitempty"`
}

type ListResponse struct {
	Operations []*Operation `json:"operations"`
	Operation  *Operation   `json:"operation,omitempty"`
}

type manager struct {
	registry RepositoryRegistry
	service  Service
	binary   string
	gate     *operation.Gate
	history  int

	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc

	mu      sync.RWMutex
	closing bool
	items   map[string]*Operation
	cancel  map[string]context.CancelFunc
	global  map[string]func()
	repo    map[string]func()
	order   []string
	wg      sync.WaitGroup
}

// Manager is exported as an alias so callers cannot construct one without
// NewManager's dependency checks.
type Manager = manager

func NewManager(deps Dependencies) (*Manager, error) {
	if deps.Registry == nil || deps.RoboRev == nil {
		return nil, errors.New("fix operation dependencies are required")
	}
	if deps.Gate == nil {
		deps.Gate = operation.NewGate()
	}
	if deps.HistoryLimit <= 0 {
		deps.HistoryLimit = defaultHistoryLimit
	}
	if strings.TrimSpace(deps.Binary) == "" {
		deps.Binary = "roborev"
	}
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	return &manager{registry: deps.Registry, service: deps.RoboRev, binary: deps.Binary, gate: deps.Gate, history: deps.HistoryLimit, items: make(map[string]*Operation), cancel: make(map[string]context.CancelFunc), global: make(map[string]func()), repo: make(map[string]func()), shutdownCtx: shutdownCtx, shutdownCancel: shutdownCancel}, nil
}

// mergePreflightContext derives one validation context from the caller's
// request, the manager-owned shutdown context, and the inspection ceiling so
// Shutdown can cancel an accepted Start that is still inside its preflight.
func mergePreflightContext(parent, shutdown context.Context, ceiling time.Duration) (context.Context, func()) {
	ctx, cancel := context.WithCancel(shutdown)
	timer := time.AfterFunc(ceiling, cancel)
	stop := make(chan struct{})
	go func() {
		select {
		case <-parent.Done():
			cancel()
		case <-stop:
		}
	}()
	return ctx, func() {
		timer.Stop()
		close(stop)
		cancel()
	}
}

func (m *manager) currentBinary() string {
	if provider, ok := m.service.(interface{ BinaryPath() string }); ok {
		if value := strings.TrimSpace(provider.BinaryPath()); value != "" {
			return value
		}
	}
	return m.binary
}

func (m *manager) Prepare(ctx context.Context, request Request) (*Preview, error) {
	if m == nil {
		return nil, typed(ErrorUnavailable, errors.New("fix operation manager is unavailable"))
	}
	request = normalizeRequest(request)
	if err := validateRequest(request); err != nil {
		return nil, typed(ErrorInvalid, err)
	}
	preview, err := m.validate(ctx, request)
	if err != nil {
		return nil, err
	}
	return preview, nil
}

func validateRequest(request Request) error {
	if strings.TrimSpace(request.RepositoryIdentity) == "" {
		return errors.New("repository identity is required")
	}
	if request.TargetJobID <= 0 {
		return errors.New("target job ID must be positive")
	}
	if request.Agent != "codex" {
		return errors.New("only the codex fix agent is supported")
	}
	return nil
}

func normalizeRequest(request Request) Request {
	if strings.TrimSpace(request.Agent) == "" {
		request.Agent = "codex"
	}
	return request
}

func (m *manager) validate(ctx context.Context, request Request) (*Preview, error) {
	if err := validateRequest(request); err != nil {
		return nil, typed(ErrorInvalid, err)
	}
	installation := m.service.Installation(ctx)
	if !installation.Installed || !roborevcompat.IsSupportedVersion(installation.Version) || !installation.Capabilities.FixCommand || !installation.Capabilities.JobListAPI {
		return nil, typed(ErrorUnavailable, errors.New("RoboRev v0.64 fix capability is unavailable"))
	}
	path, err := m.registry.ResolveIdentity(request.RepositoryIdentity)
	if err != nil {
		return nil, typed(ErrorForbidden, err)
	}
	if selected, ok := m.registry.(selectedRepositoryRegistry); ok {
		selectedPath := strings.TrimSpace(selected.Selected())
		if selectedPath == "" {
			return nil, typed(ErrorConflict, errors.New("no repository is selected"))
		}
		selectedIdentity, identityErr := m.registry.Identity(selectedPath)
		if identityErr != nil || selectedIdentity != request.RepositoryIdentity {
			return nil, typed(ErrorConflict, errors.New("selected repository changed; refresh and retry"))
		}
	}
	repo, err := m.registry.InspectIdentity(ctx, request.RepositoryIdentity)
	if err != nil {
		return nil, typed(ErrorConflict, fmt.Errorf("repository state is unavailable: %w", err))
	}
	if !gitx.SameRepositoryPath(path, repo.Path) {
		return nil, typed(ErrorForbidden, errors.New("repository identity changed"))
	}
	if repo.Dirty {
		return nil, typed(ErrorConflict, errors.New("Fix requires a clean working tree"))
	}
	job, err := m.service.TerminalJobMetadata(ctx, request.TargetJobID)
	if err != nil {
		return nil, metadataError(err)
	}
	if job.ID != request.TargetJobID {
		return nil, typed(ErrorConflict, errors.New("target job identity changed"))
	}
	if err := eligible(job, repo); err != nil {
		return nil, typed(ErrorConflict, err)
	}
	args, err := roborev.FixArgs(job.ID, request.Agent)
	if err != nil {
		return nil, typed(ErrorInvalid, err)
	}
	preview := &Preview{RepositoryIdentity: request.RepositoryIdentity, RepositoryPath: repo.Path, StartingHead: repo.Head, StartingBranch: repo.Branch, Target: targetProjection(job), Agent: request.Agent, Command: Command{Executable: m.currentBinary(), Args: append([]string(nil), args...)}}
	preview.PreviewToken = previewToken(preview)
	return preview, nil
}

func metadataError(err error) error {
	if err == nil {
		return nil
	}
	// The v0.64 adapter uses a stable plain "job not found" error for the
	// authoritative empty result. Transport, malformed JSON, and unsupported
	// contracts are unavailable rather than a misleading 404.
	if strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "job not found") {
		return typed(ErrorNotFound, err)
	}
	return typed(ErrorUnavailable, err)
}

func eligible(job roborev.JobMetadata, repo gitx.Repository) error {
	if job.Status != "done" {
		return errors.New("target job is not terminal")
	}
	if job.Verdict != "F" {
		return errors.New("target job is not an actionable finding")
	}
	if job.Skipped {
		return errors.New("skipped jobs cannot be fixed")
	}
	if job.Closed == nil {
		return errors.New("target job closure state is unavailable")
	}
	if *job.Closed {
		return errors.New("closed findings cannot be fixed")
	}
	if strings.TrimSpace(job.RepoPath) == "" || !gitx.SameRepositoryPath(job.RepoPath, repo.Path) {
		return errors.New("target job repository does not match the selected repository")
	}
	if job.GitRef == "" || job.GitRef != repo.Head || job.Branch == "" || job.Branch != repo.Branch {
		return errors.New("target review is stale; review the current HEAD before fixing")
	}
	switch {
	case job.JobType == "review" && job.PanelRole == "" && job.PanelName == "" && job.PanelRunUUID == "":
		return nil
	case job.JobType == "synthesis" && job.PanelRole == "synthesis" && job.PanelName != "" && job.PanelRunUUID != "":
		return nil
	case job.PanelRole == "member":
		return errors.New("panel member jobs cannot be fixed directly")
	default:
		return errors.New("target job is not a supported single review or panel synthesis")
	}
}

func targetProjection(job roborev.JobMetadata) Target {
	return Target{JobID: job.ID, GitRef: job.GitRef, Branch: job.Branch, JobType: job.JobType, PanelRole: job.PanelRole, PanelName: job.PanelName}
}

func previewToken(preview *Preview) string {
	h := sha256.New()
	writeField := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(value))
	}
	for _, value := range []string{
		preview.RepositoryIdentity,
		preview.RepositoryPath,
		preview.StartingHead,
		preview.StartingBranch,
		strconv.FormatInt(preview.Target.JobID, 10),
		preview.Target.GitRef,
		preview.Target.Branch,
		preview.Target.JobType,
		preview.Target.PanelRole,
		preview.Target.PanelName,
		preview.Agent,
		preview.Command.Executable,
	} {
		writeField(value)
	}
	for _, arg := range preview.Command.Args {
		writeField(arg)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (m *manager) Start(parent context.Context, request StartRequest) (*Operation, error) {
	if parent == nil {
		parent = context.Background()
	}
	request.Request = normalizeRequest(request.Request)
	if err := parent.Err(); err != nil {
		return nil, typed(ErrorConflict, err)
	}
	if err := validateRequest(request.Request); err != nil {
		return nil, typed(ErrorInvalid, err)
	}
	if strings.TrimSpace(request.PreviewToken) == "" {
		return nil, typed(ErrorInvalid, errors.New("preview token is required"))
	}
	// Acceptance into the shutdown lifecycle is linearized under the manager
	// mutex before any gate lease or blocking preflight: once Shutdown has set
	// closing, no new Start lifecycle can be registered, and every accepted
	// Start is waited on by Shutdown until it terminates.
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return nil, typed(ErrorConflict, errors.New("server is shutting down"))
	}
	m.wg.Add(1)
	m.mu.Unlock()
	tracked := true
	defer func() {
		if tracked {
			m.wg.Done()
		}
	}()
	global, err := m.gate.AcquireFix()
	if err != nil {
		return nil, typed(ErrorConflict, err)
	}
	repoLease, err := m.gate.AcquireRepository(request.RepositoryIdentity)
	if err != nil {
		global()
		return nil, typed(ErrorConflict, err)
	}
	validationCtx, validationCancel := mergePreflightContext(parent, m.shutdownCtx, inspectTimeout)
	preview, err := m.validate(validationCtx, request.Request)
	validationCancel()
	if parentErr := parent.Err(); parentErr != nil {
		err = typed(ErrorConflict, parentErr)
	}
	if err != nil {
		repoLease()
		global()
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(preview.PreviewToken), []byte(request.PreviewToken)) != 1 {
		repoLease()
		global()
		return nil, typed(ErrorConflict, errors.New("fix preview is stale; prepare a new preview"))
	}
	if err := parent.Err(); err != nil {
		repoLease()
		global()
		return nil, typed(ErrorConflict, err)
	}
	now := time.Now().UTC()
	id := newID()
	item := &Operation{ID: id, RepositoryIdentity: preview.RepositoryIdentity, RepositoryPath: preview.RepositoryPath, TargetJobID: preview.Target.JobID, Target: preview.Target, Agent: preview.Agent, Phase: StatusRunning, Status: StatusRunning, Command: preview.Command, StartingHead: preview.StartingHead, StartingBranch: preview.StartingBranch, StartedAt: now, UpdatedAt: now}
	ctx, cancel := context.WithTimeout(context.Background(), fixCeiling)
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		cancel()
		repoLease()
		global()
		return nil, typed(ErrorConflict, errors.New("server is shutting down"))
	}
	m.items[id] = item
	m.cancel[id] = cancel
	m.global[id] = global
	m.repo[id] = repoLease
	snapshot := clone(item)
	m.mu.Unlock()
	// Ownership of the lifecycle counter moves to execute's deferred Done.
	tracked = false
	go m.execute(ctx, id)
	return snapshot, nil
}

func newID() string { return fmt.Sprintf("fix-%d", time.Now().UnixNano()) }

func (m *manager) execute(ctx context.Context, id string) {
	defer m.wg.Done()
	defer m.finish(id)
	item, ok := m.Get(id)
	if !ok {
		return
	}
	err := m.service.Fix(ctx, roborev.FixRequest{RepoPath: item.RepositoryPath, JobID: item.TargetJobID, Agent: item.Agent})
	inspectCtx, cancel := context.WithTimeout(context.Background(), inspectTimeout)
	repo, inspectErr := m.registry.InspectIdentity(inspectCtx, item.RepositoryIdentity)
	cancel()
	if errors.Is(ctx.Err(), context.Canceled) {
		m.complete(id, StatusCanceled, ErrorCodeCanceled, repo, inspectErr, context.Canceled)
		return
	}
	if ctx.Err() != nil {
		m.complete(id, StatusFailed, ErrorCodeOperationTimedOut, repo, inspectErr, safeError(ctx.Err()))
		return
	}
	if err != nil {
		m.complete(id, StatusFailed, ErrorCodeFixCommandFailed, repo, inspectErr, safeError(err))
		return
	}
	if inspectErr != nil {
		m.complete(id, StatusFailed, ErrorCodeRepositoryUnavailable, repo, inspectErr, errors.New("repository state could not be verified after fix"))
		return
	}
	if !gitx.SameRepositoryPath(repo.Path, item.RepositoryPath) {
		m.complete(id, StatusFailed, ErrorCodeRepositoryChanged, repo, nil, errors.New("repository path changed while Fix was running"))
		return
	}
	identity, identityErr := m.registry.Identity(repo.Path)
	if identityErr != nil {
		m.complete(id, StatusFailed, ErrorCodeRepositoryUnavailable, repo, nil, errors.New("repository identity could not be verified after fix"))
		return
	}
	if identity != item.RepositoryIdentity || repo.Branch != item.StartingBranch {
		m.complete(id, StatusFailed, ErrorCodeRepositoryChanged, repo, nil, errors.New("repository changed while Fix was running"))
		return
	}
	if repo.Head == item.StartingHead {
		m.complete(id, StatusFailed, ErrorCodeFixDidNotCommit, repo, nil, errors.New("Fix did not create a new commit"))
		return
	}
	if repo.Dirty {
		m.complete(id, StatusFailed, ErrorCodeFixLeftDirty, repo, nil, errors.New("Fix left the working tree dirty"))
		return
	}
	m.complete(id, StatusSucceeded, "", repo, nil, nil)
}

func safeError(err error) error {
	if err == nil {
		return nil
	}
	value := strings.TrimSpace(err.Error())
	value = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	if len(value) > 1024 {
		truncated := value[:1021]
		for !utf8.ValidString(truncated) {
			truncated = truncated[:len(truncated)-1]
		}
		value = truncated + "..."
	}
	return errors.New(value)
}

func (m *manager) complete(id string, status Status, errorCode string, repo gitx.Repository, inspectErr error, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	if item == nil || item.CompletedAt != nil {
		return
	}
	now := time.Now().UTC()
	item.Phase, item.Status, item.UpdatedAt, item.CompletedAt = status, status, now, &now
	item.ErrorCode = errorCode
	if inspectErr != nil && status == StatusCanceled {
		item.Error = safeError(inspectErr).Error()
	} else if err != nil {
		item.Error = err.Error()
	}
	if repo.Head != "" {
		item.NewHead = repo.Head
		item.EndingHead = repo.Head
		item.EndingBranch = repo.Branch
		item.EndingDirty = repo.Dirty
	}
	m.order = append(m.order, id)
	for len(m.order) > m.history {
		old := m.order[0]
		m.order = m.order[1:]
		if oldItem := m.items[old]; oldItem != nil && oldItem.CompletedAt != nil {
			delete(m.items, old)
		}
	}
}

func (m *manager) finish(id string) {
	m.mu.Lock()
	global, repo := m.global[id], m.repo[id]
	delete(m.global, id)
	delete(m.repo, id)
	delete(m.cancel, id)
	m.mu.Unlock()
	if repo != nil {
		repo()
	}
	if global != nil {
		global()
	}
}

func (m *manager) Get(id string) (*Operation, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	item, ok := m.items[id]
	if !ok {
		return nil, false
	}
	return clone(item), true
}

func (m *manager) List(identity string) ListResponse {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]*Operation, 0)
	for _, item := range m.items {
		if identity == "" || item.RepositoryIdentity == identity {
			items = append(items, clone(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartedAt.Before(items[j].StartedAt) })
	var latest *Operation
	if len(items) > 0 {
		latest = items[len(items)-1]
	}
	return ListResponse{Operations: items, Operation: latest}
}

func (m *manager) Cancel(id string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cancel := m.cancel[id]
	item := m.items[id]
	if item == nil {
		return typed(ErrorNotFound, errors.New("fix operation not found"))
	}
	if cancel == nil || item.CompletedAt != nil {
		return typed(ErrorConflict, errors.New("fix operation is already terminal"))
	}
	cancel()
	return nil
}

// Shutdown closes new Start acceptance under the manager mutex, cancels the
// manager-owned preflight context and every registered operation, and waits
// for the gate leases and Fix work to finish.
func (m *manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closing = true
	cancels := make([]context.CancelFunc, 0, len(m.cancel))
	for _, cancel := range m.cancel {
		cancels = append(cancels, cancel)
	}
	m.mu.Unlock()
	if m.shutdownCancel != nil {
		m.shutdownCancel()
	}
	for _, cancel := range cancels {
		cancel()
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

func clone(item *Operation) *Operation {
	if item == nil {
		return nil
	}
	copy := *item
	copy.Command.Args = append([]string(nil), item.Command.Args...)
	return &copy
}
