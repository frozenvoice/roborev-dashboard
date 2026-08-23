// Package repomerge contains the narrow, version-pinned contract for the
// stale RoboRev repository duplicate-recovery operation.  It deliberately
// does not move files, mutate the Dashboard registry, or open RoboRev's DB.
package repomerge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/repomove"
)

var (
	ErrInvalidRequest          = errors.New("invalid repository merge request")
	ErrSourcePathRequired      = errors.New("source repository path is required")
	ErrTargetPathRequired      = errors.New("target repository path is required")
	ErrSourcePathNotAbsolute   = errors.New("source repository path must be absolute")
	ErrTargetPathNotAbsolute   = errors.New("target repository path must be absolute")
	ErrSourceEmpty             = errors.New("source repository has no jobs to merge")
	ErrSourceStale             = errors.New("source repository is not uniquely tracked by RoboRev")
	ErrTargetStale             = errors.New("target repository is not uniquely tracked by RoboRev")
	ErrSourceTargetSame        = errors.New("source and target are the same repository")
	ErrSourceFallbackAmbiguous = errors.New("source repository identifier is ambiguous")
	ErrTargetFallbackAmbiguous = errors.New("target repository identifier is ambiguous")
	ErrActiveSourceJobs        = errors.New("source repository has active jobs")
	ErrSourceJobMismatch       = errors.New("source repository job inventory is inconsistent")
	ErrSourceExists            = repomove.ErrSourceExists
	ErrReadSource              = repomove.ErrReadSource
)

// Request identifies the stale RoboRev source and the already-existing target.
// The Dashboard resolves both paths from its own authoritative sources before
// calling the adapter; display names and upstream identity values are not part
// of the mutation request.
type Request struct {
	SourcePath string
	TargetPath string
}

// RepositorySnapshot is the server-owned, token-bound fact set displayed by
// the merge preview. JobCount is the RoboRev inventory count for this row.
type RepositorySnapshot struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	JobCount int64  `json:"jobCount"`
}

// RepositoryCandidate carries a Dashboard opaque identity alongside the
// upstream inventory facts. The adapter never derives authorization from the
// upstream inventory identity field.
type RepositoryCandidate struct {
	RepositorySnapshot
	RepositoryIdentity string `json:"repositoryIdentity"`
}

// EligibilitySnapshot is the adapter's fresh source/inventory evidence. The
// inventory is kept in the domain contract so the app can construct target
// candidates without another inventory probe; it is never sent to the browser
// directly and its upstream identity values are not authorization inputs.
type EligibilitySnapshot struct {
	Source    RepositorySnapshot
	Inventory repoinventory.Inventory
}

// Eligibility is the concise name used by the optional adapter interface.
// Keep one underlying shape so app and adapter cannot drift.
type Eligibility = EligibilitySnapshot

// Command is a shell-free invocation of the exact v0.64 contract:
// repo merge --yes <source> <target>. WorkingDirectory is intentionally empty.
type Command struct {
	Executable       string   `json:"executable"`
	Args             []string `json:"args"`
	WorkingDirectory string   `json:"workingDirectory"`
}

// Preflight is the complete fact set bound into an app preview token.
type Preflight struct {
	Source            RepositorySnapshot `json:"source"`
	Target            RepositorySnapshot `json:"target"`
	Command           Command            `json:"command"`
	Binding           string             `json:"-"`
	CLIVersion        string             `json:"-"`
	DaemonVersion     string             `json:"-"`
	SourceFallbackKey string             `json:"-"`
	TargetFallbackKey string             `json:"-"`
}

// ExecutionResult intentionally exposes no process exit status. Once the
// process starts, only one authoritative inventory readback can verify that
// the source disappeared and the target consolidated its expected jobs.
type ExecutionResult struct {
	Started  bool `json:"started"`
	Verified bool `json:"verified"`
}

// FileSystem is the stale-source filesystem boundary. os.Lstat is used in
// production so symlinks and junctions count as existing entries.
type FileSystem interface {
	Lstat(string) (os.FileInfo, error)
}

type OSFileSystem struct{}

func (OSFileSystem) Lstat(path string) (os.FileInfo, error) { return os.Lstat(path) }

// ValidateRequest permits only distinct absolute paths. It does not follow
// links or perform any filesystem mutation.
func ValidateRequest(request Request) (Request, error) {
	if strings.TrimSpace(request.SourcePath) == "" {
		return Request{}, ErrSourcePathRequired
	}
	if !filepath.IsAbs(request.SourcePath) {
		return Request{}, ErrSourcePathNotAbsolute
	}
	if strings.TrimSpace(request.TargetPath) == "" {
		return Request{}, ErrTargetPathRequired
	}
	if !filepath.IsAbs(request.TargetPath) {
		return Request{}, ErrTargetPathNotAbsolute
	}
	if gitx.SameRepositoryPath(request.SourcePath, request.TargetPath) {
		return Request{}, ErrSourceTargetSame
	}
	return request, nil
}

// BuildCommand builds the exact typed command and does not invoke a shell.
func BuildCommand(executable string, request Request) (Command, error) {
	if strings.TrimSpace(executable) == "" {
		return Command{}, errors.New("RoboRev executable is required")
	}
	request, err := ValidateRequest(request)
	if err != nil {
		return Command{}, err
	}
	return Command{Executable: executable, Args: []string{"repo", "merge", "--yes", request.SourcePath, request.TargetPath}, WorkingDirectory: ""}, nil
}

// SourceFallbackKey mirrors RoboRev's exact path-first/name-fallback key for
// an absolute source. filepath.Abs retains native separators on Windows.
func SourceFallbackKey(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", ErrSourcePathRequired
	}
	if !filepath.IsAbs(path) {
		return path, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve source fallback key: %w", err)
	}
	return abs, nil
}

// TargetFallbackKey mirrors RoboRev's resolveRepoIdentifier for an existing
// target. Repo merge resolves a path to git/repo.Root, whose v0.64
// NormalizePath result uses the host-native separator spelling. The
// Dashboard Registry supplies that canonical root; filepath.Abs preserves the
// same process-boundary spelling for the fallback name key.
func TargetFallbackKey(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", ErrTargetPathRequired
	}
	if !filepath.IsAbs(path) {
		return path, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve target fallback key: %w", err)
	}
	return abs, nil
}

// SourceAbsent reuses the audited stale-source primitive from repository move.
func SourceAbsent(fs FileSystem, source string) error {
	if fs == nil {
		fs = OSFileSystem{}
	}
	return repomove.SourceAbsent(fs, source)
}
