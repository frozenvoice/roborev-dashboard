package roborev

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/repomerge"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	repositoryMergeOutputLimit = 64 << 10
	repositoryMergeTimeout     = 10 * time.Second
)

var (
	ErrUnsupportedRepositoryMerge = errors.New("RoboRev repository merge capability is unavailable")
	ErrRepositoryMergeOffline     = errors.New("RoboRev daemon is offline; repository merge is unavailable")
	ErrRepositoryMergeUnhealthy   = errors.New("RoboRev daemon is unhealthy; repository merge is unavailable")
)

func (c *Client) repositoryMergeFileSystem() repomerge.FileSystem {
	if c != nil && c.repositoryMoveFS != nil {
		return c.repositoryMoveFS
	}
	return repomerge.OSFileSystem{}
}

// RepositoryMergeEligibility performs the explicit, read-only source check.
// The app remains responsible for Dashboard Registry authorization and for
// constructing opaque target candidates from this fresh inventory.
func (c *Client) RepositoryMergeEligibility(ctx context.Context, sourcePath string) (repomerge.Eligibility, error) {
	if c == nil || c.Runner == nil || c.HTTP == nil {
		return repomerge.Eligibility{}, ErrUnsupportedRepositoryMerge
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sourcePath) == "" {
		return repomerge.Eligibility{}, repomerge.ErrSourcePathRequired
	}
	if !isAbsoluteRepositoryPath(sourcePath) {
		return repomerge.Eligibility{}, repomerge.ErrSourcePathNotAbsolute
	}
	probe, err := c.repositoryMergeProbe(ctx)
	if err != nil {
		return repomerge.Eligibility{}, err
	}
	source, count := findMergeRepository(probe.inventory, sourcePath)
	if count != 1 {
		return repomerge.Eligibility{}, fmt.Errorf("%w: source path is not unique", repomerge.ErrSourceStale)
	}
	if source.Count <= 0 {
		return repomerge.Eligibility{}, repomerge.ErrSourceEmpty
	}
	if err := repomerge.SourceAbsent(c.repositoryMergeFileSystem(), source.RootPath); err != nil {
		return repomerge.Eligibility{}, err
	}
	if err := c.repositoryMergeJobsRead(ctx, source.RootPath, source.Count); err != nil {
		return repomerge.Eligibility{}, mapMergeJobsError(err)
	}
	return repomerge.Eligibility{Source: mergeSnapshot(source), Inventory: probe.inventory}, nil
}

// RepositoryMergePreflight owns all fresh version, health, inventory,
// fallback, stale-source, count, and active-source checks used by the preview
// token. It does not invoke the merge process.
func (c *Client) RepositoryMergePreflight(ctx context.Context, request repomerge.Request) (repomerge.Preflight, error) {
	if c == nil || c.Runner == nil || c.HTTP == nil {
		return repomerge.Preflight{}, ErrUnsupportedRepositoryMerge
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := repomerge.ValidateRequest(request)
	if err != nil {
		return repomerge.Preflight{}, err
	}
	probe, err := c.repositoryMergeProbe(ctx)
	if err != nil {
		return repomerge.Preflight{}, err
	}
	source, sourceCount := findMergeRepository(probe.inventory, request.SourcePath)
	target, targetCount := findMergeRepository(probe.inventory, request.TargetPath)
	if sourceCount != 1 {
		return repomerge.Preflight{}, fmt.Errorf("%w: source path is not unique", repomerge.ErrSourceStale)
	}
	if targetCount != 1 {
		return repomerge.Preflight{}, fmt.Errorf("%w: target path is not unique", repomerge.ErrTargetStale)
	}
	if source.Count <= 0 {
		return repomerge.Preflight{}, repomerge.ErrSourceEmpty
	}
	if gitx.SameRepositoryPath(source.RootPath, target.RootPath) {
		return repomerge.Preflight{}, repomerge.ErrSourceTargetSame
	}
	if err := repomerge.SourceAbsent(c.repositoryMergeFileSystem(), source.RootPath); err != nil {
		return repomerge.Preflight{}, err
	}
	if err := validateMergeFallbacks(probe.inventory, source.RootPath, target.RootPath); err != nil {
		return repomerge.Preflight{}, err
	}
	if err := c.repositoryMergeJobsRead(ctx, source.RootPath, source.Count); err != nil {
		return repomerge.Preflight{}, mapMergeJobsError(err)
	}
	if !c.generationCurrent(probe.generation) {
		return repomerge.Preflight{}, ErrBindingChanged
	}
	command, err := repomerge.BuildCommand(probe.binary, repomerge.Request{SourcePath: source.RootPath, TargetPath: target.RootPath})
	if err != nil {
		return repomerge.Preflight{}, err
	}
	sourceKey, err := repomerge.SourceFallbackKey(source.RootPath)
	if err != nil {
		return repomerge.Preflight{}, err
	}
	targetKey, err := repomerge.TargetFallbackKey(target.RootPath)
	if err != nil {
		return repomerge.Preflight{}, err
	}
	return repomerge.Preflight{
		Source:            mergeSnapshot(source),
		Target:            mergeSnapshot(target),
		Command:           command,
		Binding:           fmt.Sprintf("%d:%s", probe.generation, probe.binary),
		CLIVersion:        probe.cliVersion,
		DaemonVersion:     probe.daemonVersion,
		SourceFallbackKey: sourceKey,
		TargetFallbackKey: targetKey,
	}, nil
}

// RepositoryMerge performs one and only one merge process after a fresh
// process-boundary preflight. Once started, process exit/stdout are ignored;
// one fresh inventory readback is the sole verification authority.
func (c *Client) RepositoryMerge(ctx context.Context, request repomerge.Request, expected repomerge.Preflight) (repomerge.ExecutionResult, error) {
	if c == nil || c.Runner == nil || c.HTTP == nil {
		return repomerge.ExecutionResult{}, ErrUnsupportedRepositoryMerge
	}
	if ctx == nil {
		ctx = context.Background()
	}
	fresh, err := c.RepositoryMergePreflight(ctx, request)
	if err != nil {
		return repomerge.ExecutionResult{}, err
	}
	if !sameMergePreflight(fresh, expected) {
		return repomerge.ExecutionResult{}, ErrBindingChanged
	}
	if err := repomerge.SourceAbsent(c.repositoryMergeFileSystem(), fresh.Source.Path); err != nil {
		return repomerge.ExecutionResult{}, err
	}
	if c.BindingIdentity() != fresh.Binding {
		return repomerge.ExecutionResult{}, ErrBindingChanged
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{
		Executable:  fresh.Command.Executable,
		Args:        append([]string(nil), fresh.Command.Args...),
		Dir:         fresh.Command.WorkingDirectory,
		Timeout:     repositoryMergeTimeout,
		OutputLimit: repositoryMergeOutputLimit,
	})
	if !result.Started {
		if runErr != nil {
			return repomerge.ExecutionResult{}, runErr
		}
		return repomerge.ExecutionResult{}, errors.New("RoboRev repository merge process did not start")
	}
	readback, readbackErr := c.repositoryInventoryRead(ctx)
	if readbackErr != nil || c.BindingIdentity() != fresh.Binding {
		return repomerge.ExecutionResult{Started: true}, nil
	}
	return repomerge.ExecutionResult{Started: true, Verified: validateMergeReadback(readback, fresh)}, nil
}

type repositoryMergeProbe struct {
	binary        string
	generation    uint64
	cliVersion    string
	daemonVersion string
	inventory     repoinventory.Inventory
}

func (c *Client) repositoryMergeProbe(ctx context.Context) (repositoryMergeProbe, error) {
	binary, _, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return repositoryMergeProbe{}, ErrUnsupportedRepositoryMerge
	}
	versionResult, err := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: []string{"version", "--json"}, Timeout: 5 * time.Second, OutputLimit: repositoryMergeOutputLimit})
	if err != nil || !versionResult.Started || versionResult.StdoutTruncated || versionResult.StderrTruncated {
		if ctx.Err() != nil {
			return repositoryMergeProbe{}, ctx.Err()
		}
		if err == nil {
			err = errors.New("version probe failed")
		}
		return repositoryMergeProbe{}, fmt.Errorf("%w: version preflight failed: %v", ErrUnsupportedRepositoryMerge, err)
	}
	cliVersion, err := decodeExactVersion(versionResult.Stdout)
	if err != nil || !roborevcompat.IsSupportedVersion(cliVersion) || !capabilities(cliVersion).RepositoryMerge {
		return repositoryMergeProbe{}, fmt.Errorf("%w: unsupported CLI version", ErrUnsupportedRepositoryMerge)
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return repositoryMergeProbe{}, ctx.Err()
		}
		if isUnreachableHealthError(err) {
			return repositoryMergeProbe{}, fmt.Errorf("%w: %v", ErrRepositoryMergeOffline, err)
		}
		return repositoryMergeProbe{}, fmt.Errorf("repository merge health preflight failed: %w", err)
	}
	if !health.Healthy {
		return repositoryMergeProbe{}, ErrRepositoryMergeUnhealthy
	}
	if !roborevcompat.IsMatchingSupportedVersion(cliVersion, health.Version) {
		return repositoryMergeProbe{}, fmt.Errorf("%w: CLI/daemon version mismatch", ErrUnsupportedRepositoryMerge)
	}
	inventory, err := c.repositoryInventoryRead(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return repositoryMergeProbe{}, ctx.Err()
		}
		if isUnreachableHealthError(err) {
			return repositoryMergeProbe{}, fmt.Errorf("%w: %v", ErrRepositoryMergeOffline, err)
		}
		return repositoryMergeProbe{}, err
	}
	if !c.generationCurrent(generation) {
		return repositoryMergeProbe{}, ErrBindingChanged
	}
	return repositoryMergeProbe{binary: binary, generation: generation, cliVersion: cliVersion, daemonVersion: health.Version, inventory: inventory}, nil
}

func findMergeRepository(inventory repoinventory.Inventory, path string) (repoinventory.Repository, int) {
	var result repoinventory.Repository
	count := 0
	for _, row := range inventory.Repositories {
		if gitx.SameRepositoryPath(row.RootPath, path) {
			result = row
			count++
		}
	}
	return result, count
}

func validateMergeFallbacks(inventory repoinventory.Inventory, sourcePath, targetPath string) error {
	sourceKey, err := repomerge.SourceFallbackKey(sourcePath)
	if err != nil {
		return err
	}
	targetKey, err := repomerge.TargetFallbackKey(targetPath)
	if err != nil {
		return err
	}
	for _, row := range inventory.Repositories {
		if !gitx.SameRepositoryPath(row.RootPath, sourcePath) && row.Name == sourceKey {
			return fmt.Errorf("%w: source fallback key %q", repomerge.ErrSourceFallbackAmbiguous, sourceKey)
		}
		if !gitx.SameRepositoryPath(row.RootPath, targetPath) && row.Name == targetKey {
			return fmt.Errorf("%w: target fallback key %q", repomerge.ErrTargetFallbackAmbiguous, targetKey)
		}
	}
	return nil
}

func mergeSnapshot(row repoinventory.Repository) repomerge.RepositorySnapshot {
	return repomerge.RepositorySnapshot{Path: row.RootPath, Name: row.Name, JobCount: row.Count}
}

func validateMergeReadback(inventory repoinventory.Inventory, expected repomerge.Preflight) bool {
	targetCount := 0
	for _, row := range inventory.Repositories {
		if gitx.SameRepositoryPath(row.RootPath, expected.Source.Path) {
			return false
		}
		if gitx.SameRepositoryPath(row.RootPath, expected.Target.Path) {
			targetCount++
			if row.Name != expected.Target.Name || row.Count < expected.Source.JobCount+expected.Target.JobCount {
				return false
			}
		}
	}
	return targetCount == 1
}

func sameMergePreflight(left, right repomerge.Preflight) bool {
	return left.Source == right.Source && left.Target == right.Target && left.Binding == right.Binding && left.CLIVersion == right.CLIVersion && left.DaemonVersion == right.DaemonVersion && left.SourceFallbackKey == right.SourceFallbackKey && left.TargetFallbackKey == right.TargetFallbackKey && left.Command.Executable == right.Command.Executable && left.Command.WorkingDirectory == right.Command.WorkingDirectory && sameArgs(left.Command.Args, right.Command.Args)
}

func mapMergeJobsError(err error) error {
	if errors.Is(err, ErrRepositoryMergeActiveJobs) {
		return ErrRepositoryMergeActiveJobs
	}
	if errors.Is(err, ErrRepositoryMergeMalformedJobs) {
		return err
	}
	if errors.Is(err, repomerge.ErrSourceJobMismatch) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if isUnreachableHealthError(err) {
		return fmt.Errorf("%w: %v", ErrRepositoryMergeOffline, err)
	}
	return err
}

func isAbsoluteRepositoryPath(path string) bool {
	// filepath.IsAbs is authoritative on the host; the additional drive form
	// keeps deterministic Windows-shaped fixtures meaningful on non-Windows CI.
	return filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || (len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/'))
}

var _ RepositoryMergeService = (*Client)(nil)
