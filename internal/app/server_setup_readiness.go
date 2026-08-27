package app

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/hookstatus"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/setupreadiness"
)

func newSetupReadinessChecker(service roborev.Service, registry *Registry, hooks *hookstatus.Service) *setupreadiness.Checker {
	deps := setupreadiness.Dependencies{FS: nil}
	if resolver, ok := service.(interface {
		SnapshotDirectory(context.Context, string) (string, error)
	}); ok {
		deps.Configuration = resolver.SnapshotDirectory
	}
	deps.Snapshot = func(ctx context.Context, root, relative string) (setupreadiness.SnapshotObservation, error) {
		value, err := registry.SnapshotProbe(ctx, root, relative)
		return setupreadiness.SnapshotObservation{Tracked: value.Tracked, Ignored: value.Ignored}, err
	}
	if hooks != nil {
		deps.Hooks = func(ctx context.Context, identity, root string) (setupreadiness.GitHooks, error) {
			value, err := hooks.Inspect(ctx, identity, root)
			return setupreadiness.GitHooks(value.Status), err
		}
	}
	if daemon, ok := service.(roborev.RepositorySetupReadinessService); ok {
		deps.Daemon = func(ctx context.Context) (setupreadiness.DaemonObservation, error) {
			value, err := daemon.RepositorySetupReadiness(ctx)
			observation := setupreadiness.DaemonObservation{State: setupreadiness.Daemon(value.DaemonState), InventoryAvailable: value.InventoryAvailable}
			for _, repository := range value.Inventory.Repositories {
				observation.InventoryRoots = append(observation.InventoryRoots, repository.RootPath)
			}
			return observation, err
		}
	}
	return setupreadiness.New(deps)
}

// repositorySetupReadiness is the only HTTP entry point for the combined
// check. All authorization and selection checks precede every readiness
// adapter call.
func (s *Server) repositorySetupReadiness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	if !exactHeaderValue(r.Header, "X-CSRF-Token", s.csrfToken) {
		writeError(w, http.StatusForbidden, "invalid CSRF token", "csrf")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "setup readiness does not accept query parameters", "request")
		return
	}
	var body struct {
		RepositoryIdentity string `json:"repositoryIdentity"`
	}
	if !decodeUniqueBody(w, r, &body, "repositoryIdentity") {
		return
	}
	identity := body.RepositoryIdentity
	if identity == "" || strings.TrimSpace(identity) != identity {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is invalid", "repository")
		return
	}
	resolvedRoot, err := s.Registry.ResolveIdentity(identity)
	if err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return
	}
	release, leasedRoot, err := s.Registry.AcquireSelected(identity)
	if err != nil {
		writeError(w, http.StatusConflict, "selected repository changed; refresh and retry", "repository")
		return
	}
	defer release()
	if !gitx.SameRepositoryPath(resolvedRoot, leasedRoot) {
		writeError(w, http.StatusConflict, "selected repository changed; refresh and retry", "repository")
		return
	}
	repo, err := s.Registry.InspectIdentity(r.Context(), identity)
	if err != nil || !gitx.SameRepositoryPath(repo.Path, resolvedRoot) || !gitx.SameRepositoryPath(repo.Path, leasedRoot) {
		writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
		return
	}
	if s.SetupReadiness == nil {
		writeError(w, http.StatusServiceUnavailable, "repository setup readiness is unavailable", "setup-readiness")
		return
	}
	result, err := s.SetupReadiness.Check(r.Context(), identity, repo.Path)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "repository setup readiness timed out", "setup-readiness")
		} else if errors.Is(err, context.Canceled) {
			writeError(w, http.StatusRequestTimeout, "repository setup readiness was canceled", "setup-readiness")
		} else {
			writeError(w, http.StatusBadGateway, "repository setup readiness is unavailable", "setup-readiness")
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}
