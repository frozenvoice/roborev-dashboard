package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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

// HooksPath returns Git's effective hooks directory without changing any
// repository configuration. It follows the v0.1.8 kit semantics: Git's
// --git-path result is authoritative when absolute; relative results are
// rooted at the main checkout so linked worktrees share their hooks path.
// Hook inspection uses a sanitized Git environment so inherited GIT_* values
// and global/system configuration cannot redirect the read.
func (s *Service) HooksPath(ctx context.Context, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("repository path is required")
	}
	env := s.sanitizedHookGitEnv(ctx, path)
	output, err := s.hookCommandWithEnv(ctx, path, env, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", errors.New("resolve Git hooks path")
	}
	hooksPath := normalizeHookPath(output)
	if filepath.IsAbs(hooksPath) {
		return filepath.Clean(hooksPath), nil
	}
	mainRoot, err := s.mainRootForHooks(ctx, path, env)
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(mainRoot, hooksPath)), nil
}

func (s *Service) mainRootForHooks(ctx context.Context, path string, env []string) (string, error) {
	gitDirOutput, err := s.hookCommandWithEnv(ctx, path, env, "rev-parse", "--git-dir")
	if err != nil {
		return "", errors.New("resolve Git directory")
	}
	commonDirOutput, err := s.hookCommandWithEnv(ctx, path, env, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", errors.New("resolve Git common directory")
	}
	gitDir := absoluteHookPath(path, normalizeHookPath(gitDirOutput))
	commonDir := absoluteHookPath(path, normalizeHookPath(commonDirOutput))
	if gitDir == "" || commonDir == "" {
		return "", errors.New("Git metadata path is malformed")
	}
	if SameRepositoryPath(gitDir, commonDir) {
		root, rootErr := s.hookCommandWithEnv(ctx, path, env, "rev-parse", "--show-toplevel")
		if rootErr != nil {
			return "", errors.New("resolve Git repository root")
		}
		return absoluteHookPath(path, normalizeHookPath(root)), nil
	}
	// This mirrors kit's linked-worktree handling. A common directory ending
	// in .git identifies the ordinary main checkout; submodule/separate-git-dir
	// layouts use the configured core.worktree from the common config.
	bare, bareErr := s.hookCommandWithEnv(ctx, path, env, "config", "--file", filepath.Join(commonDir, "config"), "--bool", "core.bare")
	if bareErr == nil && strings.EqualFold(strings.TrimSpace(bare), "true") {
		root, rootErr := s.hookCommandWithEnv(ctx, path, env, "rev-parse", "--show-toplevel")
		if rootErr != nil {
			return "", errors.New("resolve Git repository root")
		}
		return absoluteHookPath(path, normalizeHookPath(root)), nil
	}
	if strings.EqualFold(filepath.Base(commonDir), ".git") {
		return filepath.Dir(commonDir), nil
	}
	worktree, worktreeErr := s.hookCommandWithEnv(ctx, path, env, "config", "--file", filepath.Join(commonDir, "config"), "core.worktree")
	if worktreeErr != nil || strings.TrimSpace(worktree) == "" {
		return "", errors.New("resolve Git main worktree")
	}
	return absoluteHookPath(commonDir, normalizeHookPath(worktree)), nil
}

func (s *Service) hookCommandWithEnv(ctx context.Context, path string, env []string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", path}, args...)
	result, err := s.Runner.Run(ctx, proc.Command{
		Executable:  s.Git,
		Args:        commandArgs,
		Env:         env,
		Timeout:     10 * time.Second,
		OutputLimit: 64 << 10,
	})
	if err != nil || result.StdoutTruncated {
		return "", errors.New("Git hook path command failed")
	}
	return strings.TrimSpace(result.Stdout), nil
}

// sanitizedHookGitEnv builds the command environment once per HooksPath call.
// It strips inherited Git injection and then forwards only fresh
// safe.directory values as command-scope config. Global/system config itself
// is hidden from the actual resolver; no empty config file is created.
func (s *Service) sanitizedHookGitEnv(ctx context.Context, path string) []string {
	base := minimalHookGitEnv(os.Environ())
	safeDirectories := make([]string, 0, 4)
	for _, scope := range []string{"--system", "--global"} {
		result, err := s.Runner.Run(ctx, proc.Command{
			Executable:  s.Git,
			Args:        []string{"-C", path, "config", scope, "--includes", "-z", "--get-all", "safe.directory"},
			Env:         base,
			Timeout:     10 * time.Second,
			OutputLimit: 64 << 10,
		})
		if err != nil || result.StdoutTruncated {
			continue
		}
		if result.Stdout == "" {
			continue
		}
		for _, value := range strings.Split(strings.TrimSuffix(result.Stdout, "\x00"), "\x00") {
			// Empty safe.directory resets the protected list. Preserve it
			// exactly, matching kit's command-scope forwarding semantics.
			safeDirectories = append(safeDirectories, value)
		}
	}
	// Empty GIT_CONFIG_GLOBAL disables global config on Git for Windows and
	// Unix without the NUL-device compatibility issue or a filesystem write.
	env := minimalHookGitEnv(os.Environ())
	env = append(env, "GIT_CONFIG_GLOBAL=", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(safeDirectories)))
	for i, value := range safeDirectories {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=safe.directory", i), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, value))
	}
	return env
}

// minimalHookGitEnv retains only the process variables required to start Git
// and resolve home-relative config paths. HOME/USERPROFILE/HOMEDRIVE/HOMEPATH
// are location hints, not arbitrary config or credential inputs; all
// inherited GIT_* variables remain excluded by MinimalProbeEnv.
func minimalHookGitEnv(source []string) []string {
	env := proc.MinimalProbeEnv(source)
	return appendAllowedEnv(env, source, "HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH")
}

func appendAllowedEnv(result, source []string, keys ...string) []string {
	for _, entry := range source {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		for _, key := range keys {
			if strings.EqualFold(name, key) {
				result = append(result, entry)
				break
			}
		}
	}
	return result
}

func normalizeHookPath(value string) string {
	value = strings.TrimSpace(value)
	if runtime.GOOS == "windows" && len(value) >= 3 && value[0] == '/' && isASCIIAlpha(value[1]) && value[2] == '/' {
		value = strings.ToUpper(string(value[1])) + ":" + value[2:]
	}
	return filepath.FromSlash(value)
}

func absoluteHookPath(base, path string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path)
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
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
