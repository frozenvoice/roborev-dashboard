package app

import (
	"net/http"
	"net/url"
	"strings"
)

const reviewMetadataExportDisabledMessage = "RoboRev v0.64.0 lacks an atomic canonical-root identity for review export; selected-repository-only export is unsupported."

// reviewMetadataExport is an explicit endpoint tombstone. Exact v0.64 has no
// atomic canonical-root selector for the export query, so the
// selected-repository-only invariant cannot be provided in the presence of an
// external repository producer. Keep authorization and fresh repository
// inspection before the unsupported result so this endpoint preserves the
// same browser/registry boundary as other repository-scoped reads, while
// never reaching the RoboRev service or daemon.
func (s *Server) reviewMetadataExport(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "repository")
		return
	}
	identities, ok := query["repositoryIdentity"]
	if !ok || len(query) != 1 || len(identities) != 1 || strings.TrimSpace(identities[0]) == "" {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "repository")
		return
	}
	registeredPath, err := s.Registry.ResolveIdentity(identities[0])
	if err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return
	}
	if _, err := s.Registry.Inspect(r.Context(), registeredPath); err != nil {
		writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
		return
	}
	writeError(w, http.StatusNotImplemented, reviewMetadataExportDisabledMessage, "review-export-repository-isolation-blocked")
}
