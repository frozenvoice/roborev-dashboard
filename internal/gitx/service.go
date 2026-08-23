package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type Repository struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
	// Detached distinguishes an actual detached HEAD from a checked-out
	// branch whose literal name is "detached". It is intentionally omitted
	// from the browser contract; callers that need this safety distinction
	// consume the typed repository value server-side.
	Detached      bool   `json:"-"`
	Head          string `json:"head"`
	HeadShort     string `json:"headShort"`
	CommitSubject string `json:"commitSubject"`
	Dirty         bool   `json:"dirty"`
	HasConfig     bool   `json:"hasConfig"`
}

// RepositoryMetadata is the canonical Git metadata needed to classify a
// checkout layout. Git emits these paths with --path-format=absolute.
type RepositoryMetadata struct {
	GitDir    string
	CommonDir string
}

type Service struct {
	Git    string
	Runner proc.Runner
}

func New(runner proc.Runner) *Service { return &Service{Git: "git", Runner: runner} }

func (s *Service) command(ctx context.Context, path string, args ...string) (string, error) {
	all := append([]string{"-C", path}, args...)
	result, err := s.Runner.Run(ctx, proc.Command{Executable: s.Git, Args: all, Timeout: 10 * time.Second, OutputLimit: 1 << 20})
	if err != nil {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = err.Error()
		}
		return "", errors.New(message)
	}
	return strings.TrimSpace(result.Stdout), nil
}

// RepositoryKey returns the platform-aware identity key for a canonical
// repository path. Windows path identity is case-insensitive; platforms with
// case-sensitive paths retain exact cleaned spelling.
func RepositoryKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func SameRepositoryPath(left, right string) bool {
	return RepositoryKey(left) == RepositoryKey(right)
}

// IsLinkedWorktree distinguishes an actual linked worktree from other
// checkout layouts that happen to store .git as a file (for example a
// submodule or a separate-git-dir checkout). A linked worktree has a private
// git-dir whose canonical path differs from the repository's common dir.
func (s *Service) IsLinkedWorktree(ctx context.Context, path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, errors.New("repository path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("absolute repository path: %w", err)
	}
	metadataOutput, err := s.command(ctx, abs, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return false, fmt.Errorf("resolve Git metadata: %w", err)
	}
	lines := strings.Split(metadataOutput, "\n")
	if len(lines) != 2 {
		return false, errors.New("Git metadata response is malformed")
	}
	metadata := RepositoryMetadata{}
	metadata.GitDir, err = canonicalMetadataPath(lines[0])
	if err != nil {
		return false, errors.New("Git metadata response is malformed")
	}
	metadata.CommonDir, err = canonicalMetadataPath(lines[1])
	if err != nil {
		return false, errors.New("Git metadata response is malformed")
	}
	return !SameRepositoryPath(metadata.GitDir, metadata.CommonDir), nil
}

func canonicalMetadataPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("empty Git metadata path")
	}
	path := filepath.FromSlash(value)
	if !filepath.IsAbs(path) {
		return "", errors.New("Git metadata path is not absolute")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func (s *Service) CanonicalRoot(ctx context.Context, input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", errors.New("repository path is required")
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("absolute repository path: %w", err)
	}
	if resolved, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = resolved
	}
	root, err := s.command(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not a Git repository: %w", err)
	}
	root, err = filepath.Abs(filepath.FromSlash(root))
	if err != nil {
		return "", fmt.Errorf("canonical Git root: %w", err)
	}
	if resolved, evalErr := filepath.EvalSymlinks(root); evalErr == nil {
		root = resolved
	}
	if !SameRepositoryPath(abs, root) {
		return "", fmt.Errorf("path must be the Git repository root: %s", root)
	}
	return filepath.Clean(root), nil
}

func (s *Service) Inspect(ctx context.Context, path string) (Repository, error) {
	root, err := s.CanonicalRoot(ctx, path)
	if err != nil {
		return Repository{}, err
	}
	head, err := s.command(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return Repository{}, fmt.Errorf("resolve HEAD: %w", err)
	}
	branch, _ := s.command(ctx, root, "branch", "--show-current")
	detached := strings.TrimSpace(branch) == ""
	if detached {
		branch = "detached"
	}
	subject, _ := s.command(ctx, root, "log", "-1", "--pretty=%s")
	status, err := s.command(ctx, root, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return Repository{}, fmt.Errorf("read worktree status: %w", err)
	}
	short := head
	if len(short) > 8 {
		short = short[:8]
	}
	configInfo, configErr := os.Stat(filepath.Join(root, ".roborev.toml"))
	hasConfig := configErr == nil && !configInfo.IsDir()
	return Repository{Name: filepath.Base(root), Path: root, Branch: branch, Detached: detached, Head: head, HeadShort: short, CommitSubject: subject, Dirty: status != "", HasConfig: hasConfig}, nil
}
