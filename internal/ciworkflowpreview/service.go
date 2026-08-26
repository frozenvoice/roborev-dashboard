// Package ciworkflowpreview owns the server-side, read-only CI workflow
// preview transaction. It deliberately never writes into the authorized
// repository: RoboRev writes into an unpredictable private temporary
// directory, the bytes are verified, and that directory is removed before a
// response is returned.
package ciworkflowpreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

const (
	Version              = "v0.64.0"
	WorkflowName         = "roborev.yml"
	WorkflowTarget       = ".github/workflows/roborev.yml"
	MaxBytes       int64 = 256 << 10
	tempPattern          = "roborev-dashboard-ci-"
)

var (
	ErrUnavailable       = errors.New("CI workflow preview is unavailable")
	ErrInvalidRepository = errors.New("CI workflow preview repository is invalid")
	ErrUnsafeTemporary   = errors.New("CI workflow preview temporary workspace is unsafe")
	ErrTemporaryOutput   = errors.New("CI workflow preview output is unsafe")
	ErrInvalidWorkflow   = errors.New("CI workflow preview output is invalid")
	ErrCleanup           = errors.New("CI workflow preview cleanup failed")
)

// Generator is intentionally the smallest adapter surface. It may launch
// only the allowlisted RoboRev command and receives a server-owned absolute
// temporary output path, never browser input.
type Generator interface {
	GenerateCIWorkflow(context.Context, string, string, roborev.CIWorkflowPreLaunchGuard) error
}

// TempWorkspace is injectable for deterministic tests. The production
// implementation delegates to os.MkdirTemp/os.RemoveAll.
type TempWorkspace interface {
	TempDir() (string, error)
	MkdirTemp(string, string) (string, error)
	RemoveAll(string) error
}

// FileSystem is kept as a named alias so tests can provide the same race-aware
// path implementation used by the rest of the dashboard.
type FileSystem = pathsafety.FileSystem

type osTempWorkspace struct{}

func (osTempWorkspace) TempDir() (string, error) { return os.TempDir(), nil }

func (osTempWorkspace) MkdirTemp(dir, pattern string) (string, error) {
	return os.MkdirTemp(dir, pattern)
}

func (osTempWorkspace) RemoveAll(path string) error { return os.RemoveAll(path) }

// Workflow is the only successful result of a preview. Content is retained
// solely for the immediate HTTP response and is never logged by this package.
type Workflow struct {
	Content string
	SHA256  string
}

// tempOutputBinding is the complete server-owned proof that the private
// output is still the exact direct child of the unique temporary directory
// created for this request. It deliberately retains metadata only; workflow
// bytes are read only after the generator returns.
type tempOutputBinding struct {
	tempRoot pathsafety.Snapshot
	output   pathsafety.Snapshot
	path     string
}

func (b tempOutputBinding) guard(fs FileSystem) roborev.CIWorkflowPreLaunchGuard {
	return func(context.Context) error {
		root, err := pathsafety.CaptureDirectory(fs, b.tempRoot.Path)
		if err != nil || !root.Equal(b.tempRoot) {
			return ErrTemporaryOutput
		}
		output, err := pathsafety.Capture(fs, b.path, pathsafety.RegularFile)
		if err != nil || output.Exists || !output.Equal(b.output) {
			return ErrTemporaryOutput
		}
		// Keep the direct-parent and direct-path checks explicit even though
		// they are also represented in the absence snapshot. This prevents a
		// future Snapshot field change from weakening this launch boundary.
		if !samePreviewPath(output.Path, b.path) ||
			!samePreviewPath(filepath.Dir(output.Path), b.tempRoot.Path) ||
			!samePreviewPath(output.Absence.AncestorPath, b.tempRoot.Path) ||
			output.Absence.AncestorIdentity != b.output.Absence.AncestorIdentity {
			return ErrTemporaryOutput
		}
		return nil
	}
}

// Service performs one bounded preview. A Service value is safe for
// concurrent use provided its injected seams are safe for concurrent use.
type Service struct {
	Generator Generator
	FS        FileSystem
	Temp      TempWorkspace
}

func NewService(generator Generator) *Service {
	return &Service{Generator: generator, FS: pathsafety.OSFileSystem{}, Temp: osTempWorkspace{}}
}

// NewServiceWithOptions is intended for deterministic tests and local
// contract fixtures. Production callers should use NewService.
func NewServiceWithOptions(generator Generator, fs FileSystem, temp TempWorkspace) *Service {
	return &Service{Generator: generator, FS: fs, Temp: temp}
}

// Generate creates a preview from the freshly inspected canonical repository
// root. It rejects every path/safety/read/validation error with a stable
// package error so private repository and temporary paths cannot cross the
// HTTP boundary.
func (s *Service) Generate(ctx context.Context, repositoryRoot string) (Workflow, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Workflow{}, err
	}
	if s == nil || s.Generator == nil || s.FS == nil || s.Temp == nil {
		return Workflow{}, ErrUnavailable
	}

	canonicalRoot, err := pathsafety.CanonicalAbsolutePath(s.FS, repositoryRoot)
	if err != nil {
		return Workflow{}, ErrInvalidRepository
	}
	rootSnapshot, err := pathsafety.CaptureDirectory(s.FS, canonicalRoot)
	if err != nil || !rootSnapshot.Exists {
		return Workflow{}, ErrInvalidRepository
	}

	base, err := s.Temp.TempDir()
	if err != nil {
		return Workflow{}, ErrUnavailable
	}
	base, err = pathsafety.CanonicalAbsolutePath(s.FS, base)
	if err != nil {
		return Workflow{}, ErrUnsafeTemporary
	}
	baseSnapshot, err := pathsafety.CaptureDirectory(s.FS, base)
	if err != nil || !baseSnapshot.Exists || pathWithin(canonicalRoot, baseSnapshot.Path) {
		return Workflow{}, ErrUnsafeTemporary
	}
	createdRoot, err := s.Temp.MkdirTemp(base, tempPattern)
	if err != nil {
		return Workflow{}, ErrUnavailable
	}
	// Do not register cleanup until the returned path is proven to be the
	// unique direct child that this request asked the OS to create. This keeps
	// a faulty seam (or an unexpected OS contract) from ever handing a broad
	// path such as the base directory to RemoveAll.
	if strings.TrimSpace(createdRoot) == "" || !filepath.IsAbs(createdRoot) || filepath.Clean(createdRoot) != createdRoot {
		return Workflow{}, ErrUnsafeTemporary
	}
	tempRoot, err := filepath.Abs(createdRoot)
	if err != nil || !filepath.IsAbs(tempRoot) || tempRoot == "." || pathWithin(canonicalRoot, tempRoot) || !pathWithin(base, tempRoot) || filepath.Dir(tempRoot) != base || !strings.HasPrefix(filepath.Base(tempRoot), tempPattern) {
		return Workflow{}, ErrUnsafeTemporary
	}
	// Cleanup is registered immediately after the returned path is proven to
	// be the unique direct child and runs for success, generator/read errors,
	// and cancellation alike. The cleanup error is deliberately sanitized and
	// takes precedence over a successful result.
	cleaned := false
	defer func() {
		if !cleaned {
			_ = s.Temp.RemoveAll(createdRoot)
		}
	}()
	tempSnapshot, err := pathsafety.CaptureDirectory(s.FS, tempRoot)
	if err != nil || !tempSnapshot.Exists || pathWithin(canonicalRoot, tempSnapshot.Path) {
		return Workflow{}, ErrUnsafeTemporary
	}
	outputPath := filepath.Join(tempRoot, WorkflowName)
	absence, err := pathsafety.Capture(s.FS, outputPath, pathsafety.RegularFile)
	if err != nil || absence.Exists ||
		!samePreviewPath(absence.Path, outputPath) ||
		!samePreviewPath(absence.Absence.Path, outputPath) ||
		!samePreviewPath(absence.Absence.AncestorPath, tempSnapshot.Path) ||
		absence.Absence.AncestorIdentity != tempSnapshot.FileIdentity {
		return Workflow{}, ErrTemporaryOutput
	}
	binding := tempOutputBinding{tempRoot: tempSnapshot, output: absence, path: outputPath}
	if err := ctx.Err(); err != nil {
		return Workflow{}, err
	}
	if err := s.Generator.GenerateCIWorkflow(ctx, canonicalRoot, outputPath, binding.guard(s.FS)); err != nil {
		if ctx.Err() != nil {
			return Workflow{}, ctx.Err()
		}
		if errors.Is(err, roborev.ErrCIWorkflowOutput) || errors.Is(err, ErrTemporaryOutput) {
			return Workflow{}, ErrTemporaryOutput
		}
		return Workflow{}, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Workflow{}, err
	}
	content, outputSnapshot, err := pathsafety.ReadStableFile(s.FS, outputPath, MaxBytes)
	if err != nil {
		return Workflow{}, ErrTemporaryOutput
	}
	if outputSnapshot.AncestorIdentity != tempSnapshot.FileIdentity || !samePreviewPath(outputSnapshot.AncestorPath, tempSnapshot.Path) {
		return Workflow{}, ErrTemporaryOutput
	}
	if len(content) == 0 || !utf8.Valid(content) || hasForbiddenControl(content) {
		return Workflow{}, ErrInvalidWorkflow
	}
	hash := sha256.Sum256(content)
	result := Workflow{Content: string(content), SHA256: hex.EncodeToString(hash[:])}
	cleanupErr := s.Temp.RemoveAll(createdRoot)
	cleaned = true
	if cleanupErr != nil {
		return Workflow{}, ErrCleanup
	}
	return result, nil
}

func samePreviewPath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if filepath.VolumeName(left) != "" || filepath.VolumeName(right) != "" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func hasForbiddenControl(content []byte) bool {
	for _, char := range string(content) {
		if (char < 0x20 && char != '\t' && char != '\n' && char != '\r') || (char >= 0x7f && char <= 0x9f) {
			return true
		}
	}
	return false
}

func pathWithin(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if strings.EqualFold(parent, child) {
		return true
	}
	// filepath.Rel rejects paths on different Windows volumes. Such a temp
	// directory is necessarily outside the repository and is valid here.
	if !strings.EqualFold(filepath.VolumeName(parent), filepath.VolumeName(child)) {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
