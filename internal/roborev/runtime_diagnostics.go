package roborev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	runtimeDiagnosticsResponseLimit int64 = 256 << 10
	runtimeDiagnosticsUptimeLimit         = 1024
)

var (
	ErrUnsupportedRuntimeDiagnostics = errors.New("RoboRev runtime diagnostics are unsupported")
	ErrRuntimeDiagnosticsOffline     = errors.New("RoboRev daemon is offline")
	ErrMalformedRuntimeDiagnostics   = errors.New("malformed RoboRev runtime diagnostics response")
)

// RuntimeDiagnostics reads only the already-running daemon's machine-facing
// health and status endpoints. It intentionally never invokes `status` (whose
// v0.64 CLI path can start a daemon and fetch recent jobs).
func (c *Client) RuntimeDiagnostics(ctx context.Context) (RuntimeDiagnostics, error) {
	if c == nil {
		return RuntimeDiagnostics{}, fmt.Errorf("%w: client is unavailable", ErrUnsupportedRuntimeDiagnostics)
	}
	_, daemonURL, generation := c.snapshot()
	if daemonURL == "" {
		return RuntimeDiagnostics{}, fmt.Errorf("%w: daemon binding is unavailable", ErrRuntimeDiagnosticsOffline)
	}

	cliVersion, err := c.detectVersion(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return RuntimeDiagnostics{}, ctxErr
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return RuntimeDiagnostics{}, context.DeadlineExceeded
		}
		if errors.Is(err, ErrBindingChanged) {
			return RuntimeDiagnostics{}, ErrBindingChanged
		}
		return RuntimeDiagnostics{}, fmt.Errorf("%w: CLI version could not be verified", ErrUnsupportedRuntimeDiagnostics)
	}
	if !roborevcompat.IsSupportedVersion(cliVersion) {
		return RuntimeDiagnostics{}, fmt.Errorf("%w: CLI version is not v0.64.0", ErrUnsupportedRuntimeDiagnostics)
	}
	if !c.generationCurrent(generation) {
		return RuntimeDiagnostics{}, ErrBindingChanged
	}

	healthRaw, err := c.runtimeDiagnosticsJSON(ctx, daemonURL, "/api/health", generation)
	if err != nil {
		return RuntimeDiagnostics{}, err
	}
	health, err := decodeRuntimeDiagnosticsHealth(healthRaw)
	if err != nil {
		return RuntimeDiagnostics{}, err
	}
	if health.version != roborevcompat.Manifest064().Version || health.version != cliVersion {
		return RuntimeDiagnostics{}, fmt.Errorf("%w: daemon version does not match CLI", ErrUnsupportedRuntimeDiagnostics)
	}

	statusRaw, err := c.runtimeDiagnosticsJSON(ctx, daemonURL, "/api/status", generation)
	if err != nil {
		return RuntimeDiagnostics{}, err
	}
	status, err := decodeRuntimeDiagnosticsStatus(statusRaw)
	if err != nil {
		return RuntimeDiagnostics{}, err
	}
	if status.version != roborevcompat.Manifest064().Version || status.version != cliVersion {
		return RuntimeDiagnostics{}, fmt.Errorf("%w: status version does not match CLI", ErrUnsupportedRuntimeDiagnostics)
	}
	if !c.generationCurrent(generation) {
		return RuntimeDiagnostics{}, ErrBindingChanged
	}

	components := make([]RuntimeDiagnosticsComponent, len(health.components))
	for i, component := range health.components {
		components[i] = RuntimeDiagnosticsComponent{Name: component.name, Healthy: component.healthy}
	}
	return RuntimeDiagnostics{
		Daemon:  RuntimeDiagnosticsDaemon{Running: true, Version: cliVersion, Uptime: health.uptime},
		Workers: RuntimeDiagnosticsWorkers{Active: status.activeWorkers, Max: status.maxWorkers},
		Queue:   RuntimeDiagnosticsQueue{Paused: status.queuePaused, Queued: status.queuedJobs, Running: status.runningJobs},
		Jobs:    RuntimeDiagnosticsJobs{Completed: status.completedJobs, Failed: status.failedJobs, Skipped: status.skippedJobs},
		Health:  RuntimeDiagnosticsHealth{Healthy: health.healthy, ErrorCount24h: health.errorCount24h, Components: components},
	}, nil
}

var _ RuntimeDiagnosticsService = (*Client)(nil)

// runtimeDiagnosticsJSON is intentionally separate from getDaemonJSON: this
// read surface must check media type, UTF-8, body bounds, and an object root,
// while never including arbitrary daemon response bodies in returned errors.
func (c *Client) runtimeDiagnosticsJSON(ctx context.Context, daemonURL, path string, generation uint64) (json.RawMessage, error) {
	if !c.generationCurrent(generation) {
		return nil, ErrBindingChanged
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: request construction failed", ErrMalformedRuntimeDiagnostics)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	client := noRedirectHTTPClient(c.HTTP)
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		if isUnreachableHealthError(err) || isConnectionRefused(err) {
			return nil, fmt.Errorf("%w: daemon request failed", ErrRuntimeDiagnosticsOffline)
		}
		return nil, fmt.Errorf("%w: daemon request failed", ErrMalformedRuntimeDiagnostics)
	}
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("%w: empty daemon response", ErrMalformedRuntimeDiagnostics)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: daemon returned a non-success status", ErrMalformedRuntimeDiagnostics)
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, runtimeDiagnosticsResponseLimit+1))
	if readErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(readErr, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("%w: daemon response could not be read", ErrMalformedRuntimeDiagnostics)
	}
	if int64(len(data)) > runtimeDiagnosticsResponseLimit {
		return nil, fmt.Errorf("%w: daemon response exceeded bounded output", ErrMalformedRuntimeDiagnostics)
	}
	mediaType, _, parseErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if parseErr != nil || !strings.EqualFold(mediaType, "application/json") {
		return nil, fmt.Errorf("%w: daemon response has an invalid media type", ErrMalformedRuntimeDiagnostics)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: daemon response is not valid UTF-8", ErrMalformedRuntimeDiagnostics)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var root json.RawMessage
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("%w: daemon response is malformed JSON", ErrMalformedRuntimeDiagnostics)
	}
	if trimmed := bytes.TrimSpace(root); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%w: daemon response must be one JSON object", ErrMalformedRuntimeDiagnostics)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("%w: daemon response has trailing JSON", ErrMalformedRuntimeDiagnostics)
	}
	if !c.generationCurrent(generation) {
		return nil, ErrBindingChanged
	}
	return root, nil
}

func isConnectionRefused(err error) bool {
	for _, errno := range []error{syscall.ECONNREFUSED, syscall.Errno(10061)} {
		if errors.Is(err, errno) {
			return true
		}
	}
	return false
}

type runtimeDiagnosticsStatusPayload struct {
	Schema              *string         `json:"$schema"`
	Version             *string         `json:"version"`
	QueuedJobs          *int64          `json:"queued_jobs"`
	RunningJobs         *int64          `json:"running_jobs"`
	CompletedJobs       *int64          `json:"completed_jobs"`
	FailedJobs          *int64          `json:"failed_jobs"`
	CanceledJobs        *int64          `json:"canceled_jobs"`
	AppliedJobs         *int64          `json:"applied_jobs"`
	RebasedJobs         *int64          `json:"rebased_jobs"`
	SkippedJobs         *int64          `json:"skipped_jobs"`
	ActiveWorkers       *int64          `json:"active_workers"`
	MaxWorkers          *int64          `json:"max_workers"`
	QueuePaused         *bool           `json:"queue_paused"`
	Network             *string         `json:"network"`
	Address             *string         `json:"address"`
	Port                *int64          `json:"port"`
	MachineID           *string         `json:"machine_id"`
	ConfigReloadedAt    *string         `json:"config_reloaded_at"`
	ConfigReloadCounter *int64          `json:"config_reload_counter"`
	AutoDesign          json.RawMessage `json:"auto_design"`
}

type runtimeDiagnosticsStatus struct {
	version, network, address, machineID, configReloadedAt string
	queuedJobs, runningJobs, completedJobs, failedJobs     int64
	canceledJobs, appliedJobs, rebasedJobs, skippedJobs    int64
	activeWorkers, maxWorkers                              int64
	queuePaused                                            bool
}

func decodeRuntimeDiagnosticsStatus(raw json.RawMessage) (runtimeDiagnosticsStatus, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload runtimeDiagnosticsStatusPayload
	if err := decoder.Decode(&payload); err != nil {
		return runtimeDiagnosticsStatus{}, malformedRuntimeDiagnostics("status")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return runtimeDiagnosticsStatus{}, malformedRuntimeDiagnostics("status")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return runtimeDiagnosticsStatus{}, malformedRuntimeDiagnostics("status")
	}
	for _, field := range []string{"$schema", "network", "address", "port", "machine_id", "config_reloaded_at", "config_reload_counter"} {
		if value, present := fields[field]; present && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return runtimeDiagnosticsStatus{}, malformedRuntimeDiagnostics("status")
		}
	}
	if payload.Version == nil || payload.QueuedJobs == nil || payload.RunningJobs == nil || payload.CompletedJobs == nil || payload.FailedJobs == nil || payload.CanceledJobs == nil || payload.AppliedJobs == nil || payload.RebasedJobs == nil || payload.SkippedJobs == nil || payload.ActiveWorkers == nil || payload.MaxWorkers == nil || payload.QueuePaused == nil {
		return runtimeDiagnosticsStatus{}, malformedRuntimeDiagnostics("status")
	}
	values := []*int64{payload.QueuedJobs, payload.RunningJobs, payload.CompletedJobs, payload.FailedJobs, payload.CanceledJobs, payload.AppliedJobs, payload.RebasedJobs, payload.SkippedJobs, payload.ActiveWorkers, payload.MaxWorkers}
	for _, value := range values {
		if *value < 0 {
			return runtimeDiagnosticsStatus{}, malformedRuntimeDiagnostics("status")
		}
	}
	if *payload.MaxWorkers <= 0 || *payload.ActiveWorkers > *payload.MaxWorkers {
		return runtimeDiagnosticsStatus{}, malformedRuntimeDiagnostics("status")
	}
	if payload.Port != nil && *payload.Port < 0 || payload.ConfigReloadCounter != nil && *payload.ConfigReloadCounter < 0 {
		return runtimeDiagnosticsStatus{}, malformedRuntimeDiagnostics("status")
	}
	if err := validateRuntimeDiagnosticsAutoDesign(payload.AutoDesign); err != nil {
		return runtimeDiagnosticsStatus{}, malformedRuntimeDiagnostics("status")
	}
	return runtimeDiagnosticsStatus{
		version: *payload.Version, network: optionalString(payload.Network), address: optionalString(payload.Address), machineID: optionalString(payload.MachineID), configReloadedAt: optionalString(payload.ConfigReloadedAt),
		queuedJobs: *payload.QueuedJobs, runningJobs: *payload.RunningJobs, completedJobs: *payload.CompletedJobs, failedJobs: *payload.FailedJobs, canceledJobs: *payload.CanceledJobs, appliedJobs: *payload.AppliedJobs, rebasedJobs: *payload.RebasedJobs, skippedJobs: *payload.SkippedJobs,
		activeWorkers: *payload.ActiveWorkers, maxWorkers: *payload.MaxWorkers, queuePaused: *payload.QueuePaused,
	}, nil
}

// v0.64's auto_design is either omitted/null (when the feature is disabled)
// or a complete typed status object. It is validated here but deliberately
// never copied into the Dashboard DTO because it is not part of diagnostics
// v1's browser surface.
func validateRuntimeDiagnosticsAutoDesign(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] != '{' {
		return errors.New("auto_design must be an object or null")
	}
	type autoDesignPayload struct {
		Enabled             *bool  `json:"enabled"`
		TriggeredHeuristic  *int64 `json:"triggered_heuristic"`
		SkippedHeuristic    *int64 `json:"skipped_heuristic"`
		TriggeredClassifier *int64 `json:"triggered_classifier"`
		SkippedClassifier   *int64 `json:"skipped_classifier"`
		ClassifierFailed    *int64 `json:"classifier_failed"`
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var payload autoDesignPayload
	if err := decoder.Decode(&payload); err != nil {
		return errors.New("auto_design object is malformed")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("auto_design object has trailing JSON")
	}
	if payload.Enabled == nil || payload.TriggeredHeuristic == nil || payload.SkippedHeuristic == nil || payload.TriggeredClassifier == nil || payload.SkippedClassifier == nil || payload.ClassifierFailed == nil {
		return errors.New("auto_design object is incomplete")
	}
	for _, counter := range []*int64{payload.TriggeredHeuristic, payload.SkippedHeuristic, payload.TriggeredClassifier, payload.SkippedClassifier, payload.ClassifierFailed} {
		if *counter < 0 {
			return errors.New("auto_design counters must be nonnegative")
		}
	}
	return nil
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type runtimeDiagnosticsHealthPayload struct {
	Schema        *string                              `json:"$schema"`
	Healthy       *bool                                `json:"healthy"`
	Uptime        *string                              `json:"uptime"`
	Version       *string                              `json:"version"`
	Components    *[]runtimeDiagnosticsHealthComponent `json:"components"`
	RecentErrors  json.RawMessage                      `json:"recent_errors"`
	ErrorCount24h *int64                               `json:"error_count_24h"`
}

type runtimeDiagnosticsHealthComponent struct {
	Name           *string `json:"name"`
	Healthy        *bool   `json:"healthy"`
	Message        *string `json:"message"`
	messagePresent bool
}

func (c *runtimeDiagnosticsHealthComponent) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return errors.New("health component must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	type plain runtimeDiagnosticsHealthComponent
	var decoded plain
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	decoded.messagePresent = false
	if _, ok := fields["message"]; ok {
		decoded.messagePresent = true
	}
	*c = runtimeDiagnosticsHealthComponent(decoded)
	return nil
}

type runtimeDiagnosticsHealth struct {
	healthy       bool
	uptime        string
	version       string
	errorCount24h int64
	components    []runtimeDiagnosticsHealthComponentValue
}

type runtimeDiagnosticsHealthComponentValue struct {
	name    string
	healthy bool
}

func decodeRuntimeDiagnosticsHealth(raw json.RawMessage) (runtimeDiagnosticsHealth, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload runtimeDiagnosticsHealthPayload
	if err := decoder.Decode(&payload); err != nil {
		return runtimeDiagnosticsHealth{}, malformedRuntimeDiagnostics("health")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return runtimeDiagnosticsHealth{}, malformedRuntimeDiagnostics("health")
	}
	if payload.Healthy == nil || payload.Uptime == nil || strings.TrimSpace(*payload.Uptime) == "" || len(*payload.Uptime) > runtimeDiagnosticsUptimeLimit || payload.Version == nil || payload.Components == nil || payload.ErrorCount24h == nil || len(payload.RecentErrors) == 0 {
		return runtimeDiagnosticsHealth{}, malformedRuntimeDiagnostics("health")
	}
	if payload.Schema == nil {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return runtimeDiagnosticsHealth{}, malformedRuntimeDiagnostics("health")
		}
		if _, present := fields["$schema"]; present {
			return runtimeDiagnosticsHealth{}, malformedRuntimeDiagnostics("health")
		}
	}
	if *payload.ErrorCount24h < 0 {
		return runtimeDiagnosticsHealth{}, malformedRuntimeDiagnostics("health")
	}
	if err := validateRuntimeDiagnosticsRecentErrors(payload.RecentErrors); err != nil {
		return runtimeDiagnosticsHealth{}, malformedRuntimeDiagnostics("health")
	}
	seen := make(map[string]bool, len(*payload.Components))
	components := make([]runtimeDiagnosticsHealthComponentValue, len(*payload.Components))
	anyUnhealthy := false
	for i, component := range *payload.Components {
		if component.Name == nil || strings.TrimSpace(*component.Name) == "" || component.Healthy == nil || seen[*component.Name] {
			return runtimeDiagnosticsHealth{}, malformedRuntimeDiagnostics("health")
		}
		if *component.Name != "database" && *component.Name != "workers" && *component.Name != "sync" {
			return runtimeDiagnosticsHealth{}, malformedRuntimeDiagnostics("health")
		}
		if component.messagePresent && component.Message == nil {
			return runtimeDiagnosticsHealth{}, malformedRuntimeDiagnostics("health")
		}
		seen[*component.Name] = true
		components[i] = runtimeDiagnosticsHealthComponentValue{name: *component.Name, healthy: *component.Healthy}
		anyUnhealthy = anyUnhealthy || !*component.Healthy
	}
	if !seen["database"] || !seen["workers"] || (*payload.Healthy && anyUnhealthy) {
		return runtimeDiagnosticsHealth{}, malformedRuntimeDiagnostics("health")
	}
	return runtimeDiagnosticsHealth{healthy: *payload.Healthy, uptime: *payload.Uptime, version: *payload.Version, errorCount24h: *payload.ErrorCount24h, components: components}, nil
}

func validateRuntimeDiagnosticsRecentErrors(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return errors.New("recent_errors must be null or an array")
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return err
	}
	for _, entry := range entries {
		decoder := json.NewDecoder(bytes.NewReader(entry))
		decoder.DisallowUnknownFields()
		var value struct {
			Timestamp *time.Time `json:"ts"`
			Level     *string    `json:"level"`
			Component *string    `json:"component"`
			Message   *string    `json:"message"`
			JobID     *int64     `json:"job_id"`
		}
		if err := decoder.Decode(&value); err != nil || value.Timestamp == nil || value.Timestamp.IsZero() || value.Level == nil || value.Component == nil || value.Message == nil {
			return errors.New("recent_errors entry is malformed")
		}
		var trailing json.RawMessage
		if err := decoder.Decode(&trailing); err != io.EOF {
			return errors.New("recent_errors entry has trailing JSON")
		}
	}
	return nil
}

func malformedRuntimeDiagnostics(phase string) error {
	return fmt.Errorf("%w: %s contract invalid", ErrMalformedRuntimeDiagnostics, phase)
}
