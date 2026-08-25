package app

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/froze/roborev-dashboard/internal/hookstatus"
)

func (s *Server) repositoryHooks(w http.ResponseWriter, r *http.Request) {
	// Keep browser authorization first: rejected requests must not probe the
	// version, registry, Git, or hook filesystem.
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	identity, ok := strictRepositoryHooksIdentity(w, r)
	if !ok {
		return
	}
	path, err := s.Registry.ResolveIdentity(identity)
	if err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return
	}
	repo, err := s.Registry.Inspect(r.Context(), path)
	if err != nil {
		writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
		return
	}
	if s.HookStatus == nil {
		writeJSON(w, http.StatusOK, hookstatus.Response{
			RepositoryIdentity: identity,
			Status:             hookstatus.Unavailable,
			PostCommit:         hookstatus.HookResult{Status: hookstatus.Unknown},
			PostRewrite:        hookstatus.HookResult{Status: hookstatus.Unknown},
		})
		return
	}
	result, err := s.HookStatus.Inspect(r.Context(), identity, repo.Path)
	if err != nil {
		status := http.StatusServiceUnavailable
		message := "Git hook status is unavailable"
		phase := "hook-status"
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
			message = "Git hook status timed out"
		} else if errors.Is(err, context.Canceled) {
			message = "Git hook status was canceled"
		}
		writeError(w, status, message, phase)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func strictRepositoryHooksIdentity(w http.ResponseWriter, r *http.Request) (string, bool) {
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
