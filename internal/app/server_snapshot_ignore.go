package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/setupreadiness"
	"github.com/froze/roborev-dashboard/internal/snapshotignoremanagement"
)

const snapshotIgnoreAliasDomain = "roborev-dashboard/snapshot-ignore/v2\x00"

type snapshotIgnorePreviewBody struct {
	RepositoryIdentity string `json:"repositoryIdentity"`
}

type snapshotIgnoreExecuteBody struct {
	Token string `json:"token"`
}

type snapshotIgnorePreviewDTO struct {
	Version            string    `json:"version"`
	RepositoryIdentity string    `json:"repositoryIdentity"`
	Action             string    `json:"action"`
	Target             string    `json:"target"`
	Summary            string    `json:"summary"`
	Token              string    `json:"token"`
	ExpiresAt          time.Time `json:"expiresAt"`
}

type snapshotIgnoreResultDTO struct {
	Status string `json:"status"`
}

func (s *Server) snapshotIgnoreAlias(root string) string {
	mac := hmac.New(sha256.New, s.snapshotIgnoreKey[:])
	_, _ = mac.Write([]byte(snapshotIgnoreAliasDomain))
	_, _ = mac.Write([]byte(gitx.RepositoryKey(root)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// resolveSnapshotIgnoreAlias performs alias lookup only against the current
// selected repository. It never scans registered paths and uses constant-time
// comparison, preventing the browser alias from becoming a path oracle.
func (s *Server) resolveSnapshotIgnoreAlias(alias string) (string, string, bool) {
	if s == nil || s.Registry == nil || strings.TrimSpace(alias) == "" || alias != strings.TrimSpace(alias) {
		return "", "", false
	}
	selected := s.Registry.Selected()
	if selected == "" {
		return "", "", false
	}
	legacy, err := s.Registry.Identity(selected)
	if err != nil {
		return "", "", false
	}
	root, err := s.Registry.ResolveIdentity(legacy)
	if err != nil || !gitx.SameRepositoryPath(root, selected) {
		return "", "", false
	}
	expected := s.snapshotIgnoreAlias(root)
	if subtle.ConstantTimeCompare([]byte(alias), []byte(expected)) != 1 {
		return "", "", false
	}
	return legacy, root, true
}

func (s *Server) authorizeSnapshotIgnore(w http.ResponseWriter, r *http.Request) bool {
	if !s.authorizeSameOrigin(w, r) {
		return false
	}
	if !exactHeaderValue(r.Header, "X-CSRF-Token", s.csrfToken) {
		writeError(w, http.StatusForbidden, "invalid CSRF token", "csrf")
		return false
	}
	if !exactHeaderValue(r.Header, "Sec-Fetch-Site", "same-origin") ||
		!exactHeaderValue(r.Header, "Sec-Fetch-Mode", "cors") ||
		!exactHeaderValue(r.Header, "Sec-Fetch-Dest", "empty") {
		writeError(w, http.StatusForbidden, "same-origin browser request required", "origin")
		return false
	}
	return true
}

func (s *Server) snapshotIgnorePreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorizeSnapshotIgnore(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body snapshotIgnorePreviewBody
	if !decodeUniqueBody(w, r, &body, "repositoryIdentity") {
		return
	}
	alias := body.RepositoryIdentity
	if alias == "" || strings.TrimSpace(alias) != alias {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is invalid", "repository")
		return
	}
	legacy, root, ok := s.resolveSnapshotIgnoreAlias(alias)
	if !ok {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return
	}
	if s.SnapshotIgnore == nil || s.snapshotIgnoreWrites == nil {
		writeError(w, http.StatusServiceUnavailable, "snapshot ignore management is unavailable", "snapshot-ignore")
		return
	}
	releaseSelected, selectedRoot, err := s.Registry.AcquireSelected(legacy)
	if err != nil || !gitx.SameRepositoryPath(selectedRoot, root) {
		if releaseSelected != nil {
			releaseSelected()
		}
		writeError(w, http.StatusConflict, "repository selection changed", "repository")
		return
	}
	defer releaseSelected()
	resolver, ok := s.RoboRev.(interface {
		SnapshotDirectory(context.Context, string) (string, error)
	})
	if !ok {
		writeError(w, http.StatusConflict, "snapshot ignore preview is unavailable", "snapshot-ignore")
		return
	}
	configRaw, err := resolver.SnapshotDirectory(r.Context(), root)
	if err != nil {
		writeError(w, http.StatusConflict, "snapshot ignore preview is unavailable", "snapshot-ignore")
		return
	}
	relative, err := setupreadiness.ResolveSnapshotDir(configRaw)
	if err != nil {
		writeError(w, http.StatusConflict, "snapshot ignore preview is unavailable", "snapshot-ignore")
		return
	}
	if err := s.SnapshotIgnore.ValidateSnapshotDirectory(r.Context(), root, relative); err != nil {
		writeError(w, http.StatusConflict, "snapshot ignore preview is unavailable", "snapshot-ignore")
		return
	}
	state, continuation, err := s.Registry.InspectSnapshotIgnoreThroughTracked(r.Context(), legacy, relative)
	if err != nil || !gitx.SameRepositoryPath(state.Root, root) {
		writeError(w, http.StatusConflict, "snapshot ignore preview is unavailable", "snapshot-ignore")
		return
	}
	if state.Tracked {
		writeError(w, http.StatusConflict, "Tracked files exist under the snapshot directory.", "snapshot-ignore")
		return
	}
	if state.Detached || state.Branch == "" || !validFullCommitSHA(state.Head) || state.Dirty {
		writeError(w, http.StatusConflict, "snapshot ignore preview is unavailable", "snapshot-ignore")
		return
	}
	if _, err := snapshotignoremanagement.IgnorePatternForDir(root, relative); err != nil {
		writeError(w, http.StatusConflict, "snapshot ignore preview is unavailable", "snapshot-ignore")
		return
	}
	state, err = s.Registry.FinishSnapshotIgnoreInspection(r.Context(), legacy, root, continuation)
	if err != nil || !gitx.SameRepositoryPath(state.Root, root) {
		writeError(w, http.StatusConflict, "snapshot ignore preview is unavailable", "snapshot-ignore")
		return
	}
	if state.Ignored {
		writeError(w, http.StatusConflict, "snapshot directory is already ignored", "snapshot-ignore")
		return
	}
	plan, err := s.SnapshotIgnore.Plan(r.Context(), root, relative)
	if err != nil || !plan.WriteAvailable() || plan.Status() == snapshotignoreStatusDuplicate() {
		writeError(w, http.StatusConflict, "snapshot ignore preview is unavailable", "snapshot-ignore")
		return
	}
	expires := s.snapshotIgnoreWrites.expiresAt()
	record := snapshotIgnoreWriteRecord{Alias: alias, Identity: legacy, Root: root, ConfigRaw: configRaw, Relative: relative, Git: state, Plan: plan, Expires: expires}
	token, err := s.snapshotIgnoreWrites.put(record)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "snapshot ignore preview is unavailable", "snapshot-ignore")
		return
	}
	writeJSON(w, http.StatusOK, snapshotIgnorePreviewDTO{Version: snapshotignoremanagement.Version, RepositoryIdentity: alias, Action: "append-snapshot-ignore", Target: snapshotignoremanagement.Target, Summary: "Add the RoboRev snapshot directory to Git ignore rules.", Token: token, ExpiresAt: expires})
}

// Kept in this file to avoid exposing core status constants through the HTTP
// package while still making the duplicate/no-op gate explicit.
func snapshotignoreStatusDuplicate() snapshotignoremanagement.TargetStatus {
	return snapshotignoremanagement.TargetDuplicate
}

func (s *Server) snapshotIgnoreExecute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorizeSnapshotIgnore(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body snapshotIgnoreExecuteBody
	if !decodeUniqueBody(w, r, &body, "token") {
		return
	}
	if body.Token == "" || strings.TrimSpace(body.Token) != body.Token {
		writeError(w, http.StatusBadRequest, "confirmation token is required", "request")
		return
	}
	if s.snapshotIgnoreWrites == nil {
		writeError(w, http.StatusServiceUnavailable, "snapshot ignore management is unavailable", "snapshot-ignore")
		return
	}
	record, ok := s.snapshotIgnoreWrites.claim(body.Token)
	if !ok {
		writeError(w, http.StatusConflict, "confirmation token is missing or expired", "stale")
		return
	}
	// Claim is deliberately before capability/authority and gate checks so a
	// token can never be replayed after any execute attempt.
	if s.SnapshotIgnore == nil {
		writeError(w, http.StatusServiceUnavailable, "snapshot ignore management is unavailable", "snapshot-ignore")
		return
	}
	legacy, root, ok := s.resolveSnapshotIgnoreAlias(record.Alias)
	if !ok || legacy != record.Identity || !gitx.SameRepositoryPath(root, record.Root) {
		writeError(w, http.StatusConflict, "snapshot ignore preview is stale", "stale")
		return
	}
	if s.Gate == nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	releaseFix, err := s.Gate.AcquireFix()
	if err != nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	defer releaseFix()
	releaseRepo, err := s.Gate.AcquireRepository(gitx.RepositoryKey(record.Root))
	if err != nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	defer releaseRepo()
	releaseSelected, selectedRoot, err := s.Registry.AcquireSelected(record.Identity)
	if err != nil || !gitx.SameRepositoryPath(selectedRoot, record.Root) {
		if releaseSelected != nil {
			releaseSelected()
		}
		writeError(w, http.StatusConflict, "repository selection changed", "repository")
		return
	}
	defer releaseSelected()
	if err := r.Context().Err(); err != nil {
		writeError(w, http.StatusRequestTimeout, "snapshot ignore update was canceled", "snapshot-ignore")
		return
	}
	if !s.snapshotIgnoreFresh(r.Context(), record) {
		if err := r.Context().Err(); err != nil {
			writeError(w, http.StatusRequestTimeout, "snapshot ignore update was canceled", "snapshot-ignore")
		} else {
			writeError(w, http.StatusConflict, "snapshot ignore preview is stale", "stale")
		}
		return
	}
	result := s.SnapshotIgnore.Append(r.Context(), record.Plan)
	if result.Status == snapshotignoremanagement.StatusPreMutationCanceled {
		writeError(w, http.StatusRequestTimeout, "snapshot ignore update was canceled", "snapshot-ignore")
		return
	}
	if result.Status == snapshotignoremanagement.StatusPreMutationStale {
		writeError(w, http.StatusConflict, "snapshot ignore preview is stale", "stale")
		return
	}
	if result.Receipt == nil {
		writeJSON(w, http.StatusOK, snapshotIgnoreResultDTO{Status: "failed-unverified"})
		return
	}
	// All verification is detached from the browser request and occurs while
	// both operation gates and the selected-repository lease remain held.
	verifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	initialFS := s.SnapshotIgnore.Verify(verifyCtx, *result.Receipt)
	postGit, postContinuation, gitErr := s.Registry.InspectSnapshotIgnoreThroughTracked(verifyCtx, record.Identity, record.Relative)
	if gitErr == nil && !postGit.Tracked {
		if _, patternErr := snapshotignoremanagement.IgnorePatternForDir(record.Root, record.Relative); patternErr != nil {
			gitErr = patternErr
		} else {
			postGit, gitErr = s.Registry.FinishSnapshotIgnoreInspection(verifyCtx, record.Identity, record.Root, postContinuation)
		}
	}
	postGitOK := gitErr == nil && sameSnapshotIgnorePostState(postGit, record.Git, record.Root)
	secondFS := s.SnapshotIgnore.Verify(verifyCtx, *result.Receipt)
	if result.Status == snapshotignoremanagement.StatusVerifiedSuccess && initialFS.Status == snapshotignoremanagement.StatusVerifiedSuccess && secondFS.Status == snapshotignoremanagement.StatusVerifiedSuccess && postGitOK {
		writeJSON(w, http.StatusOK, snapshotIgnoreResultDTO{Status: "verified-success"})
		return
	}
	writeJSON(w, http.StatusOK, snapshotIgnoreResultDTO{Status: "failed-unverified"})
}

func (s *Server) snapshotIgnoreFresh(ctx context.Context, record snapshotIgnoreWriteRecord) bool {
	resolver, ok := s.RoboRev.(interface {
		SnapshotDirectory(context.Context, string) (string, error)
	})
	if !ok {
		return false
	}
	configRaw, err := resolver.SnapshotDirectory(ctx, record.Root)
	if err != nil || configRaw != record.ConfigRaw {
		return false
	}
	relative, err := setupreadiness.ResolveSnapshotDir(configRaw)
	if err != nil || relative != record.Relative {
		return false
	}
	if err := s.SnapshotIgnore.ValidateSnapshotDirectory(ctx, record.Root, relative); err != nil {
		return false
	}
	state, continuation, err := s.Registry.InspectSnapshotIgnoreThroughTracked(ctx, record.Identity, relative)
	if err != nil || !sameSnapshotIgnoreState(state, record.Git, record.Root) {
		return false
	}
	if state.Tracked {
		return false
	}
	if _, err := snapshotignoremanagement.IgnorePatternForDir(record.Root, relative); err != nil {
		return false
	}
	state, err = s.Registry.FinishSnapshotIgnoreInspection(ctx, record.Identity, record.Root, continuation)
	if err != nil || !sameSnapshotIgnoreState(state, record.Git, record.Root) {
		return false
	}
	if state.Ignored {
		return false
	}
	plan, err := s.SnapshotIgnore.Plan(ctx, record.Root, relative)
	return err == nil && plan.Equal(record.Plan) && plan.WriteAvailable()
}

func sameSnapshotIgnoreState(got, want gitx.SnapshotIgnoreRepositoryState, root string) bool {
	return gitx.SameRepositoryPath(got.Root, root) && gitx.SameRepositoryPath(want.Root, root) &&
		got.Branch == want.Branch && got.Detached == want.Detached && got.Head == want.Head &&
		!got.Detached && got.Branch != "" && validFullCommitSHA(got.Head) && !got.Dirty && !got.Tracked && !got.Ignored
}

func sameSnapshotIgnorePostState(got, want gitx.SnapshotIgnoreRepositoryState, root string) bool {
	return gitx.SameRepositoryPath(got.Root, root) && gitx.SameRepositoryPath(want.Root, root) &&
		got.Branch == want.Branch && got.Detached == want.Detached && got.Head == want.Head &&
		!got.Detached && got.Branch != "" && validFullCommitSHA(got.Head) && !got.Tracked && got.Ignored
}
