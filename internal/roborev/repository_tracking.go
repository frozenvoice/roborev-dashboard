package roborev

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	repositoryTrackingRegisterTimeout = 5 * time.Second
	repositoryTrackingResponseLimit   = 64 << 10
)

var ErrRepositoryTrackingUnavailable = errors.New("RoboRev repository tracking is unavailable")

const repositoryTrackingDaemonDomain = "roborev-dashboard/repository-tracking/daemon/v2\x00"

func repositoryTrackingDaemonID(daemonURL string) string {
	digest := sha256.Sum256([]byte(repositoryTrackingDaemonDomain + daemonURL))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// ProbeRepositoryTrackingBinding performs the uncached executable/file
// binding probe used by tracking preview and execute. Endpoint and binary
// details remain in Client and never cross the app's browser DTO boundary.
func (c *Client) ProbeRepositoryTrackingBinding(ctx context.Context) (RepositoryTrackingBinding, error) {
	if c == nil || c.Runner == nil {
		return RepositoryTrackingBinding{}, ErrRepositoryTrackingUnavailable
	}
	binding, err := c.ProbeGitHookBinding(ctx)
	if err != nil || binding.Version != "v0.64.0" {
		return RepositoryTrackingBinding{}, ErrRepositoryTrackingUnavailable
	}
	_, daemonURL, generation := c.snapshot()
	if generation != binding.Generation || strings.TrimSpace(daemonURL) == "" {
		return RepositoryTrackingBinding{}, ErrBindingChanged
	}
	return RepositoryTrackingBinding{Version: binding.Version, Generation: binding.Generation, FileID: binding.FileID, DaemonID: repositoryTrackingDaemonID(daemonURL)}, nil
}

// RepositoryTrackingPreflight verifies the exact v0.64 executable, the
// already-running healthy daemon, and one complete strict inventory read.
// It intentionally does not start, stop, discover, or otherwise manage the
// daemon lifecycle.
func (c *Client) RepositoryTrackingPreflight(ctx context.Context) (RepositoryTrackingPreflight, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	binding, err := c.ProbeRepositoryTrackingBinding(ctx)
	if err != nil {
		return RepositoryTrackingPreflight{}, ErrRepositoryTrackingUnavailable
	}
	health, err := c.healthStatus(ctx)
	if err != nil || !health.Healthy || !roborevcompat.IsMatchingSupportedVersion(binding.Version, health.Version) {
		return RepositoryTrackingPreflight{}, ErrRepositoryTrackingUnavailable
	}
	if !c.generationCurrent(binding.Generation) {
		return RepositoryTrackingPreflight{}, ErrBindingChanged
	}
	inventory, err := c.repositoryTrackingInventoryRead(ctx)
	if err != nil {
		return RepositoryTrackingPreflight{}, ErrRepositoryTrackingUnavailable
	}
	if !c.generationCurrent(binding.Generation) {
		return RepositoryTrackingPreflight{}, ErrBindingChanged
	}
	return RepositoryTrackingPreflight{Binding: binding, Inventory: inventory}, nil
}

// RegisterRepository makes one exact v0.64 daemon HTTP mutation. The caller
// performs its final root guard immediately before entering this method. No
// subprocess, retry, endpoint discovery, or redirect is permitted here.
func (c *Client) RegisterRepository(ctx context.Context, root string, binding RepositoryTrackingBinding, guard RepositoryTrackingPreLaunchGuard) (RepositoryTrackingResult, error) {
	if c == nil || c.HTTP == nil || strings.TrimSpace(root) == "" || !filepath.IsAbs(root) || !binding.Valid() {
		return RepositoryTrackingResult{}, ErrRepositoryTrackingUnavailable
	}
	if !c.generationCurrent(binding.Generation) {
		return RepositoryTrackingResult{}, ErrBindingChanged
	}
	_, daemonURL, generation := c.snapshot()
	if generation != binding.Generation || strings.TrimSpace(daemonURL) == "" || binding.DaemonID == "" || repositoryTrackingDaemonID(daemonURL) != binding.DaemonID {
		return RepositoryTrackingResult{}, ErrBindingChanged
	}
	payload, err := json.Marshal(struct {
		RepoPath string `json:"repo_path"`
	}{RepoPath: filepath.Clean(root)})
	if err != nil {
		return RepositoryTrackingResult{}, ErrRepositoryTrackingUnavailable
	}
	requestCtx := ctx
	if requestCtx == nil {
		requestCtx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(requestCtx, repositoryTrackingRegisterTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, daemonURL+"/api/repos/register", bytes.NewReader(payload))
	if err != nil {
		return RepositoryTrackingResult{}, ErrRepositoryTrackingUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	client := noRedirectHTTPClient(c.HTTP)
	if guard == nil {
		return RepositoryTrackingResult{}, ErrRepositoryTrackingUnavailable
	}
	if err := guard(requestCtx); err != nil {
		return RepositoryTrackingResult{}, err
	}
	if err := requestCtx.Err(); err != nil {
		return RepositoryTrackingResult{}, err
	}
	_, currentDaemonURL, currentGeneration := c.snapshot()
	if currentGeneration != binding.Generation || currentDaemonURL != daemonURL || repositoryTrackingDaemonID(currentDaemonURL) != binding.DaemonID {
		return RepositoryTrackingResult{}, ErrBindingChanged
	}
	// Attempted is deliberately set at the HTTP.Do boundary. A request
	// construction or binding failure before this line receives no external
	// success credit; every response/transport result after it is ambiguous
	// until detached inventory verification completes.
	result := RepositoryTrackingResult{Attempted: true}
	response, err := client.Do(req)
	if err != nil {
		return result, ErrRepositoryTrackingUnavailable
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	data, readErr := io.ReadAll(io.LimitReader(response.Body, repositoryTrackingResponseLimit+1))
	if readErr != nil || int64(len(data)) > repositoryTrackingResponseLimit {
		return result, ErrRepositoryTrackingUnavailable
	}
	if response.StatusCode != http.StatusOK {
		// Preserve the upstream helper's non-200 error contract while keeping
		// Attempted=true. The app intentionally ignores this sanitized error
		// until detached inventory readback classifies an ambiguous mutation.
		return result, ErrRepositoryTrackingUnavailable
	}
	// v0.64's CLI helper treats HTTP 200 as the registration success signal;
	// response body domain fields are not authoritative. Drain and bound the
	// body for connection hygiene, then let post-inventory readback decide.
	return result, nil
}

func (c *Client) repositoryTrackingInventoryRead(ctx context.Context) (repoinventory.Inventory, error) {
	if c == nil || c.HTTP == nil {
		return repoinventory.Inventory{}, ErrRepositoryTrackingUnavailable
	}
	raw, err := c.getDaemonJSONBoundedWithClient(ctx, "/api/repos", int64(repoinventory.MaxResponseBytes), noRedirectHTTPClient(c.HTTP))
	if err != nil {
		return repoinventory.Inventory{}, err
	}
	value, err := repoinventory.Parse(raw)
	if err != nil {
		return repoinventory.Inventory{}, err
	}
	return value, nil
}

func (c *Client) getDaemonJSONBoundedWithClient(ctx context.Context, path string, limit int64, client *http.Client) (json.RawMessage, error) {
	_, daemonURL, _ := c.snapshot()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	if client == nil {
		client = noRedirectHTTPClient(c.HTTP)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, ErrRepositoryTrackingUnavailable
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, ErrRepositoryTrackingUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return nil, ErrRepositoryTrackingUnavailable
	}
	if len(bytes.TrimSpace(data)) == 0 || !json.Valid(data) {
		return nil, ErrRepositoryTrackingUnavailable
	}
	return json.RawMessage(data), nil
}

var _ RepositoryTrackingTransport = (*Client)(nil)
