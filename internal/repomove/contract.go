// Package repomove contains the narrow, version-pinned contract used to repair
// a stale RoboRev repository path.  It intentionally does not move files or
// know anything about the Dashboard registry.
package repomove

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/froze/roborev-dashboard/internal/gitx"
)

var (
	ErrInvalidRequest          = errors.New("invalid repository move request")
	ErrSourcePathRequired      = errors.New("source repository path is required")
	ErrSourcePathNotAbsolute   = errors.New("source repository path must be absolute")
	ErrDestinationPathRequired = errors.New("destination repository path is required")
	ErrDestinationNotAbsolute  = errors.New("destination repository path must be absolute")
	ErrSourceExists            = errors.New("source repository path still exists")
	ErrSourceTracked           = errors.New("source repository is registered in the Dashboard")
	ErrSourceStale             = errors.New("source repository is not uniquely tracked by RoboRev")
	ErrDestinationConflict     = errors.New("destination repository is already tracked by RoboRev")
	ErrSourceDestinationSame   = errors.New("source and destination are the same repository")
	ErrFallbackAmbiguous       = errors.New("source repository identifier is ambiguous")
	ErrReadSource              = errors.New("unable to inspect stale source path")
)

// Request identifies an existing RoboRev row and a previously registered Git
// destination.  Both paths are canonical absolute paths by the time the
// request reaches the adapter.
type Request struct {
	SourcePath      string
	SourceName      string
	DestinationPath string
}

// Command is a shell-free process invocation.  Dir is the destination Git
// root; the upstream `.` destination is resolved against this working
// directory.
type Command struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	Dir        string   `json:"dir"`
}

// Preflight is the complete binding used by a preview token.  The source
// fallback key is recorded because RoboRev's resolver can eventually treat a
// missing path-like identifier as a display name.
type Preflight struct {
	Command           Command
	Binding           string
	CLIVersion        string
	DaemonVersion     string
	SourceFallbackKey string
}

// ExecutionResult deliberately does not expose process exit status.  Once a
// process starts, only the authoritative inventory readback can establish a
// verified move.
type ExecutionResult struct {
	Started  bool
	Verified bool
}

// FileSystem is the read-only filesystem boundary used by preview and the
// process adapter.  os.Lstat is used in production so symlinks and junctions
// are treated as existing entries rather than followed.
type FileSystem interface {
	Lstat(string) (os.FileInfo, error)
}

type OSFileSystem struct{}

func (OSFileSystem) Lstat(path string) (os.FileInfo, error) { return os.Lstat(path) }

func ValidateRequest(request Request) (Request, error) {
	if strings.TrimSpace(request.SourcePath) == "" {
		return Request{}, ErrSourcePathRequired
	}
	if !filepath.IsAbs(request.SourcePath) {
		return Request{}, ErrSourcePathNotAbsolute
	}
	if strings.TrimSpace(request.DestinationPath) == "" {
		return Request{}, ErrDestinationPathRequired
	}
	if !filepath.IsAbs(request.DestinationPath) {
		return Request{}, ErrDestinationNotAbsolute
	}
	if gitx.SameRepositoryPath(request.SourcePath, request.DestinationPath) {
		return Request{}, ErrSourceDestinationSame
	}
	return request, nil
}

// BuildCommand is the exact v0.64 contract: source is the fresh inventory
// spelling and the destination is the process cwd, represented as `.`.
func BuildCommand(executable string, request Request) (Command, error) {
	if strings.TrimSpace(executable) == "" {
		return Command{}, errors.New("RoboRev executable is required")
	}
	request, err := ValidateRequest(request)
	if err != nil {
		return Command{}, err
	}
	return Command{Executable: executable, Args: []string{"repo", "move", request.SourcePath, "."}, Dir: request.DestinationPath}, nil
}

// SourceFallbackKey mirrors the post-resolve identifier used by RoboRev's
// path-first/name-fallback lookup for an absolute stale source.  filepath.Abs
// intentionally retains the host-native separators; GetRepoByName receives
// this value without slash normalization.
func SourceFallbackKey(source string) (string, error) {
	if strings.TrimSpace(source) == "" {
		return "", ErrSourcePathRequired
	}
	if !filepath.IsAbs(source) {
		return source, nil
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("resolve source fallback key: %w", err)
	}
	return abs, nil
}

// SourceAbsent applies the fail-closed source stale invariant.  Only
// os.IsNotExist is accepted; a permission or unknown error is not treated as
// evidence that the source is absent.
func SourceAbsent(fs FileSystem, source string) error {
	if fs == nil {
		fs = OSFileSystem{}
	}
	_, err := fs.Lstat(source)
	if err == nil {
		return ErrSourceExists
	}
	if os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrReadSource, err)
}
