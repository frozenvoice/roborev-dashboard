package roborev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const reviewMetadataExportResponseLimit int64 = 16 << 20

var (
	ErrUnsupportedReviewMetadataExport = errors.New("RoboRev review metadata export is unsupported")
	ErrReviewMetadataExportOffline     = errors.New("RoboRev daemon is offline")
	ErrReviewMetadataExportUnhealthy   = errors.New("RoboRev daemon health is unhealthy")
	ErrMalformedReviewMetadataExport   = errors.New("malformed RoboRev review metadata export response")
	ErrReviewMetadataExportConflict    = errors.New("RoboRev review metadata export repository binding conflict")
)

// ReviewMetadataExport performs the narrow v0.64 daemon export contract. It
// never invokes the export CLI, starts a daemon, or accepts a browser
// identity/name as a filter. The root is expected to be the fresh canonical
// root inspected and authorized by the HTTP layer.
func (c *Client) ReviewMetadataExport(ctx context.Context, root string) (ReviewMetadataExportResult, error) {
	if c == nil {
		return ReviewMetadataExportResult{}, ErrUnsupportedReviewMetadataExport
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(root) == "" {
		return ReviewMetadataExportResult{}, fmt.Errorf("%w: canonical repository root is required", ErrReviewMetadataExportConflict)
	}

	binary, daemonURL, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" || strings.TrimSpace(daemonURL) == "" || c.Runner == nil {
		return ReviewMetadataExportResult{}, ErrUnsupportedReviewMetadataExport
	}

	cliVersion, err := c.detectVersion(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ReviewMetadataExportResult{}, ctxErr
		}
		if errors.Is(err, ErrBindingChanged) {
			return ReviewMetadataExportResult{}, ErrBindingChanged
		}
		return ReviewMetadataExportResult{}, fmt.Errorf("%w: CLI version could not be verified", ErrUnsupportedReviewMetadataExport)
	}
	if !roborevcompat.IsSupportedVersion(cliVersion) {
		return ReviewMetadataExportResult{}, ErrUnsupportedReviewMetadataExport
	}
	if !c.generationCurrent(generation) {
		return ReviewMetadataExportResult{}, ErrBindingChanged
	}

	health, err := c.reviewMetadataHealthAt(ctx, daemonURL)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ReviewMetadataExportResult{}, ctxErr
		}
		if errors.Is(err, ErrBindingChanged) {
			return ReviewMetadataExportResult{}, ErrBindingChanged
		}
		if isUnreachableHealthError(err) {
			return ReviewMetadataExportResult{}, ErrReviewMetadataExportOffline
		}
		return ReviewMetadataExportResult{}, fmt.Errorf("%w: daemon health response is invalid", ErrMalformedReviewMetadataExport)
	}
	if !health.Healthy {
		return ReviewMetadataExportResult{}, ErrReviewMetadataExportUnhealthy
	}
	if !roborevcompat.IsMatchingSupportedVersion(cliVersion, health.Version) {
		return ReviewMetadataExportResult{}, ErrUnsupportedReviewMetadataExport
	}
	if !c.generationCurrent(generation) {
		return ReviewMetadataExportResult{}, ErrBindingChanged
	}

	inventory, err := c.reviewMetadataInventory(ctx, daemonURL, generation)
	if err != nil {
		return ReviewMetadataExportResult{}, err
	}
	exportRepo, err := resolveReviewMetadataExportRepo(inventory, root)
	if err != nil {
		return ReviewMetadataExportResult{}, err
	}
	if !c.generationCurrent(generation) {
		return ReviewMetadataExportResult{}, ErrBindingChanged
	}

	raw, truncated, err := c.reviewMetadataExportGET(ctx, daemonURL, generation, exportRepo)
	if err != nil {
		return ReviewMetadataExportResult{}, err
	}
	return ReviewMetadataExportResult{Raw: raw, Truncated: truncated}, nil
}

var _ ReviewMetadataExportService = (*Client)(nil)

func (c *Client) reviewMetadataInventory(ctx context.Context, daemonURL string, generation uint64) (repoinventory.Inventory, error) {
	if !c.generationCurrent(generation) {
		return repoinventory.Inventory{}, ErrBindingChanged
	}
	raw, err := c.reviewMetadataGETJSON(ctx, daemonURL, "/api/repos", int64(repoinventory.MaxResponseBytes))
	if err != nil {
		return repoinventory.Inventory{}, err
	}
	inventory, err := repoinventory.Parse(raw)
	if err != nil {
		return repoinventory.Inventory{}, fmt.Errorf("%w: repository inventory is invalid", ErrMalformedReviewMetadataExport)
	}
	if !c.generationCurrent(generation) {
		return repoinventory.Inventory{}, ErrBindingChanged
	}
	return inventory, nil
}

// reviewMetadataHealthAt is export-specific because Review Export freezes the
// daemon URL/generation for the entire operation. It deliberately does not
// reuse the broader runtime-control health helper: this boundary additionally
// requires an application/json media type, UTF-8 when charset is present,
// bounded UTF-8 bytes, and a no-body-read policy for non-success responses.
func (c *Client) reviewMetadataHealthAt(ctx context.Context, daemonURL string) (daemonHealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL+"/api/health", nil)
	if err != nil {
		return daemonHealthResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := noRedirectHTTPClient(c.HTTP).Do(req)
	if err != nil {
		return daemonHealthResponse{}, err
	}
	if resp == nil || resp.Body == nil {
		return daemonHealthResponse{}, errors.New("empty daemon health response")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return daemonHealthResponse{}, fmt.Errorf("daemon health returned non-success status")
	}
	if !reviewMetadataJSONContentType(resp.Header.Get("Content-Type")) {
		return daemonHealthResponse{}, errors.New("daemon health media type is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, runtimeControlResponseLimit+1))
	if err != nil {
		return daemonHealthResponse{}, err
	}
	if int64(len(data)) > runtimeControlResponseLimit {
		return daemonHealthResponse{}, errors.New("daemon health response exceeded bounded output")
	}
	if !utf8.Valid(data) {
		return daemonHealthResponse{}, errors.New("daemon health response is not valid UTF-8")
	}
	// The typed daemonHealthResponse decoder rejects unknown fields, but the
	// standard JSON decoder otherwise accepts duplicate keys with last-wins
	// semantics. Parse the complete value first so the export preflight has an
	// exact one-object contract before typed decoding.
	value, err := parseReviewMetadataJSON(data)
	if err != nil {
		return daemonHealthResponse{}, fmt.Errorf("malformed daemon health: %w", err)
	}
	if _, ok := value.(reviewMetadataObject); !ok {
		return daemonHealthResponse{}, errors.New("daemon health response must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var health daemonHealthResponse
	if err := decoder.Decode(&health); err != nil {
		return daemonHealthResponse{}, fmt.Errorf("malformed daemon health: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return daemonHealthResponse{}, errors.New("daemon health response has trailing JSON")
		}
		return daemonHealthResponse{}, fmt.Errorf("malformed daemon health: %w", err)
	}
	if health.schemaPresent && health.Schema == nil {
		return daemonHealthResponse{}, errors.New(`daemon health "$schema" must be a string`)
	}
	if strings.TrimSpace(health.Version) == "" {
		return daemonHealthResponse{}, errors.New("daemon health version is required")
	}
	return health, nil
}

func resolveReviewMetadataExportRepo(inventory repoinventory.Inventory, root string) (string, error) {
	matched := -1
	for index, repository := range inventory.Repositories {
		if gitx.SameRepositoryPath(repository.RootPath, root) {
			if matched >= 0 {
				return "", fmt.Errorf("%w: multiple tracked roots match the canonical repository", ErrReviewMetadataExportConflict)
			}
			matched = index
		}
	}
	if matched < 0 {
		return "", fmt.Errorf("%w: canonical repository is absent from RoboRev inventory", ErrReviewMetadataExportConflict)
	}
	selected := inventory.Repositories[matched]
	selectedIdentityNonblank := strings.TrimSpace(selected.Identity) != ""
	if !selectedIdentityNonblank {
		if strings.TrimSpace(selected.Name) == "" {
			return "", fmt.Errorf("%w: tracked repository has no export identifier", ErrReviewMetadataExportConflict)
		}
		nameMatches := 0
		for _, repository := range inventory.Repositories {
			if repository.Name == selected.Name {
				nameMatches++
			}
		}
		if nameMatches != 1 {
			return "", fmt.Errorf("%w: repository name is not unique", ErrReviewMetadataExportConflict)
		}
	}

	// This mirrors storage's COALESCE(NULLIF(TRIM(identity), ''), name):
	// TRIM decides emptiness only; a nonblank identity is passed through with
	// its exact original bytes. The effective filter must be globally unique,
	// otherwise a repository row outside the selected root could be exported.
	effectiveIdentifier := selected.Name
	if selectedIdentityNonblank {
		effectiveIdentifier = selected.Identity
	}
	if strings.TrimSpace(effectiveIdentifier) == "" {
		return "", fmt.Errorf("%w: tracked repository has no export identifier", ErrReviewMetadataExportConflict)
	}
	effectiveMatches := 0
	for _, repository := range inventory.Repositories {
		candidate := repository.Name
		if strings.TrimSpace(repository.Identity) != "" {
			candidate = repository.Identity
		}
		if candidate == effectiveIdentifier {
			effectiveMatches++
		}
	}
	if effectiveMatches != 1 {
		return "", fmt.Errorf("%w: export identifier is not unique", ErrReviewMetadataExportConflict)
	}
	return effectiveIdentifier, nil
}

func (c *Client) reviewMetadataExportGET(ctx context.Context, daemonURL string, generation uint64, exportRepo string) (json.RawMessage, bool, error) {
	query := url.Values{}
	query.Set("format", "json")
	query.Set("profile", "metadata")
	query.Set("repo", exportRepo)
	query.Set("limit", "500")
	raw, headers, err := c.reviewMetadataGET(ctx, daemonURL, "/api/export/reviews?"+query.Encode(), generation, reviewMetadataExportResponseLimit)
	if err != nil {
		return nil, false, err
	}
	if !c.generationCurrent(generation) {
		return nil, false, ErrBindingChanged
	}
	validated, truncated, err := validateReviewMetadataExport(raw, exportRepo)
	if err != nil {
		return nil, false, err
	}
	if !c.generationCurrent(generation) {
		return nil, false, ErrBindingChanged
	}
	if !reviewMetadataJSONContentType(headers.Get("Content-Type")) {
		return nil, false, fmt.Errorf("%w: export media type is invalid", ErrMalformedReviewMetadataExport)
	}
	return validated, truncated, nil
}

func (c *Client) reviewMetadataGETJSON(ctx context.Context, daemonURL, path string, limit int64) ([]byte, error) {
	raw, _, err := c.reviewMetadataGET(ctx, daemonURL, path, 0, limit)
	return raw, err
}

func (c *Client) reviewMetadataGET(ctx context.Context, daemonURL, path string, generation uint64, limit int64) (json.RawMessage, http.Header, error) {
	if generation != 0 && !c.generationCurrent(generation) {
		return nil, nil, ErrBindingChanged
	}
	client := noRedirectHTTPClient(c.HTTP)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL+path, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: request could not be built", ErrMalformedReviewMetadataExport)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		if isUnreachableHealthError(err) {
			return nil, nil, ErrReviewMetadataExportOffline
		}
		return nil, nil, fmt.Errorf("%w: daemon request failed", ErrMalformedReviewMetadataExport)
	}
	if resp == nil || resp.Body == nil {
		return nil, nil, fmt.Errorf("%w: daemon response was empty", ErrMalformedReviewMetadataExport)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Do not read or include an upstream error body. In particular, this
		// keeps daemon secrets out of browser-visible errors and avoids turning
		// a non-success response into an unbounded read.
		return nil, resp.Header, fmt.Errorf("%w: daemon returned non-success status", ErrMalformedReviewMetadataExport)
	}
	if !reviewMetadataJSONContentType(resp.Header.Get("Content-Type")) {
		return nil, resp.Header, fmt.Errorf("%w: daemon export media type is invalid", ErrMalformedReviewMetadataExport)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, resp.Header, ctxErr
		}
		return nil, resp.Header, fmt.Errorf("%w: daemon response could not be read", ErrMalformedReviewMetadataExport)
	}
	if int64(len(data)) > limit {
		return nil, resp.Header, fmt.Errorf("%w: daemon response exceeded bounded output", ErrMalformedReviewMetadataExport)
	}
	if !utf8.Valid(data) {
		return nil, resp.Header, fmt.Errorf("%w: daemon response is not valid UTF-8", ErrMalformedReviewMetadataExport)
	}
	return json.RawMessage(data), resp.Header, nil
}

func reviewMetadataJSONContentType(value string) bool {
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return false
	}
	if charset, present := params["charset"]; present && !strings.EqualFold(strings.TrimSpace(charset), "utf-8") {
		return false
	}
	return true
}

type reviewMetadataObject map[string]any

// validateReviewMetadataExport accepts the exact v0.64 metadata document and
// returns the original bytes. The generic parser is intentional: struct
// decoding alone accepts duplicate keys and silently permits trailing values.
func validateReviewMetadataExport(raw []byte, expectedRepo string) (json.RawMessage, bool, error) {
	value, err := parseReviewMetadataJSON(raw)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrMalformedReviewMetadataExport, err)
	}
	root, ok := value.(reviewMetadataObject)
	if !ok {
		return nil, false, fmt.Errorf("%w: document must be an object", ErrMalformedReviewMetadataExport)
	}
	if err := exactReviewMetadataFields(root, "schema_version", "tool", "tool_version", "generated_at", "database_id", "profile", "window", "truncated", "next_cursor", "reviews"); err != nil {
		return nil, false, malformedReviewMetadata(err)
	}
	if number, err := requiredReviewMetadataNumber(root, "schema_version"); err != nil || number != 1 {
		return nil, false, malformedReviewMetadata(errors.New("schema_version must equal 1"))
	}
	if value, err := requiredReviewMetadataString(root, "tool", true); err != nil || value != "roborev" {
		return nil, false, malformedReviewMetadata(errors.New("tool must equal roborev"))
	}
	if value, err := requiredReviewMetadataString(root, "tool_version", true); err != nil || value != roborevcompat.Manifest064().Version {
		return nil, false, malformedReviewMetadata(errors.New("tool_version must equal v0.64.0"))
	}
	generatedAt, err := requiredReviewMetadataString(root, "generated_at", true)
	if err != nil || !validReviewMetadataUTC(generatedAt) {
		return nil, false, malformedReviewMetadata(errors.New("generated_at must be a UTC RFC3339 timestamp"))
	}
	if _, err := requiredReviewMetadataString(root, "database_id", true); err != nil {
		return nil, false, malformedReviewMetadata(err)
	}
	if value, err := requiredReviewMetadataString(root, "profile", true); err != nil || value != "metadata" {
		return nil, false, malformedReviewMetadata(errors.New("profile must equal metadata"))
	}
	if err := validateReviewMetadataWindow(root["window"]); err != nil {
		return nil, false, malformedReviewMetadata(err)
	}
	truncated, err := requiredReviewMetadataBool(root, "truncated")
	if err != nil {
		return nil, false, malformedReviewMetadata(err)
	}
	reviewsValue, ok := root["reviews"].([]any)
	if !ok {
		return nil, false, malformedReviewMetadata(errors.New("reviews must be an array"))
	}
	if len(reviewsValue) > 500 {
		return nil, false, malformedReviewMetadata(errors.New("reviews exceeds the 500 row limit"))
	}
	if truncated && len(reviewsValue) != 500 {
		return nil, false, malformedReviewMetadata(errors.New("truncated export must contain exactly 500 reviews"))
	}
	nextCursor, err := nullableReviewMetadataString(root, "next_cursor")
	if err != nil {
		return nil, false, malformedReviewMetadata(err)
	}
	if len(reviewsValue) == 0 && nextCursor != nil {
		return nil, false, malformedReviewMetadata(errors.New("empty export must have a null next_cursor"))
	}
	if len(reviewsValue) > 0 && (nextCursor == nil || strings.TrimSpace(*nextCursor) == "") {
		return nil, false, malformedReviewMetadata(errors.New("non-empty export must have a next_cursor"))
	}
	var previousTime time.Time
	previousID := ""
	for index, item := range reviewsValue {
		row, ok := item.(reviewMetadataObject)
		if !ok {
			return nil, false, malformedReviewMetadata(fmt.Errorf("reviews[%d] must be an object", index))
		}
		completedAt, reviewID, err := validateReviewMetadataRow(row, expectedRepo, index)
		if err != nil {
			return nil, false, malformedReviewMetadata(err)
		}
		if index > 0 && (completedAt.Before(previousTime) || (completedAt.Equal(previousTime) && reviewID <= previousID)) {
			return nil, false, malformedReviewMetadata(errors.New("reviews are not ordered by completed_at and review_id"))
		}
		previousTime, previousID = completedAt, reviewID
	}
	return json.RawMessage(raw), truncated, nil
}

func malformedReviewMetadata(err error) error {
	return fmt.Errorf("%w: %v", ErrMalformedReviewMetadataExport, err)
}

func validateReviewMetadataWindow(value any) error {
	window, ok := value.(reviewMetadataObject)
	if !ok {
		return errors.New("window must be an object")
	}
	if err := exactReviewMetadataFields(window, "field", "since", "until"); err != nil {
		return err
	}
	field, err := requiredReviewMetadataString(window, "field", true)
	if err != nil || field != "completed_at" {
		return errors.New("window.field must equal completed_at")
	}
	for _, name := range []string{"since", "until"} {
		value, present := window[name]
		if !present || value != nil {
			return fmt.Errorf("window.%s must be null", name)
		}
	}
	return nil
}

func validateReviewMetadataRow(row reviewMetadataObject, expectedRepo string, index int) (time.Time, string, error) {
	if err := exactReviewMetadataFields(row, "review_id", "status", "verdict", "created_at", "completed_at", "duration_ms", "project", "repo", "branch", "commit_sha", "pr_number", "pr_url", "agent", "model", "cost", "content", "subagents"); err != nil {
		return time.Time{}, "", fmt.Errorf("reviews[%d]: %v", index, err)
	}
	reviewID, err := requiredReviewMetadataString(row, "review_id", true)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("reviews[%d]: %v", index, err)
	}
	status, err := requiredReviewMetadataString(row, "status", true)
	if err != nil || status != "done" {
		return time.Time{}, "", fmt.Errorf("reviews[%d]: status must equal done", index)
	}
	verdict, err := requiredReviewMetadataString(row, "verdict", true)
	if err != nil || (verdict != "pass" && verdict != "fail") {
		return time.Time{}, "", fmt.Errorf("reviews[%d]: verdict must be pass or fail", index)
	}
	createdAt, err := requiredReviewMetadataString(row, "created_at", true)
	if err != nil || !validReviewMetadataUTC(createdAt) {
		return time.Time{}, "", fmt.Errorf("reviews[%d]: created_at must be a UTC RFC3339 timestamp", index)
	}
	completedAt, err := requiredReviewMetadataString(row, "completed_at", true)
	if err != nil || !validReviewMetadataUTC(completedAt) {
		return time.Time{}, "", fmt.Errorf("reviews[%d]: completed_at must be a UTC RFC3339 timestamp", index)
	}
	completed, _ := time.Parse(time.RFC3339Nano, completedAt)
	for _, name := range []string{"duration_ms", "branch", "commit_sha", "pr_number", "pr_url", "model"} {
		if err := validateNullableReviewMetadataScalar(row, name); err != nil {
			return time.Time{}, "", fmt.Errorf("reviews[%d]: %v", index, err)
		}
	}
	for _, name := range []string{"project", "repo", "agent"} {
		value, err := requiredReviewMetadataString(row, name, true)
		if err != nil {
			return time.Time{}, "", fmt.Errorf("reviews[%d]: %v", index, err)
		}
		if name == "repo" && value != expectedRepo {
			return time.Time{}, "", fmt.Errorf("reviews[%d]: repo does not match selected repository", index)
		}
	}
	if row["content"] != nil {
		return time.Time{}, "", fmt.Errorf("reviews[%d]: metadata content must be null", index)
	}
	if err := validateReviewMetadataCost(row["cost"]); err != nil {
		return time.Time{}, "", fmt.Errorf("reviews[%d]: %v", index, err)
	}
	subagents, ok := row["subagents"].([]any)
	if !ok {
		return time.Time{}, "", fmt.Errorf("reviews[%d]: subagents must be an array", index)
	}
	for subIndex, value := range subagents {
		if err := validateReviewMetadataSubagent(value, index, subIndex); err != nil {
			return time.Time{}, "", err
		}
	}
	return completed, reviewID, nil
}

func validateReviewMetadataSubagent(value any, rowIndex, index int) error {
	subagent, ok := value.(reviewMetadataObject)
	if !ok {
		return fmt.Errorf("reviews[%d].subagents[%d] must be an object", rowIndex, index)
	}
	if err := exactReviewMetadataFields(subagent, "review_id", "name", "agent", "model", "review_type", "verdict", "completed_at", "duration_ms", "cost", "content"); err != nil {
		return fmt.Errorf("reviews[%d].subagents[%d]: %v", rowIndex, index, err)
	}
	for _, name := range []string{"review_id", "name", "agent"} {
		if _, err := requiredReviewMetadataString(subagent, name, true); err != nil {
			return fmt.Errorf("reviews[%d].subagents[%d]: %v", rowIndex, index, err)
		}
	}
	verdict, err := requiredReviewMetadataString(subagent, "verdict", true)
	if err != nil || (verdict != "pass" && verdict != "fail") {
		return fmt.Errorf("reviews[%d].subagents[%d]: verdict must be pass or fail", rowIndex, index)
	}
	completedAt, err := requiredReviewMetadataString(subagent, "completed_at", true)
	if err != nil || !validReviewMetadataUTC(completedAt) {
		return fmt.Errorf("reviews[%d].subagents[%d]: completed_at must be a UTC RFC3339 timestamp", rowIndex, index)
	}
	for _, name := range []string{"model", "review_type", "duration_ms"} {
		if err := validateNullableReviewMetadataScalar(subagent, name); err != nil {
			return fmt.Errorf("reviews[%d].subagents[%d]: %v", rowIndex, index, err)
		}
	}
	if subagent["content"] != nil {
		return fmt.Errorf("reviews[%d].subagents[%d]: metadata content must be null", rowIndex, index)
	}
	if err := validateReviewMetadataCost(subagent["cost"]); err != nil {
		return fmt.Errorf("reviews[%d].subagents[%d]: %v", rowIndex, index, err)
	}
	return nil
}

func validateNullableReviewMetadataScalar(object reviewMetadataObject, name string) error {
	value, ok := object[name]
	if !ok || value == nil {
		return nil
	}
	switch name {
	case "duration_ms", "pr_number":
		number, ok := value.(json.Number)
		if !ok || !validNonNegativeInteger(number) {
			return fmt.Errorf("%s must be a non-negative integer or null", name)
		}
	default:
		if stringValue, ok := value.(string); !ok || len(stringValue) > 4096 {
			return fmt.Errorf("%s must be a string or null", name)
		}
	}
	return nil
}

func validateReviewMetadataCost(value any) error {
	cost, ok := value.(reviewMetadataObject)
	if !ok {
		return errors.New("cost must be an object")
	}
	if err := exactReviewMetadataFields(cost, "tokens_in", "tokens_out", "usd"); err != nil {
		return err
	}
	for _, name := range []string{"tokens_in", "tokens_out"} {
		value := cost[name]
		if value != nil {
			number, ok := value.(json.Number)
			if !ok || !validNonNegativeInteger(number) {
				return fmt.Errorf("cost.%s must be a non-negative integer or null", name)
			}
		}
	}
	if value := cost["usd"]; value != nil {
		number, ok := value.(json.Number)
		if !ok {
			return errors.New("cost.usd must be a non-negative number or null")
		}
		parsed, err := strconvParseFloat(string(number))
		if err != nil || parsed < 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return errors.New("cost.usd must be a non-negative number or null")
		}
	}
	return nil
}

func validNonNegativeInteger(value json.Number) bool {
	text := string(value)
	if text == "" || strings.ContainsAny(text, ".eE+-") {
		return false
	}
	for _, char := range text {
		if char < '0' || char > '9' {
			return false
		}
	}
	_, err := json.Number(text).Int64()
	return err == nil
}

func strconvParseFloat(value string) (float64, error) {
	var parsed float64
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return 0, err
	}
	return parsed, nil
}

func validReviewMetadataUTC(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return false
	}
	_, offset := parsed.Zone()
	return offset == 0
}

func exactReviewMetadataFields(object reviewMetadataObject, fields ...string) error {
	allowed := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		allowed[field] = struct{}{}
		if _, ok := object[field]; !ok {
			return fmt.Errorf("%s is required", field)
		}
	}
	for field := range object {
		if _, ok := allowed[field]; !ok {
			return fmt.Errorf("unknown field %q", field)
		}
	}
	return nil
}

func requiredReviewMetadataString(object reviewMetadataObject, name string, nonEmpty bool) (string, error) {
	value, ok := object[name]
	if !ok {
		return "", fmt.Errorf("%s is required", name)
	}
	stringValue, ok := value.(string)
	if !ok || len(stringValue) > 4096 || (nonEmpty && strings.TrimSpace(stringValue) == "") {
		return "", fmt.Errorf("%s must be a non-empty string", name)
	}
	return stringValue, nil
}

func nullableReviewMetadataString(object reviewMetadataObject, name string) (*string, error) {
	value, ok := object[name]
	if !ok {
		return nil, fmt.Errorf("%s is required", name)
	}
	if value == nil {
		return nil, nil
	}
	stringValue, ok := value.(string)
	if !ok || len(stringValue) > 4096 {
		return nil, fmt.Errorf("%s must be a string or null", name)
	}
	return &stringValue, nil
}

func requiredReviewMetadataNumber(object reviewMetadataObject, name string) (int64, error) {
	value, ok := object[name]
	if !ok {
		return 0, fmt.Errorf("%s is required", name)
	}
	number, ok := value.(json.Number)
	if !ok || !validNonNegativeInteger(number) {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	parsed, err := number.Int64()
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func requiredReviewMetadataBool(object reviewMetadataObject, name string) (bool, error) {
	value, ok := object[name]
	if !ok {
		return false, fmt.Errorf("%s is required", name)
	}
	boolean, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return boolean, nil
}

func parseReviewMetadataJSON(raw []byte) (any, error) {
	if len(raw) == 0 || int64(len(raw)) > reviewMetadataExportResponseLimit {
		return nil, errors.New("body exceeds bounded output")
	}
	if !utf8.Valid(raw) {
		return nil, errors.New("body is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := parseReviewMetadataValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("trailing JSON value")
		}
		return nil, fmt.Errorf("trailing JSON: %w", err)
	}
	return value, nil
}

func parseReviewMetadataValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			object := reviewMetadataObject{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("object key is not a string")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("duplicate field %q", key)
				}
				fieldValue, err := parseReviewMetadataValue(decoder)
				if err != nil {
					return nil, err
				}
				object[key] = fieldValue
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return nil, errors.New("malformed object")
			}
			return object, nil
		case '[':
			array := make([]any, 0)
			for decoder.More() {
				item, err := parseReviewMetadataValue(decoder)
				if err != nil {
					return nil, err
				}
				array = append(array, item)
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return nil, errors.New("malformed array")
			}
			return array, nil
		default:
			return nil, errors.New("unexpected JSON delimiter")
		}
	case nil, bool, string, json.Number:
		return value, nil
	default:
		return nil, errors.New("unsupported JSON value")
	}
}
