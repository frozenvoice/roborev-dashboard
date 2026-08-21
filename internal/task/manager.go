// Package task owns the narrow, non-agentic Custom Task enqueue workflow.
// It deliberately has no task history: RoboRev's jobs API remains the source
// of truth after a launch receipt is returned.
package task

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"context"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

const (
	// MaxTaskBytes is shared by request validation and the process runner.
	MaxTaskBytes = roborev.TaskMaxBytes
	previewTTL   = 5 * time.Minute
	enqueueLimit = 64 << 10
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type ErrorKind string

const (
	ErrorInvalid     ErrorKind = "invalid"
	ErrorForbidden   ErrorKind = "forbidden"
	ErrorUnavailable ErrorKind = "unavailable"
	ErrorConflict    ErrorKind = "conflict"
	ErrorAmbiguous   ErrorKind = "ambiguous"
	ErrorInternal    ErrorKind = "internal"
)

// Error is intentionally small and stable for the HTTP adapter. Its messages
// never include task text.
type Error struct {
	Kind ErrorKind
	Code string
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
func typedCode(kind ErrorKind, code string, err error) error {
	return &Error{Kind: kind, Code: code, Err: err}
}

type RepositoryRegistry interface {
	ResolveIdentity(string) (string, error)
	InspectIdentity(context.Context, string) (gitx.Repository, error)
	Identity(string) (string, error)
}

type selectedRegistry interface{ Selected() string }

// Safety is the effective global safety state. Known must be true: an
// adapter maps an unset TOML key to false, but malformed/unreadable state is
// represented as unknown and therefore fails closed.
type Safety = roborev.TaskSafety

// RunRequest is the exact typed process request sent to the RoboRev adapter.
// Task is carried as stdin and is never inserted into Args.
// Adapter is deliberately narrower than roborev.Service. The concrete
// RoboRev client supplies this surface; tests use a deterministic fake.
type Adapter interface {
	TaskPreflight(context.Context) (roborev.TaskPreflight, error)
	RunTask(context.Context, roborev.TaskRequest) (roborev.TaskReceipt, error)
}

type Command = roborev.TaskCommand

type Request struct {
	RepositoryIdentity string `json:"repositoryIdentity"`
	Task               string `json:"task"`
}

type Preview struct {
	RepositoryIdentity string  `json:"repositoryIdentity"`
	RepositoryPath     string  `json:"repositoryPath"`
	Head               string  `json:"head"`
	Branch             string  `json:"branch"`
	Agent              string  `json:"agent"`
	Mode               string  `json:"mode"`
	Safety             Safety  `json:"safety"`
	Command            Command `json:"command"`
	RoboRevVersion     string  `json:"roborevVersion"`
	Binding            string  `json:"binding"`
	PreviewToken       string  `json:"previewToken"`
}

type StartRequest struct {
	Request
	PreviewToken string `json:"previewToken"`
}

type Receipt struct {
	JobID   int64  `json:"jobId"`
	JobUUID string `json:"jobUUID"`
	GitRef  string `json:"gitRef"`
	Status  string `json:"status"`
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type Dependencies struct {
	Registry RepositoryRegistry
	RoboRev  Adapter
	Gate     *operation.Gate
	// AcquireSelected holds the registry selection read lease through the
	// enqueue acceptance boundary. It is optional for narrow unit fakes.
	AcquireSelected func(string) (func(), error)
	Secret          []byte // optional test secret; production uses crypto/rand
	Now             func() time.Time
}

type Manager struct {
	registry       RepositoryRegistry
	service        Adapter
	gate           *operation.Gate
	selected       func(string) (func(), error)
	secret         []byte
	now            func() time.Time
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc

	mu       sync.Mutex
	used     map[string]time.Time
	active   map[string]context.CancelFunc
	sequence uint64
	wg       sync.WaitGroup
}

func NewManager(deps Dependencies) (*Manager, error) {
	if deps.Registry == nil || deps.RoboRev == nil {
		return nil, errors.New("task dependencies are required")
	}
	if deps.Gate == nil {
		deps.Gate = operation.NewGate()
	}
	secret := append([]byte(nil), deps.Secret...)
	if len(secret) < 32 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("generate task preview secret: %w", err)
		}
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	return &Manager{registry: deps.Registry, service: deps.RoboRev, gate: deps.Gate, selected: deps.AcquireSelected, secret: secret, now: now, used: make(map[string]time.Time), active: make(map[string]context.CancelFunc), shutdownCtx: shutdownCtx, shutdownCancel: shutdownCancel}, nil
}

func (m *Manager) Prepare(ctx context.Context, request Request) (*Preview, error) {
	if m == nil || m.service == nil {
		return nil, typed(ErrorUnavailable, errors.New("task manager is unavailable"))
	}
	if err := validateRequest(request); err != nil {
		return nil, typed(ErrorInvalid, err)
	}
	preview, err := m.validate(ctx, request)
	if err != nil {
		return nil, err
	}
	preview.PreviewToken, err = m.signPreview(preview)
	if err != nil {
		return nil, typed(ErrorInternal, errors.New("could not create task preview token"))
	}
	return preview, nil
}

func validateRequest(request Request) error {
	if strings.TrimSpace(request.RepositoryIdentity) == "" {
		return errors.New("repository identity is required")
	}
	if err := ValidateTask(request.Task); err != nil {
		return err
	}
	return nil
}

// ValidateTask permits normal Unicode, Markdown, quotes, shell metacharacters,
// tabs, and newlines while rejecting NUL and other control bytes.
func ValidateTask(value string) error {
	return roborev.ValidateTaskText(value)
}

func (m *Manager) validate(ctx context.Context, request Request) (*Preview, error) {
	if err := validateRequest(request); err != nil {
		return nil, typed(ErrorInvalid, err)
	}
	path, err := m.registry.ResolveIdentity(strings.TrimSpace(request.RepositoryIdentity))
	if err != nil {
		return nil, typed(ErrorForbidden, errors.New("repository is not authorized"))
	}
	selected, ok := m.registry.(selectedRegistry)
	if !ok || strings.TrimSpace(selected.Selected()) == "" {
		return nil, typed(ErrorConflict, errors.New("no repository is selected"))
	}
	selectedPath := selected.Selected()
	selectedIdentity, identityErr := m.registry.Identity(selectedPath)
	if identityErr != nil || selectedIdentity != strings.TrimSpace(request.RepositoryIdentity) || !gitx.SameRepositoryPath(path, selectedPath) {
		return nil, typed(ErrorConflict, errors.New("selected repository changed; refresh and retry"))
	}
	repo, err := m.registry.InspectIdentity(ctx, request.RepositoryIdentity)
	if err != nil {
		return nil, typed(ErrorConflict, errors.New("repository state is unavailable"))
	}
	if !gitx.SameRepositoryPath(path, repo.Path) {
		return nil, typed(ErrorForbidden, errors.New("repository identity changed"))
	}
	if strings.TrimSpace(repo.Head) == "" || strings.TrimSpace(repo.Branch) == "" {
		return nil, typed(ErrorConflict, errors.New("repository state is unavailable"))
	}
	preflight, err := m.service.TaskPreflight(ctx)
	if err != nil {
		return nil, mapPreflightError(err)
	}
	if strings.TrimSpace(preflight.Binary) == "" || strings.TrimSpace(preflight.Version) == "" || strings.TrimSpace(preflight.DaemonVersion) == "" || preflight.Version != preflight.DaemonVersion || strings.TrimSpace(preflight.BindingIdentity) == "" {
		return nil, typed(ErrorUnavailable, errors.New("RoboRev task preflight is incomplete"))
	}
	args, err := roborev.TaskArgs("codex")
	if err != nil {
		return nil, typed(ErrorInvalid, err)
	}
	if !preflight.Safety.Safe() {
		return nil, typed(ErrorConflict, errors.New("RoboRev task safety configuration is unsafe"))
	}
	command := Command{Executable: preflight.Binary, Args: args, Stdin: request.Task}
	return &Preview{RepositoryIdentity: request.RepositoryIdentity, RepositoryPath: repo.Path, Head: repo.Head, Branch: repo.Branch, Agent: "codex", Mode: "read-only", Safety: preflight.Safety, Command: command, RoboRevVersion: preflight.Version, Binding: preflight.BindingIdentity}, nil
}

func mapPreflightError(err error) error {
	var typedErr *roborev.TaskError
	if !errors.As(err, &typedErr) {
		return typed(ErrorUnavailable, errors.New("RoboRev task preflight is unavailable"))
	}
	switch typedErr.Kind {
	case roborev.TaskErrorInvalid:
		return typed(ErrorInvalid, errors.New("invalid RoboRev task request"))
	case roborev.TaskErrorConflict:
		return typed(ErrorConflict, errors.New("RoboRev task state changed; refresh and retry"))
	case roborev.TaskErrorUnsafe:
		return typedCode(ErrorConflict, "unsafe_task_configuration", errors.New("RoboRev task safety configuration is unsafe"))
	default:
		return typed(ErrorUnavailable, errors.New("RoboRev v0.64 daemon is unavailable"))
	}
}

func (m *Manager) signPreview(preview *Preview) (string, error) {
	nonce := make([]byte, 24)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	claims := tokenClaims{Nonce: hex.EncodeToString(nonce), Expiry: m.now().Add(previewTTL).UnixNano(), Tuple: previewTuple(preview)}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write(payload)
	encoded := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded, nil
}

type tokenClaims struct {
	Nonce  string `json:"nonce"`
	Expiry int64  `json:"expiry"`
	Tuple  string `json:"tuple"`
}

func previewTuple(preview *Preview) string {
	h := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(value))
	}
	for _, value := range []string{preview.RepositoryIdentity, preview.RepositoryPath, preview.Head, preview.Branch, preview.Agent, preview.Mode, preview.SafetyString(), preview.Command.Executable, preview.RoboRevVersion, preview.Binding} {
		write(value)
	}
	for _, arg := range preview.Command.Args {
		write(arg)
	}
	write(preview.Command.Stdin)
	return hex.EncodeToString(h.Sum(nil))
}

func (p *Preview) SafetyString() string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("allow_unsafe_agents=%t\x00disable_codex_sandbox=%t", p.Safety.AllowUnsafeAgents, p.Safety.DisableCodexSandbox)
}

func (m *Manager) verifyToken(preview *Preview, value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write(payload)
	if subtle.ConstantTimeCompare(provided, mac.Sum(nil)) != 1 {
		return false
	}
	var claims tokenClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Nonce == "" || claims.Expiry <= m.now().UnixNano() || claims.Tuple != previewTuple(preview) {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for nonce, expiry := range m.used {
		if !expiry.After(now) {
			delete(m.used, nonce)
		}
	}
	if _, used := m.used[claims.Nonce]; used {
		return false
	}
	m.used[claims.Nonce] = time.Unix(0, claims.Expiry)
	return true
}

func (m *Manager) Start(parent context.Context, request StartRequest) (Receipt, error) {
	if m == nil || m.service == nil {
		return Receipt{}, typed(ErrorUnavailable, errors.New("task manager is unavailable"))
	}
	if parent == nil {
		parent = context.Background()
	}
	if err := validateRequest(request.Request); err != nil {
		return Receipt{}, typed(ErrorInvalid, err)
	}
	if strings.TrimSpace(request.PreviewToken) == "" {
		return Receipt{}, typed(ErrorInvalid, errors.New("preview token is required"))
	}
	// Acquire the global and repository leases before inspecting mutable state.
	// This closes the acceptance gap where another operation could start after
	// preview validation but before the task's final fresh preflight.
	release, err := m.gate.AcquireTaskRepository(request.RepositoryIdentity)
	if err != nil {
		return Receipt{}, typed(ErrorConflict, errors.New("another Dashboard operation is active"))
	}
	defer release()
	var selectedRelease func()
	if m.selected != nil {
		selectedRelease, err = m.selected(request.RepositoryIdentity)
		if err != nil {
			return Receipt{}, typed(ErrorConflict, errors.New("selected repository changed; refresh and retry"))
		}
		defer selectedRelease()
	}
	if err := parent.Err(); err != nil {
		return Receipt{}, typed(ErrorConflict, err)
	}
	preview, err := m.validate(parent, request.Request)
	if err != nil {
		return Receipt{}, err
	}
	if !m.verifyToken(preview, request.PreviewToken) {
		return Receipt{}, typed(ErrorConflict, errors.New("task preview is stale; prepare a new preview"))
	}
	if err := parent.Err(); err != nil {
		return Receipt{}, typed(ErrorConflict, err)
	}
	// After this point the HTTP request is no longer the owner of the enqueue
	// process. Only Dashboard shutdown or the bounded 30s ceiling can cancel it.
	ctx, cancel := context.WithTimeout(m.shutdownCtx, 30*time.Second)
	key := fmt.Sprintf("task-%d", atomic.AddUint64(&m.sequence, 1))
	m.mu.Lock()
	m.active[key] = cancel
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.active, key)
		m.mu.Unlock()
		m.wg.Done()
	}()
	result, err := m.service.RunTask(ctx, roborev.TaskRequest{RepoPath: preview.RepositoryPath, Agent: "codex", Task: request.Task, ExpectedBindingIdentity: preview.Binding})
	if err != nil {
		var taskErr *roborev.TaskError
		if errors.As(err, &taskErr) {
			switch taskErr.Kind {
			case roborev.TaskErrorInvalid:
				return Receipt{}, typed(ErrorInvalid, errors.New("invalid task request"))
			case roborev.TaskErrorUnsafe:
				return Receipt{}, typedCode(ErrorConflict, "unsafe_task_configuration", errors.New("RoboRev task safety configuration is unsafe"))
			case roborev.TaskErrorConflict:
				return Receipt{}, typed(ErrorConflict, errors.New("RoboRev task state changed; refresh and retry"))
			case roborev.TaskErrorUnavailable, roborev.TaskErrorCapability:
				return Receipt{}, typed(ErrorUnavailable, errors.New("RoboRev task capability is unavailable"))
			case roborev.TaskErrorAmbiguous:
				return Receipt{}, typedCode(ErrorAmbiguous, "ambiguous_enqueue", errors.New("task enqueue result is uncertain; check RoboRev jobs"))
			}
		}
		// Once RunTask has been invoked, an untyped transport failure may still
		// follow a daemon acceptance and is therefore ambiguous.
		return Receipt{}, typedCode(ErrorAmbiguous, "ambiguous_enqueue", errors.New("task enqueue result is uncertain; check RoboRev jobs"))
	}
	receipt := Receipt{JobID: result.JobID, JobUUID: result.JobUUID, GitRef: result.GitRef, Status: result.Status, Skipped: result.Skipped, Reason: result.Reason}
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, typedCode(ErrorAmbiguous, "ambiguous_enqueue", errors.New("task enqueue result is uncertain; check RoboRev jobs"))
	}
	return receipt, nil
}

func ValidateReceipt(receipt Receipt) error {
	if receipt.Skipped {
		if strings.TrimSpace(receipt.Reason) == "" || len([]byte(receipt.Reason)) > enqueueLimit {
			return errors.New("malformed skipped task receipt")
		}
		if receipt.JobID != 0 || receipt.JobUUID != "" || receipt.GitRef != "" || receipt.Status != "" {
			return errors.New("malformed skipped task receipt")
		}
		return nil
	}
	if receipt.JobID <= 0 || !uuidPattern.MatchString(receipt.JobUUID) || receipt.GitRef != "run" || receipt.Status != "queued" {
		return errors.New("malformed task receipt")
	}
	return nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if m.shutdownCancel != nil {
		m.shutdownCancel()
	}
	m.mu.Lock()
	for _, cancel := range m.active {
		cancel()
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
