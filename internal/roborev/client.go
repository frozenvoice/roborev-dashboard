package roborev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/froze/roborev-dashboard/internal/gitx"
	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/repomove"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

type Service interface {
	Installation(context.Context) Installation
	List(context.Context, string, int) (json.RawMessage, error)
	SynthesisStatus(context.Context, int64) (SynthesisStatus, error)
	JobOutputStatus(context.Context, int64) (JobOutputStatus, error)
	JobMetadata(context.Context, int64) (JobMetadata, error)
	TerminalJobMetadata(context.Context, int64) (JobMetadata, error)
	Show(context.Context, int64) (json.RawMessage, error)
	PanelMembers(context.Context, string) (json.RawMessage, error)
	Review(context.Context, ReviewRequest) (json.RawMessage, error)
	ReviewPanel(context.Context, ReviewRequest) (PanelEnqueueResponse, error)
	Fix(context.Context, FixRequest) error
	Rerun(context.Context, int64) error
	Comment(context.Context, int64, string) error
	Close(context.Context, int64, bool) error
	Cancel(context.Context, int64) error
	StartDaemon(context.Context) error
}

var ErrMalformedPanelEnqueueResponse = errors.New("malformed panel enqueue response")

var (
	ErrBindingChanged             = errors.New("RoboRev binding changed during verification")
	errUnsupportedDaemonBinding   = errors.New("RoboRev daemon binding version is unsupported")
	ErrUnsupportedJobOutputStatus = errors.New("RoboRev job output status contract is unsupported")
	ErrMalformedJobOutputStatus   = errors.New("malformed RoboRev job output status response")
	ErrUnsupportedJobMetadata     = errors.New("RoboRev job metadata contract is unsupported")
	ErrMalformedJobMetadata       = errors.New("malformed RoboRev job metadata response")
	ErrJobMetadataNotFound        = errors.New("RoboRev job metadata was not found")
)

func (c *Client) ReviewPanel(ctx context.Context, request ReviewRequest) (PanelEnqueueResponse, error) {
	raw, err := c.Review(ctx, request)
	if err != nil {
		return PanelEnqueueResponse{}, err
	}
	return DecodePanelEnqueueResponse(raw, request)
}

func DecodePanelEnqueueResponse(raw json.RawMessage, request ReviewRequest) (PanelEnqueueResponse, error) {
	if err := validateUniqueJSON(raw); err != nil {
		return PanelEnqueueResponse{}, fmt.Errorf("%w: invalid JSON", ErrMalformedPanelEnqueueResponse)
	}
	var receipt PanelEnqueueResponse
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return PanelEnqueueResponse{}, fmt.Errorf("%w: invalid JSON", ErrMalformedPanelEnqueueResponse)
	}
	if receipt.Skipped {
		return PanelEnqueueResponse{}, fmt.Errorf("%w: enqueue was skipped", ErrMalformedPanelEnqueueResponse)
	}
	if receipt.ID <= 0 || strings.TrimSpace(receipt.RepoPath) == "" || strings.TrimSpace(receipt.GitRef) == "" || strings.TrimSpace(receipt.Branch) == "" || strings.TrimSpace(receipt.PanelName) == "" || strings.TrimSpace(receipt.PanelRunUUID) == "" || receipt.PanelRole != "synthesis" || receipt.JobType != "synthesis" {
		return PanelEnqueueResponse{}, fmt.Errorf("%w: missing synthesis identity", ErrMalformedPanelEnqueueResponse)
	}
	if !gitx.SameRepositoryPath(receipt.RepoPath, request.RepoPath) || receipt.GitRef != request.GitRef || receipt.Branch != request.Branch || receipt.PanelName != request.Panel {
		return PanelEnqueueResponse{}, fmt.Errorf("%w: identity does not match request", ErrMalformedPanelEnqueueResponse)
	}
	return receipt, nil
}

type Client struct {
	Runner proc.Runner
	// repositoryMoveFS is injectable only for deterministic adapter tests. A
	// nil value uses the production os.Lstat implementation.
	repositoryMoveFS repomove.FileSystem
	// StreamRunner is injectable for deterministic tests.  A nil value uses
	// the same ExecRunner implementation as short-lived commands.
	StreamRunner      proc.StreamRunner
	HTTP              *http.Client
	bindingMu         sync.RWMutex
	binding           clientBinding
	explicitOverride  bool
	versionMu         sync.RWMutex
	version           string
	versionAt         time.Time
	versionGeneration uint64
	versionRefreshMu  sync.Mutex
	versionErr        error
	versionErrAt      time.Time
	lazyWitnessCursor atomic.Uint64
}

type clientBinding struct {
	binary     string
	daemonURL  string
	generation uint64
}

func ResolveBinary(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("resolve RoboRev override: %w", err)
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			return "", fmt.Errorf("RoboRev override is not an executable file: %s", abs)
		}
		return abs, nil
	}
	path, err := exec.LookPath("roborev")
	if err == nil {
		return filepath.Abs(path)
	}
	home, homeErr := os.UserHomeDir()
	if homeErr == nil {
		managed := roborevcompat.ManagedTarget(home)
		if info, statErr := os.Stat(managed); statErr == nil && !info.IsDir() {
			return filepath.Abs(managed)
		}
	}
	return "", errors.New("roborev executable not found on PATH or managed user install")
}

func NewClient(binary string, runner proc.Runner, daemonAddress string) (*Client, error) {
	return NewClientWithOptions(binary, runner, daemonAddress, false)
}

func NewClientWithOptions(binary string, runner proc.Runner, daemonAddress string, explicitOverride bool) (*Client, error) {
	if runner == nil {
		return nil, errors.New("process runner is required")
	}
	if daemonAddress == "" {
		daemonAddress = "127.0.0.1:7373"
	}
	if !strings.Contains(daemonAddress, "://") {
		daemonAddress = "http://" + daemonAddress
	}
	parsed, err := url.Parse(daemonAddress)
	if err != nil {
		return nil, fmt.Errorf("parse daemon address: %w", err)
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("RoboRev daemon address must be loopback")
	}
	return &Client{Runner: runner, binding: clientBinding{binary: binary, daemonURL: strings.TrimRight(daemonAddress, "/"), generation: 1}, explicitOverride: explicitOverride, HTTP: &http.Client{Timeout: 15 * time.Second}}, nil
}

func (c *Client) snapshot() (string, string, uint64) {
	c.bindingMu.RLock()
	defer c.bindingMu.RUnlock()
	return c.binding.binary, c.binding.daemonURL, c.binding.generation
}

func (c *Client) BinaryPath() string { binary, _, _ := c.snapshot(); return binary }

// BindingIdentity is an opaque process-local identity for the validated
// executable binding. Rebinding the same path increments generation and
// therefore invalidates previously issued preview tokens.
func (c *Client) BindingIdentity() string {
	binary, _, generation := c.snapshot()
	return fmt.Sprintf("%d:%s", generation, binary)
}
func (c *Client) HasExplicitOverride() bool                        { return c.explicitOverride }
func (c *Client) ProbeVersion(ctx context.Context) (string, error) { return c.detectVersion(ctx) }

// ProbeGitHookBinding performs an uncached version probe for the currently
// bound executable. Management previews and executes use this instead of the
// ordinary installation cache because a preview must be bound to the exact
// v0.64 binary generation and file identity.
func (c *Client) ProbeGitHookBinding(ctx context.Context) (GitHookBinding, error) {
	binary, _, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return GitHookBinding{}, errors.New("roborev executable is unavailable")
	}
	abs, err := filepath.Abs(binary)
	if err != nil {
		return GitHookBinding{}, errors.New("roborev executable path is malformed")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Some Windows filesystems do not expose a resolvable link target for
		// an ordinary regular file. Lstat below still rejects symlinks/reparse
		// points; retain the cleaned absolute path for that non-ambiguous case.
		resolved = abs
	}
	if !sameExecutablePath(abs, resolved) {
		return GitHookBinding{}, errors.New("roborev executable path is not canonical")
	}
	binary = filepath.Clean(resolved)
	info, err := os.Lstat(binary)
	if err != nil || !safeExecutableInfo(info) {
		return GitHookBinding{}, errors.New("roborev executable is not a regular file")
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: []string{"version", "--json"}, Env: sanitizedGitHookEnv(), Timeout: 5 * time.Second, OutputLimit: 64 << 10})
	if runErr != nil || result.StdoutTruncated {
		return GitHookBinding{}, errors.New("roborev version probe failed")
	}
	var version struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if json.Unmarshal([]byte(result.Stdout), &version) != nil || roborevcompat.ValidateVersionIdentity(version.Name, version.Version) != nil || !roborevcompat.IsSupportedVersion(version.Version) {
		return GitHookBinding{}, errors.New("unsupported RoboRev version")
	}
	return GitHookBinding{Binary: binary, Version: version.Version, Generation: generation, FileID: fmt.Sprintf("%d:%d:%o", info.Size(), info.ModTime().UnixNano(), info.Mode())}, nil
}

func sameExecutablePath(left, right string) bool {
	return gitx.SameRepositoryPath(filepath.Clean(left), filepath.Clean(right))
}

// RunGitHook launches exactly one allowlisted hook command in the authorized
// repository root. It re-probes the binding immediately before launch and
// refuses to run if the executable/version/generation changed.
func (c *Client) RunGitHook(ctx context.Context, root string, action GitHookAction, expected GitHookBinding) (GitHookExecution, error) {
	binding, err := c.ProbeGitHookBinding(ctx)
	if err != nil || binding != expected {
		return GitHookExecution{}, ErrBindingChanged
	}
	var args []string
	switch action {
	case GitHookInstall:
		args = []string{"install-hook", "--binary", binding.Binary}
	case GitHookRepair:
		args = []string{"install-hook", "repair", "--binary", binding.Binary}
	case GitHookRemove:
		args = []string{"uninstall-hook"}
	default:
		return GitHookExecution{}, errors.New("unsupported Git hook action")
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{Executable: binding.Binary, Args: args, Dir: root, Env: sanitizedGitHookEnv(), Timeout: 30 * time.Second, OutputLimit: 1 << 20})
	execution := GitHookExecution{Started: result.Started, ExitCode: result.ExitCode}
	if runErr != nil {
		return execution, errors.New("RoboRev hook command failed")
	}
	return execution, nil
}

// sanitizedGitHookEnv removes inherited Git routing/configuration variables
// from both the fresh binding probe and the mutation launch. RoboRev invokes
// Git from the repository cwd; retaining any GIT_* value here could redirect
// that operation away from the server-authorized root. HOME hints and the
// platform runtime allowlist are retained so normal Git safe.directory and
// user configuration behavior continues to work.
func sanitizedGitHookEnv() []string {
	source := os.Environ()
	env := proc.MinimalProbeEnv(source)
	for _, entry := range source {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		for _, allowed := range []string{"HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH"} {
			if strings.EqualFold(name, allowed) {
				env = append(env, entry)
				break
			}
		}
	}
	return env
}

func (c *Client) Rebind(ctx context.Context, binary string) error {
	if strings.TrimSpace(binary) == "" {
		return errors.New("roborev binary path is required")
	}
	abs, err := filepath.Abs(binary)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("inspect RoboRev binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("RoboRev binary is not a regular file")
	}
	result, err := c.Runner.Run(ctx, proc.Command{Executable: abs, Args: []string{"version", "--json"}, Timeout: 5 * time.Second, OutputLimit: 64 << 10})
	if err != nil {
		return fmt.Errorf("verify RoboRev binary: %w", err)
	}
	if result.StdoutTruncated {
		return errors.New("verify RoboRev binary: version response exceeded bounded output")
	}
	var version struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &version); err != nil {
		return fmt.Errorf("verify RoboRev binary: malformed version response: %w", err)
	}
	if err := roborevcompat.ValidateVersionIdentity(version.Name, version.Version); err != nil {
		return fmt.Errorf("verify RoboRev binary: %w", err)
	}
	if !roborevcompat.IsSupportedVersion(version.Version) {
		return fmt.Errorf("verify RoboRev binary: %w", roborevcompat.ErrUnsupportedVersion)
	}
	c.bindingMu.Lock()
	c.binding.binary = abs
	c.binding.generation++
	generation := c.binding.generation
	c.bindingMu.Unlock()
	c.versionMu.Lock()
	c.version = version.Version
	c.versionAt = time.Now()
	c.versionErr = nil
	c.versionErrAt = time.Time{}
	c.versionGeneration = generation
	c.versionMu.Unlock()
	return nil
}

func (c *Client) run(ctx context.Context, args []string, timeout time.Duration, limit int) (proc.Result, error) {
	binary, _, _ := c.snapshot()
	if binary == "" {
		return proc.Result{}, errors.New("roborev is not installed")
	}
	result, err := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: args, Timeout: timeout, OutputLimit: limit})
	if err != nil {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = err.Error()
		}
		return result, fmt.Errorf("%s: %w", message, err)
	}
	return result, nil
}

func decodeRaw(output string) (json.RawMessage, error) {
	raw := json.RawMessage(strings.TrimSpace(output))
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, errors.New("RoboRev returned malformed JSON")
	}
	return raw, nil
}

func capabilities(version string) Capabilities {
	v64 := roborevcompat.IsSupportedVersion(version)
	// `roborev stream` invokes ensureDaemon in v0.64 and is consequently not
	// a safe transport for this long-lived dashboard process. Keep the
	// capability disabled so callers retain the bounded polling fallback.
	return Capabilities{ReviewEnqueueAPI: v64, FrozenRerunAPI: v64, JobListAPI: v64, JobOutputStatusAPI: v64, PanelReview: v64, FixCommand: v64, PanelMembersAPI: v64, StreamEvents: false, AnalyzeIntrospection: v64, ConfigIntrospection: v64, ConfigMutation: v64, SummaryStats: v64, CostStats: v64, GuidelineInsights: v64, RepositoryInventory: v64, RepositoryRename: v64, RepositoryMove: v64, RepositoryMerge: v64, PromptLazyLoad: v64, RawLog: v64}
}

func (c *Client) Installation(ctx context.Context) Installation {
	binary, _, _ := c.snapshot()
	if binary == "" {
		return Installation{Installed: false, Error: "roborev executable not found", Capabilities: Capabilities{}}
	}
	versionValue, err := c.detectVersion(ctx)
	if err != nil {
		return Installation{Installed: false, Binary: binary, Error: err.Error(), FailureCode: probeFailureCode(err)}
	}
	installation := Installation{Installed: true, Binary: binary, Version: versionValue, Capabilities: capabilities(versionValue), Status: json.RawMessage(`{"running":false,"known":false}`), DaemonHealth: DaemonHealthUnknown, DaemonCondition: DaemonConditionUnknown}
	if installation.Capabilities.ReviewEnqueueAPI {
		if health, healthErr := c.healthStatus(ctx); healthErr == nil {
			installation.DaemonHealth = DaemonHealthOnline
			installation.DaemonVersion = health.Version
			if health.Healthy {
				installation.DaemonCondition = DaemonConditionHealthy
			} else {
				installation.DaemonCondition = DaemonConditionUnhealthy
			}
			// A valid response proves the daemon is reachable even when its
			// health payload reports unhealthy components. Stats applies the
			// typed health result separately and rejects that condition.
			status, _ := json.Marshal(map[string]any{"running": true, "known": true, "health": health})
			installation.Status = status
		} else {
			if ctx.Err() != nil || !isUnreachableHealthError(healthErr) {
				installation.DaemonHealth = DaemonHealthInvalid
			} else {
				installation.DaemonHealth = DaemonHealthOffline
			}
			installation.Status = json.RawMessage(`{"running":false,"known":true}`)
			installation.Error = "RoboRev daemon health check failed: " + healthErr.Error()
		}
	}
	return installation
}

func probeFailureCode(err error) string {
	if err == nil {
		return ""
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.Errno(1260), syscall.Errno(786):
			return "application_control"
		case syscall.EACCES, syscall.EPERM:
			return "permission_denied"
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	return "invalid_binary"
}

func (c *Client) detectVersion(ctx context.Context) (string, error) {
	binary, _, generation := c.snapshot()
	if binary == "" {
		return "", errors.New("roborev executable not found")
	}
	c.versionMu.RLock()
	value := c.version
	detectedAt := c.versionAt
	detectionErr := c.versionErr
	errorAt := c.versionErrAt
	cachedGeneration := c.versionGeneration
	c.versionMu.RUnlock()
	if cachedGeneration == generation && value != "" && time.Since(detectedAt) < 30*time.Second {
		return value, nil
	}
	if cachedGeneration == generation && detectionErr != nil && time.Since(errorAt) < 5*time.Second {
		return "", detectionErr
	}
	c.versionRefreshMu.Lock()
	defer c.versionRefreshMu.Unlock()
	c.versionMu.RLock()
	value = c.version
	detectedAt = c.versionAt
	detectionErr = c.versionErr
	errorAt = c.versionErrAt
	cachedGeneration = c.versionGeneration
	c.versionMu.RUnlock()
	if cachedGeneration == generation && value != "" && time.Since(detectedAt) < 30*time.Second {
		return value, nil
	}
	if cachedGeneration == generation && detectionErr != nil && time.Since(errorAt) < 5*time.Second {
		return "", detectionErr
	}
	result, err := c.run(ctx, []string{"version", "--json"}, 5*time.Second, 64<<10)
	if err != nil {
		c.cacheVersionError(ctx, err, generation)
		return "", err
	}
	if result.StdoutTruncated {
		err = errors.New("invalid roborev version response: bounded output was truncated")
		c.cacheVersionError(ctx, err, generation)
		return "", err
	}
	var version struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if json.Unmarshal([]byte(result.Stdout), &version) != nil || roborevcompat.ValidateVersionIdentity(version.Name, version.Version) != nil {
		err = errors.New("invalid roborev version response: expected name roborev and a version")
		c.cacheVersionError(ctx, err, generation)
		return "", err
	}
	c.versionMu.Lock()
	if !c.generationCurrent(generation) {
		c.versionMu.Unlock()
		return "", ErrBindingChanged
	}
	c.version = version.Version
	c.versionAt = time.Now()
	c.versionGeneration = generation
	c.versionErr = nil
	c.versionErrAt = time.Time{}
	c.versionMu.Unlock()
	return version.Version, nil
}

func (c *Client) cacheVersionError(ctx context.Context, err error, generation uint64) {
	if ctx.Err() != nil {
		return
	}
	c.versionMu.Lock()
	if !c.generationCurrent(generation) {
		c.versionMu.Unlock()
		return
	}
	c.versionErr = err
	c.versionErrAt = time.Now()
	c.versionGeneration = generation
	c.versionMu.Unlock()
}

func (c *Client) generationCurrent(generation uint64) bool {
	c.bindingMu.RLock()
	defer c.bindingMu.RUnlock()
	return c.binding.generation == generation
}

func (c *Client) List(ctx context.Context, repo string, limit int) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	binding, err := c.daemonHTTPPreflight(ctx)
	if err != nil {
		return nil, err
	}
	if !capabilities(binding.version).JobListAPI {
		return nil, fmt.Errorf("RoboRev %s does not support metadata-only job listing", binding.version)
	}
	if limit < 1 || limit > 200 {
		limit = 100
	}
	query := url.Values{}
	query.Set("repo", repo)
	query.Set("limit", strconv.Itoa(limit))
	query.Set("omit_prompt", "true")
	raw, err := c.daemonHTTPGet(ctx, binding, "/api/jobs?"+query.Encode(), http.StatusOK, 64<<20)
	if err != nil {
		return nil, err
	}
	clean, err := stripHeavyJobFields(raw)
	if err != nil {
		return nil, err
	}
	if !c.daemonHTTPBindingCurrent(binding) {
		return nil, ErrBindingChanged
	}
	return clean, nil
}

func (c *Client) JobMetadata(ctx context.Context, id int64) (JobMetadata, error) {
	if err := validateJobID(id); err != nil {
		return JobMetadata{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	binding, err := c.daemonHTTPPreflight(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return JobMetadata{}, ctx.Err()
		}
		if errors.Is(err, ErrBindingChanged) {
			return JobMetadata{}, ErrBindingChanged
		}
		return JobMetadata{}, fmt.Errorf("%w: version could not be verified", ErrUnsupportedJobMetadata)
	}
	query := url.Values{}
	query.Set("id", strconv.FormatInt(id, 10))
	query.Set("limit", "1")
	query.Set("omit_prompt", "true")
	data, err := c.daemonHTTPGet(ctx, binding, "/api/jobs?"+query.Encode(), http.StatusOK, 256<<10)
	if err != nil {
		if ctx.Err() != nil {
			return JobMetadata{}, ctx.Err()
		}
		if errors.Is(err, ErrBindingChanged) {
			return JobMetadata{}, ErrBindingChanged
		}
		if isUnreachableHealthError(err) {
			return JobMetadata{}, fmt.Errorf("%w: metadata request failed", ErrDaemonOffline)
		}
		return JobMetadata{}, fmt.Errorf("%w: metadata request failed", ErrMalformedJobMetadata)
	}
	var response struct {
		Jobs []JobMetadata `json:"jobs"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return JobMetadata{}, fmt.Errorf("%w: invalid JSON", ErrMalformedJobMetadata)
	}
	if len(response.Jobs) != 1 || response.Jobs[0].ID != id {
		if len(response.Jobs) == 0 {
			return JobMetadata{}, ErrJobMetadataNotFound
		}
		return JobMetadata{}, fmt.Errorf("%w: job identity mismatch", ErrMalformedJobMetadata)
	}
	if !c.daemonHTTPBindingCurrent(binding) {
		return JobMetadata{}, ErrBindingChanged
	}
	return response.Jobs[0], nil
}

// TerminalJobMetadata is the bounded variant used immediately after a
// terminal output-status prefix. v0.64 omits prompt data for terminal jobs;
// retaining the smaller cap protects the one-time reconciliation fetch from
// unexpected future payload growth.
func (c *Client) TerminalJobMetadata(ctx context.Context, id int64) (JobMetadata, error) {
	if err := validateJobID(id); err != nil {
		return JobMetadata{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	binding, err := c.daemonHTTPPreflight(ctx)
	if err != nil {
		if errors.Is(err, errUnsupportedDaemonBinding) {
			return JobMetadata{}, fmt.Errorf("%w: %v", ErrUnsupportedJobMetadata, err)
		}
		return JobMetadata{}, err
	}
	if !capabilities(binding.version).JobListAPI {
		return JobMetadata{}, fmt.Errorf("%w: %s", ErrUnsupportedJobMetadata, binding.version)
	}
	query := url.Values{}
	query.Set("id", strconv.FormatInt(id, 10))
	query.Set("limit", "1")
	query.Set("omit_prompt", "true")
	raw, err := c.daemonHTTPGet(ctx, binding, "/api/jobs?"+query.Encode(), http.StatusOK, 256<<10)
	if err != nil {
		if ctx.Err() != nil {
			return JobMetadata{}, ctx.Err()
		}
		if errors.Is(err, ErrBindingChanged) {
			return JobMetadata{}, err
		}
		return JobMetadata{}, fmt.Errorf("%w: metadata request failed", ErrMalformedJobMetadata)
	}
	var response struct {
		Jobs []JobMetadata `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return JobMetadata{}, fmt.Errorf("%w: invalid JSON", ErrMalformedJobMetadata)
	}
	if len(response.Jobs) != 1 || response.Jobs[0].ID != id {
		return JobMetadata{}, fmt.Errorf("%w: job not found", ErrMalformedJobMetadata)
	}
	if !c.daemonHTTPBindingCurrent(binding) {
		return JobMetadata{}, ErrBindingChanged
	}
	return response.Jobs[0], nil
}

// JobOutputStatus reads only the ordered job_id/status prefix of RoboRev's
// v0.64 output response. It never decodes or buffers the potentially huge
// lines value and closes the response before reading that value.
func (c *Client) JobOutputStatus(ctx context.Context, id int64) (JobOutputStatus, error) {
	if err := validateJobID(id); err != nil {
		return JobOutputStatus{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	binding, err := c.daemonHTTPPreflight(ctx)
	if err != nil {
		if errors.Is(err, errUnsupportedDaemonBinding) {
			return JobOutputStatus{}, fmt.Errorf("%w: %v", ErrUnsupportedJobOutputStatus, err)
		}
		return JobOutputStatus{}, err
	}
	if !capabilities(binding.version).JobOutputStatusAPI {
		return JobOutputStatus{}, fmt.Errorf("%w: %s", ErrUnsupportedJobOutputStatus, binding.version)
	}
	query := url.Values{}
	query.Set("job_id", strconv.FormatInt(id, 10))
	if !c.daemonHTTPBindingCurrent(binding) {
		return JobOutputStatus{}, ErrBindingChanged
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, binding.daemonURL+"/api/job/output?"+query.Encode(), nil)
	if err != nil {
		return JobOutputStatus{}, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := noRedirectHTTPClient(c.HTTP).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return JobOutputStatus{}, ctx.Err()
		}
		return JobOutputStatus{}, fmt.Errorf("RoboRev daemon request: %w", err)
	}
	status, err := decodeJobOutputStatus(resp, id)
	if err != nil {
		return JobOutputStatus{}, err
	}
	if !c.daemonHTTPBindingCurrent(binding) {
		return JobOutputStatus{}, ErrBindingChanged
	}
	return status, nil
}

const jobOutputPrefixLimit = 8 << 10

type prefixReader struct {
	r     io.Reader
	read  int
	limit int
}

func (r *prefixReader) Read(p []byte) (int, error) {
	if r.read >= r.limit {
		return 0, fmt.Errorf("%w: prefix exceeds %d bytes", ErrMalformedJobOutputStatus, r.limit)
	}
	if len(p) > 1 {
		p = p[:1]
	}
	if r.limit-r.read < len(p) {
		p = p[:r.limit-r.read]
	}
	n, err := r.r.Read(p)
	r.read += n
	return n, err
}

func decodeJobOutputStatus(resp *http.Response, expectedID int64) (JobOutputStatus, error) {
	if resp == nil {
		return JobOutputStatus{}, fmt.Errorf("%w: empty response", ErrMalformedJobOutputStatus)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return JobOutputStatus{}, fmt.Errorf("%w: job output returned %d", ErrUnsupportedJobOutputStatus, resp.StatusCode)
		}
		return JobOutputStatus{}, fmt.Errorf("RoboRev job output returned %d", resp.StatusCode)
	}
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return JobOutputStatus{}, fmt.Errorf("%w: expected application/json", ErrMalformedJobOutputStatus)
	}
	reader := &prefixReader{r: resp.Body, limit: jobOutputPrefixLimit}
	read := func() (byte, error) {
		var one [1]byte
		_, err := reader.Read(one[:])
		return one[0], err
	}
	// whitespaceUntil consumes whitespace and returns the first non-space byte.
	whitespaceUntil := func() (byte, error) {
		for {
			one, err := read()
			if err != nil {
				return 0, err
			}
			if one != ' ' && one != '\t' && one != '\r' && one != '\n' {
				return one, nil
			}
		}
	}
	readString := func(first byte) (string, error) {
		if first != '"' {
			return "", fmt.Errorf("expected JSON string")
		}
		value := []byte{'"'}
		escaped := false
		for {
			one, err := read()
			if err != nil {
				return "", err
			}
			value = append(value, one)
			if escaped {
				escaped = false
				continue
			}
			if one == '\\' {
				escaped = true
				continue
			}
			if one == '"' {
				var result string
				if err := json.Unmarshal(value, &result); err != nil {
					return "", err
				}
				return result, nil
			}
			if one < 0x20 {
				return "", fmt.Errorf("control byte in JSON string")
			}
		}
	}
	readKey := func(want string) error {
		first, err := whitespaceUntil()
		if err != nil {
			return err
		}
		key, err := readString(first)
		if err != nil || key != want {
			return fmt.Errorf("expected key %q", want)
		}
		colon, err := whitespaceUntil()
		if err != nil || colon != ':' {
			return fmt.Errorf("expected colon after %q", want)
		}
		return nil
	}
	first, err := whitespaceUntil()
	if err != nil || first != '{' {
		return JobOutputStatus{}, fmt.Errorf("%w: expected object", ErrMalformedJobOutputStatus)
	}
	if err := readKey("job_id"); err != nil {
		return JobOutputStatus{}, fmt.Errorf("%w: %v", ErrMalformedJobOutputStatus, err)
	}
	idFirst, err := whitespaceUntil()
	if err != nil || idFirst < '0' || idFirst > '9' {
		return JobOutputStatus{}, fmt.Errorf("%w: expected job_id", ErrMalformedJobOutputStatus)
	}
	idBytes := []byte{idFirst}
	var idDelimiter byte
	for {
		one, err := read()
		if err != nil {
			return JobOutputStatus{}, fmt.Errorf("%w: invalid job_id", ErrMalformedJobOutputStatus)
		}
		if one < '0' || one > '9' {
			if one != ',' && one != ' ' && one != '\t' && one != '\r' && one != '\n' {
				return JobOutputStatus{}, fmt.Errorf("%w: invalid job_id", ErrMalformedJobOutputStatus)
			}
			idDelimiter = one
			break
		}
		idBytes = append(idBytes, one)
	}
	parsedID, err := strconv.ParseInt(string(idBytes), 10, 64)
	if err != nil || parsedID != expectedID {
		return JobOutputStatus{}, fmt.Errorf("%w: job ID mismatch", ErrMalformedJobOutputStatus)
	}
	if idDelimiter != ',' {
		// Consume the separator comma after whitespace.
		for {
			one, readErr := read()
			if readErr != nil {
				return JobOutputStatus{}, fmt.Errorf("%w: missing status", ErrMalformedJobOutputStatus)
			}
			if one == ',' {
				break
			}
			if one != ' ' && one != '\t' && one != '\r' && one != '\n' {
				return JobOutputStatus{}, fmt.Errorf("%w: missing status", ErrMalformedJobOutputStatus)
			}
		}
	}
	if err := readKey("status"); err != nil {
		return JobOutputStatus{}, fmt.Errorf("%w: %v", ErrMalformedJobOutputStatus, err)
	}
	statusFirst, err := whitespaceUntil()
	if err != nil {
		return JobOutputStatus{}, fmt.Errorf("%w: missing status value", ErrMalformedJobOutputStatus)
	}
	status, err := readString(statusFirst)
	if err != nil || strings.TrimSpace(status) == "" {
		return JobOutputStatus{}, fmt.Errorf("%w: invalid status", ErrMalformedJobOutputStatus)
	}
	separator, err := whitespaceUntil()
	if err != nil || separator != ',' {
		return JobOutputStatus{}, fmt.Errorf("%w: expected lines after status", ErrMalformedJobOutputStatus)
	}
	if err := readKey("lines"); err != nil {
		return JobOutputStatus{}, fmt.Errorf("%w: %v", ErrMalformedJobOutputStatus, err)
	}
	lineFirst, err := whitespaceUntil()
	if err != nil || lineFirst != '[' {
		return JobOutputStatus{}, fmt.Errorf("%w: lines must be an array", ErrMalformedJobOutputStatus)
	}
	return JobOutputStatus{JobID: parsedID, Status: status}, nil
}

func (c *Client) SynthesisStatus(ctx context.Context, id int64) (SynthesisStatus, error) {
	prefix, err := c.JobOutputStatus(ctx, id)
	if err != nil {
		return SynthesisStatus{}, err
	}
	if prefix.Status == "queued" || prefix.Status == "running" {
		return SynthesisStatus{JobMetadata: JobMetadata{ID: prefix.JobID, Status: prefix.Status}}, nil
	}
	if prefix.Status != "done" {
		return SynthesisStatus{}, fmt.Errorf("unknown panel review status: %s", prefix.Status)
	}
	metadata, err := c.TerminalJobMetadata(ctx, id)
	if err != nil {
		return SynthesisStatus{}, err
	}
	return SynthesisStatus{JobMetadata: metadata}, nil
}

func (c *Client) Show(ctx context.Context, id int64) (json.RawMessage, error) {
	if err := validateJobID(id); err != nil {
		return nil, err
	}
	binding, err := c.daemonHTTPPreflight(ctx)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("job_id", strconv.FormatInt(id, 10))
	data, err := c.daemonHTTPGet(ctx, binding, "/api/review?"+query.Encode(), http.StatusOK, daemonHTTPShowLimit)
	if err != nil {
		return nil, err
	}
	clean, err := sanitizeReviewPayload(data)
	if err != nil {
		return nil, errors.New("RoboRev review response is malformed")
	}
	return clean, nil
}

func (c *Client) PanelMembers(ctx context.Context, runUUID string) (json.RawMessage, error) {
	if len(runUUID) != 36 {
		return nil, errors.New("invalid panel run UUID")
	}
	for _, char := range runUUID {
		if !(char == '-' || char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F') {
			return nil, errors.New("invalid panel run UUID")
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	binding, err := c.daemonHTTPPreflight(ctx)
	if err != nil {
		return nil, err
	}
	if !capabilities(binding.version).PanelMembersAPI {
		return nil, fmt.Errorf("RoboRev %s does not support panel member listing", binding.version)
	}
	raw, err := c.daemonHTTPGet(ctx, binding, "/api/jobs?panel_run="+url.QueryEscape(runUUID)+"&limit=0&omit_prompt=true", http.StatusOK, 64<<20)
	if err != nil {
		return nil, err
	}
	clean, err := stripHeavyJobFields(raw)
	if err != nil {
		return nil, err
	}
	if !c.daemonHTTPBindingCurrent(binding) {
		return nil, ErrBindingChanged
	}
	return clean, nil
}

func stripHeavyJobFields(raw json.RawMessage) (json.RawMessage, error) {
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, errors.New("invalid job list response")
	}
	var jobs []map[string]json.RawMessage
	if err := json.Unmarshal(response["jobs"], &jobs); err != nil {
		return nil, errors.New("invalid jobs payload")
	}
	cleanJobs := make([]json.RawMessage, 0, len(jobs))
	for _, job := range jobs {
		jobRaw, err := json.Marshal(job)
		if err != nil {
			return nil, errors.New("invalid job list response")
		}
		clean, err := sanitizeJobObject(jobRaw)
		if err != nil {
			return nil, errors.New("invalid job list response")
		}
		cleanJobs = append(cleanJobs, clean)
	}
	jobsJSON, err := json.Marshal(cleanJobs)
	if err != nil {
		return nil, err
	}
	cleanResponse := map[string]json.RawMessage{"jobs": jobsJSON}
	if value, ok := response["has_more"]; ok {
		cleanResponse["has_more"] = value
	}
	clean, err := json.Marshal(cleanResponse)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(clean), nil
}

// reviewPublicFields is deliberately an allowlist. The daemon review object
// contains storage, worker, command, and machine/session fields that must not
// cross the dashboard boundary, even if a future endpoint adds them under an
// otherwise familiar object.
var reviewPublicFields = map[string]struct{}{
	"id": {}, "job_id": {}, "review_id": {}, "status": {}, "state": {},
	"closed": {}, "skipped": {}, "verdict": {}, "verdict_bool": {},
	"repo_path": {}, "git_ref": {}, "branch": {}, "commit_subject": {},
	"job_type": {}, "agent": {}, "model": {}, "reasoning": {},
	"panel_name": {}, "panel_run_uuid": {}, "panel_role": {},
	"panel_member_name": {}, "panel_member_index": {},
	"enqueued_at": {}, "started_at": {}, "finished_at": {}, "created_at": {},
	"output": {}, "job": {}, "review": {}, "members": {}, "text": {}, "markdown": {},
	"result": {},
}

var jobPublicFields = map[string]struct{}{
	"id": {}, "job_id": {}, "review_id": {}, "uuid": {}, "status": {}, "state": {},
	"closed": {}, "skipped": {}, "verdict": {}, "verdict_bool": {},
	"repo_path": {}, "repo_name": {}, "git_ref": {}, "branch": {}, "commit_subject": {},
	"job_type": {}, "agent": {}, "model": {}, "reasoning": {},
	"panel_name": {}, "panel_run_uuid": {}, "panel_role": {},
	"panel_member_name": {}, "panel_member_index": {}, "parent_job_id": {}, "member_job_ids": {},
	"enqueued_at": {}, "started_at": {}, "finished_at": {},
}

func sanitizeReviewPayload(raw []byte) (json.RawMessage, error) {
	return sanitizeReviewObject(raw)
}

func sanitizeReviewObject(raw []byte) (json.RawMessage, error) {
	return sanitizeObjectWithFields(raw, reviewPublicFields)
}

func sanitizeJobObject(raw []byte) (json.RawMessage, error) {
	return sanitizeObjectWithFields(raw, jobPublicFields)
}

func sanitizeObjectWithFields(raw []byte, allowed map[string]struct{}) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, errors.New("review payload is not an object")
	}
	clean := make(map[string]json.RawMessage, len(object))
	for key, value := range object {
		if _, ok := allowed[key]; !ok {
			continue
		}
		switch key {
		case "job":
			nested, err := sanitizeJobObject(value)
			if err != nil {
				return nil, err
			}
			clean[key] = nested
		case "review":
			nested, err := sanitizeReviewObject(value)
			if err != nil {
				return nil, err
			}
			clean[key] = nested
		case "members":
			var members []json.RawMessage
			if err := json.Unmarshal(value, &members); err != nil {
				return nil, errors.New("review members are malformed")
			}
			cleanMembers := make([]json.RawMessage, 0, len(members))
			for _, member := range members {
				nested, err := sanitizeReviewObject(member)
				if err != nil {
					return nil, err
				}
				cleanMembers = append(cleanMembers, nested)
			}
			encoded, err := json.Marshal(cleanMembers)
			if err != nil {
				return nil, err
			}
			clean[key] = encoded
		default:
			trimmed := bytes.TrimSpace(value)
			if len(trimmed) > 0 && trimmed[0] == '{' {
				nested, err := sanitizeObjectWithFields(value, allowed)
				if err != nil {
					return nil, err
				}
				clean[key] = nested
			} else {
				clean[key] = append(json.RawMessage(nil), value...)
			}
		}
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// daemonHTTPBinding is the immutable binding captured for one direct daemon
// request.  These lifecycle-free endpoints must never trigger a CLI version
// probe or daemon start; an already verified cached CLI version and a fresh
// daemon health/version match are therefore required instead.
type daemonHTTPBinding struct {
	daemonURL  string
	generation uint64
	version    string
}

func (c *Client) daemonHTTPPreflight(ctx context.Context) (daemonHTTPBinding, error) {
	if c == nil {
		return daemonHTTPBinding{}, errors.New("RoboRev daemon binding is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	binary, daemonURL, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" || strings.TrimSpace(daemonURL) == "" {
		return daemonHTTPBinding{}, errors.New("RoboRev daemon binding is unavailable")
	}
	c.versionMu.RLock()
	version := c.version
	versionGeneration := c.versionGeneration
	versionAt := c.versionAt
	c.versionMu.RUnlock()
	if versionGeneration != generation || versionAt.IsZero() || time.Since(versionAt) >= 30*time.Second || !roborevcompat.IsSupportedVersion(version) {
		return daemonHTTPBinding{}, fmt.Errorf("%w: executable version is unavailable", errUnsupportedDaemonBinding)
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return daemonHTTPBinding{}, ctxErr
		}
		// Preserve transport classification for authority-bearing callers while
		// retaining the health parser's sanitized, body-free error text.
		return daemonHTTPBinding{}, fmt.Errorf("RoboRev daemon binding could not be verified: %w", err)
	}
	if !roborevcompat.IsMatchingSupportedVersion(version, health.Version) {
		return daemonHTTPBinding{}, fmt.Errorf("%w: executable and daemon versions differ", ErrBindingChanged)
	}
	if !c.generationCurrent(generation) {
		return daemonHTTPBinding{}, ErrBindingChanged
	}
	return daemonHTTPBinding{daemonURL: daemonURL, generation: generation, version: version}, nil
}

func (c *Client) daemonHTTPBindingCurrent(binding daemonHTTPBinding) bool {
	if c == nil {
		return false
	}
	binary, daemonURL, generation := c.snapshot()
	return strings.TrimSpace(binary) != "" && daemonURL == binding.daemonURL && generation == binding.generation
}

const daemonHTTPShowLimit int64 = 8 << 20
const daemonHTTPMutationLimit int64 = 1 << 20

// daemonHTTPGet reads only successful response bodies. Error bodies are
// intentionally left unread and never included in returned errors because a
// daemon can put sensitive review data in them.
func (c *Client) daemonHTTPGet(ctx context.Context, binding daemonHTTPBinding, path string, expectedStatus int, limit int64) ([]byte, error) {
	if !c.daemonHTTPBindingCurrent(binding) {
		return nil, ErrBindingChanged
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, binding.daemonURL+path, nil)
	if err != nil {
		return nil, errors.New("RoboRev daemon request could not be built")
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := noRedirectHTTPClient(c.HTTP).Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("RoboRev daemon request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		return nil, fmt.Errorf("RoboRev daemon returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("RoboRev daemon response could not be read")
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("RoboRev daemon response exceeds %d bytes", limit)
	}
	if err := validateDaemonJSON(resp.Header, data); err != nil {
		return nil, errors.New("RoboRev daemon returned malformed JSON")
	}
	if !c.daemonHTTPBindingCurrent(binding) {
		return nil, ErrBindingChanged
	}
	return data, nil
}

func (c *Client) postDaemonMutation(ctx context.Context, binding daemonHTTPBinding, path string, body any, expectedStatus int) error {
	if !c.daemonHTTPBindingCurrent(binding) {
		return ErrBindingChanged
	}
	if ctx == nil {
		ctx = context.Background()
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return errors.New("RoboRev daemon request could not be encoded")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, binding.daemonURL+path, bytes.NewReader(payload))
	if err != nil {
		return errors.New("RoboRev daemon request could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := noRedirectHTTPClient(c.HTTP).Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("RoboRev daemon request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		return fmt.Errorf("RoboRev daemon returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, daemonHTTPMutationLimit+1))
	if err != nil {
		return errors.New("RoboRev daemon response could not be read")
	}
	if int64(len(data)) > daemonHTTPMutationLimit {
		return fmt.Errorf("RoboRev daemon response exceeds %d bytes", daemonHTTPMutationLimit)
	}
	if !c.daemonHTTPBindingCurrent(binding) {
		return ErrBindingChanged
	}
	return nil
}

// postDaemonJSON is the exact-status JSON-returning variant used by enqueue.
// Unlike postJSON it is tied to an already verified binding and does not
// accept arbitrary 2xx responses.
func (c *Client) postDaemonJSON(ctx context.Context, binding daemonHTTPBinding, path string, body any, expectedStatuses map[int]struct{}, limit int64) (json.RawMessage, error) {
	if !c.daemonHTTPBindingCurrent(binding) {
		return nil, ErrBindingChanged
	}
	if ctx == nil {
		ctx = context.Background()
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("RoboRev daemon request could not be encoded")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, binding.daemonURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("RoboRev daemon request could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := noRedirectHTTPClient(c.HTTP).Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("RoboRev daemon request: %w", err)
	}
	defer resp.Body.Close()
	if _, ok := expectedStatuses[resp.StatusCode]; !ok {
		return nil, fmt.Errorf("RoboRev daemon returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("RoboRev daemon response could not be read")
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("RoboRev daemon response exceeds %d bytes", limit)
	}
	if err := validateDaemonJSON(resp.Header, data); err != nil {
		return nil, errors.New("RoboRev daemon returned malformed JSON")
	}
	if !c.daemonHTTPBindingCurrent(binding) {
		return nil, ErrBindingChanged
	}
	return json.RawMessage(data), nil
}

func validateDaemonJSON(header http.Header, data []byte) error {
	mediaType, _, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("unexpected daemon response media type")
	}
	if len(bytes.TrimSpace(data)) == 0 || !utf8.Valid(data) {
		return errors.New("invalid daemon JSON encoding")
	}
	return validateUniqueJSON(data)
}

func validateUniqueJSON(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 || !utf8.Valid(data) {
		return errors.New("invalid JSON encoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanUniqueJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("trailing daemon JSON value")
		}
		return err
	}
	return nil
}

const maxDaemonJSONDepth = 64

func scanUniqueJSONValue(decoder *json.Decoder, depth int) error {
	if depth > maxDaemonJSONDepth {
		return errors.New("daemon JSON nesting is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("daemon JSON object key is malformed")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("daemon JSON contains duplicate fields")
			}
			seen[key] = struct{}{}
			if err := scanUniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("daemon JSON object is malformed")
		}
	case '[':
		for decoder.More() {
			if err := scanUniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("daemon JSON array is malformed")
		}
	default:
		return errors.New("daemon JSON delimiter is malformed")
	}
	return nil
}

func (c *Client) getDaemonJSON(ctx context.Context, path string, limit int64) (json.RawMessage, error) {
	_, daemonURL, _ := c.snapshot()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := noRedirectHTTPClient(c.HTTP).Do(req)
	if err != nil {
		return nil, fmt.Errorf("RoboRev daemon request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RoboRev daemon returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("RoboRev daemon response exceeds %d bytes", limit)
	}
	return decodeRaw(string(data))
}

func (c *Client) getDaemonJSONBounded(ctx context.Context, path string, limit int64) (json.RawMessage, error) {
	_, daemonURL, _ := c.snapshot()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := noRedirectHTTPClient(c.HTTP).Do(req)
	if err != nil {
		return nil, fmt.Errorf("RoboRev daemon request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, fmt.Errorf("%w: daemon returned %d", ErrUnsupportedJobMetadata, resp.StatusCode)
		}
		return nil, fmt.Errorf("RoboRev daemon returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrMalformedJobMetadata, limit)
	}
	if raw, err := decodeRaw(string(data)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedJobMetadata, err)
	} else {
		return raw, nil
	}
}

func (c *Client) postJSON(ctx context.Context, path string, body any) (json.RawMessage, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	_, daemonURL, _ := c.snapshot()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, daemonURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := noRedirectHTTPClient(c.HTTP).Do(req)
	if err != nil {
		return nil, fmt.Errorf("RoboRev daemon request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("RoboRev daemon returned %d", resp.StatusCode)
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if readErr != nil {
		return nil, readErr
	}
	if len(data) > 8<<20 {
		return nil, errors.New("RoboRev daemon response exceeds 8388608 bytes")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(data) {
		return nil, errors.New("RoboRev daemon returned malformed JSON")
	}
	return json.RawMessage(data), nil
}

func (c *Client) Review(ctx context.Context, request ReviewRequest) (json.RawMessage, error) {
	if err := ValidateReviewRequest(request); err != nil {
		return nil, err
	}
	binding, err := c.daemonHTTPPreflight(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"repo_path": request.RepoPath, "git_ref": request.GitRef, "branch": request.Branch}
	if request.Panel != "" {
		body["panel"] = request.Panel
	}
	return c.postDaemonJSON(ctx, binding, "/api/enqueue", body, map[int]struct{}{http.StatusOK: {}, http.StatusCreated: {}}, daemonHTTPMutationLimit)
}

// Fix executes RoboRev's v0.64 fix command. It is deliberately a typed
// process invocation: the synthesis job id is the only review-derived
// argument and the repository is supplied as the process working directory.
func (c *Client) Fix(ctx context.Context, request FixRequest) error {
	args, err := FixArgs(request.JobID, request.Agent)
	if err != nil {
		return err
	}
	if strings.TrimSpace(request.RepoPath) == "" {
		return errors.New("repository path is required")
	}
	installation := c.Installation(ctx)
	if !installation.Installed {
		return errors.New("RoboRev is not installed or version detection failed")
	}
	if !installation.Capabilities.FixCommand {
		return fmt.Errorf("RoboRev %s does not support the version-gated fix command", installation.Version)
	}
	binary, _, _ := c.snapshot()
	result, runErr := c.Runner.Run(ctx, proc.Command{
		Executable:  binary,
		Args:        args,
		Dir:         request.RepoPath,
		Timeout:     45 * time.Minute,
		OutputLimit: 8 << 20,
	})
	if runErr != nil {
		_ = result
		return fmt.Errorf("RoboRev fix command failed: %w", runErr)
	}
	return nil
}

func (c *Client) Rerun(ctx context.Context, id int64) error {
	if err := validateJobID(id); err != nil {
		return err
	}
	binding, err := c.daemonHTTPPreflight(ctx)
	if err != nil {
		return err
	}
	return c.postDaemonMutation(ctx, binding, "/api/job/rerun", map[string]int64{"job_id": id}, http.StatusOK)
}

func (c *Client) Comment(ctx context.Context, id int64, comment string) error {
	args, err := CommentArgs(id, comment)
	if err != nil {
		return err
	}
	_ = args // retain the public command contract's validation rules without invoking the CLI.
	binding, err := c.daemonHTTPPreflight(ctx)
	if err != nil {
		return err
	}
	return c.postDaemonMutation(ctx, binding, "/api/comment", map[string]any{
		"job_id":    id,
		"commenter": "roborev-dashboard",
		"comment":   comment,
	}, http.StatusCreated)
}

func (c *Client) Close(ctx context.Context, id int64, reopen bool) error {
	args, err := CloseArgs(id, reopen)
	if err != nil {
		return err
	}
	_ = args
	binding, err := c.daemonHTTPPreflight(ctx)
	if err != nil {
		return err
	}
	return c.postDaemonMutation(ctx, binding, "/api/review/close", map[string]any{
		"job_id": id,
		"closed": !reopen,
	}, http.StatusOK)
}

func (c *Client) Cancel(ctx context.Context, id int64) error {
	args, err := CancelArgs(id)
	if err != nil {
		return err
	}
	_ = args
	binding, err := c.daemonHTTPPreflight(ctx)
	if err != nil {
		return err
	}
	return c.postDaemonMutation(ctx, binding, "/api/job/cancel", map[string]any{"job_id": id}, http.StatusOK)
}

func (c *Client) StartDaemon(ctx context.Context) error {
	binary, _, _ := c.snapshot()
	if binary == "" {
		return errors.New("roborev is not installed")
	}
	result, err := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: []string{"daemon", "start"}, Timeout: 30 * time.Second, OutputLimit: 1 << 20, AllowDetachedChildren: true})
	if err != nil {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			return err
		}
		return fmt.Errorf("%s: %w", message, err)
	}
	return err
}
