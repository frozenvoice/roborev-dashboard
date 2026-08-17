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
	Name          string `json:"name"`
	Path          string `json:"path"`
	Branch        string `json:"branch"`
	Head          string `json:"head"`
	HeadShort     string `json:"headShort"`
	CommitSubject string `json:"commitSubject"`
	Dirty         bool   `json:"dirty"`
	HasConfig     bool   `json:"hasConfig"`
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

func samePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
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
	if !samePath(abs, root) {
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
	if branch == "" {
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
	return Repository{Name: filepath.Base(root), Path: root, Branch: branch, Head: head, HeadShort: short, CommitSubject: subject, Dirty: status != "", HasConfig: hasConfig}, nil
}
