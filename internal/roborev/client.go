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
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	proc "github.com/froze/roborev-dashboard/internal/process"
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
	ErrUnsupportedJobOutputStatus = errors.New("RoboRev job output status contract is unsupported")
	ErrMalformedJobOutputStatus   = errors.New("malformed RoboRev job output status response")
	ErrUnsupportedJobMetadata     = errors.New("RoboRev job metadata contract is unsupported")
	ErrMalformedJobMetadata       = errors.New("malformed RoboRev job metadata response")
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
	Binary           string
	Runner           proc.Runner
	DaemonURL        string
	HTTP             *http.Client
	versionMu        sync.RWMutex
	version          string
	versionAt        time.Time
	versionRefreshMu sync.Mutex
	versionErr       error
	versionErrAt     time.Time
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
	if err != nil {
		return "", errors.New("roborev executable not found on PATH")
	}
	return filepath.Abs(path)
}

func NewClient(binary string, runner proc.Runner, daemonAddress string) (*Client, error) {
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
	return &Client{Binary: binary, Runner: runner, DaemonURL: strings.TrimRight(daemonAddress, "/"), HTTP: &http.Client{Timeout: 15 * time.Second}}, nil
}

func (c *Client) run(ctx context.Context, args []string, timeout time.Duration, limit int) (proc.Result, error) {
	if c.Binary == "" {
		return proc.Result{}, errors.New("roborev is not installed")
	}
	result, err := c.Runner.Run(ctx, proc.Command{Executable: c.Binary, Args: args, Timeout: timeout, OutputLimit: limit})
	if err != nil {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = err.Error()
		}
		return result, errors.New(message)
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
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	minor := 0
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	v64 := len(parts) > 0 && parts[0] == "0" && minor >= 64
	fixV64 := len(parts) > 1 && parts[0] == "0" && minor == 64
	return Capabilities{ReviewEnqueueAPI: v64, FrozenRerunAPI: v64, JobListAPI: v64, JobOutputStatusAPI: fixV64, PanelReview: v64, FixCommand: fixV64, PanelMembersAPI: v64, PromptLazyLoad: true, RawLog: true}
}

func (c *Client) Installation(ctx context.Context) Installation {
	if c.Binary == "" {
		return Installation{Installed: false, Error: "roborev executable not found", Capabilities: Capabilities{}}
	}
	versionValue, err := c.detectVersion(ctx)
	if err != nil {
		return Installation{Installed: false, Binary: c.Binary, Error: err.Error()}
	}
	installation := Installation{Installed: true, Binary: c.Binary, Version: versionValue, Capabilities: capabilities(versionValue), Status: json.RawMessage(`{"running":false,"known":false}`)}
	if installation.Capabilities.ReviewEnqueueAPI {
		if health, healthErr := c.health(ctx); healthErr == nil {
			status, _ := json.Marshal(map[string]any{"running": true, "known": true, "health": health})
			installation.Status = status
		} else {
			installation.Status = json.RawMessage(`{"running":false,"known":true}`)
			installation.Error = "RoboRev daemon health check failed: " + healthErr.Error()
		}
	}
	return installation
}

func (c *Client) detectVersion(ctx context.Context) (string, error) {
	c.versionMu.RLock()
	value := c.version
	detectedAt := c.versionAt
	detectionErr := c.versionErr
	errorAt := c.versionErrAt
	c.versionMu.RUnlock()
	if value != "" && time.Since(detectedAt) < 30*time.Second {
		return value, nil
	}
	if detectionErr != nil && time.Since(errorAt) < 5*time.Second {
		return "", detectionErr
	}
	c.versionRefreshMu.Lock()
	defer c.versionRefreshMu.Unlock()
	c.versionMu.RLock()
	value = c.version
	detectedAt = c.versionAt
	detectionErr = c.versionErr
	errorAt = c.versionErrAt
	c.versionMu.RUnlock()
	if value != "" && time.Since(detectedAt) < 30*time.Second {
		return value, nil
	}
	if detectionErr != nil && time.Since(errorAt) < 5*time.Second {
		return "", detectionErr
	}
	result, err := c.run(ctx, []string{"version", "--json"}, 5*time.Second, 64<<10)
	if err != nil {
		c.cacheVersionError(ctx, err)
		return "", err
	}
	var version struct {
		Version string `json:"version"`
	}
	if json.Unmarshal([]byte(result.Stdout), &version) != nil || version.Version == "" {
		err = errors.New("invalid roborev version response")
		c.cacheVersionError(ctx, err)
		return "", err
	}
	c.versionMu.Lock()
	c.version = version.Version
	c.versionAt = time.Now()
	c.versionErr = nil
	c.versionErrAt = time.Time{}
	c.versionMu.Unlock()
	return version.Version, nil
}

func (c *Client) cacheVersionError(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	c.versionMu.Lock()
	c.versionErr = err
	c.versionErrAt = time.Now()
	c.versionMu.Unlock()
}

func (c *Client) health(ctx context.Context) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.DaemonURL+"/api/health", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("health endpoint returned %d", resp.StatusCode)
	}
	return decodeRaw(string(data))
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
	version, err := c.detectVersion(ctx)
	if err != nil {
		return JobMetadata{}, err
	}
	if !capabilities(version).JobListAPI {
		return JobMetadata{}, fmt.Errorf("RoboRev %s does not support metadata-only job lookup", version)
	}
	query := url.Values{}
	query.Set("id", strconv.FormatInt(id, 10))
	query.Set("limit", "1")
	query.Set("omit_prompt", "true")
	raw, err := c.getDaemonJSON(ctx, "/api/jobs?"+query.Encode(), 64<<20)
	if err != nil {
		return JobMetadata{}, err
	}
	var response struct {
		Jobs []JobMetadata `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return JobMetadata{}, errors.New("invalid job metadata response")
	}
	if len(response.Jobs) != 1 || response.Jobs[0].ID != id {
		return JobMetadata{}, errors.New("job not found")
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.DaemonURL+"/api/job/output?"+query.Encode(), nil)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.DaemonURL+path, nil)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.DaemonURL+path, nil)
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

func (c *Client) Prompt(ctx context.Context, id int64) (string, error) {
	args, err := PromptArgs(id)
	if err != nil {
		return "", err
	}
	result, err := c.run(ctx, args, 10*time.Second, 8<<20)
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func (c *Client) Log(ctx context.Context, id int64) (string, error) {
	args, err := LogArgs(id)
	if err != nil {
		return "", err
	}
	result, err := c.run(ctx, args, 15*time.Second, 8<<20)
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func (c *Client) postJSON(ctx context.Context, path string, body any) (json.RawMessage, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.DaemonURL+path, bytes.NewReader(payload))
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
	result, runErr := c.Runner.Run(ctx, proc.Command{
		Executable:  c.Binary,
		Args:        args,
		Dir:         request.RepoPath,
		Timeout:     45 * time.Minute,
		OutputLimit: 8 << 20,
	})
	if runErr != nil {
		_ = result
		return errors.New("RoboRev fix command failed")
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
	if c.Binary == "" {
		return errors.New("roborev is not installed")
	}
	result, err := c.Runner.Run(ctx, proc.Command{Executable: c.Binary, Args: []string{"daemon", "start"}, Timeout: 30 * time.Second, OutputLimit: 1 << 20, AllowDetachedChildren: true})
	if err != nil {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return err
}
