package roborev

// This file contains the small, typed transport surface used by the
// dashboard's runtime-control manager.  Runtime control intentionally talks to
// the daemon API for health and queue state; it never falls back to the
// human-oriented CLI status/pause commands or opens RoboRev's database.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

const runtimeControlResponseLimit int64 = 256 << 10

var (
	// ErrDaemonOffline is returned by queue operations when the daemon cannot
	// serve its status endpoint.  A caller may use this to disable queue
	// controls without treating the condition as an adapter contract error.
	ErrDaemonOffline      = errors.New("RoboRev daemon is offline")
	ErrDaemonUnhealthy    = errors.New("RoboRev daemon health is unhealthy")
	ErrQueueStateMismatch = errors.New("RoboRev daemon returned a mismatched queue state")
)

// QueueState is the only queue state the dashboard needs from the daemon's
// much larger /api/status payload.
type QueueState struct {
	Known  bool `json:"known"`
	Paused bool `json:"paused"`
}

// DaemonArgs builds the exact v0.64 lifecycle argument array.  Keeping this
// as a whitelist prevents user input from becoming a subprocess argument.
func DaemonArgs(action string) ([]string, error) {
	switch action {
	case "start", "stop", "restart":
		return []string{"daemon", action}, nil
	default:
		return nil, fmt.Errorf("unsupported daemon action %q", action)
	}
}

func StartDaemonArgs() []string   { return []string{"daemon", "start"} }
func StopDaemonArgs() []string    { return []string{"daemon", "stop"} }
func RestartDaemonArgs() []string { return []string{"daemon", "restart"} }

// DaemonHealth checks the daemon's machine-facing health endpoint.  The bool
// is deliberately separate from the error: a failed health request means the
// daemon is not currently reachable, which is a valid offline state for
// lifecycle operations rather than a malformed response.
func (c *Client) DaemonHealth(ctx context.Context) (bool, error) {
	if c == nil {
		return false, errors.New("RoboRev client is unavailable")
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		if isUnreachableHealthError(err) {
			return false, nil
		}
		return true, err
	}
	if !health.Healthy {
		return true, ErrDaemonUnhealthy
	}
	return true, nil
}

// IsDaemonRunning is a convenience alias for callers that only need a
// machine-readable liveness bit.
func (c *Client) IsDaemonRunning(ctx context.Context) (bool, error) {
	return c.DaemonHealth(ctx)
}

func (c *Client) CheckDaemonHealth(ctx context.Context) (bool, error) {
	return c.DaemonHealth(ctx)
}

type daemonHealthResponse struct {
	// Schema is emitted by RoboRev v0.64.0's generated OpenAPI response. The
	// pointer plus schemaPresent bit distinguish an omitted field from a
	// present value; the custom decoder rejects present null/non-string values.
	Schema        *daemonHealthSchema     `json:"$schema"`
	Healthy       bool                    `json:"healthy"`
	Uptime        string                  `json:"uptime"`
	Version       string                  `json:"version"`
	Components    []daemonHealthComponent `json:"components"`
	RecentErrors  []daemonHealthError     `json:"recent_errors"`
	ErrorCount    int                     `json:"error_count_24h"`
	schemaPresent bool
}

// daemonHealthSchema is deliberately a string wrapper so a present schema
// must be a JSON string. daemonHealthResponse's custom decoder records field
// presence separately so an omitted schema remains valid while null is not.
type daemonHealthSchema string

func (s *daemonHealthSchema) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New(`RoboRev daemon health "$schema" must be a string`)
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New(`RoboRev daemon health "$schema" must be a string`)
	}
	*s = daemonHealthSchema(value)
	return nil
}

func (h *daemonHealthResponse) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return errors.New("RoboRev daemon health must be a JSON object")
	}
	rawHealthy, ok := fields["healthy"]
	if !ok || bytes.Equal(bytes.TrimSpace(rawHealthy), []byte("null")) {
		return errors.New(`RoboRev daemon health "healthy" must be a boolean`)
	}
	var healthy bool
	if err := json.Unmarshal(rawHealthy, &healthy); err != nil {
		return errors.New(`RoboRev daemon health "healthy" must be a boolean`)
	}
	type plain daemonHealthResponse
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	_, decoded.schemaPresent = fields["$schema"]
	*h = daemonHealthResponse(decoded)
	return nil
}

type daemonHealthComponent struct {
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
	Message string `json:"message,omitempty"`
}

type daemonHealthError struct {
	Timestamp time.Time `json:"ts"`
	Level     string    `json:"level"`
	Component string    `json:"component"`
	Message   string    `json:"message"`
	JobID     int64     `json:"job_id,omitempty"`
}

func (c *Client) healthStatus(ctx context.Context) (daemonHealthResponse, error) {
	_, daemonURL, _ := c.snapshot()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL+"/api/health", nil)
	if err != nil {
		return daemonHealthResponse{}, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return daemonHealthResponse{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, runtimeControlResponseLimit+1))
	if err != nil {
		return daemonHealthResponse{}, err
	}
	if int64(len(data)) > runtimeControlResponseLimit {
		return daemonHealthResponse{}, errors.New("RoboRev health response exceeded bounded output")
	}
	if resp.StatusCode != http.StatusOK {
		return daemonHealthResponse{}, fmt.Errorf("RoboRev daemon health returned %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var health daemonHealthResponse
	if err := decoder.Decode(&health); err != nil {
		return daemonHealthResponse{}, fmt.Errorf("malformed RoboRev daemon health: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return daemonHealthResponse{}, errors.New("malformed RoboRev daemon health: trailing JSON")
		}
		return daemonHealthResponse{}, fmt.Errorf("malformed RoboRev daemon health: %w", err)
	}
	if health.schemaPresent && health.Schema == nil {
		return daemonHealthResponse{}, errors.New(`malformed RoboRev daemon health: "$schema" must be a string`)
	}
	return health, nil
}

func isUnreachableHealthError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Do not classify every net.Error (especially timeout errors) as proof
	// that the daemon is offline.  The HTTP client wraps transport failures in
	// url.Error/net.OpError/os.SyscallError; errors.Is follows those wrappers
	// and lets us accept only concrete connection-loss errno values.
	for _, errno := range []error{
		syscall.ECONNREFUSED,
		syscall.ECONNRESET,
		syscall.ECONNABORTED,
		syscall.EHOSTUNREACH,
		syscall.ENETUNREACH,
		// Windows Winsock errno values are not exposed through the portable
		// syscall.E* aliases above. Keep the transport classification
		// platform-neutral by matching the documented WSA values directly.
		syscall.Errno(10061), // WSAECONNREFUSED
		syscall.Errno(10054), // WSAECONNRESET
		syscall.Errno(10053), // WSAECONNABORTED
		syscall.Errno(10065), // WSAEHOSTUNREACH
		syscall.Errno(10051), // WSAENETUNREACH
	} {
		if errors.Is(err, errno) {
			return true
		}
	}
	return false
}

// QueueStatus reads GET /api/status with a bounded body and extracts only the
// exact queue_paused boolean field.  The status endpoint itself proves that a
// daemon is online, so this method does not issue a second health request.
func (c *Client) QueueStatus(ctx context.Context) (QueueState, error) {
	if c == nil {
		return QueueState{}, errors.New("RoboRev client is unavailable")
	}
	raw, err := c.getDaemonJSONBounded(ctx, "/api/status", runtimeControlResponseLimit)
	if err != nil {
		if isUnreachableHealthError(err) {
			return QueueState{}, fmt.Errorf("%w: %v", ErrDaemonOffline, err)
		}
		return QueueState{}, err
	}
	paused, err := decodeQueuePaused(raw)
	if err != nil {
		return QueueState{}, err
	}
	return QueueState{Known: true, Paused: paused}, nil
}

// GetQueueStatus is retained as a descriptive alias for QueueStatus.
func (c *Client) GetQueueStatus(ctx context.Context) (QueueState, error) {
	return c.QueueStatus(ctx)
}

// QueuePaused returns only the queue pause bit for small callers.
func (c *Client) QueuePaused(ctx context.Context) (bool, error) {
	state, err := c.QueueStatus(ctx)
	return state.Paused, err
}

// SetQueuePaused performs the exact v0.64 queue operation.  The status read
// before POST is intentional: queue controls are disabled while the daemon is
// offline, and the POST response is verified rather than assumed successful.
// The returned state is the authoritative state from that POST response; a
// caller must not perform a second GET that could race with this mutation.
func (c *Client) SetQueuePaused(ctx context.Context, paused bool) (QueueState, error) {
	if c == nil {
		return QueueState{}, errors.New("RoboRev client is unavailable")
	}
	if _, err := c.QueueStatus(ctx); err != nil {
		return QueueState{}, err
	}
	path := "/api/queue/unpause"
	if paused {
		path = "/api/queue/pause"
	}
	raw, err := c.postEmptyJSON(ctx, path)
	if err != nil {
		return QueueState{}, err
	}
	actual, err := decodeQueuePaused(raw)
	if err != nil {
		return QueueState{}, err
	}
	state := QueueState{Known: true, Paused: actual}
	if actual != paused {
		return state, fmt.Errorf("%w: requested %t, daemon returned %t", ErrQueueStateMismatch, paused, actual)
	}
	return state, nil
}

func (c *Client) PauseQueue(ctx context.Context) (QueueState, error) {
	return c.SetQueuePaused(ctx, true)
}

func (c *Client) ResumeQueue(ctx context.Context) (QueueState, error) {
	return c.SetQueuePaused(ctx, false)
}

func decodeQueuePaused(raw json.RawMessage) (bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false, fmt.Errorf("malformed queue state response: %w", err)
	}
	value, ok := fields["queue_paused"]
	if !ok {
		return false, errors.New("malformed queue state response: queue_paused is required")
	}
	var paused bool
	if err := json.Unmarshal(value, &paused); err != nil {
		return false, errors.New("malformed queue state response: queue_paused must be boolean")
	}
	return paused, nil
}

func (c *Client) postEmptyJSON(ctx context.Context, path string) (json.RawMessage, error) {
	_, daemonURL, _ := c.snapshot()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, daemonURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RoboRev daemon request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, runtimeControlResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > runtimeControlResponseLimit {
		return nil, errors.New("RoboRev queue response exceeded bounded output")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RoboRev daemon returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("RoboRev queue response was empty")
	}
	if !json.Valid(data) {
		return nil, errors.New("RoboRev queue response was malformed JSON")
	}
	return json.RawMessage(data), nil
}

// StopDaemon and RestartDaemon complement the existing StartDaemon service
// method. All are typed lifecycle operations; none uses a shell or accepts
// arbitrary command text.
func (c *Client) StopDaemon(ctx context.Context) error {
	return c.runDaemonAction(ctx, "stop")
}

func (c *Client) RestartDaemon(ctx context.Context) error {
	return c.runDaemonAction(ctx, "restart")
}

// Daemon runs one of the three whitelisted lifecycle operations.  It is
// useful to adapters that model one operation method rather than three.
func (c *Client) Daemon(ctx context.Context, action string) error {
	return c.runDaemonAction(ctx, action)
}

func (c *Client) runDaemonAction(ctx context.Context, action string) error {
	args, err := DaemonArgs(action)
	if err != nil {
		return err
	}
	if c == nil {
		return errors.New("RoboRev client is unavailable")
	}
	binary, _, _ := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return errors.New("RoboRev is not installed")
	}
	allowDetached := action == "start" || action == "restart"
	result, err := c.Runner.Run(ctx, proc.Command{
		Executable:            binary,
		Args:                  args,
		Timeout:               30 * time.Second,
		OutputLimit:           1 << 20,
		AllowDetachedChildren: allowDetached,
	})
	if err != nil {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			return err
		}
		return fmt.Errorf("%s: %w", message, err)
	}
	return nil
}
