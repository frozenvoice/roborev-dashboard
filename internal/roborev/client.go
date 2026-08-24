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
	"syscall"
	"time"

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
	Prompt(context.Context, int64) (string, error)
	Log(context.Context, int64) (string, error)
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
	return Capabilities{ReviewEnqueueAPI: v64, FrozenRerunAPI: v64, JobListAPI: v64, JobOutputStatusAPI: v64, PanelReview: v64, FixCommand: v64, PanelMembersAPI: v64, StreamEvents: v64, AnalyzeIntrospection: v64, ConfigIntrospection: v64, ConfigMutation: v64, SummaryStats: v64, CostStats: v64, GuidelineInsights: v64, RepositoryInventory: v64, RepositoryRename: v64, RepositoryMove: v64, RepositoryMerge: v64, PromptLazyLoad: v64, RawLog: v64}
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
	version, err := c.detectVersion(ctx)
	if err != nil {
		return nil, err
	}
	if !capabilities(version).JobListAPI {
		return nil, fmt.Errorf("RoboRev %s does not support metadata-only job listing", version)
	}
	if limit < 1 || limit > 200 {
		limit = 100
	}
	query := url.Values{}
	query.Set("repo", repo)
	query.Set("limit", strconv.Itoa(limit))
	query.Set("omit_prompt", "true")
	raw, err := c.getDaemonJSON(ctx, "/api/jobs?"+query.Encode(), 64<<20)
	if err != nil {
		return nil, err
	}
	return stripHeavyJobFields(raw)
}

func (c *Client) JobMetadata(ctx context.Context, id int64) (JobMetadata, error) {
	if err := validateJobID(id); err != nil {
		return JobMetadata{}, err
	}
	_, _, generation := c.snapshot()
	version, err := c.detectVersion(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return JobMetadata{}, ctx.Err()
		}
		if errors.Is(err, ErrBindingChanged) {
			return JobMetadata{}, ErrBindingChanged
		}
		return JobMetadata{}, fmt.Errorf("%w: version could not be verified", ErrUnsupportedJobMetadata)
	}
	if !capabilities(version).JobListAPI {
		return JobMetadata{}, fmt.Errorf("%w: %s", ErrUnsupportedJobMetadata, version)
	}
	query := url.Values{}
	query.Set("id", strconv.FormatInt(id, 10))
	query.Set("limit", "1")
	query.Set("omit_prompt", "true")
	raw, err := c.getDaemonJSON(ctx, "/api/jobs?"+query.Encode(), 64<<20)
	if err != nil {
		if ctx.Err() != nil {
			return JobMetadata{}, ctx.Err()
		}
		if isUnreachableHealthError(err) {
			return JobMetadata{}, fmt.Errorf("%w: metadata request failed", ErrDaemonOffline)
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
		if len(response.Jobs) == 0 {
			return JobMetadata{}, ErrJobMetadataNotFound
		}
		return JobMetadata{}, fmt.Errorf("%w: job identity mismatch", ErrMalformedJobMetadata)
	}
	if !c.generationCurrent(generation) {
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
	version, err := c.detectVersion(ctx)
	if err != nil {
		return JobMetadata{}, err
	}
	if !capabilities(version).JobListAPI {
		return JobMetadata{}, fmt.Errorf("%w: %s", ErrUnsupportedJobMetadata, version)
	}
	query := url.Values{}
	query.Set("id", strconv.FormatInt(id, 10))
	query.Set("limit", "1")
	query.Set("omit_prompt", "true")
	raw, err := c.getDaemonJSONBounded(ctx, "/api/jobs?"+query.Encode(), 256<<10)
	if err != nil {
		return JobMetadata{}, err
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
	return response.Jobs[0], nil
}

// JobOutputStatus reads only the ordered job_id/status prefix of RoboRev's
// v0.64 output response. It never decodes or buffers the potentially huge
// lines value and closes the response before reading that value.
func (c *Client) JobOutputStatus(ctx context.Context, id int64) (JobOutputStatus, error) {
	if err := validateJobID(id); err != nil {
		return JobOutputStatus{}, err
	}
	version, err := c.detectVersion(ctx)
	if err != nil {
		return JobOutputStatus{}, err
	}
	if !capabilities(version).JobOutputStatusAPI {
		return JobOutputStatus{}, fmt.Errorf("%w: %s", ErrUnsupportedJobOutputStatus, version)
	}
	query := url.Values{}
	query.Set("job_id", strconv.FormatInt(id, 10))
	_, daemonURL, _ := c.snapshot()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL+"/api/job/output?"+query.Encode(), nil)
	if err != nil {
		return JobOutputStatus{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return JobOutputStatus{}, fmt.Errorf("RoboRev daemon request: %w", err)
	}
	return decodeJobOutputStatus(resp, id)
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
	args, err := ShowArgs(id)
	if err != nil {
		return nil, err
	}
	result, err := c.run(ctx, args, 10*time.Second, 8<<20)
	if err != nil {
		return nil, err
	}
	return decodeRaw(result.Stdout)
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
	version, err := c.detectVersion(ctx)
	if err != nil {
		return nil, err
	}
	if !capabilities(version).PanelMembersAPI {
		return nil, fmt.Errorf("RoboRev %s does not support panel member listing", version)
	}
	raw, err := c.getDaemonJSON(ctx, "/api/jobs?panel_run="+url.QueryEscape(runUUID)+"&limit=0&omit_prompt=true", 64<<20)
	if err != nil {
		return nil, err
	}
	return stripHeavyJobFields(raw)
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
	for _, job := range jobs {
		for _, field := range []string{"prompt", "diff_content", "patch", "review", "output", "log", "raw_log"} {
			delete(job, field)
		}
	}
	jobsJSON, err := json.Marshal(jobs)
	if err != nil {
		return nil, err
	}
	response["jobs"] = jobsJSON
	clean, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(clean), nil
}

func (c *Client) getDaemonJSON(ctx context.Context, path string, limit int64) (json.RawMessage, error) {
	_, daemonURL, _ := c.snapshot()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RoboRev daemon request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RoboRev daemon returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return decodeRaw(string(data))
}

func (c *Client) getDaemonJSONBounded(ctx context.Context, path string, limit int64) (json.RawMessage, error) {
	_, daemonURL, _ := c.snapshot()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RoboRev daemon request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrMalformedJobMetadata, limit)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, fmt.Errorf("%w: daemon returned %d", ErrUnsupportedJobMetadata, resp.StatusCode)
		}
		return nil, fmt.Errorf("RoboRev daemon returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
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
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RoboRev daemon request: %w", err)
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("RoboRev daemon returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
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
	installation := c.Installation(ctx)
	if !installation.Installed {
		return nil, errors.New("RoboRev is not installed or version detection failed")
	}
	if !installation.Capabilities.ReviewEnqueueAPI {
		return nil, fmt.Errorf("RoboRev %s does not support the version-gated enqueue API", installation.Version)
	}
	if err := ValidateReviewRequest(request); err != nil {
		return nil, err
	}
	body := map[string]any{"repo_path": request.RepoPath, "git_ref": request.GitRef, "branch": request.Branch}
	if request.Panel != "" {
		body["panel"] = request.Panel
	}
	return c.postJSON(ctx, "/api/enqueue", body)
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
	installation := c.Installation(ctx)
	if !installation.Installed || !installation.Capabilities.FrozenRerunAPI {
		return fmt.Errorf("RoboRev %s does not support frozen-target rerun", installation.Version)
	}
	_, err := c.postJSON(ctx, "/api/job/rerun", map[string]int64{"job_id": id})
	return err
}

func (c *Client) Comment(ctx context.Context, id int64, comment string) error {
	args, err := CommentArgs(id, comment)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, args, 15*time.Second, 1<<20)
	return err
}

func (c *Client) Close(ctx context.Context, id int64, reopen bool) error {
	args, err := CloseArgs(id, reopen)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, args, 10*time.Second, 1<<20)
	return err
}

func (c *Client) Cancel(ctx context.Context, id int64) error {
	args, err := CancelArgs(id)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, args, 10*time.Second, 1<<20)
	return err
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
