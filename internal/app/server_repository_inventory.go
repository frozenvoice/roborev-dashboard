package app

import (
	"context"
	"errors"
	"net/http"

	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

var errRepositoryInventoryUnavailable = errors.New("RoboRev repository inventory is unavailable")

// RepositoryInventoryResponse is the Dashboard-owned browser contract.  It
// intentionally does not carry RoboRev's identity field: that value belongs
// to RoboRev's storage namespace and is not a Dashboard authorization key.
type RepositoryInventoryResponse struct {
	Repositories    []RepositoryInventoryRow `json:"repositories"`
	RepositoryCount int                      `json:"repositoryCount"`
	TotalJobCount   int64                    `json:"totalJobCount"`
}

type RepositoryInventoryRow struct {
	Name                string `json:"name"`
	Path                string `json:"path"`
	JobCount            int64  `json:"jobCount"`
	DashboardRegistered bool   `json:"dashboardRegistered"`
}

func (s *Server) repositoryInventory(w http.ResponseWriter, r *http.Request) {
	// This must stay the first operation: an unauthenticated request must not
	// probe installation, contact the daemon, or inspect registry state.
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "repository inventory does not accept query parameters", "request")
		return
	}

	service, ok := s.repositoryInventoryService(w, r.Context())
	if !ok {
		return
	}
	inventory, err := service.RepositoryInventory(r.Context())
	if err != nil {
		writeRepositoryInventoryError(w, err)
		return
	}

	rows := make([]RepositoryInventoryRow, 0, len(inventory.Repositories))
	for _, repository := range inventory.Repositories {
		// Contains performs the registry's platform-aware key lookup.  Do not
		// inspect, canonicalize, add, or otherwise touch a tracked path here:
		// stale RoboRev rows are valid input for this read-only inventory.
		rows = append(rows, RepositoryInventoryRow{
			Name:                repository.Name,
			Path:                repository.RootPath,
			JobCount:            repository.Count,
			DashboardRegistered: s.Registry.Contains(repository.RootPath),
		})
	}
	writeJSON(w, http.StatusOK, RepositoryInventoryResponse{
		Repositories:    rows,
		RepositoryCount: len(rows),
		TotalJobCount:   inventory.TotalCount,
	})
}

func writeRepositoryInventoryError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case errors.Is(err, roborev.ErrRepositoryInventoryOffline),
		errors.Is(err, roborev.ErrRepositoryInventoryUnhealthy),
		errors.Is(err, roborev.ErrUnsupportedRepositoryInventory):
		status = http.StatusServiceUnavailable
	case errors.Is(err, roborev.ErrBindingChanged):
		status = http.StatusConflict
	}
	writeError(w, status, errRepositoryInventoryUnavailable.Error(), "repository-inventory")
}

func (s *Server) repositoryInventoryService(w http.ResponseWriter, ctx context.Context) (roborev.RepositoryInventoryService, bool) {
	installation := s.RoboRev.Installation(ctx)
	if !installation.Installed || !roborevcompat.IsSupportedVersion(installation.Version) || !installation.Capabilities.RepositoryInventory {
		writeError(w, http.StatusServiceUnavailable, "Repository inventory requires RoboRev v0.64.0", "repository-inventory")
		return nil, false
	}
	if installation.DaemonHealth == roborev.DaemonHealthOffline {
		writeError(w, http.StatusServiceUnavailable, "Repository inventory requires the RoboRev daemon to be online.", "repository-inventory")
		return nil, false
	}
	if installation.DaemonHealth != roborev.DaemonHealthOnline {
		writeError(w, http.StatusBadGateway, errRepositoryInventoryUnavailable.Error(), "repository-inventory")
		return nil, false
	}
	if !roborevcompat.IsMatchingSupportedVersion(installation.Version, installation.DaemonVersion) {
		writeError(w, http.StatusServiceUnavailable, "Repository inventory requires RoboRev v0.64.0", "repository-inventory")
		return nil, false
	}
	if installation.DaemonCondition == roborev.DaemonConditionUnhealthy {
		writeError(w, http.StatusServiceUnavailable, "Repository inventory requires the RoboRev daemon to be healthy.", "repository-inventory")
		return nil, false
	}
	if installation.DaemonCondition != roborev.DaemonConditionHealthy {
		writeError(w, http.StatusBadGateway, errRepositoryInventoryUnavailable.Error(), "repository-inventory")
		return nil, false
	}
	service, ok := s.RoboRev.(roborev.RepositoryInventoryService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, errRepositoryInventoryUnavailable.Error(), "repository-inventory")
		return nil, false
	}
	return service, true
}
