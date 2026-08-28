package roborev

import (
	"bufio"
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

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/repomerge"
)

const (
	repositoryMergeJobsLimit      = 10000
	repositoryMergeJobsBodyLimit  = 16 << 20
	repositoryMergeJobsLineBuffer = 32 << 10
)

var (
	ErrRepositoryMergeActiveJobs    = repomerge.ErrActiveSourceJobs
	ErrRepositoryMergeMalformedJobs = errors.New("malformed RoboRev repository merge job response")
)

// repositoryMergeJobsRead performs the one bounded typed job-list request
// required by merge preflight. It intentionally parses the stream in order:
// status is read before prompt, and a queued/running row closes the response
// immediately without retaining the prompt or any other heavy field.
func (c *Client) repositoryMergeJobsRead(ctx context.Context, sourcePath string, expectedCount int64) error {
	if c == nil || c.HTTP == nil {
		return errors.New("RoboRev daemon transport is unavailable")
	}
	_, daemonURL, _ := c.snapshot()
	query := url.Values{}
	query.Set("repo", sourcePath)
	query.Set("limit", strconv.Itoa(repositoryMergeJobsLimit))
	query.Set("omit_prompt", "true")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL+"/api/jobs?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := noRedirectHTTPClient(c.HTTP).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RoboRev daemon returned %d", resp.StatusCode)
	}
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return fmt.Errorf("%w: expected application/json", ErrRepositoryMergeMalformedJobs)
	}
	reader := &mergeJobsLimitReader{reader: bufio.NewReaderSize(resp.Body, repositoryMergeJobsLineBuffer), limit: repositoryMergeJobsBodyLimit}
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	if err := decodeMergeJobsResponse(decoder, sourcePath, expectedCount); err != nil {
		if errors.Is(err, ErrRepositoryMergeActiveJobs) || errors.Is(err, repomerge.ErrSourceJobMismatch) {
			return err
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrRepositoryMergeMalformedJobs, err)
	}
	if reader.overflow {
		return fmt.Errorf("%w: response exceeds %d bytes", ErrRepositoryMergeMalformedJobs, repositoryMergeJobsBodyLimit)
	}
	return nil
}

type mergeJobsLimitReader struct {
	reader   *bufio.Reader
	limit    int
	read     int
	overflow bool
}

func (r *mergeJobsLimitReader) Read(p []byte) (int, error) {
	// Permit exactly one byte past the configured limit. This is the bounded
	// limit+1 probe that distinguishes an exactly-at-limit response from an
	// oversized response without retaining an unbounded body.
	if r.read > r.limit {
		r.overflow = true
		return 0, errors.New("bounded response exceeded limit")
	}
	maxRead := r.limit + 1 - r.read
	if len(p) > maxRead {
		p = p[:maxRead]
	}
	n, err := r.reader.Read(p)
	r.read += n
	if r.read > r.limit {
		r.overflow = true
	}
	return n, err
}

type mergeJobRow struct {
	ID           int64
	RepoPath     string
	Status       string
	PanelRole    string
	PanelRunUUID string
	JobType      string
	PanelSummary *mergePanelSummary
}

type mergePanelSummary struct {
	RunUUID         string
	MembersTotal    int64
	MembersTerminal int64
}

func decodeMergeJobsResponse(decoder *json.Decoder, sourcePath string, expectedCount int64) error {
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	if first != json.Delim('{') {
		return errors.New("top-level response must be an object")
	}
	seen := map[string]bool{}
	var jobsCount int64
	var hasMore *bool
	for decoder.More() {
		key, err := decodeObjectKey(decoder)
		if err != nil {
			return err
		}
		if seen[key] {
			return fmt.Errorf("duplicate field %q", key)
		}
		seen[key] = true
		switch key {
		case "jobs":
			count, err := decodeMergeJobsArray(decoder, sourcePath)
			if err != nil {
				return err
			}
			jobsCount = count
		case "has_more":
			var value bool
			if err := decoder.Decode(&value); err != nil {
				return fmt.Errorf("has_more: %w", err)
			}
			hasMore = &value
		case "stats":
			if err := skipMergeJSONValue(decoder); err != nil {
				return fmt.Errorf("stats: %w", err)
			}
		default:
			return fmt.Errorf("unknown top-level field %q", key)
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return errors.New("malformed top-level object")
	}
	if !seen["jobs"] || !seen["has_more"] || hasMore == nil {
		return errors.New("jobs and has_more are required")
	}
	if *hasMore {
		return errors.New("job list is truncated")
	}
	if jobsCount != expectedCount {
		return fmt.Errorf("%w: represented jobs %d does not equal repository count %d", repomerge.ErrSourceJobMismatch, jobsCount, expectedCount)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON: %w", err)
	}
	return nil
}

func decodeMergeJobsArray(decoder *json.Decoder, sourcePath string) (int64, error) {
	first, err := decoder.Token()
	if err != nil {
		return 0, err
	}
	if first != json.Delim('[') {
		return 0, errors.New("jobs must be an array")
	}
	var represented int64
	seenIDs := make(map[int64]struct{})
	seenPanelRuns := make(map[string]struct{})
	for decoder.More() {
		row, err := decodeMergeJob(decoder)
		if err != nil {
			return 0, err
		}
		if row.Status == "queued" || row.Status == "running" {
			return 0, ErrRepositoryMergeActiveJobs
		}
		if !gitx.SameRepositoryPath(row.RepoPath, sourcePath) {
			return 0, fmt.Errorf("%w: job %d repository path mismatch", repomerge.ErrSourceJobMismatch, row.ID)
		}
		if _, exists := seenIDs[row.ID]; exists {
			return 0, fmt.Errorf("duplicate job id %d", row.ID)
		}
		seenIDs[row.ID] = struct{}{}
		if row.PanelRole == "member" {
			// The endpoint is intentionally parent-only. Seeing a member means
			// the upstream representation is not one we can account for safely.
			return 0, errors.New("panel member row was returned without its parent")
		}
		if row.PanelRole == "synthesis" {
			if row.JobType != "synthesis" || row.PanelRunUUID == "" || row.PanelSummary == nil || row.PanelSummary.RunUUID != row.PanelRunUUID || row.PanelSummary.MembersTerminal != row.PanelSummary.MembersTotal {
				return 0, errors.New("panel synthesis summary is missing or incomplete")
			}
			if _, exists := seenPanelRuns[row.PanelRunUUID]; exists {
				return 0, fmt.Errorf("duplicate panel synthesis run %q", row.PanelRunUUID)
			}
			seenPanelRuns[row.PanelRunUUID] = struct{}{}
			if row.PanelSummary.MembersTotal < 0 || row.PanelSummary.MembersTotal > repoinventory.MaxSafeInteger {
				return 0, errors.New("panel member count is outside the safe integer range")
			}
			if represented > repoinventory.MaxSafeInteger-row.PanelSummary.MembersTotal-1 {
				return 0, errors.New("represented job count exceeds safe integer range")
			}
			represented += row.PanelSummary.MembersTotal + 1
		} else if row.PanelRole == "" {
			if row.PanelSummary != nil || row.PanelRunUUID != "" || row.JobType == "synthesis" {
				return 0, errors.New("non-panel job has panel metadata")
			}
			if represented == repoinventory.MaxSafeInteger {
				return 0, errors.New("represented job count exceeds safe integer range")
			}
			represented++
		} else {
			return 0, fmt.Errorf("unknown panel role %q", row.PanelRole)
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim(']') {
		return 0, errors.New("malformed jobs array")
	}
	return represented, nil
}

func decodeMergeJob(decoder *json.Decoder) (mergeJobRow, error) {
	first, err := decoder.Token()
	if err != nil {
		return mergeJobRow{}, err
	}
	if first != json.Delim('{') {
		return mergeJobRow{}, errors.New("job must be an object")
	}
	row := mergeJobRow{}
	seen := map[string]bool{}
	statusSeen := false
	for decoder.More() {
		key, err := decodeObjectKey(decoder)
		if err != nil {
			return mergeJobRow{}, err
		}
		if seen[key] {
			return mergeJobRow{}, fmt.Errorf("duplicate job field %q", key)
		}
		seen[key] = true
		if !knownMergeJobField(key) {
			return mergeJobRow{}, fmt.Errorf("unknown job field %q", key)
		}
		switch key {
		case "id":
			row.ID, err = decodePositiveInteger(decoder, "id")
		case "repo_path":
			row.RepoPath, err = decodeRequiredString(decoder, "repo_path")
		case "status":
			row.Status, err = decodeRequiredString(decoder, "status")
			statusSeen = err == nil
			if err == nil && !knownMergeJobStatus(row.Status) {
				return mergeJobRow{}, fmt.Errorf("unknown job status %q", row.Status)
			}
			if err == nil && (row.Status == "queued" || row.Status == "running") {
				// Status is deliberately decoded before prompt. Returning here
				// closes the response without reading or retaining the prompt.
				return mergeJobRow{}, ErrRepositoryMergeActiveJobs
			}
		case "panel_role":
			row.PanelRole, err = decodeString(decoder, "panel_role")
		case "panel_run_uuid":
			row.PanelRunUUID, err = decodeString(decoder, "panel_run_uuid")
		case "job_type":
			row.JobType, err = decodeString(decoder, "job_type")
		case "panel_summary":
			row.PanelSummary, err = decodeMergePanelSummary(decoder)
		case "prompt":
			if !statusSeen {
				return mergeJobRow{}, errors.New("job status must precede prompt")
			}
			err = skipMergeJSONValue(decoder)
		default:
			err = skipMergeJSONValue(decoder)
		}
		if err != nil {
			return mergeJobRow{}, fmt.Errorf("job field %q: %w", key, err)
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return mergeJobRow{}, errors.New("malformed job object")
	}
	if !seen["id"] || !seen["repo_path"] || !seen["status"] || row.ID <= 0 || row.RepoPath == "" || row.Status == "" {
		return mergeJobRow{}, errors.New("job id, repo_path, and status are required")
	}
	return row, nil
}

func decodeMergePanelSummary(decoder *json.Decoder) (*mergePanelSummary, error) {
	first, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if first != json.Delim('{') {
		return nil, errors.New("panel_summary must be an object")
	}
	value := &mergePanelSummary{}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decodeObjectKey(decoder)
		if err != nil {
			return nil, err
		}
		if seen[key] {
			return nil, fmt.Errorf("duplicate panel summary field %q", key)
		}
		seen[key] = true
		switch key {
		case "panel_run_uuid":
			value.RunUUID, err = decodeString(decoder, key)
		case "members_total":
			value.MembersTotal, err = decodeNonNegativeInteger(decoder, key)
		case "members_terminal":
			value.MembersTerminal, err = decodeNonNegativeInteger(decoder, key)
		case "members_succeeded", "members_failed", "members_canceled", "members_skipped", "members_with_cost", "members_cost_usd", "members_cost_complete", "first_started_at":
			err = skipMergeJSONValue(decoder)
		default:
			return nil, fmt.Errorf("unknown panel summary field %q", key)
		}
		if err != nil {
			return nil, err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return nil, errors.New("malformed panel summary")
	}
	if !seen["panel_run_uuid"] || !seen["members_total"] || !seen["members_terminal"] || value.RunUUID == "" {
		return nil, errors.New("panel summary identity/counts are required")
	}
	return value, nil
}

func decodeObjectKey(decoder *json.Decoder) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	key, ok := token.(string)
	if !ok {
		return "", errors.New("object key is not a string")
	}
	return key, nil
}

func decodeRequiredString(decoder *json.Decoder, field string) (string, error) {
	value, err := decodeString(decoder, field)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", field)
	}
	return value, nil
}

func decodeString(decoder *json.Decoder, field string) (string, error) {
	var value string
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("%s must be a string", field)
	}
	return value, nil
}

func decodePositiveInteger(decoder *json.Decoder, field string) (int64, error) {
	value, err := decodeNonNegativeInteger(decoder, field)
	if err != nil {
		return 0, err
	}
	if value == 0 {
		return 0, fmt.Errorf("%s must be positive", field)
	}
	return value, nil
}

func decodeNonNegativeInteger(decoder *json.Decoder, field string) (int64, error) {
	token, err := decoder.Token()
	if err != nil {
		return 0, err
	}
	number, ok := token.(json.Number)
	if !ok || strings.ContainsAny(string(number), ".eE+-") {
		return 0, fmt.Errorf("%s must be a non-negative integer", field)
	}
	value, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil || value < 0 || value > repoinventory.MaxSafeInteger {
		return 0, fmt.Errorf("%s is outside the safe integer range", field)
	}
	return value, nil
}

func skipMergeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok || (delim != '{' && delim != '[') {
		return nil
	}
	depth := 1
	for depth > 0 {
		next, err := decoder.Token()
		if err != nil {
			return err
		}
		if d, ok := next.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}

func knownMergeJobStatus(status string) bool {
	switch status {
	case "done", "applied", "rebased", "failed", "canceled", "skipped", "queued", "running":
		return true
	default:
		return false
	}
}

func knownMergeJobField(field string) bool {
	switch field {
	case "id", "repo_id", "sha", "author", "subject", "timestamp", "created_at", "commit_id", "git_ref", "branch", "session_id", "agent", "model", "provider", "requested_model", "requested_provider", "reasoning", "job_type", "status", "enqueued_at", "started_at", "finished_at", "worker_id", "error", "prompt", "retry_count", "diff_content", "dirty_files", "agentic", "prompt_prebuilt", "review_type", "patch_id", "output_prefix", "skip_reason", "source", "parent_job_id", "patch", "worktree_path", "command_line", "min_severity", "backup_agent", "backup_model", "panel_run_uuid", "panel_role", "panel_name", "panel_member_name", "panel_member_index", "panel_member_config_json", "claim_blocked", "token_usage", "uuid", "source_machine_id", "updated_at", "synced_at", "repo_path", "repo_name", "commit_subject", "closed", "verdict", "panel_summary":
		return true
	default:
		return false
	}
}
