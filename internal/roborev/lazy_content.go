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
	"time"
	"unicode/utf8"

	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const lazyContentLimit int64 = 8 << 20

// The v0.64 ownership proof only needs the ordered jobs/id/repo_id prefix.
// Keep this deliberately tiny: the parser stops as soon as repo_id and its
// delimiter have been validated, before any prompt or later job field.
const lazyOwnerPrefixLimit = 128

var (
	ErrLazyContentUnsupported = errors.New("RoboRev lazy content contract is unsupported")
	ErrLazyContentOffline     = errors.New("RoboRev daemon is offline for lazy content")
	ErrLazyContentNotFound    = errors.New("RoboRev lazy content was not found")
	ErrLazyContentForbidden   = errors.New("RoboRev lazy content job belongs to a different repository")
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

// noRedirectHTTPClient keeps the configured transport, timeout, and other
// client settings while making a request boundary return redirect responses
// directly.  Lazy reads must not follow a Location to another origin/path or
// let net/http consume the redirect body while preparing a second request.
func noRedirectHTTPClient(configured *http.Client) *http.Client {
	if configured == nil {
		configured = http.DefaultClient
	}
	client := *configured
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client
}

func (c *Client) lazyGET(ctx context.Context, binding lazyBinding, path string) (lazyHTTPResponse, error) {
	if !c.lazyBindingCurrent(binding) {
		return lazyHTTPResponse{}, ErrBindingChanged
	}
	client := noRedirectHTTPClient(c.HTTP)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, binding.daemonURL+path, nil)
	if err != nil {
		return lazyHTTPResponse{}, fmt.Errorf("%w: request could not be built", ErrLazyContentMalformed)
	}
	req.Header.Set("Accept-Encoding", "identity")
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
	if err := requireIdentityEncoding(resp.Header); err != nil {
		return lazyHTTPResponse{}, err
	}
	// Error bodies are never useful to the browser and may contain prompt,
	// filesystem, or other daemon details.  Do not consume them.
	if resp.StatusCode != http.StatusOK {
		return lazyHTTPResponse{status: resp.StatusCode, headers: resp.Header.Clone()}, nil
	}
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

func requireIdentityEncoding(headers http.Header) error {
	values := headers.Values("Content-Encoding")
	if len(values) == 0 {
		return nil
	}
	if len(values) != 1 || !strings.EqualFold(strings.TrimSpace(values[0]), "identity") {
		return fmt.Errorf("%w: non-identity content encoding", ErrLazyContentMalformed)
	}
	return nil
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

type lazyOwnerHTTPResponse struct {
	status  int
	headers http.Header
	repoID  int64
	found   bool
}

// lazyOwnerGET reads only the bounded identity prefix of a v0.64 jobs list.
// In particular it does not use json.Decoder or io.ReadAll: queued/running
// rows can retain their prompt even with omit_prompt=true, so reading the
// complete response would violate authorization-before-content.
func (c *Client) lazyOwnerGET(ctx context.Context, binding lazyBinding, path string, expectedID int64) (lazyOwnerHTTPResponse, error) {
	if !c.lazyBindingCurrent(binding) {
		return lazyOwnerHTTPResponse{}, ErrBindingChanged
	}
	client := noRedirectHTTPClient(c.HTTP)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, binding.daemonURL+path, nil)
	if err != nil {
		return lazyOwnerHTTPResponse{}, fmt.Errorf("%w: request could not be built", ErrLazyContentMalformed)
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return lazyOwnerHTTPResponse{}, ctxErr
		}
		if isUnreachableHealthError(err) {
			return lazyOwnerHTTPResponse{}, ErrLazyContentOffline
		}
		return lazyOwnerHTTPResponse{}, fmt.Errorf("%w: daemon request failed", ErrLazyContentMalformed)
	}
	defer resp.Body.Close()
	if err := requireIdentityEncoding(resp.Header); err != nil {
		return lazyOwnerHTTPResponse{}, err
	}
	// Never read a non-success body during ownership authorization.
	if resp.StatusCode != http.StatusOK {
		return lazyOwnerHTTPResponse{status: resp.StatusCode, headers: resp.Header.Clone()}, nil
	}
	if err := requireMediaType(resp.Header, "application/json"); err != nil {
		return lazyOwnerHTTPResponse{}, err
	}
	repoID, found, err := parseLazyOwnerPrefix(resp.Body, expectedID)
	if err != nil {
		return lazyOwnerHTTPResponse{}, err
	}
	return lazyOwnerHTTPResponse{status: resp.StatusCode, headers: resp.Header.Clone(), repoID: repoID, found: found}, nil
}

type lazyOwnerPrefixReader struct {
	r     io.Reader
	read  int
	limit int
}

func (r *lazyOwnerPrefixReader) Read(p []byte) (int, error) {
	if r.read >= r.limit {
		return 0, ErrLazyContentOversized
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

func readLazyOwnerByte(reader *lazyOwnerPrefixReader) (byte, error) {
	var one [1]byte
	n, err := reader.Read(one[:])
	if n == 1 {
		return one[0], nil
	}
	if err == nil {
		return 0, io.ErrUnexpectedEOF
	}
	return 0, err
}

func lazyJSONWhitespace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func readLazyOwnerNonWhitespace(reader *lazyOwnerPrefixReader) (byte, error) {
	for {
		value, err := readLazyOwnerByte(reader)
		if err != nil {
			return 0, err
		}
		if !lazyJSONWhitespace(value) {
			return value, nil
		}
	}
}

func readLazyOwnerString(reader *lazyOwnerPrefixReader, first byte) (string, error) {
	if first != '"' {
		return "", errors.New("expected JSON string")
	}
	value := []byte{'"'}
	escaped := false
	for {
		one, err := readLazyOwnerByte(reader)
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
			return "", errors.New("control byte in JSON string")
		}
	}
}

func readLazyOwnerKey(reader *lazyOwnerPrefixReader, want string) error {
	first, err := readLazyOwnerNonWhitespace(reader)
	if err != nil {
		return err
	}
	key, err := readLazyOwnerString(reader, first)
	if err != nil {
		return err
	}
	if key != want {
		return fmt.Errorf("expected key %q", want)
	}
	colon, err := readLazyOwnerNonWhitespace(reader)
	if err != nil {
		return err
	}
	if colon != ':' {
		return fmt.Errorf("expected colon after %q", want)
	}
	return nil
}

func readLazyOwnerPositiveInt(reader *lazyOwnerPrefixReader) (int64, byte, error) {
	first, err := readLazyOwnerNonWhitespace(reader)
	if err != nil {
		return 0, 0, err
	}
	if first < '1' || first > '9' {
		return 0, 0, errors.New("expected positive JSON integer")
	}
	digits := []byte{first}
	for {
		one, err := readLazyOwnerByte(reader)
		if err != nil {
			return 0, 0, err
		}
		if one >= '0' && one <= '9' {
			digits = append(digits, one)
			continue
		}
		for lazyJSONWhitespace(one) {
			one, err = readLazyOwnerByte(reader)
			if err != nil {
				return 0, 0, err
			}
		}
		if one != ',' && one != '}' {
			return 0, 0, errors.New("expected JSON object delimiter")
		}
		parsed, parseErr := strconv.ParseInt(string(digits), 10, 64)
		if parseErr != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != string(digits) {
			return 0, 0, errors.New("JSON integer is outside positive int64 range")
		}
		return parsed, one, nil
	}
}

func lazyOwnerPrefixFailure(err error, detail string) error {
	if errors.Is(err, ErrLazyContentOversized) {
		return fmt.Errorf("%w: %s", ErrLazyContentOversized, detail)
	}
	return fmt.Errorf("%w: %s", ErrLazyContentMalformed, detail)
}

func parseLazyOwnerPrefix(body io.Reader, expectedID int64) (int64, bool, error) {
	reader := &lazyOwnerPrefixReader{r: body, limit: lazyOwnerPrefixLimit}
	first, err := readLazyOwnerNonWhitespace(reader)
	if err != nil || first != '{' {
		return 0, false, lazyOwnerPrefixFailure(err, "jobs response must begin with an object")
	}
	if err := readLazyOwnerKey(reader, "jobs"); err != nil {
		return 0, false, lazyOwnerPrefixFailure(err, err.Error())
	}
	array, err := readLazyOwnerNonWhitespace(reader)
	if err != nil || array != '[' {
		return 0, false, lazyOwnerPrefixFailure(err, "jobs must be an array")
	}
	first, err = readLazyOwnerNonWhitespace(reader)
	if err != nil {
		return 0, false, lazyOwnerPrefixFailure(err, "jobs array is incomplete")
	}
	if first == ']' {
		return 0, false, nil
	}
	if first != '{' {
		return 0, false, lazyOwnerPrefixFailure(nil, "first job row must be an object")
	}
	if err := readLazyOwnerKey(reader, "id"); err != nil {
		return 0, false, lazyOwnerPrefixFailure(err, err.Error())
	}
	jobID, delimiter, err := readLazyOwnerPositiveInt(reader)
	if err != nil || delimiter != ',' {
		return 0, false, lazyOwnerPrefixFailure(err, "job id must precede repo_id")
	}
	if expectedID > 0 && jobID != expectedID {
		return 0, false, lazyOwnerPrefixFailure(nil, "target job ID does not match request")
	}
	if err := readLazyOwnerKey(reader, "repo_id"); err != nil {
		return 0, false, lazyOwnerPrefixFailure(err, err.Error())
	}
	repoID, delimiter, err := readLazyOwnerPositiveInt(reader)
	if err != nil || delimiter != ',' {
		return 0, false, lazyOwnerPrefixFailure(err, "repo_id must be followed by a later job field")
	}
	// readLazyOwnerPositiveInt consumes only the delimiter after repo_id.  No
	// read is performed for the following field, which is where prompt bytes
	// appear in queued/running rows.
	return repoID, true, nil
}

func lazyOwnerQuery(id int64, repo string) string {
	query := url.Values{}
	if id > 0 {
		query.Set("id", strconv.FormatInt(id, 10))
	}
	if repo != "" {
		query.Set("repo", repo)
	}
	query.Set("limit", "1")
	query.Set("omit_prompt", "true")
	return "/api/jobs?" + query.Encode()
}

func (c *Client) authorizeLazyContent(ctx context.Context, binding lazyBinding, id int64, registeredPaths []string) error {
	target, err := c.lazyOwnerGET(ctx, binding, lazyOwnerQuery(id, ""), id)
	if err != nil {
		return err
	}
	if target.status == http.StatusNotFound || (target.status == http.StatusOK && !target.found) {
		return ErrLazyContentNotFound
	}
	if target.status != http.StatusOK {
		return lazyStatusError(target.status)
	}
	if !target.found || target.repoID <= 0 {
		return fmt.Errorf("%w: target repository identity is missing", ErrLazyContentMalformed)
	}
	if !c.lazyBindingCurrent(binding) {
		return ErrBindingChanged
	}
	var firstWitnessErr error
	for _, path := range registeredPaths {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("%w: registered repository path is empty", ErrLazyContentMalformed)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !c.lazyBindingCurrent(binding) {
			return ErrBindingChanged
		}
		witness, witnessErr := c.lazyOwnerGET(ctx, binding, lazyOwnerQuery(0, path), 0)
		if witnessErr != nil {
			if errors.Is(witnessErr, context.Canceled) || errors.Is(witnessErr, context.DeadlineExceeded) || errors.Is(witnessErr, ErrBindingChanged) {
				return witnessErr
			}
			if firstWitnessErr == nil {
				firstWitnessErr = witnessErr
			}
			continue
		}
		// A repository-specific witness returning 404 is a valid negative:
		// that daemon query simply has no row for this registered path.  It must
		// not turn a mismatch-only authorization result into NotFound.
		if witness.status == http.StatusNotFound {
			continue
		}
		if witness.status != http.StatusOK {
			if firstWitnessErr == nil {
				firstWitnessErr = lazyStatusError(witness.status)
			}
			continue
		}
		if witness.found && witness.repoID == target.repoID {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !c.lazyBindingCurrent(binding) {
				return ErrBindingChanged
			}
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.lazyBindingCurrent(binding) {
		return ErrBindingChanged
	}
	if firstWitnessErr != nil {
		return firstWitnessErr
	}
	return ErrLazyContentForbidden
}

const lazyContentOperationTimeout = 15 * time.Second

func (c *Client) AuthorizedPrompt(ctx context.Context, id int64, registeredPaths []string) (string, error) {
	if err := validateJobID(id); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, lazyContentOperationTimeout)
	defer cancel()
	binding, err := c.lazyPreflight(ctx)
	if err != nil {
		return "", err
	}
	if err := c.authorizeLazyContent(ctx, binding, id, registeredPaths); err != nil {
		return "", err
	}
	return c.promptWithBinding(ctx, binding, id)
}

func (c *Client) promptWithBinding(ctx context.Context, binding lazyBinding, id int64) (string, error) {
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

func (c *Client) Prompt(ctx context.Context, id int64) (string, error) {
	if err := validateJobID(id); err != nil {
		return "", err
	}
	binding, err := c.lazyPreflight(ctx)
	if err != nil {
		return "", err
	}
	return c.promptWithBinding(ctx, binding, id)
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
	return c.logWithBinding(ctx, binding, id)
}

func (c *Client) AuthorizedLog(ctx context.Context, id int64, registeredPaths []string) (string, error) {
	if err := validateJobID(id); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, lazyContentOperationTimeout)
	defer cancel()
	binding, err := c.lazyPreflight(ctx)
	if err != nil {
		return "", err
	}
	if err := c.authorizeLazyContent(ctx, binding, id, registeredPaths); err != nil {
		return "", err
	}
	return c.logWithBinding(ctx, binding, id)
}

func (c *Client) logWithBinding(ctx context.Context, binding lazyBinding, id int64) (string, error) {
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
