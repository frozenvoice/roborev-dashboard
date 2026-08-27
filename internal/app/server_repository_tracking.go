package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/repositorytracking"
)

const repositoryTrackingVerificationTimeout = 10 * time.Second

type repositoryTrackingPreviewBody struct {
	RepositoryIdentity string `json:"repositoryIdentity"`
}

type repositoryTrackingExecuteBody struct {
	Token string `json:"token"`
}

type repositoryTrackingPreviewDTO struct {
	Version            string    `json:"version"`
	RepositoryIdentity string    `json:"repositoryIdentity"`
	Action             string    `json:"action"`
	Target             string    `json:"target"`
	Summary            string    `json:"summary"`
	Token              string    `json:"token"`
	ExpiresAt          time.Time `json:"expiresAt"`
}

type repositoryTrackingResultDTO struct {
	Status string `json:"status"`
}

func (s *Server) authorizeRepositoryTracking(w http.ResponseWriter, r *http.Request) bool {
	if !s.authorizeSameOrigin(w, r) {
		return false
	}
	if !exactHeaderValue(r.Header, "Sec-Fetch-Site", "same-origin") ||
		!exactHeaderValue(r.Header, "Sec-Fetch-Mode", "cors") ||
		!exactHeaderValue(r.Header, "Sec-Fetch-Dest", "empty") {
		writeError(w, http.StatusForbidden, "same-origin browser request required", "origin")
		return false
	}
	if !exactHeaderValue(r.Header, "X-CSRF-Token", s.csrfToken) {
		writeError(w, http.StatusForbidden, "invalid CSRF token", "csrf")
		return false
	}
	return true
}

func (s *Server) repositoryTrackingPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorizeRepositoryTracking(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body repositoryTrackingPreviewBody
	if !decodeUniqueBody(w, r, &body, "repositoryIdentity") {
		return
	}
	alias := body.RepositoryIdentity
	if alias == "" || strings.TrimSpace(alias) != alias {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is invalid", "repository")
		return
	}
	legacy, selectedRoot, ok := s.resolveRepositoryMutationAlias(alias)
	if !ok {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return
	}
	releaseSelected, leasedRoot, err := s.Registry.AcquireSelected(legacy)
	if err != nil || !gitx.SameRepositoryPath(leasedRoot, selectedRoot) {
		if releaseSelected != nil {
			releaseSelected()
		}
		writeError(w, http.StatusConflict, "repository selection changed", "repository")
		return
	}
	defer releaseSelected()
	transport := s.RepositoryTracking
	if transport == nil || s.repositoryTracking == nil {
		writeError(w, http.StatusServiceUnavailable, "repository tracking is unavailable", "repository-tracking")
		return
	}

	// Registry inspection is the sole source of selected-root authority. The
	// browser alias is never converted into a path and never enters the daemon
	// request body.
	inspectedRoot, err := s.Registry.InspectRoot(r.Context(), selectedRoot)
	if err != nil || !gitx.SameRepositoryPath(inspectedRoot, selectedRoot) {
		writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
		return
	}
	effectiveRoot, err := s.Registry.RegistrationRoot(r.Context(), selectedRoot)
	if err != nil {
		writeError(w, http.StatusConflict, "repository tracking preview is unavailable", "repository-tracking")
		return
	}
	selectedSnapshot, effectiveSnapshot, err := s.captureTrackingRoots(selectedRoot, effectiveRoot)
	if err != nil {
		writeError(w, http.StatusConflict, "repository tracking preview is unavailable", "repository-tracking")
		return
	}
	preflight, err := transport.RepositoryTrackingPreflight(r.Context())
	if err != nil || !preflight.Binding.Valid() {
		writeError(w, http.StatusConflict, "repository tracking preview is unavailable", "repository-tracking")
		return
	}
	match, err := preflight.Inventory.MatchRoot(effectiveRoot)
	if err != nil {
		if errors.Is(err, repoinventory.ErrAmbiguousRoot) {
			writeError(w, http.StatusConflict, "repository tracking preview is unavailable", "repository-tracking")
			return
		}
		writeError(w, http.StatusConflict, "repository tracking preview is unavailable", "repository-tracking")
		return
	}
	if match != 0 {
		writeError(w, http.StatusConflict, "Repository is already tracked by RoboRev.", "repository-tracking")
		return
	}
	expires := s.repositoryTracking.ExpiresAt()
	record := repositorytracking.Record{
		Alias:                 alias,
		Identity:              legacy,
		SelectedRoot:          selectedRoot,
		EffectiveRoot:         effectiveRoot,
		SelectedRootSnapshot:  selectedSnapshot,
		EffectiveRootSnapshot: effectiveSnapshot,
		Binding:               preflight.Binding,
		Inventory:             preflight.Inventory,
		ExpiresAt:             expires,
	}
	token, err := s.repositoryTracking.Put(record)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "repository tracking preview is unavailable", "repository-tracking")
		return
	}
	writeJSON(w, http.StatusOK, repositoryTrackingPreviewDTO{
		Version:            repositorytracking.Version,
		RepositoryIdentity: alias,
		Action:             "register-repository",
		Target:             "RoboRev tracking",
		Summary:            "Register the selected repository with the running RoboRev daemon.",
		Token:              token,
		ExpiresAt:          expires,
	})
}

func (s *Server) repositoryTrackingExecute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorizeRepositoryTracking(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body repositoryTrackingExecuteBody
	if !decodeUniqueBody(w, r, &body, "token") {
		return
	}
	if body.Token == "" || strings.TrimSpace(body.Token) != body.Token {
		writeError(w, http.StatusBadRequest, "confirmation token is required", "request")
		return
	}
	if s.repositoryTracking == nil {
		writeError(w, http.StatusServiceUnavailable, "repository tracking is unavailable", "repository-tracking")
		return
	}
	record, ok := s.repositoryTracking.Claim(body.Token)
	if !ok {
		writeError(w, http.StatusConflict, "confirmation token is missing or expired", "stale")
		return
	}
	if s.RepositoryTracking == nil {
		writeError(w, http.StatusServiceUnavailable, "repository tracking is unavailable", "repository-tracking")
		return
	}
	// Claim precedes every authority, gate, lease, and probe check. A busy or
	// stale execute can never replay a confirmation token.
	legacy, selectedRoot, ok := s.resolveRepositoryMutationAlias(record.Alias)
	if !ok || legacy != record.Identity || !gitx.SameRepositoryPath(selectedRoot, record.SelectedRoot) {
		writeError(w, http.StatusConflict, "repository tracking preview is stale", "stale")
		return
	}
	if s.Gate == nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	releaseGlobal, err := s.Gate.AcquireFix()
	if err != nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	defer releaseGlobal()
	releaseRepositories, err := s.Gate.AcquireRepositories(gitx.RepositoryKey(record.SelectedRoot), gitx.RepositoryKey(record.EffectiveRoot))
	if err != nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	defer releaseRepositories()
	releaseSelected, leasedRoot, err := s.Registry.AcquireSelected(record.Identity)
	if err != nil || !gitx.SameRepositoryPath(leasedRoot, record.SelectedRoot) {
		if releaseSelected != nil {
			releaseSelected()
		}
		writeError(w, http.StatusConflict, "repository selection changed", "repository")
		return
	}
	defer releaseSelected()
	if err := r.Context().Err(); err != nil {
		writeError(w, http.StatusRequestTimeout, "repository tracking was canceled", "repository-tracking")
		return
	}

	// Every slow authority probe is completed while the leases are held.
	inspectedRoot, err := s.Registry.InspectRoot(r.Context(), record.SelectedRoot)
	if err != nil || !gitx.SameRepositoryPath(inspectedRoot, record.SelectedRoot) {
		writeError(w, http.StatusConflict, "repository tracking preview is stale", "stale")
		return
	}
	effectiveRoot, err := s.Registry.RegistrationRoot(r.Context(), record.SelectedRoot)
	if err != nil || !gitx.SameRepositoryPath(effectiveRoot, record.EffectiveRoot) {
		writeError(w, http.StatusConflict, "repository tracking preview is stale", "stale")
		return
	}
	selectedSnapshot, effectiveSnapshot, err := s.captureTrackingRoots(record.SelectedRoot, effectiveRoot)
	if err != nil || !selectedSnapshot.Equal(record.SelectedRootSnapshot) || !effectiveSnapshot.Equal(record.EffectiveRootSnapshot) {
		writeError(w, http.StatusConflict, "repository tracking preview is stale", "stale")
		return
	}
	preflight, err := s.RepositoryTracking.RepositoryTrackingPreflight(r.Context())
	if err != nil || !preflight.Binding.Equal(record.Binding) {
		writeError(w, http.StatusConflict, "repository tracking preview is stale", "stale")
		return
	}
	match, err := preflight.Inventory.MatchRoot(record.EffectiveRoot)
	if err != nil {
		writeError(w, http.StatusConflict, "repository tracking preview is stale", "stale")
		return
	}
	if match != 0 {
		writeError(w, http.StatusConflict, "Repository is already tracked by RoboRev.", "repository-tracking")
		return
	}
	if err := r.Context().Err(); err != nil {
		writeError(w, http.StatusRequestTimeout, "repository tracking was canceled", "repository-tracking")
		return
	}

	// FINAL_ROOT_GUARD is executed by the transport after its fixed payload,
	// request, and client are prepared. It is intentionally the last
	// filesystem operation before the one typed daemon POST: there is no Git,
	// CLI, health, inventory, or endpoint probe between this guard and HTTP.Do.
	finalGuard := func(ctx context.Context) error {
		finalSelected, finalEffective, guardErr := s.captureTrackingRoots(record.SelectedRoot, record.EffectiveRoot)
		if guardErr != nil || !finalSelected.Equal(record.SelectedRootSnapshot) || !finalEffective.Equal(record.EffectiveRootSnapshot) {
			return errors.New("repository tracking preview is stale")
		}
		return nil
	}
	registration, requestErr := s.RepositoryTracking.RegisterRepository(r.Context(), record.SelectedRoot, record.Binding, finalGuard)
	if !registration.Attempted {
		writeError(w, http.StatusConflict, "repository tracking preview is stale", "stale")
		return
	}

	// Once HTTP.Do was entered, the browser request no longer controls
	// verification. Keep gates and the selected lease held through this bounded
	// detached readback so a response/transport ambiguity is classified from
	// fresh authority rather than guessed from the HTTP result alone.
	verifyCtx, cancel := context.WithTimeout(context.Background(), repositoryTrackingVerificationTimeout)
	defer cancel()
	postSelected, postEffective, rootErr := s.captureTrackingRoots(record.SelectedRoot, record.EffectiveRoot)
	rootOK := rootErr == nil && postSelected.Equal(record.SelectedRootSnapshot) && postEffective.Equal(record.EffectiveRootSnapshot)
	postRoot, postRootErr := s.Registry.InspectRoot(verifyCtx, record.SelectedRoot)
	postEffectiveRoot, postEffectiveRootErr := s.Registry.RegistrationRoot(verifyCtx, record.SelectedRoot)
	rootOK = rootOK && postRootErr == nil && postEffectiveRootErr == nil && gitx.SameRepositoryPath(postRoot, record.SelectedRoot) && gitx.SameRepositoryPath(postEffectiveRoot, record.EffectiveRoot)
	post, postErr := s.RepositoryTracking.RepositoryTrackingPreflight(verifyCtx)
	tracked := false
	if postErr == nil && post.Binding.Equal(record.Binding) {
		count, matchErr := post.Inventory.MatchRoot(record.EffectiveRoot)
		tracked = matchErr == nil && count == 1
	}
	// A root replacement can occur while the detached daemon probes are in
	// flight. Re-witness both roots after those probes before crediting the
	// registration as verified.
	postSelectedAfter, postEffectiveAfter, afterErr := s.captureTrackingRoots(record.SelectedRoot, record.EffectiveRoot)
	rootOK = rootOK && afterErr == nil && postSelectedAfter.Equal(record.SelectedRootSnapshot) && postEffectiveAfter.Equal(record.EffectiveRootSnapshot)
	verified := rootOK && tracked && (registration.StatusCode == http.StatusOK || registration.StatusCode == 0 || (registration.StatusCode >= http.StatusInternalServerError && registration.StatusCode <= 599))
	if verified {
		writeJSON(w, http.StatusOK, repositoryTrackingResultDTO{Status: "verified-success"})
		return
	}
	_ = requestErr // response details are intentionally never exposed
	writeJSON(w, http.StatusOK, repositoryTrackingResultDTO{Status: "failed-unverified"})
}

func (s *Server) captureTrackingRoots(selectedRoot, effectiveRoot string) (pathsafety.Snapshot, pathsafety.Snapshot, error) {
	fs := s.RepositoryTrackingFS
	if fs == nil {
		fs = pathsafety.OSFileSystem{}
	}
	selected, err := pathsafety.CaptureDirectory(fs, selectedRoot)
	if err != nil {
		return pathsafety.Snapshot{}, pathsafety.Snapshot{}, err
	}
	if gitx.SameRepositoryPath(selectedRoot, effectiveRoot) {
		return selected, selected, nil
	}
	effective, err := pathsafety.CaptureDirectory(fs, effectiveRoot)
	if err != nil {
		return pathsafety.Snapshot{}, pathsafety.Snapshot{}, err
	}
	return selected, effective, nil
}
