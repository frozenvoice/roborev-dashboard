package app

import (
	"errors"
	"net/http"
	"strings"

	"github.com/froze/roborev-dashboard/internal/repomove"
)

// repositoryMoveEligibilityBody is deliberately smaller than the preview
// request. Eligibility is a discovery check for one inventory source; it
// never accepts a destination or creates a preview token.
type repositoryMoveEligibilityBody struct {
	SourcePath *string `json:"sourcePath"`
}

// repositoryMoveEligibilityDTO is a strict, branch-shaped browser contract.
// The omitempty fields ensure false responses expose only their reason while
// successful responses expose only the fresh RoboRev source binding.
type repositoryMoveEligibilityDTO struct {
	Eligible   bool    `json:"eligible"`
	SourcePath string  `json:"sourcePath,omitempty"`
	SourceName *string `json:"sourceName,omitempty"`
	Reason     string  `json:"reason,omitempty"`
}

// repositoryMoveEligibility performs the user-triggered, read-only gate for
// path repair. It intentionally does not run during inventory loading: stale
// source probing is a filesystem operation and must happen only after an
// explicit user action. Preview and confirm remain the authoritative
// revalidation boundary.
func (s *Server) repositoryMoveEligibility(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "repository move eligibility does not accept query parameters", "request")
		return
	}
	var body repositoryMoveEligibilityBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.SourcePath == nil || strings.TrimSpace(*body.SourcePath) == "" {
		writeError(w, http.StatusBadRequest, "sourcePath is required", "request")
		return
	}

	// Capability/runtime validation precedes inventory and all source-side
	// observations, matching the preview's fail-closed adapter boundary.
	if _, ok := s.repositoryMoveAvailable(w, r.Context()); !ok {
		return
	}
	inventory, err := s.repositoryMoveInventory(r.Context())
	if err != nil {
		writeRepositoryMoveError(w, err)
		return
	}
	source, ok := resolveMoveSource(w, inventory, *body.SourcePath)
	if !ok {
		return
	}
	if s.Registry.Contains(source.RootPath) {
		writeJSON(w, http.StatusOK, repositoryMoveEligibilityDTO{Eligible: false, Reason: "dashboard-registered"})
		return
	}
	if err := repomove.SourceAbsent(s.repositoryMoveFileSystem(), source.RootPath); err != nil {
		if errors.Is(err, repomove.ErrSourceExists) {
			writeJSON(w, http.StatusOK, repositoryMoveEligibilityDTO{Eligible: false, Reason: "source-exists"})
			return
		}
		writeRepositoryMoveError(w, err)
		return
	}
	sourceName := source.Name
	writeJSON(w, http.StatusOK, repositoryMoveEligibilityDTO{Eligible: true, SourcePath: source.RootPath, SourceName: &sourceName})
}
