package roborev

// This file contains the read-only, explicit-load transport for review
// prompts and raw logs.  It deliberately uses the already-running daemon's
// v0.64 HTTP API: these reads must never start or restart a daemon and must
// never invoke the human-oriented CLI output commands.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const lazyContentLimit int64 = 8 << 20

var (
	ErrLazyContentUnsupported = errors.New("RoboRev lazy content contract is unsupported")
	ErrLazyContentOffline     = errors.New("RoboRev daemon is offline for lazy content")
	ErrLazyContentNotFound    = errors.New("RoboRev lazy content was not found")
	ErrLazyContentMalformed   = errors.New("RoboRev lazy content response is malformed")
	ErrLazyContentOversized   = errors.New("RoboRev lazy content response is oversized")
)

type lazyBinding struct {
	daemonURL  string
	generation uint64
	version    string
}

// lazyPreflight verifies the executable/daemon contract once for a lazy
// read.  Reachability is required, while the semantic healthy bit is not:
// the daemon's health endpoint can report unhealthy components while still
// serving these read-only endpoints.
func (c *Client) lazyPreflight(ctx context.Context) (lazyBinding, error) {
	if c == nil {
		return lazyBinding{}, fmt.Errorf("%w: client is unavailable", ErrLazyContentUnsupported)
	}
	_, daemonURL, generation := c.snapshot()
	if daemonURL == "" {
		return lazyBinding{}, fmt.Errorf("%w: daemon binding is unavailable", ErrLazyContentUnsupported)
	}
	version, err := c.detectVersion(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return lazyBinding{}, ctxErr
		}
		if errors.Is(err, ErrBindingChanged) {
			return lazyBinding{}, ErrBindingChanged
		}
		return lazyBinding{}, fmt.Errorf("%w: executable version could not be verified", ErrLazyContentUnsupported)
	}
	if !capabilities(version).PromptLazyLoad || !capabilities(version).RawLog {
		return lazyBinding{}, fmt.Errorf("%w: version %s", ErrLazyContentUnsupported, version)
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return lazyBinding{}, ctxErr
		}
		if isUnreachableHealthError(err) {
			return lazyBinding{}, fmt.Errorf("%w", ErrLazyContentOffline)
		}
		return lazyBinding{}, fmt.Errorf("%w: daemon health could not be validated", ErrLazyContentMalformed)
	}
	if !roborevcompat.IsMatchingSupportedVersion(version, health.Version) {
		return lazyBinding{}, fmt.Errorf("%w: executable and daemon versions differ", ErrLazyContentUnsupported)
	}
	if !c.generationCurrent(generation) {
		return lazyBinding{}, ErrBindingChanged
	}
	return lazyBinding{daemonURL: daemonURL, generation: generation, version: version}, nil
}

func (c *Client) lazyBindingCurrent(binding lazyBinding) bool {
	return c != nil && c.generationCurrent(binding.generation)
}

type lazyHTTPResponse struct {
	status  int
	headers http.Header
	body    []byte
}

func (c *Client) lazyGET(ctx context.Context, binding lazyBinding, path string) (lazyHTTPResponse, error) {
	if !c.lazyBindingCurrent(binding) {
		return lazyHTTPResponse{}, ErrBindingChanged
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, binding.daemonURL+path, nil)
	if err != nil {
		return lazyHTTPResponse{}, fmt.Errorf("%w: request could not be built", ErrLazyContentMalformed)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return lazyHTTPResponse{}, ctxErr
		}
		if isUnreachableHealthError(err) {
			return lazyHTTPResponse{}, ErrLazyContentOffline
		}
		return lazyHTTPResponse{}, fmt.Errorf("%w: daemon request failed", ErrLazyContentMalformed)
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, lazyContentLimit+1))
	if readErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return lazyHTTPResponse{}, ctxErr
		}
		return lazyHTTPResponse{}, fmt.Errorf("%w: response could not be read", ErrLazyContentMalformed)
	}
	if int64(len(data)) > lazyContentLimit {
		return lazyHTTPResponse{}, ErrLazyContentOversized
	}
	return lazyHTTPResponse{status: resp.StatusCode, headers: resp.Header.Clone(), body: data}, nil
}

func requireMediaType(headers http.Header, expected string) error {
	value := strings.TrimSpace(headers.Get("Content-Type"))
	if value == "" {
		return fmt.Errorf("%w: content type is missing", ErrLazyContentMalformed)
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || !strings.EqualFold(mediaType, expected) {
		return fmt.Errorf("%w: unexpected content type", ErrLazyContentMalformed)
	}
	return nil
}

func requireExactMediaType(headers http.Header, expected string) error {
	values := headers.Values("Content-Type")
	if len(values) != 1 || values[0] != expected {
		return fmt.Errorf("%w: unexpected content type", ErrLazyContentMalformed)
	}
	return nil
}

func decodeSingleJSON(data []byte, target any) error {
	if !utf8.Valid(data) {
		return errors.New("invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func decodeReviewPrompt(data []byte, id int64) (string, error) {
	var object map[string]json.RawMessage
	if err := decodeSingleJSON(data, &object); err != nil || object == nil {
		return "", fmt.Errorf("%w: review JSON is invalid", ErrLazyContentMalformed)
	}
	jobID, ok := object["job_id"]
	if !ok || bytes.Equal(bytes.TrimSpace(jobID), []byte("null")) {
		return "", fmt.Errorf("%w: review job_id is missing", ErrLazyContentMalformed)
	}
	var gotID int64
	if err := json.Unmarshal(jobID, &gotID); err != nil || gotID != id {
		return "", fmt.Errorf("%w: review job ID does not match request", ErrLazyContentMalformed)
	}
	prompt, ok := object["prompt"]
	if !ok || bytes.Equal(bytes.TrimSpace(prompt), []byte("null")) {
		return "", fmt.Errorf("%w: review prompt is missing", ErrLazyContentMalformed)
	}
	var value string
	if err := json.Unmarshal(prompt, &value); err != nil {
		return "", fmt.Errorf("%w: review prompt is not a string", ErrLazyContentMalformed)
	}
	return value, nil
}

func decodeFallbackPrompt(data []byte, id int64) (string, error) {
	var object map[string]json.RawMessage
	if err := decodeSingleJSON(data, &object); err != nil || object == nil {
		return "", fmt.Errorf("%w: job list JSON is invalid", ErrLazyContentMalformed)
	}
	rawJobs, ok := object["jobs"]
	if !ok {
		return "", fmt.Errorf("%w: job list is missing jobs", ErrLazyContentMalformed)
	}
	if bytes.Equal(bytes.TrimSpace(rawJobs), []byte("null")) {
		return "", fmt.Errorf("%w: job list is null", ErrLazyContentMalformed)
	}
	var jobs []json.RawMessage
	if err := json.Unmarshal(rawJobs, &jobs); err != nil {
		return "", fmt.Errorf("%w: job list is not an array", ErrLazyContentMalformed)
	}
	if len(jobs) == 0 {
		return "", ErrLazyContentNotFound
	}
	if len(jobs) != 1 {
		return "", fmt.Errorf("%w: job list returned multiple rows", ErrLazyContentMalformed)
	}
	var job map[string]json.RawMessage
	if err := decodeSingleJSON(jobs[0], &job); err != nil || job == nil {
		return "", fmt.Errorf("%w: job row is invalid", ErrLazyContentMalformed)
	}
	var gotID int64
	rawID, ok := job["id"]
	if !ok || json.Unmarshal(rawID, &gotID) != nil || gotID != id {
		return "", fmt.Errorf("%w: fallback job ID does not match request", ErrLazyContentMalformed)
	}
	var status string
	rawStatus, ok := job["status"]
	if !ok || json.Unmarshal(rawStatus, &status) != nil || (status != "queued" && status != "running") {
		return "", fmt.Errorf("%w: fallback job status is not queued or running", ErrLazyContentMalformed)
	}
	rawPrompt, ok := job["prompt"]
	if !ok || bytes.Equal(bytes.TrimSpace(rawPrompt), []byte("null")) {
		return "", fmt.Errorf("%w: fallback prompt is missing", ErrLazyContentMalformed)
	}
	var prompt string
	if err := json.Unmarshal(rawPrompt, &prompt); err != nil || prompt == "" {
		return "", fmt.Errorf("%w: fallback prompt is empty or invalid", ErrLazyContentMalformed)
	}
	return prompt, nil
}

func (c *Client) Prompt(ctx context.Context, id int64) (string, error) {
	if err := validateJobID(id); err != nil {
		return "", err
	}
	binding, err := c.lazyPreflight(ctx)
	if err != nil {
		return "", err
	}
	query := url.Values{}
	query.Set("job_id", strconv.FormatInt(id, 10))
	response, err := c.lazyGET(ctx, binding, "/api/review?"+query.Encode())
	if err != nil {
		return "", err
	}
	var value string
	if response.status == http.StatusOK {
		if err := requireMediaType(response.headers, "application/json"); err != nil {
			return "", err
		}
		value, err = decodeReviewPrompt(response.body, id)
	} else if response.status == http.StatusNotFound {
		fallbackQuery := url.Values{}
		fallbackQuery.Set("id", strconv.FormatInt(id, 10))
		fallbackQuery.Set("limit", "1")
		fallbackQuery.Set("omit_prompt", "true")
		fallback, fallbackErr := c.lazyGET(ctx, binding, "/api/jobs?"+fallbackQuery.Encode())
		if fallbackErr != nil {
			return "", fallbackErr
		}
		if fallback.status == http.StatusNotFound {
			return "", ErrLazyContentNotFound
		}
		if fallback.status != http.StatusOK {
			return "", fmt.Errorf("%w: fallback returned unexpected status", ErrLazyContentMalformed)
		}
		if err := requireMediaType(fallback.headers, "application/json"); err != nil {
			return "", err
		}
		value, err = decodeFallbackPrompt(fallback.body, id)
	} else {
		return "", lazyStatusError(response.status)
	}
	if err != nil {
		return "", err
	}
	if !c.lazyBindingCurrent(binding) {
		return "", ErrBindingChanged
	}
	return value, nil
}

var knownLazyJobStatuses = map[string]struct{}{
	"queued": {}, "running": {}, "done": {}, "failed": {}, "canceled": {}, "applied": {}, "rebased": {}, "skipped": {},
}

func canonicalNonnegativeInt64(value string) (int64, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, errors.New("offset is not canonical")
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, errors.New("offset is not a non-negative decimal integer")
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(parsed, 10) != value {
		return 0, errors.New("offset is outside int64 range")
	}
	return parsed, nil
}

func lazyStatusError(status int) error {
	if status == http.StatusNotFound {
		return ErrLazyContentNotFound
	}
	return fmt.Errorf("%w: daemon returned unexpected status", ErrLazyContentMalformed)
}

func (c *Client) Log(ctx context.Context, id int64) (string, error) {
	if err := validateJobID(id); err != nil {
		return "", err
	}
	binding, err := c.lazyPreflight(ctx)
	if err != nil {
		return "", err
	}
	query := url.Values{}
	query.Set("job_id", strconv.FormatInt(id, 10))
	query.Set("offset", "0")
	response, err := c.lazyGET(ctx, binding, "/api/job/log?"+query.Encode())
	if err != nil {
		return "", err
	}
	if response.status == http.StatusNotFound {
		return "", ErrLazyContentNotFound
	}
	if response.status != http.StatusOK {
		return "", lazyStatusError(response.status)
	}
	if err := requireExactMediaType(response.headers, "application/x-ndjson"); err != nil {
		return "", err
	}
	status := response.headers.Get("X-Job-Status")
	if _, ok := knownLazyJobStatuses[status]; !ok {
		return "", fmt.Errorf("%w: job status header is unknown", ErrLazyContentMalformed)
	}
	offset, err := canonicalNonnegativeInt64(response.headers.Get("X-Log-Offset"))
	if err != nil || offset != int64(len(response.body)) {
		return "", fmt.Errorf("%w: log offset does not match response body", ErrLazyContentMalformed)
	}
	if !utf8.Valid(response.body) {
		return "", fmt.Errorf("%w: log is not valid UTF-8", ErrLazyContentMalformed)
	}
	if !c.lazyBindingCurrent(binding) {
		return "", ErrBindingChanged
	}
	return string(response.body), nil
}
