package roborev

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	// TaskMaxBytes is the maximum number of UTF-8 bytes accepted for a custom
	// task. The same bound is enforced by the process runner's stdin sink.
	TaskMaxBytes = 16 << 10
	// TaskOutputLimit bounds the launch receipt and diagnostic streams. A task
	// is an enqueue operation; it must not become a long-running child process.
	TaskOutputLimit = 64 << 10
	TaskTimeout     = 30 * time.Second
	maxTaskReason   = 16 << 10
)

var (
	ErrTaskAgentNotAllowed = errors.New("task agent is not allowed")
	ErrTaskUnsafeConfig    = errors.New("RoboRev task safety configuration is unsafe")
	ErrTaskCapability      = errors.New("RoboRev task capability is unavailable")
	ErrTaskDaemonVersion   = errors.New("RoboRev daemon version does not match the CLI")
	ErrTaskProcessStart    = errors.New("RoboRev task process did not start")
	ErrTaskReceipt         = errors.New("malformed RoboRev task launch receipt")
	// ErrTaskEnqueueAmbiguous deliberately does not claim that a task was not
	// queued. The daemon may have accepted the request before the CLI process
	// timed out or returned an invalid receipt.
	ErrTaskEnqueueAmbiguous = errors.New("RoboRev task enqueue result is ambiguous")
)

// TaskErrorKind is intentionally small so HTTP layers can map ambiguity to a
// stable user-facing error without exposing child stderr or task text.
type TaskErrorKind string

const (
	TaskErrorInvalid     TaskErrorKind = "invalid"
	TaskErrorUnavailable TaskErrorKind = "unavailable"
	TaskErrorCapability  TaskErrorKind = "capability"
	TaskErrorUnsafe      TaskErrorKind = "unsafe"
	TaskErrorConflict    TaskErrorKind = "conflict"
	TaskErrorAmbiguous   TaskErrorKind = "ambiguous"
)

type TaskError struct {
	Kind TaskErrorKind
	Err  error
}

func (e *TaskError) Error() string {
	if e == nil || e.Err == nil {
		return string(TaskErrorUnavailable)
	}
	return e.Err.Error()
}

func (e *TaskError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func taskError(kind TaskErrorKind, err error) error {
	return &TaskError{Kind: kind, Err: err}
}

type TaskRequest struct {
	RepoPath                string
	Task                    string
	Agent                   string
	ExpectedBindingIdentity string
}

// TaskCommand is the complete previewable process projection. Stdin is kept
// separate from Args so callers cannot accidentally turn prompt text into a
// shell or argv fragment.
type TaskCommand struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	Stdin      string   `json:"stdin"`
}

type TaskSafety struct {
	Known               bool `json:"known"`
	AllowUnsafeAgents   bool `json:"allowUnsafeAgents"`
	DisableCodexSandbox bool `json:"disableCodexSandbox"`
}

func (s TaskSafety) Safe() bool {
	return s.Known && !s.AllowUnsafeAgents && !s.DisableCodexSandbox
}

type TaskPreflight struct {
	Binary          string     `json:"executable"`
	Version         string     `json:"version"`
	DaemonVersion   string     `json:"daemonVersion"`
	BindingIdentity string     `json:"bindingIdentity"`
	Safety          TaskSafety `json:"safety"`
}

type TaskReceipt struct {
	JobID   int64  `json:"job_id,omitempty"`
	JobUUID string `json:"job_uuid,omitempty"`
	GitRef  string `json:"git_ref,omitempty"`
	Status  string `json:"status,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func (r TaskReceipt) Created() bool { return !r.Skipped && r.JobID > 0 }

// TaskArgs is the only task command shape supported by this checkpoint. In
// particular, no caller-provided agent or future run flags are accepted.
func TaskArgs(agent string) ([]string, error) {
	if strings.TrimSpace(agent) != "codex" {
		return nil, ErrTaskAgentNotAllowed
	}
	return []string{"run", "--agent", "codex", "--json"}, nil
}

// ValidateTaskText validates the browser-provided prompt without changing it.
// Newlines, tabs, Unicode and shell metacharacters are ordinary prompt bytes;
// NUL and other control characters are rejected.
func ValidateTaskText(task string) error {
	if !utf8.ValidString(task) {
		return errors.New("task must be valid UTF-8")
	}
	if len([]byte(task)) > TaskMaxBytes {
		return fmt.Errorf("task exceeds %d bytes", TaskMaxBytes)
	}
	if strings.TrimSpace(task) == "" {
		return errors.New("task is required")
	}
	for _, char := range task {
		if char == '\n' || char == '\r' || char == '\t' {
			continue
		}
		if char == 0 || unicode.IsControl(char) {
			return errors.New("task contains unsupported control characters")
		}
	}
	return nil
}

var taskUUIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// DecodeTaskReceipt accepts exactly one bounded JSON value and rejects
// unknown/trailing values. It deliberately does not retain arbitrary fields
// from RoboRev's receipt, keeping the browser projection small and stable.
func DecodeTaskReceipt(raw []byte) (TaskReceipt, error) {
	if len(raw) == 0 || len(raw) > TaskOutputLimit {
		return TaskReceipt{}, fmt.Errorf("%w: output is empty or exceeds the bound", ErrTaskReceipt)
	}
	if err := rejectDuplicateTaskKeys(raw); err != nil {
		return TaskReceipt{}, fmt.Errorf("%w: %v", ErrTaskReceipt, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil {
		return TaskReceipt{}, fmt.Errorf("%w: invalid JSON", ErrTaskReceipt)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return TaskReceipt{}, fmt.Errorf("%w: trailing JSON", ErrTaskReceipt)
	}
	if fields == nil {
		return TaskReceipt{}, fmt.Errorf("%w: top-level value must be an object", ErrTaskReceipt)
	}
	if skippedRaw, ok := fields["skipped"]; ok {
		for key := range fields {
			if key != "skipped" && key != "reason" {
				return TaskReceipt{}, fmt.Errorf("%w: unknown skipped field", ErrTaskReceipt)
			}
		}
		var skipped bool
		if err := json.Unmarshal(skippedRaw, &skipped); err != nil || !skipped {
			return TaskReceipt{}, fmt.Errorf("%w: skipped must be true", ErrTaskReceipt)
		}
		var reason string
		reasonRaw, ok := fields["reason"]
		if !ok || json.Unmarshal(reasonRaw, &reason) != nil || strings.TrimSpace(reason) == "" || len([]byte(reason)) > maxTaskReason {
			return TaskReceipt{}, fmt.Errorf("%w: skipped reason is required", ErrTaskReceipt)
		}
		// A skipped receipt must not smuggle created-job identity alongside its
		// reason. This makes the two outcomes unambiguous to callers.
		for _, key := range []string{"job_id", "job_uuid", "git_ref", "status"} {
			if _, exists := fields[key]; exists {
				return TaskReceipt{}, fmt.Errorf("%w: skipped receipt has created fields", ErrTaskReceipt)
			}
		}
		return TaskReceipt{Skipped: true, Reason: reason}, nil
	}
	var created struct {
		JobID   *int64  `json:"job_id"`
		JobUUID *string `json:"job_uuid"`
		GitRef  *string `json:"git_ref"`
		Status  *string `json:"status"`
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return TaskReceipt{}, fmt.Errorf("%w: invalid object", ErrTaskReceipt)
	}
	createdDecoder := json.NewDecoder(bytes.NewReader(encoded))
	createdDecoder.DisallowUnknownFields()
	if err := createdDecoder.Decode(&created); err != nil {
		return TaskReceipt{}, fmt.Errorf("%w: unknown receipt shape", ErrTaskReceipt)
	}
	if created.JobID == nil || *created.JobID <= 0 || created.JobUUID == nil || !taskUUIDRE.MatchString(*created.JobUUID) || created.GitRef == nil || *created.GitRef != "run" || created.Status == nil || *created.Status != "queued" {
		return TaskReceipt{}, fmt.Errorf("%w: created receipt identity is invalid", ErrTaskReceipt)
	}
	return TaskReceipt{JobID: *created.JobID, JobUUID: *created.JobUUID, GitRef: *created.GitRef, Status: *created.Status}, nil
}

func rejectDuplicateTaskKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil // The main decoder returns the precise malformed-JSON error.
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate receipt field %q", key)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil
		}
	}
	_, _ = decoder.Token()
	return nil
}

func DecodeTaskReceiptOutput(output string, truncated bool) (TaskReceipt, error) {
	if truncated {
		return TaskReceipt{}, fmt.Errorf("%w: output was truncated", ErrTaskReceipt)
	}
	return DecodeTaskReceipt([]byte(output))
}

// LoadTaskSafety reads only the two global safety keys through the existing
// bounded, no-follow TOML reader. A missing file or missing key means the
// upstream safe default; malformed TOML/types are unknown and fail closed.
func LoadTaskSafety(path string) (TaskSafety, error) {
	if strings.TrimSpace(path) == "" {
		resolver := NewPanelResolver()
		var err error
		path, err = resolver.globalPath()
		if err != nil {
			return TaskSafety{}, err
		}
	}
	file, _, err := loadPanelConfigFile(path)
	if err != nil {
		return TaskSafety{}, err
	}
	safety := TaskSafety{Known: true}
	if file.AllowUnsafeAgents != nil {
		safety.AllowUnsafeAgents = *file.AllowUnsafeAgents
	}
	if file.DisableCodexSandbox != nil {
		safety.DisableCodexSandbox = *file.DisableCodexSandbox
	}
	return safety, nil
}

// TaskSafety performs a fresh global safety read for task acceptance.
func (c *Client) TaskSafety(_ context.Context) (TaskSafety, error) {
	if c == nil {
		return TaskSafety{}, errors.New("RoboRev client is unavailable")
	}
	return LoadTaskSafety(c.TaskConfigPath)
}

// TaskPreflight verifies the pinned binary/version, healthy daemon and exact
// matching daemon version immediately before a task command is built.
func (c *Client) TaskPreflight(ctx context.Context) (TaskPreflight, error) {
	if c == nil || c.Runner == nil || c.HTTP == nil {
		return TaskPreflight{}, taskError(TaskErrorUnavailable, errors.New("RoboRev client is unavailable"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	binary, _, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return TaskPreflight{}, taskError(TaskErrorUnavailable, errors.New("RoboRev is not installed"))
	}
	version, err := c.freshTaskVersion(ctx, binary)
	if err != nil {
		return TaskPreflight{}, taskError(TaskErrorUnavailable, err)
	}
	if !roborevcompat.IsSupportedVersion(version) {
		return TaskPreflight{}, taskError(TaskErrorCapability, ErrTaskCapability)
	}
	safety, err := c.TaskSafety(ctx)
	if err != nil {
		return TaskPreflight{}, taskError(TaskErrorUnsafe, fmt.Errorf("task safety state is unknown: %w", err))
	}
	if !safety.Safe() {
		return TaskPreflight{}, taskError(TaskErrorUnsafe, ErrTaskUnsafeConfig)
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		if isUnreachableHealthError(err) {
			return TaskPreflight{}, taskError(TaskErrorUnavailable, ErrDaemonOffline)
		}
		return TaskPreflight{}, taskError(TaskErrorUnavailable, err)
	}
	if !health.Healthy {
		return TaskPreflight{}, taskError(TaskErrorUnavailable, ErrDaemonUnhealthy)
	}
	if !roborevcompat.IsSupportedVersion(strings.TrimSpace(health.Version)) || strings.TrimSpace(health.Version) != version {
		return TaskPreflight{}, taskError(TaskErrorCapability, fmt.Errorf("%w: cli=%s daemon=%s", ErrTaskDaemonVersion, version, health.Version))
	}
	currentBinary, _, currentGeneration := c.snapshot()
	if currentBinary != binary || currentGeneration != generation {
		return TaskPreflight{}, taskError(TaskErrorConflict, ErrBindingChanged)
	}
	return TaskPreflight{Binary: binary, Version: version, DaemonVersion: health.Version, BindingIdentity: fmt.Sprintf("%d:%s", generation, binary), Safety: safety}, nil
}

func (c *Client) freshTaskVersion(ctx context.Context, binary string) (string, error) {
	result, err := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: []string{"version", "--json"}, Timeout: 5 * time.Second, OutputLimit: TaskOutputLimit})
	if err != nil || result.StdoutTruncated || result.StderrTruncated {
		return "", errors.New("RoboRev version probe failed")
	}
	decoder := json.NewDecoder(strings.NewReader(result.Stdout))
	decoder.DisallowUnknownFields()
	var value struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := decoder.Decode(&value); err != nil {
		return "", errors.New("RoboRev version response is malformed")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF || value.Name != "roborev" || !roborevcompat.IsSupportedVersion(value.Version) {
		return "", errors.New("RoboRev version is not the supported v0.64.0 release")
	}
	return value.Version, nil
}

// RunTask performs one bounded enqueue invocation after a fresh preflight.
// Once the child has started, all process/receipt failures are typed as
// ambiguous because the daemon may already have accepted the task. A process
// that never started is a definite local launch failure.
func (c *Client) RunTask(ctx context.Context, request TaskRequest) (TaskReceipt, error) {
	if c == nil || c.Runner == nil {
		return TaskReceipt{}, taskError(TaskErrorUnavailable, errors.New("RoboRev client is unavailable"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	args, err := TaskArgs(request.Agent)
	if err != nil {
		return TaskReceipt{}, taskError(TaskErrorInvalid, err)
	}
	if strings.TrimSpace(request.RepoPath) == "" {
		return TaskReceipt{}, taskError(TaskErrorInvalid, errors.New("repository path is required"))
	}
	for _, char := range request.RepoPath {
		if unicode.IsControl(char) {
			return TaskReceipt{}, taskError(TaskErrorInvalid, errors.New("repository path contains control characters"))
		}
	}
	if err := ValidateTaskText(request.Task); err != nil {
		return TaskReceipt{}, taskError(TaskErrorInvalid, err)
	}
	preflight, err := c.TaskPreflight(ctx)
	if err != nil {
		return TaskReceipt{}, err
	}
	if expected := strings.TrimSpace(request.ExpectedBindingIdentity); expected != "" && subtle.ConstantTimeCompare([]byte(preflight.BindingIdentity), []byte(expected)) != 1 {
		return TaskReceipt{}, taskError(TaskErrorConflict, errors.New("RoboRev task preview binding is stale"))
	}
	currentBinary, _, _ := c.snapshot()
	if currentBinary != preflight.Binary || subtle.ConstantTimeCompare([]byte(c.BindingIdentity()), []byte(preflight.BindingIdentity)) != 1 {
		return TaskReceipt{}, taskError(TaskErrorConflict, ErrBindingChanged)
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{
		Executable:  preflight.Binary,
		Args:        append([]string(nil), args...),
		Dir:         request.RepoPath,
		Stdin:       request.Task,
		StdinLimit:  TaskMaxBytes,
		Timeout:     TaskTimeout,
		OutputLimit: TaskOutputLimit,
	})
	if runErr != nil && !result.Started {
		return TaskReceipt{}, taskError(TaskErrorUnavailable, ErrTaskProcessStart)
	}
	if runErr != nil || result.StdoutTruncated || result.StderrTruncated {
		return TaskReceipt{}, taskError(TaskErrorAmbiguous, ErrTaskEnqueueAmbiguous)
	}
	receipt, receiptErr := DecodeTaskReceiptOutput(result.Stdout, result.StdoutTruncated)
	if receiptErr != nil {
		return TaskReceipt{}, taskError(TaskErrorAmbiguous, fmt.Errorf("%w: %v", ErrTaskEnqueueAmbiguous, receiptErr))
	}
	return receipt, nil
}
