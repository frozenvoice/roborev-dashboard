package app

import (
	"net/http"
	"strings"
)

// General Refine is intentionally not exposed as a Dashboard mutation. The
// v0.64 explicit-ID Fix command can implicitly discover, start, restart, and
// repair a daemon (and may normalize core.hooksPath). There is no lifecycle-
// free current-checkout contract that this server can authorize safely.
// Keep this message path-free: it is safe to show in the browser.
const generalRefineFixLifecycleBlockedMessage = "RoboRev v0.64.0's fix command can implicitly manage daemon lifecycle, so Dashboard execution is disabled."

type generalRefinePreviewBody struct {
	RepositoryIdentity string `json:"repositoryIdentity"`
}

type generalRefineStartBody struct {
	PreviewToken string `json:"previewToken"`
}

type generalRefineListResponse struct {
	Runs []any `json:"runs"`
}

// generalRefinePreview is a typed tombstone. It deliberately performs no
// registry, RoboRev, process, daemon, or lifecycle work after the existing
// same-origin/CSRF wrapper has authorized the request.
func (s *Server) generalRefinePreview(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body generalRefinePreviewBody
	if !decodeUniqueBody(w, r, &body, "repositoryIdentity") {
		return
	}
	if strings.TrimSpace(body.RepositoryIdentity) == "" {
		writeError(w, http.StatusBadRequest, "repository identity is required", "repository")
		return
	}
	writeError(w, http.StatusNotImplemented, generalRefineFixLifecycleBlockedMessage, "general-refine-fix-lifecycle-blocked")
}

// generalRefineStart is a typed tombstone. In particular, it never claims a
// preview token: no General Refine run can be created by this build.
func (s *Server) generalRefineStart(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body generalRefineStartBody
	if !decodeUniqueBody(w, r, &body, "previewToken") {
		return
	}
	if strings.TrimSpace(body.PreviewToken) == "" {
		writeError(w, http.StatusBadRequest, "preview token is required", "request")
		return
	}
	writeError(w, http.StatusNotImplemented, generalRefineFixLifecycleBlockedMessage, "general-refine-fix-lifecycle-blocked")
}

// Historical reads remain harmless and path-free. No new General Refine run
// is created by this server, so the durable-looking response is empty.
func (s *Server) listGeneralRefines(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	identity, ok := strictGeneralRefineIdentityQuery(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "repository identity is required", "repository")
		return
	}
	if _, err := s.Registry.ResolveIdentity(identity); err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return
	}
	writeJSON(w, http.StatusOK, generalRefineListResponse{Runs: []any{}})
}

func (s *Server) getGeneralRefine(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	writeError(w, http.StatusNotFound, "General Refine run not found", "general-refine")
}

func (s *Server) cancelGeneralRefine(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	if !requireEmptyBody(w, r) {
		return
	}
	writeError(w, http.StatusNotFound, "General Refine run not found", "general-refine")
}

func strictGeneralRefineIdentityQuery(r *http.Request) (string, bool) {
	values, ok := r.URL.Query()["repositoryIdentity"]
	if !ok || len(values) != 1 || len(r.URL.Query()) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", false
	}
	return strings.TrimSpace(values[0]), true
}
