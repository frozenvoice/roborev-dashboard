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

const (
	setupReadinessDaemonOnline      = "online"
	setupReadinessDaemonOffline     = "offline"
	setupReadinessDaemonUnavailable = "unavailable"
)

// RepositorySetupReadiness performs exactly one CLI version probe, one
// already-running health request, and (when healthy and matching) one typed
// /api/repos request. A valid health response with an invalid inventory keeps
// the daemon online while making inventory unavailable to the caller.
func (c *Client) RepositorySetupReadiness(ctx context.Context) (RepositorySetupReadiness, error) {
	result := RepositorySetupReadiness{DaemonState: setupReadinessDaemonUnavailable}
	if c == nil || c.Runner == nil {
		return result, ErrUnsupportedRepositoryInventory
	}
	binary, daemonURL, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" || strings.TrimSpace(daemonURL) == "" {
		return result, ErrUnsupportedRepositoryInventory
	}
	cliVersion, err := c.detectVersion(ctx)
	if err != nil || !roborevcompat.IsSupportedVersion(cliVersion) || !capabilities(cliVersion).RepositoryInventory {
		return result, ErrUnsupportedRepositoryInventory
	}
	if !c.generationCurrent(generation) {
		return result, ErrBindingChanged
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if isUnreachableHealthError(err) {
			result.DaemonState = setupReadinessDaemonOffline
			return result, nil
		}
		return result, err
	}
	if !health.Healthy || !roborevcompat.IsMatchingSupportedVersion(cliVersion, health.Version) {
		return result, errors.New("RoboRev daemon health is unavailable")
	}
	result.DaemonState = setupReadinessDaemonOnline
	if !c.generationCurrent(generation) {
		return RepositorySetupReadiness{DaemonState: setupReadinessDaemonUnavailable}, ErrBindingChanged
	}
	inventory, err := c.repositoryInventoryRead(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, nil
	}
	result.Inventory = inventory
	result.InventoryAvailable = true
	if !c.generationCurrent(generation) {
		return RepositorySetupReadiness{DaemonState: setupReadinessDaemonUnavailable}, ErrBindingChanged
	}
	return result, nil
}

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

	value, err := c.repositoryInventoryRead(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return repoinventory.Inventory{}, ctx.Err()
		}
		if isUnreachableHealthError(err) {
			return repoinventory.Inventory{}, fmt.Errorf("%w: %v", ErrRepositoryInventoryOffline, err)
		}
		return repoinventory.Inventory{}, err
	}
	if !c.generationCurrent(generation) {
		return repoinventory.Inventory{}, ErrBindingChanged
	}
	return value, nil
}

// repositoryInventoryRead performs only the bounded, strict /api/repos
// transport and decode. Callers that already own a fresh CLI/health preflight
// use this helper to avoid repeating those probes. The raw transport error is
// deliberately preserved so each operation can map daemon reachability to
// its own public taxonomy.
func (c *Client) repositoryInventoryRead(ctx context.Context) (repoinventory.Inventory, error) {
	if c == nil || c.HTTP == nil {
		return repoinventory.Inventory{}, errors.New("RoboRev daemon transport is unavailable")
	}
	raw, err := c.getDaemonJSONBounded(ctx, "/api/repos", int64(repoinventory.MaxResponseBytes))
	if err != nil {
		return repoinventory.Inventory{}, err
	}
	value, err := repoinventory.Parse(raw)
	if err != nil {
		return repoinventory.Inventory{}, err
	}
	return value, nil
}

var _ RepositoryInventoryService = (*Client)(nil)
