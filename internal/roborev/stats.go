package roborev

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/froze/roborev-dashboard/internal/gitx"
	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
	"github.com/froze/roborev-dashboard/internal/stats"
)

const (
	statsCommandTimeout = 15 * time.Second
	statsSince24h       = "24h"
	statsSince7d        = "7d"
	statsSince30d       = "30d"
	statsSince90d       = "90d"
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
	args, err := SummaryArgs(repoPath, branch, since)
	if err != nil {
		return stats.Summary{}, err
	}
	binary, generation, err := c.statsPreflight(ctx, true)
	if err != nil {
		return stats.Summary{}, err
	}
	result, err := c.runStatsSnapshot(ctx, binary, repoPath, args, stats.SummaryOutputLimit)
	if err != nil {
		return stats.Summary{}, err
	}
	if !c.bindingUnchanged(binary, generation) {
		return stats.Summary{}, ErrBindingChanged
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return stats.Summary{}, fmt.Errorf("%w: bounded output was truncated", stats.ErrMalformedSummary)
	}
	value, err := stats.ParseSummary(result.Stdout)
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
	args, err := CostArgs(repoPath, branch)
	if err != nil {
		return stats.Cost{}, err
	}
	binary, generation, err := c.statsPreflight(ctx, false)
	if err != nil {
		return stats.Cost{}, err
	}
	result, err := c.runStatsSnapshot(ctx, binary, repoPath, args, stats.CostOutputLimit)
	if err != nil {
		return stats.Cost{}, err
	}
	if !c.bindingUnchanged(binary, generation) {
		return stats.Cost{}, ErrBindingChanged
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return stats.Cost{}, fmt.Errorf("%w: bounded output was truncated", stats.ErrMalformedCost)
	}
	return stats.ParseCost(result.Stdout)
}

func (c *Client) statsPreflight(ctx context.Context, summary bool) (string, uint64, error) {
	if c == nil {
		return "", 0, ErrUnsupportedStats
	}
	if ctx == nil {
		ctx = context.Background()
	}
	binary, _, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return "", 0, ErrUnsupportedStats
	}
	version, err := c.detectVersion(ctx)
	if err != nil {
		return "", 0, ErrUnsupportedStats
	}
	capability := capabilities(version).CostStats
	if summary {
		capability = capabilities(version).SummaryStats
	}
	if !roborevcompat.IsSupportedVersion(version) || !capability {
		return "", 0, fmt.Errorf("%w: RoboRev %s", ErrUnsupportedStats, version)
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return "", 0, ctx.Err()
		}
		if isUnreachableHealthError(err) {
			return "", 0, ErrStatsOffline
		}
		return "", 0, ErrStatsOffline
	}
	if !health.Healthy {
		return "", 0, ErrStatsUnhealthy
	}
	if strings.TrimSpace(health.Version) != version || !roborevcompat.IsSupportedVersion(health.Version) {
		return "", 0, ErrUnsupportedStats
	}
	if !c.bindingUnchanged(binary, generation) {
		return "", 0, ErrBindingChanged
	}
	return binary, generation, nil
}

func (c *Client) runStatsSnapshot(ctx context.Context, binary, repoPath string, args []string, outputLimit int) (proc.Result, error) {
	if strings.TrimSpace(binary) == "" {
		return proc.Result{}, ErrUnsupportedStats
	}
	result, err := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: append([]string(nil), args...), Dir: repoPath, Timeout: statsCommandTimeout, OutputLimit: outputLimit})
	if err != nil {
		// The HTTP layer maps this to a stable phase/message; keeping stderr
		// attached here is useful to non-HTTP callers without logging secrets.
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = err.Error()
		}
		return result, fmt.Errorf("stats command failed: %w: %s", err, message)
	}
	return result, nil
}
