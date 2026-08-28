package roborev

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
	"github.com/froze/roborev-dashboard/internal/stats"
)

const (
	statsSince24h = "24h"
	statsSince7d  = "7d"
	statsSince30d = "30d"
	statsSince90d = "90d"
)

var (
	ErrUnsupportedStats = errors.New("RoboRev stats capability is unavailable")
	ErrStatsOffline     = errors.New("RoboRev daemon is offline; stats are unavailable")
	ErrStatsUnhealthy   = errors.New("RoboRev daemon is unhealthy; stats are unavailable")
)

// StatsService is an optional read-only extension to Service. Keeping this
// surface separate allows fake and legacy services to remain source
// compatible while the Dashboard can explicitly gate Stats v1.
type StatsService interface {
	Summary(context.Context, string, string, string) (stats.Summary, error)
	Cost(context.Context, string, string) (stats.Cost, error)
}

func SummaryArgs(repoPath, branch, since string) ([]string, error) {
	if err := validateStatsRepo(repoPath); err != nil {
		return nil, err
	}
	if err := validateStatsBranch(branch); err != nil {
		return nil, err
	}
	if !allowedStatsSince(since) {
		return nil, fmt.Errorf("unsupported stats window %q", since)
	}
	args := []string{"summary", "--repo", repoPath}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	return append(args, "--since", since, "--json"), nil
}

func CostArgs(repoPath, branch string) ([]string, error) {
	if err := validateStatsRepo(repoPath); err != nil {
		return nil, err
	}
	if err := validateStatsBranch(branch); err != nil {
		return nil, err
	}
	args := []string{"cost", "--repo", repoPath}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	return append(args, "--json"), nil
}

func allowedStatsSince(value string) bool {
	switch value {
	case statsSince24h, statsSince7d, statsSince30d, statsSince90d:
		return true
	default:
		return false
	}
}

func validateStatsRepo(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("repository path is required")
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return errors.New("repository path contains control characters")
		}
	}
	return nil
}

func validateStatsBranch(value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) == "" {
		return errors.New("branch must not be blank")
	}
	if len(value) > 256 {
		return errors.New("branch exceeds 256 bytes")
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return errors.New("branch contains control characters")
		}
	}
	return nil
}

func (c *Client) Summary(ctx context.Context, repoPath, branch, since string) (stats.Summary, error) {
	if err := validateStatsRepo(repoPath); err != nil {
		return stats.Summary{}, err
	}
	if err := validateStatsBranch(branch); err != nil {
		return stats.Summary{}, err
	}
	if !allowedStatsSince(since) {
		return stats.Summary{}, fmt.Errorf("unsupported stats window %q", since)
	}
	binding, err := c.statsHTTPPreflight(ctx, true)
	if err != nil {
		return stats.Summary{}, err
	}
	query := url.Values{}
	query.Set("repo", repoPath)
	query.Set("since", since)
	if branch != "" {
		query.Set("branch", branch)
	}
	raw, err := c.daemonHTTPGet(ctx, binding, "/api/summary?"+query.Encode(), 200, stats.SummaryOutputLimit)
	if err != nil {
		return stats.Summary{}, err
	}
	value, err := stats.ParseSummary(string(raw))
	if err != nil {
		return stats.Summary{}, err
	}
	if value.RepoPath != "" && !gitx.SameRepositoryPath(value.RepoPath, repoPath) {
		return stats.Summary{}, fmt.Errorf("%w: response repository does not match request", stats.ErrMalformedSummary)
	}
	if branch != "" && value.Branch != branch {
		return stats.Summary{}, fmt.Errorf("%w: response branch does not match request", stats.ErrMalformedSummary)
	}
	return value, nil
}

func (c *Client) Cost(ctx context.Context, repoPath, branch string) (stats.Cost, error) {
	if err := validateStatsRepo(repoPath); err != nil {
		return stats.Cost{}, err
	}
	if err := validateStatsBranch(branch); err != nil {
		return stats.Cost{}, err
	}
	binding, err := c.statsHTTPPreflight(ctx, false)
	if err != nil {
		return stats.Cost{}, err
	}
	query := url.Values{}
	query.Set("repo", repoPath)
	if branch != "" {
		query.Set("branch", branch)
	}
	raw, err := c.daemonHTTPGet(ctx, binding, "/api/cost?"+query.Encode(), 200, stats.CostOutputLimit)
	if err != nil {
		return stats.Cost{}, err
	}
	return stats.ParseCost(string(raw))
}

// statsHTTPPreflight binds the read to the already-installed executable
// generation and a fresh, healthy matching daemon without invoking a stats
// CLI command (which would call ensureDaemon in v0.64). The short version
// probe is the existing machine-readable installation probe; only the
// requested summary/cost operation is transported over HTTP.
func (c *Client) statsHTTPPreflight(ctx context.Context, summary bool) (daemonHTTPBinding, error) {
	if c == nil {
		return daemonHTTPBinding{}, ErrUnsupportedStats
	}
	if ctx == nil {
		ctx = context.Background()
	}
	binary, daemonURL, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" || strings.TrimSpace(daemonURL) == "" {
		return daemonHTTPBinding{}, ErrUnsupportedStats
	}
	version, err := c.detectVersion(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return daemonHTTPBinding{}, ctx.Err()
		}
		if errors.Is(err, ErrBindingChanged) {
			return daemonHTTPBinding{}, ErrBindingChanged
		}
		return daemonHTTPBinding{}, ErrUnsupportedStats
	}
	capability := capabilities(version).CostStats
	if summary {
		capability = capabilities(version).SummaryStats
	}
	if !roborevcompat.IsSupportedVersion(version) || !capability {
		return daemonHTTPBinding{}, fmt.Errorf("%w: RoboRev %s", ErrUnsupportedStats, version)
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return daemonHTTPBinding{}, ctx.Err()
		}
		if isUnreachableHealthError(err) {
			return daemonHTTPBinding{}, ErrStatsOffline
		}
		return daemonHTTPBinding{}, err
	}
	if !health.Healthy {
		return daemonHTTPBinding{}, ErrStatsUnhealthy
	}
	if !roborevcompat.IsMatchingSupportedVersion(version, health.Version) {
		return daemonHTTPBinding{}, ErrUnsupportedStats
	}
	if !c.generationCurrent(generation) {
		return daemonHTTPBinding{}, ErrBindingChanged
	}
	return daemonHTTPBinding{daemonURL: daemonURL, generation: generation, version: version}, nil
}
