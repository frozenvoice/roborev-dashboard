package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/insights"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

const maxInsightsMarkdown = 4 << 20

type insightsPreviewBody struct {
	RepositoryIdentity string               `json:"repositoryIdentity"`
	Window             insights.Window      `json:"window"`
	BranchScope        insights.BranchScope `json:"branchScope"`
}

type insightsStartBody struct {
	Token string `json:"token"`
}

func (s *Server) selectedInsightsIdentity(w http.ResponseWriter, expected string) bool {
	selected, err := s.Registry.Identity("")
	if err != nil || strings.TrimSpace(expected) == "" || selected != strings.TrimSpace(expected) {
		writeError(w, http.StatusConflict, "selected repository changed; prepare Insights again", "repository")
		return false
	}
	return true
}

func (s *Server) insightsPreview(w http.ResponseWriter, r *http.Request) {
	if s.Insights == nil {
		writeError(w, http.StatusServiceUnavailable, "Guideline Insights is unavailable", "insights")
		return
	}
	var body insightsPreviewBody
	if !decodeBody(w, r, &body) {
		return
	}
	if !s.selectedInsightsIdentity(w, body.RepositoryIdentity) {
		return
	}
	preview, err := s.Insights.Preview(r.Context(), insights.PreviewRequest{RepositoryIdentity: strings.TrimSpace(body.RepositoryIdentity), Window: body.Window, BranchScope: body.BranchScope})
	if err != nil {
		writeError(w, http.StatusConflict, safeInsightsError(err), "insights-preview")
		return
	}
	writeJSON(w, http.StatusOK, browserInsightsPreview(preview))
}

func (s *Server) startInsights(w http.ResponseWriter, r *http.Request) {
	if s.Insights == nil {
		writeError(w, http.StatusServiceUnavailable, "Guideline Insights is unavailable", "insights")
		return
	}
	var body insightsStartBody
	if !decodeBody(w, r, &body) {
		return
	}
	// The token itself is opaque. The workflow manager resolves its bound
	// identity and consumes it atomically before any fresh transport checks.
	if token := strings.TrimSpace(body.Token); token == "" {
		writeError(w, http.StatusBadRequest, "Insights confirmation token is required", "insights")
		return
	}
	identity, ok := s.Insights.TokenIdentity(body.Token)
	if !ok {
		writeError(w, http.StatusConflict, "Insights confirmation token is expired or already used", "insights")
		return
	}
	if s.Gate == nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	// Insights enqueue is an agentic daemon operation. Keep the same global
	// gate ordering as every other mutating surface so daemon control, review,
	// and setup cannot race the accepted /api/enqueue request.
	releaseGlobal, gateErr := s.Gate.AcquireFix()
	if gateErr != nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	defer releaseGlobal()
	releaseRepository, gateErr := s.Gate.AcquireRepository(gitx.RepositoryKey(identity))
	if gateErr != nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	defer releaseRepository()
	release, _, err := s.Registry.AcquireSelected(identity)
	if err != nil {
		writeError(w, http.StatusConflict, "selected repository changed; prepare Insights again", "repository")
		return
	}
	defer release()
	// Once the token, global gate, repository gate, and selected lease have
	// been accepted, the daemon enqueue is an independent background job. Do
	// not let a browser disconnect cancel the accepted launch boundary.
	result, err := s.Insights.Start(context.WithoutCancel(r.Context()), body.Token)
	if err != nil {
		writeError(w, http.StatusConflict, safeInsightsError(err), "insights")
		return
	}
	writeJSON(w, http.StatusAccepted, browserInsightsLaunch(result))
}

type browserInsightsPreviewDTO struct {
	RepositoryIdentity string                 `json:"repositoryIdentity"`
	RepositoryName     string                 `json:"repositoryName"`
	Window             insights.Window        `json:"window"`
	BranchScope        insights.BranchScope   `json:"branchScope"`
	Branch             string                 `json:"branch,omitempty"`
	Command            browserInsightsCommand `json:"command"`
	Warnings           []string               `json:"warnings"`
	Token              string                 `json:"token"`
	ExpiresAt          time.Time              `json:"expiresAt"`
}

type browserInsightsCommand struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

func browserInsightsPreview(preview insights.Preview) browserInsightsPreviewDTO {
	args := []string{"insights"}
	if preview.BranchScope == insights.BranchCurrent {
		args = append(args, "--branch", preview.Branch)
	}
	args = append(args, "--since", string(preview.Window), "--json")
	return browserInsightsPreviewDTO{RepositoryIdentity: preview.RepositoryIdentity, RepositoryName: preview.RepositoryName, Window: preview.Window, BranchScope: preview.BranchScope, Branch: preview.Branch, Command: browserInsightsCommand{Executable: "roborev", Args: args}, Warnings: append([]string(nil), preview.Warnings...), Token: preview.Token, ExpiresAt: preview.ExpiresAt}
}

func safeInsightsError(err error) string {
	if errors.Is(err, roborev.ErrBindingChanged) {
		return "RoboRev binding changed; prepare Insights again"
	}
	if strings.Contains(strings.ToLower(err.Error()), "repository") {
		return "repository is not authorized"
	}
	return "Guideline Insights could not be prepared"
}

type browserInsightsLaunchDTO struct {
	Status  string    `json:"status"`
	JobID   int64     `json:"jobId,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Since   time.Time `json:"since,omitempty"`
	Skipped bool      `json:"skipped,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Message string    `json:"message,omitempty"`
}

func browserInsightsLaunch(result insights.StartResponse) browserInsightsLaunchDTO {
	response := browserInsightsLaunchDTO{Status: result.Status}
	switch result.Status {
	case "queued":
		response.JobID = result.JobID
		response.Since = result.Since
		if safeInsightsAgent(result.Agent) {
			response.Agent = result.Agent
		} else {
			response.Agent = "configured"
		}
	case "skipped":
		response.Skipped = true
		response.Since = result.Since
		response.Reason = "No eligible failing reviews were queued."
	case "unverified":
		response.Message = "Launch outcome could not be verified. A RoboRev Insights job may already have been queued."
	default:
		response.Status = "unverified"
		response.Message = "Launch outcome could not be verified. A RoboRev Insights job may already have been queued."
	}
	return response
}

func safeInsightsAgent(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return true
}

func strictInsightIdentity(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !exactQuery(r, "repositoryIdentity") {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "repository")
		return "", false
	}
	identity := strings.TrimSpace(r.URL.Query().Get("repositoryIdentity"))
	if identity == "" {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "repository")
		return "", false
	}
	return identity, true
}

func exactQuery(r *http.Request, key string) bool {
	values := r.URL.Query()
	items, ok := values[key]
	return len(values) == 1 && ok && len(items) == 1 && strings.TrimSpace(items[0]) != ""
}

func (s *Server) insightMetadata(w http.ResponseWriter, r *http.Request) (roborevJobMetadata, bool) {
	rawID := r.PathValue("id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 || id > insights.MaxJobID || strconv.FormatInt(id, 10) != rawID {
		writeError(w, http.StatusBadRequest, "invalid job ID", "request")
		return roborevJobMetadata{}, false
	}
	identity, ok := strictInsightIdentity(w, r)
	if !ok {
		return roborevJobMetadata{}, false
	}
	path, err := s.Registry.ResolveIdentity(identity)
	if err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return roborevJobMetadata{}, false
	}
	metadata, err := s.RoboRev.JobMetadata(r.Context(), id)
	if err != nil || metadata.ID != id {
		writeError(w, http.StatusNotFound, "Insights job not found", "job")
		return roborevJobMetadata{}, false
	}
	if !gitx.SameRepositoryPath(metadata.RepoPath, path) {
		writeError(w, http.StatusForbidden, "job belongs to a different repository", "repository")
		return roborevJobMetadata{}, false
	}
	if metadata.JobType != "insights" {
		writeError(w, http.StatusNotFound, "job is not a Guideline Insights job", "job")
		return roborevJobMetadata{}, false
	}
	status := strings.ToLower(strings.TrimSpace(metadata.Status))
	if status == "" {
		prefix, prefixErr := s.RoboRev.JobOutputStatus(r.Context(), id)
		if prefixErr == nil && prefix.JobID == id {
			status = strings.ToLower(strings.TrimSpace(prefix.Status))
		}
	}
	if !allowedInsightsStatus(status) {
		writeError(w, http.StatusBadGateway, "RoboRev returned an unsupported Insights status", "status")
		return roborevJobMetadata{}, false
	}
	return roborevJobMetadata{id: id, status: status, path: path}, true
}

type roborevJobMetadata struct {
	id     int64
	status string
	path   string
}

// The helper above keeps status authorization local, while this adapter value
// is populated below without exposing prompt/output fields to the response.
// A concrete metadata copy is used by handlers via the dedicated fields.
func allowedInsightsStatus(status string) bool {
	switch status {
	case "queued", "running", "done", "failed", "canceled":
		return true
	default:
		return false
	}
}

func (s *Server) insightsStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	meta, ok := s.insightMetadata(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobId": meta.id, "status": meta.status})
}

func (s *Server) insightsResult(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	meta, ok := s.insightMetadata(w, r)
	if !ok {
		return
	}
	if meta.status != "done" {
		writeError(w, http.StatusConflict, "Insights result is not complete", "result")
		return
	}
	raw, err := s.RoboRev.Show(r.Context(), meta.id)
	if err != nil {
		// The daemon's transport/error body may contain prompts, paths, or
		// provider diagnostics. Keep that detail on the server boundary and
		// expose only a stable browser-safe classification.
		writeError(w, http.StatusBadGateway, "Insights result could not be loaded", "result")
		return
	}
	if len(raw) > maxInsightsMarkdown {
		writeError(w, http.StatusBadGateway, "Insights result exceeded bounded output", "result")
		return
	}
	markdown, err := decodeInsightsMarkdown(raw, meta.id)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Insights result is invalid", "result")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobId": meta.id, "status": "done", "markdown": markdown})
}

func decodeInsightsMarkdown(raw []byte, expectedID int64) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", errors.New("malformed Insights result")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", errors.New("malformed Insights result: trailing JSON")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return "", errors.New("malformed Insights result: object required")
	}
	// show --json exposes the review row's top-level id separately from the
	// requested job_id. Only job_id is a job identity signal; accepting id (or
	// a nested review/job id) would confuse a review row ID with the Insights
	// job and could return another job's output.
	jobID, exists := object["job_id"]
	if !exists {
		return "", errors.New("Insights result job ID is missing")
	}
	if !jsonIDMatches(jobID, expectedID) {
		return "", errors.New("Insights result job ID does not match requested job")
	}
	for _, key := range []string{"markdown", "output", "text", "result"} {
		if text, ok := object[key].(string); ok {
			if len(text) > maxInsightsMarkdown || !utf8.ValidString(text) {
				return "", errors.New("Insights result exceeded bounded output")
			}
			return text, nil
		}
	}
	return "", errors.New("Insights result did not contain bounded Markdown output")
}

func jsonIDMatches(value any, expected int64) bool {
	switch number := value.(type) {
	case float64:
		return number == float64(expected) && number > 0 && number == float64(int64(number))
	case json.Number:
		parsed, err := strconv.ParseInt(string(number), 10, 64)
		return err == nil && parsed == expected
	default:
		return false
	}
}
