package roborev

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/repomove"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	repositoryMoveOutputLimit = 64 << 10
	repositoryMoveTimeout     = 10 * time.Second
)

var (
	ErrUnsupportedRepositoryMove = errors.New("RoboRev repository move capability is unavailable")
	ErrRepositoryMoveOffline     = errors.New("RoboRev daemon is offline; repository move is unavailable")
	ErrRepositoryMoveUnhealthy   = errors.New("RoboRev daemon is unhealthy; repository move is unavailable")
	ErrRepositoryMoveStale       = errors.New("RoboRev repository move source changed after preview")
	ErrRepositoryMoveConflict    = errors.New("RoboRev repository move destination is already tracked")
	ErrRepositoryMoveAmbiguous   = errors.New("RoboRev repository move source is ambiguous")
)

func (c *Client) repositoryMoveFileSystem() repomove.FileSystem {
	if c != nil && c.repositoryMoveFS != nil {
		return c.repositoryMoveFS
	}
	return repomove.OSFileSystem{}
}

// RepositoryMovePreflight owns the fresh CLI, daemon, inventory, stale-source
// and conflict checks. It is intentionally independent of Installation so a
// confirm does not duplicate app-layer probes.
func (c *Client) RepositoryMovePreflight(ctx context.Context, request repomove.Request) (repomove.Preflight, error) {
	if c == nil || c.Runner == nil || c.HTTP == nil {
		return repomove.Preflight{}, ErrUnsupportedRepositoryMove
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := repomove.ValidateRequest(request)
	if err != nil {
		return repomove.Preflight{}, err
	}
	binary, _, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return repomove.Preflight{}, ErrUnsupportedRepositoryMove
	}
	versionResult, err := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: []string{"version", "--json"}, Timeout: 5 * time.Second, OutputLimit: repositoryMoveOutputLimit})
	if err != nil || !versionResult.Started || versionResult.StdoutTruncated || versionResult.StderrTruncated {
		if ctx.Err() != nil {
			return repomove.Preflight{}, ctx.Err()
		}
		if err == nil {
			err = errors.New("version probe failed")
		}
		// A supported capability is established only after a successful typed
		// version response. Process/transport failures are unknown pre-process
		// failures and remain distinct from an explicitly unsupported version.
		return repomove.Preflight{}, fmt.Errorf("repository move version preflight failed: %w", err)
	}
	cliVersion, err := decodeExactVersion(versionResult.Stdout)
	if err != nil || !roborevcompat.IsSupportedVersion(cliVersion) || !capabilities(cliVersion).RepositoryMove {
		return repomove.Preflight{}, fmt.Errorf("%w: unsupported CLI version", ErrUnsupportedRepositoryMove)
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return repomove.Preflight{}, ctx.Err()
		}
		if isUnreachableHealthError(err) {
			return repomove.Preflight{}, fmt.Errorf("%w: %v", ErrRepositoryMoveOffline, err)
		}
		return repomove.Preflight{}, fmt.Errorf("repository move health preflight failed: %w", err)
	}
	if !health.Healthy {
		return repomove.Preflight{}, ErrRepositoryMoveUnhealthy
	}
	if !roborevcompat.IsMatchingSupportedVersion(cliVersion, health.Version) {
		return repomove.Preflight{}, fmt.Errorf("%w: CLI/daemon version mismatch", ErrUnsupportedRepositoryMove)
	}
	inventory, err := c.repositoryInventoryRead(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return repomove.Preflight{}, ctx.Err()
		}
		if isUnreachableHealthError(err) {
			return repomove.Preflight{}, fmt.Errorf("%w: %v", ErrRepositoryMoveOffline, err)
		}
		return repomove.Preflight{}, err
	}
	if err := validateMoveInventory(inventory, request); err != nil {
		return repomove.Preflight{}, err
	}
	if err := repomove.SourceAbsent(c.repositoryMoveFileSystem(), request.SourcePath); err != nil {
		return repomove.Preflight{}, err
	}
	if !c.generationCurrent(generation) {
		return repomove.Preflight{}, ErrBindingChanged
	}
	command, err := repomove.BuildCommand(binary, request)
	if err != nil {
		return repomove.Preflight{}, err
	}
	fallbackKey, err := repomove.SourceFallbackKey(request.SourcePath)
	if err != nil {
		return repomove.Preflight{}, err
	}
	return repomove.Preflight{Command: command, Binding: fmt.Sprintf("%d:%s", generation, binary), CLIVersion: cliVersion, DaemonVersion: health.Version, SourceFallbackKey: fallbackKey}, nil
}

func (c *Client) RepositoryMove(ctx context.Context, request repomove.Request, expected repomove.Preflight) (repomove.ExecutionResult, error) {
	if c == nil || c.Runner == nil || c.HTTP == nil {
		return repomove.ExecutionResult{}, ErrUnsupportedRepositoryMove
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := repomove.ValidateRequest(request)
	if err != nil {
		return repomove.ExecutionResult{}, err
	}
	fresh, err := c.RepositoryMovePreflight(ctx, request)
	if err != nil {
		return repomove.ExecutionResult{}, err
	}
	if !sameMovePreflight(fresh, expected) {
		return repomove.ExecutionResult{}, ErrBindingChanged
	}
	// Keep the stale-path check immediately adjacent to process acceptance.
	if err := repomove.SourceAbsent(c.repositoryMoveFileSystem(), request.SourcePath); err != nil {
		return repomove.ExecutionResult{}, err
	}
	if c.BindingIdentity() != fresh.Binding {
		return repomove.ExecutionResult{}, ErrBindingChanged
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{Executable: fresh.Command.Executable, Args: append([]string(nil), fresh.Command.Args...), Dir: fresh.Command.Dir, Timeout: repositoryMoveTimeout, OutputLimit: repositoryMoveOutputLimit})
	if !result.Started {
		if runErr != nil {
			return repomove.ExecutionResult{}, runErr
		}
		return repomove.ExecutionResult{}, errors.New("RoboRev repository move process did not start")
	}
	readback, readbackErr := c.repositoryInventoryRead(ctx)
	if readbackErr != nil || c.BindingIdentity() != fresh.Binding {
		return repomove.ExecutionResult{Started: true}, nil
	}
	return repomove.ExecutionResult{Started: true, Verified: validateMoveReadback(readback, request)}, nil
}

func validateMoveInventory(inventory repoinventory.Inventory, request repomove.Request) error {
	sourceCount := 0
	for _, row := range inventory.Repositories {
		if gitx.SameRepositoryPath(row.RootPath, request.SourcePath) {
			sourceCount++
			if row.Name != request.SourceName {
				return ErrRepositoryMoveStale
			}
		}
	}
	if sourceCount != 1 {
		return ErrRepositoryMoveStale
	}
	for _, row := range inventory.Repositories {
		if !gitx.SameRepositoryPath(row.RootPath, request.SourcePath) && gitx.SameRepositoryPath(row.RootPath, request.DestinationPath) {
			return fmt.Errorf("%w: %s", ErrRepositoryMoveConflict, request.DestinationPath)
		}
	}
	fallbackKey, err := repomove.SourceFallbackKey(request.SourcePath)
	if err != nil {
		return err
	}
	for _, row := range inventory.Repositories {
		if !gitx.SameRepositoryPath(row.RootPath, request.SourcePath) && row.Name == fallbackKey {
			return fmt.Errorf("%w: %s", ErrRepositoryMoveAmbiguous, fallbackKey)
		}
	}
	return nil
}

func validateMoveReadback(inventory repoinventory.Inventory, request repomove.Request) bool {
	destinationCount := 0
	for _, row := range inventory.Repositories {
		if gitx.SameRepositoryPath(row.RootPath, request.SourcePath) {
			return false
		}
		if gitx.SameRepositoryPath(row.RootPath, request.DestinationPath) {
			destinationCount++
			if row.Name != request.SourceName {
				return false
			}
		}
	}
	return destinationCount == 1
}

func sameMovePreflight(left, right repomove.Preflight) bool {
	return left.Binding == right.Binding && left.CLIVersion == right.CLIVersion && left.DaemonVersion == right.DaemonVersion && left.SourceFallbackKey == right.SourceFallbackKey && left.Command.Executable == right.Command.Executable && sameArgs(left.Command.Args, right.Command.Args) && left.Command.Dir == right.Command.Dir
}

var _ RepositoryMoveService = (*Client)(nil)
