package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
	"github.com/froze/roborev-dashboard/internal/stats"
)

const (
	statsDefaultWindow = "7d"
	statsScopeAll      = "all"
	statsScopeCurrent  = "current"
)

var errStatsUnavailable = errors.New("Stats are unavailable")

type statsSummaryResponse struct {
	RepositoryIdentity string                 `json:"repositoryIdentity"`
	Window             string                 `json:"window"`
	BranchScope        string                 `json:"branchScope"`
	Summary            statsSummaryBrowserDTO `json:"summary"`
}

// statsSummaryBrowserDTO is an explicit browser projection. RoboRev's
// summary includes repo_path and per-repository path fields even when the
// request is already scoped to one authorized repository; those paths stay
// server-side. Aggregate names and counts remain useful to the dashboard,
// but no upstream path-bearing DTO crosses this boundary.
type statsSummaryBrowserDTO struct {
	Schema   string             `json:"$schema,omitempty"`
	Since    time.Time          `json:"since"`
	Branch   string             `json:"branch,omitempty"`
	Overview stats.Overview     `json:"overview"`
	Verdicts stats.Verdicts     `json:"verdicts"`
	Agents   []stats.Agent      `json:"agents"`
	Duration stats.Duration     `json:"duration"`
	JobTypes []stats.JobType    `json:"job_types"`
	Failures stats.Failures     `json:"failures"`
	Repos    []statsBrowserRepo `json:"repos,omitempty"`
	Cost     stats.Cost         `json:"cost"`
}

type statsBrowserRepo struct {
	Name      string `json:"name"`
	Total     int    `json:"total"`
	Passed    int    `json:"passed"`
	Failed    int    `json:"failed"`
	Addressed int    `json:"addressed"`
}

func browserStatsSummary(summary stats.Summary) statsSummaryBrowserDTO {
	repos := make([]statsBrowserRepo, 0, len(summary.Repos))
	for _, repo := range summary.Repos {
		repos = append(repos, statsBrowserRepo{Name: repo.Name, Total: repo.Total, Passed: repo.Passed, Failed: repo.Failed, Addressed: repo.Addressed})
	}
	return statsSummaryBrowserDTO{
		Schema: summary.Schema, Since: summary.Since, Branch: summary.Branch,
		Overview: summary.Overview, Verdicts: summary.Verdicts,
		Agents: append([]stats.Agent(nil), summary.Agents...), Duration: summary.Duration,
		JobTypes: append([]stats.JobType(nil), summary.JobTypes...), Failures: summary.Failures,
		Repos: repos, Cost: summary.Cost,
	}
}

type statsCostResponse struct {
	RepositoryIdentity string     `json:"repositoryIdentity"`
	BranchScope        string     `json:"branchScope"`
	Cost               stats.Cost `json:"cost"`
}

func (s *Server) statsSummary(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	identity, window, branchScope, ok := parseStatsQuery(w, r, true)
	if !ok {
		return
	}
	repo, branch, ok := s.statsRepositoryScope(w, r.Context(), identity, branchScope)
	if !ok {
		return
	}
	service, ok := s.statsService(w, r.Context(), true)
	if !ok {
		return
	}
	summary, err := service.Summary(r.Context(), repo.Path, branch, window)
	if err != nil {
		writeStatsError(w, err)
		return
	}
	if summary.RepoPath != "" && !gitx.SameRepositoryPath(summary.RepoPath, repo.Path) {
		writeError(w, http.StatusBadGateway, errStatsUnavailable.Error(), "stats")
		return
	}
	if branch != "" && summary.Branch != branch {
		writeError(w, http.StatusBadGateway, errStatsUnavailable.Error(), "stats")
		return
	}
	writeJSON(w, http.StatusOK, statsSummaryResponse{RepositoryIdentity: identity, Window: window, BranchScope: branchScope, Summary: browserStatsSummary(summary)})
}

func (s *Server) statsCost(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	identity, _, branchScope, ok := parseStatsQuery(w, r, false)
	if !ok {
		return
	}
	repo, branch, ok := s.statsRepositoryScope(w, r.Context(), identity, branchScope)
	if !ok {
		return
	}
	service, ok := s.statsService(w, r.Context(), false)
	if !ok {
		return
	}
	cost, err := service.Cost(r.Context(), repo.Path, branch)
	if err != nil {
		writeStatsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, statsCostResponse{RepositoryIdentity: identity, BranchScope: branchScope, Cost: cost})
}

func parseStatsQuery(w http.ResponseWriter, r *http.Request, summary bool) (identity, window, branchScope string, ok bool) {
	query := r.URL.Query()
	allowed := 2
	if summary {
		allowed = 3
	}
	if len(query) != allowed {
		writeError(w, http.StatusBadRequest, "invalid stats query", "request")
		return "", "", "", false
	}
	identityValues, identityPresent := query["repositoryIdentity"]
	branchValues, branchPresent := query["branchScope"]
	if !identityPresent || len(identityValues) != 1 || strings.TrimSpace(identityValues[0]) == "" || !branchPresent || len(branchValues) != 1 {
		writeError(w, http.StatusBadRequest, "invalid stats query", "request")
		return "", "", "", false
	}
	identity = strings.TrimSpace(identityValues[0])
	branchScope = strings.TrimSpace(branchValues[0])
	if branchScope != statsScopeAll && branchScope != statsScopeCurrent {
		writeError(w, http.StatusBadRequest, "invalid branchScope", "request")
		return "", "", "", false
	}
	if summary {
		windowValues, windowPresent := query["window"]
		if !windowPresent || len(windowValues) != 1 {
			writeError(w, http.StatusBadRequest, "invalid window", "request")
			return "", "", "", false
		}
		window = strings.TrimSpace(windowValues[0])
		switch window {
		case "24h", statsDefaultWindow, "30d", "90d":
		default:
			writeError(w, http.StatusBadRequest, "invalid window", "request")
			return "", "", "", false
		}
	}
	return identity, window, branchScope, true
}

func (s *Server) statsRepositoryScope(w http.ResponseWriter, ctx context.Context, identity, branchScope string) (gitx.Repository, string, bool) {
	repo, err := s.Registry.InspectIdentity(ctx, identity)
	if err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return gitx.Repository{}, "", false
	}
	if branchScope == statsScopeCurrent {
		branch := strings.TrimSpace(repo.Branch)
		if repo.Detached || branch == "" || strings.EqualFold(branch, "head") {
			writeError(w, http.StatusConflict, "current branch is unavailable", "branch")
			return gitx.Repository{}, "", false
		}
		return repo, branch, true
	}
	return repo, "", true
}

func (s *Server) statsService(w http.ResponseWriter, ctx context.Context, summary bool) (roborev.StatsService, bool) {
	installation := s.RoboRev.Installation(ctx)
	capability := installation.Capabilities.CostStats
	if summary {
		capability = installation.Capabilities.SummaryStats
	}
	if !installation.Installed || !roborevcompat.IsSupportedVersion(installation.Version) || !capability {
		writeError(w, http.StatusServiceUnavailable, "Stats require RoboRev v0.64.0", "stats")
		return nil, false
	}
	if installation.DaemonHealth == roborev.DaemonHealthOffline {
		writeError(w, http.StatusServiceUnavailable, "Stats require the RoboRev daemon to be online.", "stats")
		return nil, false
	}
	if installation.DaemonHealth != roborev.DaemonHealthOnline {
		writeError(w, http.StatusBadGateway, errStatsUnavailable.Error(), "stats")
		return nil, false
	}
	if !roborevcompat.IsMatchingSupportedVersion(installation.Version, installation.DaemonVersion) {
		writeError(w, http.StatusServiceUnavailable, "Stats require RoboRev v0.64.0", "stats")
		return nil, false
	}
	if installation.DaemonCondition == roborev.DaemonConditionUnhealthy {
		writeError(w, http.StatusServiceUnavailable, "Stats require the RoboRev daemon to be healthy.", "stats")
		return nil, false
	}
	if installation.DaemonCondition != roborev.DaemonConditionHealthy {
		writeError(w, http.StatusBadGateway, errStatsUnavailable.Error(), "stats")
		return nil, false
	}
	service, ok := s.RoboRev.(roborev.StatsService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Stats are unavailable", "stats")
		return nil, false
	}
	return service, true
}
func writeStatsError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	message := errStatsUnavailable.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case errors.Is(err, roborev.ErrStatsOffline), errors.Is(err, roborev.ErrStatsUnhealthy), errors.Is(err, roborev.ErrUnsupportedStats):
		status = http.StatusServiceUnavailable
	case errors.Is(err, roborev.ErrBindingChanged):
		status = http.StatusConflict
	case errors.Is(err, stats.ErrMalformedSummary), errors.Is(err, stats.ErrMalformedCost):
		status = http.StatusBadGateway
	}
	writeError(w, status, message, "stats")
}
