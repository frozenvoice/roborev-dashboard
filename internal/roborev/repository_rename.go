package roborev

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/reporename"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const repositoryRenameOutputLimit = 64 << 10
const repositoryRenameTimeout = 10 * time.Second

var (
	ErrUnsupportedRepositoryRename = errors.New("RoboRev repository rename capability is unavailable")
	ErrRepositoryRenameOffline     = errors.New("RoboRev daemon is offline; repository rename is unavailable")
	ErrRepositoryRenameUnhealthy   = errors.New("RoboRev daemon is unhealthy; repository rename is unavailable")
	ErrRepositoryRenameStale       = errors.New("RoboRev repository rename target changed after preview")
	ErrRepositoryRenameAmbiguous   = errors.New("RoboRev repository rename target is ambiguous")
)

func (c *Client) RepositoryRenamePreflight(ctx context.Context, request reporename.Request) (reporename.Preflight, error) {
	if c == nil || c.Runner == nil {
		return reporename.Preflight{}, ErrUnsupportedRepositoryRename
	}
	request, err := reporename.ValidateRequest(request)
	if err != nil {
		return reporename.Preflight{}, err
	}
	binary, _, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return reporename.Preflight{}, ErrUnsupportedRepositoryRename
	}
	versionResult, err := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: []string{"version", "--json"}, Timeout: 5 * time.Second, OutputLimit: repositoryRenameOutputLimit})
	if err != nil || !versionResult.Started || versionResult.StdoutTruncated || versionResult.StderrTruncated {
		if ctx.Err() != nil {
			return reporename.Preflight{}, ctx.Err()
		}
		if err == nil {
			err = errors.New("version probe failed")
		}
		return reporename.Preflight{}, fmt.Errorf("%w: version preflight failed: %v", ErrUnsupportedRepositoryRename, err)
	}
	cliVersion, err := decodeExactVersion(versionResult.Stdout)
	if err != nil || !roborevcompat.IsSupportedVersion(cliVersion) || !capabilities(cliVersion).RepositoryRename {
		return reporename.Preflight{}, fmt.Errorf("%w: unsupported CLI version", ErrUnsupportedRepositoryRename)
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return reporename.Preflight{}, ctx.Err()
		}
		if isUnreachableHealthError(err) {
			return reporename.Preflight{}, fmt.Errorf("%w: %v", ErrRepositoryRenameOffline, err)
		}
		return reporename.Preflight{}, fmt.Errorf("repository rename health preflight failed: %w", err)
	}
	if !health.Healthy {
		return reporename.Preflight{}, ErrRepositoryRenameUnhealthy
	}
	if !roborevcompat.IsMatchingSupportedVersion(cliVersion, health.Version) {
		return reporename.Preflight{}, fmt.Errorf("%w: CLI/daemon version mismatch", ErrUnsupportedRepositoryRename)
	}
	if !c.generationCurrent(generation) {
		return reporename.Preflight{}, ErrBindingChanged
	}
	command, err := reporename.BuildCommand(binary, request)
	if err != nil {
		return reporename.Preflight{}, err
	}
	return reporename.Preflight{Command: command, Binding: fmt.Sprintf("%d:%s", generation, binary), CLIVersion: cliVersion, DaemonVersion: health.Version}, nil
}

func (c *Client) RepositoryRename(ctx context.Context, request reporename.Request, expected reporename.Preflight) (proc.Result, error) {
	fresh, err := c.RepositoryRenamePreflight(ctx, request)
	if err != nil {
		return proc.Result{}, err
	}
	if fresh.Binding != expected.Binding || fresh.CLIVersion != expected.CLIVersion || fresh.DaemonVersion != expected.DaemonVersion || fresh.Command.Executable != expected.Command.Executable || !sameArgs(fresh.Command.Args, expected.Command.Args) || fresh.Command.Dir != expected.Command.Dir {
		return proc.Result{}, ErrBindingChanged
	}
	// Re-read the daemon inventory after the last version/health/binding probe
	// and immediately before process acceptance. This closes the Dashboard-side
	// window in which RoboRev's path-first/name-fallback resolver could otherwise
	// interpret a stale path as a display name.
	inventory, err := c.RepositoryInventory(ctx)
	if err != nil {
		return proc.Result{}, err
	}
	if err := validateRenameInventory(inventory, request); err != nil {
		return proc.Result{}, err
	}
	// RepositoryInventory has its own generation guard, but a rebind may land
	// between the final preflight and that inventory call's snapshot. Compare
	// the opaque binding once more at the actual process boundary.
	if c.BindingIdentity() != fresh.Binding {
		return proc.Result{}, ErrBindingChanged
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{Executable: fresh.Command.Executable, Args: append([]string(nil), fresh.Command.Args...), Dir: fresh.Command.Dir, Timeout: repositoryRenameTimeout, OutputLimit: repositoryRenameOutputLimit})
	if !result.Started {
		if runErr != nil {
			return result, runErr
		}
		return result, errors.New("RoboRev rename process did not start")
	}
	// Started means the external mutation may have happened. Return the result
	// even on an exit error so the HTTP layer can perform authoritative readback.
	return result, nil
}

var _ RepositoryRenameService = (*Client)(nil)

func validateRenameInventory(inventory repoinventory.Inventory, request reporename.Request) error {
	count := 0
	current := ""
	for _, row := range inventory.Repositories {
		if row.RootPath == request.Path {
			count++
			current = row.Name
		}
	}
	if count != 1 {
		return ErrRepositoryRenameStale
	}
	if current != request.CurrentName {
		return ErrRepositoryRenameStale
	}
	for _, row := range inventory.Repositories {
		if row.RootPath != request.Path && row.Name == request.Path {
			return ErrRepositoryRenameAmbiguous
		}
	}
	return nil
}
