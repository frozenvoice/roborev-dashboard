// Package analysis owns the short-lived, confirmed RoboRev Analyze command.
// It deliberately keeps no operation history: RoboRev's authoritative jobs
// list remains the source of truth after the enqueue receipt is returned.
package analysis

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	inspectTimeout         = 10 * time.Second
	analysisEnqueueCeiling = 60 * time.Second
)

type ErrorKind string

const (
	ErrorInvalid     ErrorKind = "invalid"
	ErrorForbidden   ErrorKind = "forbidden"
	ErrorUnavailable ErrorKind = "unavailable"
	ErrorConflict    ErrorKind = "conflict"
	ErrorAmbiguous   ErrorKind = "ambiguous"
)

type Error struct {
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string {
	if e == nil || e.Err == nil {
		return string(ErrorUnavailable)
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

// SelectedLeaseRegistry is implemented by app.Registry. It keeps the
// selected checkout stable through validation and the short CLI enqueue.
type SelectedLeaseRegistry interface {
	AcquireSelected(string) (func(), error)
}

type selectedRepositoryRegistry interface {
	Selected() string
}

type Service interface {
	roborev.AnalysisService
	Installation(context.Context) roborev.Installation
}

type Dependencies struct {
	Registry RepositoryRegistry
	RoboRev  Service
	Gate     *operation.Gate
	Binary   string
}

type Request struct {
	RepositoryIdentity string `json:"repositoryIdentity"`
	AnalysisType       string `json:"analysisType"`
	Scope              string `json:"scope"`
}

type Command struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type Preview struct {
	RepositoryIdentity string  `json:"repositoryIdentity"`
	RepositoryPath     string  `json:"repositoryPath"`
	Head               string  `json:"head"`
	Branch             string  `json:"branch"`
	RoboRevVersion     string  `json:"roborevVersion"`
	AnalysisType       string  `json:"analysisType"`
	Scope              string  `json:"scope"`
	Command            Command `json:"command"`
	PreviewToken       string  `json:"previewToken"`
	bindingIdentity    string
}

type StartRequest struct {
	Request
	PreviewToken string `json:"previewToken"`
}

type Result struct {
	Receipt            roborev.AnalysisReceipt `json:"receipt"`
	RepositoryIdentity string                  `json:"repositoryIdentity"`
	RepositoryPath     string                  `json:"repositoryPath"`
	Head               string                  `json:"head"`
	Branch             string                  `json:"branch"`
	RoboRevVersion     string                  `json:"roborevVersion"`
	AnalysisType       string                  `json:"analysisType"`
	Scope              string                  `json:"scope"`
	Command            Command                 `json:"command"`
}

type Manager struct {
	registry RepositoryRegistry
	service  Service
	gate     *operation.Gate
	binary   string

	mu          sync.Mutex
	closed      bool
	shutdownCtx context.Context
	shutdown    context.CancelFunc
	active      map[uint64]activeCall
	nextID      uint64
}

type activeCall struct {
	done   chan struct{}
	cancel context.CancelFunc
}

func NewManager(deps Dependencies) (*Manager, error) {
	if deps.Registry == nil || deps.RoboRev == nil {
		return nil, errors.New("analysis dependencies are required")
	}
	if deps.Gate == nil {
		deps.Gate = operation.NewGate()
	}
	if strings.TrimSpace(deps.Binary) == "" {
		deps.Binary = "roborev"
	}
	shutdownCtx, shutdown := context.WithCancel(context.Background())
	return &Manager{registry: deps.Registry, service: deps.RoboRev, gate: deps.Gate, binary: deps.Binary, shutdownCtx: shutdownCtx, shutdown: shutdown, active: make(map[uint64]activeCall)}, nil
}

func (m *Manager) currentBinary(installation roborev.Installation) string {
	if provider, ok := m.service.(interface{ BinaryPath() string }); ok {
		if value := strings.TrimSpace(provider.BinaryPath()); value != "" {
			return value
		}
	}
	if value := strings.TrimSpace(installation.Binary); value != "" {
		return value
	}
	return m.binary
}

func (m *Manager) currentBinding(installation roborev.Installation) string {
	if provider, ok := m.service.(interface{ BindingIdentity() string }); ok {
		if value := strings.TrimSpace(provider.BindingIdentity()); value != "" {
			return value
		}
	}
	return installation.Version + "\x00" + m.currentBinary(installation)
}

func analysisCapability(installation roborev.Installation) error {
	if !installation.Installed || !roborevcompat.IsSupportedVersion(installation.Version) || !installation.Capabilities.AnalyzeCommand {
		return fmt.Errorf("RoboRev v0.64 analyze capability is unavailable")
	}
	return nil
}

func daemonOnline(installation roborev.Installation) bool {
	var status struct {
		Running *bool `json:"running"`
		Known   *bool `json:"known"`
	}
	if json.Unmarshal(installation.Status, &status) != nil || status.Running == nil || status.Known == nil {
		return false
	}
	return *status.Running && *status.Known
}

func (m *Manager) catalog(ctx context.Context, installation roborev.Installation) ([]roborev.AnalysisType, error) {
	if err := analysisCapability(installation); err != nil {
		return nil, typed(ErrorUnavailable, err)
	}
	types, err := m.service.AnalysisTypes(ctx)
	if err != nil {
		return nil, typed(ErrorUnavailable, err)
	}
	if len(types) == 0 {
		return nil, typed(ErrorUnavailable, errors.New("RoboRev analysis type catalog is empty"))
	}
	seen := make(map[string]struct{}, len(types))
	for _, item := range types {
		if !validType(item.Name) || strings.TrimSpace(item.Description) == "" {
			return nil, typed(ErrorUnavailable, errors.New("RoboRev analysis type catalog is invalid"))
		}
		if _, exists := seen[item.Name]; exists {
			return nil, typed(ErrorUnavailable, errors.New("RoboRev analysis type catalog contains duplicates"))
		}
		seen[item.Name] = struct{}{}
	}
	return types, nil
}

func validType(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func findType(types []roborev.AnalysisType, name string) bool {
	for _, item := range types {
		if item.Name == name {
			return true
		}
	}
	return false
}

func (m *Manager) Types(ctx context.Context) ([]roborev.AnalysisType, error) {
	return m.catalog(ctx, m.service.Installation(ctx))
}

func (m *Manager) Prompt(ctx context.Context, analysisType string) (string, error) {
	if !validType(analysisType) {
		return "", typed(ErrorInvalid, errors.New("invalid analysis type"))
	}
	types, err := m.Types(ctx)
	if err != nil {
		return "", err
	}
	if !findType(types, analysisType) {
		return "", typed(ErrorInvalid, errors.New("analysis type is not currently available"))
	}
	prompt, err := m.service.AnalysisPrompt(ctx, analysisType)
	if err != nil {
		return "", typed(ErrorUnavailable, err)
	}
	if strings.TrimSpace(prompt) == "" {
		return "", typed(ErrorUnavailable, errors.New("analysis prompt is empty"))
	}
	return prompt, nil
}

func (m *Manager) validateRepository(ctx context.Context, identity string) (gitx.Repository, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return gitx.Repository{}, typed(ErrorInvalid, errors.New("repository identity is required"))
	}
	path, err := m.registry.ResolveIdentity(identity)
	if err != nil {
		return gitx.Repository{}, typed(ErrorForbidden, err)
	}
	if selected, ok := m.registry.(selectedRepositoryRegistry); ok {
		selectedPath := strings.TrimSpace(selected.Selected())
		if selectedPath == "" {
			return gitx.Repository{}, typed(ErrorConflict, errors.New("no repository is selected"))
		}
		selectedIdentity, identityErr := m.registry.Identity(selectedPath)
		if identityErr != nil || selectedIdentity != identity {
			return gitx.Repository{}, typed(ErrorConflict, errors.New("selected repository changed; refresh and retry"))
		}
	}
	repo, err := m.registry.InspectIdentity(ctx, identity)
	if err != nil {
		return gitx.Repository{}, typed(ErrorConflict, fmt.Errorf("repository state is unavailable: %w", err))
	}
	if !gitx.SameRepositoryPath(path, repo.Path) {
		return gitx.Repository{}, typed(ErrorForbidden, errors.New("repository identity changed"))
	}
	if currentIdentity, identityErr := m.registry.Identity(repo.Path); identityErr != nil || currentIdentity != identity {
		return gitx.Repository{}, typed(ErrorConflict, errors.New("repository identity changed; refresh and retry"))
	}
	return repo, nil
}

func (m *Manager) validate(ctx context.Context, request Request, requireDaemon bool) (*Preview, error) {
	if !validType(request.AnalysisType) {
		return nil, typed(ErrorInvalid, errors.New("invalid analysis type"))
	}
	if !roborev.ValidAnalysisScope(request.Scope) {
		return nil, typed(ErrorInvalid, errors.New("invalid analysis scope"))
	}
	installation := m.service.Installation(ctx)
	if err := analysisCapability(installation); err != nil {
		return nil, typed(ErrorUnavailable, err)
	}
	if requireDaemon && !daemonOnline(installation) {
		return nil, typed(ErrorUnavailable, errors.New("daemon is offline; start the daemon first"))
	}
	types, err := m.catalog(ctx, installation)
	if err != nil {
		return nil, err
	}
	if !findType(types, request.AnalysisType) {
		return nil, typed(ErrorInvalid, errors.New("analysis type is not currently available"))
	}
	repo, err := m.validateRepository(ctx, request.RepositoryIdentity)
	if err != nil {
		return nil, err
	}
	args, err := roborev.AnalysisArgs(request.AnalysisType, request.Scope)
	if err != nil {
		return nil, typed(ErrorInvalid, err)
	}
	preview := &Preview{RepositoryIdentity: request.RepositoryIdentity, RepositoryPath: repo.Path, Head: repo.Head, Branch: repo.Branch, RoboRevVersion: installation.Version, AnalysisType: request.AnalysisType, Scope: request.Scope, Command: Command{Executable: m.currentBinary(installation), Args: append([]string(nil), args...)}, bindingIdentity: m.currentBinding(installation)}
	preview.PreviewToken = makePreviewToken(preview)
	return preview, nil
}

func (m *Manager) Prepare(ctx context.Context, request Request) (*Preview, error) {
	if m == nil {
		return nil, typed(ErrorUnavailable, errors.New("analysis manager is unavailable"))
	}
	return m.validate(ctx, request, false)
}

func makePreviewToken(preview *Preview) string {
	hash := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	for _, value := range []string{preview.RepositoryIdentity, preview.RepositoryPath, preview.Head, preview.Branch, preview.RoboRevVersion, preview.bindingIdentity, preview.AnalysisType, preview.Scope, preview.Command.Executable} {
		write(value)
	}
	for _, arg := range preview.Command.Args {
		write(arg)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (m *Manager) Start(parent context.Context, request StartRequest) (*Result, error) {
	if m == nil {
		return nil, typed(ErrorUnavailable, errors.New("analysis manager is unavailable"))
	}
	if parent == nil {
		parent = context.Background()
	}
	if strings.TrimSpace(request.PreviewToken) == "" {
		return nil, typed(ErrorInvalid, errors.New("preview token is required"))
	}
	if strings.TrimSpace(request.RepositoryIdentity) == "" {
		return nil, typed(ErrorInvalid, errors.New("repository identity is required"))
	}
	if !validType(request.AnalysisType) {
		return nil, typed(ErrorInvalid, errors.New("invalid analysis type"))
	}
	if !roborev.ValidAnalysisScope(request.Scope) {
		return nil, typed(ErrorInvalid, errors.New("invalid analysis scope"))
	}
	global, err := m.gate.AcquireAnalysis()
	if err != nil {
		return nil, typed(ErrorConflict, err)
	}
	repoLease, err := m.gate.AcquireRepository(request.RepositoryIdentity)
	if err != nil {
		global()
		return nil, typed(ErrorConflict, err)
	}
	var selectedLease func()
	if selected, ok := m.registry.(SelectedLeaseRegistry); ok {
		selectedLease, err = selected.AcquireSelected(request.RepositoryIdentity)
		if err != nil {
			repoLease()
			global()
			return nil, typed(ErrorConflict, err)
		}
	}
	validationCtx, cancel := context.WithTimeout(parent, inspectTimeout)
	preview, err := m.validate(validationCtx, request.Request, true)
	cancel()
	if err == nil && subtle.ConstantTimeCompare([]byte(preview.PreviewToken), []byte(request.PreviewToken)) != 1 {
		err = typed(ErrorConflict, errors.New("analysis preview is stale; prepare a new preview"))
	}
	if parentErr := parent.Err(); parentErr != nil && err == nil {
		err = typed(ErrorConflict, parentErr)
	}
	if err == nil {
		installation := m.service.Installation(context.Background())
		if capabilityErr := analysisCapability(installation); capabilityErr != nil {
			err = typed(ErrorConflict, errors.New("analysis preview is stale; RoboRev capability changed"))
		} else {
			binding := m.currentBinding(installation)
			if binding != preview.bindingIdentity || installation.Version != preview.RoboRevVersion || m.currentBinary(installation) != preview.Command.Executable {
				err = typed(ErrorConflict, errors.New("analysis preview is stale; RoboRev binding changed"))
			}
		}
	}
	if err != nil {
		if selectedLease != nil {
			selectedLease()
		}
		repoLease()
		global()
		return nil, err
	}
	commandCtx, commandCancel := context.WithTimeout(m.shutdownCtx, analysisEnqueueCeiling)
	callID, done, registerErr := m.registerCall(commandCancel)
	if registerErr != nil {
		commandCancel()
		if selectedLease != nil {
			selectedLease()
		}
		repoLease()
		global()
		return nil, registerErr
	}
	receipt, analyzeErr := m.service.Analyze(commandCtx, roborev.AnalyzeRequest{RepositoryPath: preview.RepositoryPath, AnalysisType: preview.AnalysisType, Scope: preview.Scope})
	commandCancel()
	m.finishCall(callID, done)
	if selectedLease != nil {
		selectedLease()
	}
	repoLease()
	global()
	if analyzeErr != nil {
		if errors.Is(analyzeErr, roborev.ErrAmbiguousAnalysisEnqueue) {
			return nil, typed(ErrorAmbiguous, analyzeErr)
		}
		return nil, typed(ErrorUnavailable, analyzeErr)
	}
	return &Result{Receipt: receipt, RepositoryIdentity: preview.RepositoryIdentity, RepositoryPath: preview.RepositoryPath, Head: preview.Head, Branch: preview.Branch, RoboRevVersion: preview.RoboRevVersion, AnalysisType: preview.AnalysisType, Scope: preview.Scope, Command: preview.Command}, nil
}

func (m *Manager) registerCall(cancel context.CancelFunc) (uint64, chan struct{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, nil, typed(ErrorUnavailable, errors.New("analysis manager is shutting down"))
	}
	m.nextID++
	id := m.nextID
	done := make(chan struct{})
	m.active[id] = activeCall{done: done, cancel: cancel}
	return id, done, nil
}

func (m *Manager) finishCall(id uint64, done chan struct{}) {
	m.mu.Lock()
	delete(m.active, id)
	m.mu.Unlock()
	close(done)
}

// Shutdown cancels only the currently accepted enqueue call and waits for it
// to return, preserving the server's bounded shutdown contract without
// persisting operation history.
func (m *Manager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	m.closed = true
	m.shutdown()
	waits := make([]chan struct{}, 0, len(m.active))
	for _, call := range m.active {
		call.cancel()
		waits = append(waits, call.done)
	}
	m.mu.Unlock()
	for _, done := range waits {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
