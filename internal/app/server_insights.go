package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/insights"
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
		writeError(w, http.StatusConflict, err.Error(), "insights-preview")
		return
	}
	writeJSON(w, http.StatusOK, preview)
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
	// identity and consumes it atomically before any fresh process preflight.
	// Keep the selected lease across that acceptance/launch boundary so a
	// repository switch cannot race the one permitted enqueue.
	if token := strings.TrimSpace(body.Token); token == "" {
		writeError(w, http.StatusBadRequest, "Insights confirmation token is required", "insights")
		return
	}
	identity, ok := s.Insights.TokenIdentity(body.Token)
	if !ok {
		writeError(w, http.StatusConflict, "Insights confirmation token is expired or already used", "insights")
		return
	}
	release, _, err := s.Registry.AcquireSelected(identity)
	if err != nil {
		writeError(w, http.StatusConflict, "selected repository changed; prepare Insights again", "repository")
		return
	}
	defer release()
	result, err := s.Insights.Start(r.Context(), body.Token)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error(), "insights")
		return
	}
	writeJSON(w, http.StatusAccepted, result)
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
		writeError(w, http.StatusBadGateway, err.Error(), "result")
		return
	}
	if len(raw) > maxInsightsMarkdown {
		writeError(w, http.StatusBadGateway, "Insights result exceeded bounded output", "result")
		return
	}
	markdown, err := decodeInsightsMarkdown(raw, meta.id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "result")
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
