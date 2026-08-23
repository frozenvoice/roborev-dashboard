package roborev

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const repositoryInventoryVersionOutputLimit = 64 << 10

var (
	ErrUnsupportedRepositoryInventory = errors.New("RoboRev repository inventory capability is unavailable")
	ErrRepositoryInventoryOffline     = errors.New("RoboRev daemon is offline; repository inventory is unavailable")
	ErrRepositoryInventoryUnhealthy   = errors.New("RoboRev daemon is unhealthy; repository inventory is unavailable")
)

// RepositoryInventory performs a fresh CLI/version and daemon/health
// preflight before making the one typed inventory request. It never starts a
// daemon and never falls back to the human-oriented `repo list` command.
func (c *Client) RepositoryInventory(ctx context.Context) (repoinventory.Inventory, error) {
	if c == nil {
		return repoinventory.Inventory{}, ErrUnsupportedRepositoryInventory
	}
	if ctx == nil {
		ctx = context.Background()
	}
	binary, _, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" || c.Runner == nil {
		return repoinventory.Inventory{}, ErrUnsupportedRepositoryInventory
	}
	versionResult, err := c.Runner.Run(ctx, proc.Command{
		Executable:  binary,
		Args:        []string{"version", "--json"},
		Timeout:     5 * time.Second,
		OutputLimit: repositoryInventoryVersionOutputLimit,
	})
	if err != nil {
		if ctx.Err() != nil {
			return repoinventory.Inventory{}, ctx.Err()
		}
		return repoinventory.Inventory{}, fmt.Errorf("%w: version preflight failed", ErrUnsupportedRepositoryInventory)
	}
	if versionResult.StdoutTruncated || versionResult.StderrTruncated {
		return repoinventory.Inventory{}, fmt.Errorf("%w: version response exceeded bounded output", ErrUnsupportedRepositoryInventory)
	}
	cliVersion, err := decodeExactVersion(versionResult.Stdout)
	if err != nil || !roborevcompat.IsSupportedVersion(cliVersion) {
		return repoinventory.Inventory{}, fmt.Errorf("%w: unsupported CLI version", ErrUnsupportedRepositoryInventory)
	}
	if !capabilities(cliVersion).RepositoryInventory {
		return repoinventory.Inventory{}, fmt.Errorf("%w: RoboRev %s", ErrUnsupportedRepositoryInventory, cliVersion)
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return repoinventory.Inventory{}, ctx.Err()
		}
		if isUnreachableHealthError(err) {
			return repoinventory.Inventory{}, fmt.Errorf("%w: %v", ErrRepositoryInventoryOffline, err)
		}
		// A reachable but malformed health response is a contract failure, not
		// proof that the supported capability is unavailable. Keep it distinct
		// so the HTTP layer preserves the existing Stats/Insights 502 taxonomy.
		return repoinventory.Inventory{}, fmt.Errorf("repository inventory health preflight failed: %w", err)
	}
	if !health.Healthy {
		return repoinventory.Inventory{}, ErrRepositoryInventoryUnhealthy
	}
	if !roborevcompat.IsMatchingSupportedVersion(cliVersion, health.Version) {
		return repoinventory.Inventory{}, fmt.Errorf("%w: CLI/daemon version mismatch", ErrUnsupportedRepositoryInventory)
	}
	if !c.generationCurrent(generation) {
		return repoinventory.Inventory{}, ErrBindingChanged
	}

	// Reuse the bounded daemon JSON transport used by the other typed
	// adapters. It issues a GET with a nil body and receives no query string.
	raw, err := c.getDaemonJSONBounded(ctx, "/api/repos", int64(repoinventory.MaxResponseBytes))
	if err != nil {
		if ctx.Err() != nil {
			return repoinventory.Inventory{}, ctx.Err()
		}
		if isUnreachableHealthError(err) {
			return repoinventory.Inventory{}, fmt.Errorf("%w: %v", ErrRepositoryInventoryOffline, err)
		}
		return repoinventory.Inventory{}, err
	}
	value, err := repoinventory.Parse(raw)
	if err != nil {
		return repoinventory.Inventory{}, err
	}
	if !c.generationCurrent(generation) {
		return repoinventory.Inventory{}, ErrBindingChanged
	}
	return value, nil
}

var _ RepositoryInventoryService = (*Client)(nil)
