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
	"unicode/utf8"

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

// HookLayout is the complete, server-side Git hook resolution snapshot used
// by mutation preflight. Paths in this type never cross the HTTP boundary.
// HooksPath is Git's effective path; GitDir and CommonDir are the two
// allowlisted containment roots. EffectiveHooksPath records the effective
// core.hooksPath value (when present) so callers can reject the v0.64
// EnsureAbsoluteHooksPath normalization side effect before launching CLI.
type HookLayout struct {
	Root                         string
	GitDir                       string
	CommonDir                    string
	HooksPath                    string
	EffectiveHooksPath           string
	EffectiveHooksPathSet        bool
	EffectiveHooksPathIsRelative bool
	Safe                         bool
	Ambiguous                    bool
}

type Service struct {
	Git    string
	Runner proc.Runner
}

// CIWorkflowTarget is the only repository target inspected by CI Workflow
// Management. It is intentionally a relative, server-owned constant; callers
// must never substitute a browser-provided path.
const CIWorkflowTarget = ".github/workflows/roborev.yml"

// CIWorkflowRepositoryState is the small, typed Git snapshot needed by the
// CI workflow management flow. Root and all other fields are server-side
// values and are not a browser path-selection API.
type CIWorkflowRepositoryState struct {
	Root     string
	Branch   string
	Detached bool
	Head     string
	Dirty    bool
	Ignored  bool
}

// SnapshotProtection reports only the two facts needed by Repository Setup
// Readiness. Relative is server-validated; this method never accepts an
// arbitrary browser path.
type SnapshotProtection struct {
	Tracked bool
	Ignored bool
}

// SnapshotProbe performs the fixed, read-only Git probes for a validated
// repository-local snapshot directory. The tracked check intentionally wins
// over ignore rules because Git can report a tracked path as ignored by a
// broader rule.
func (s *Service) SnapshotProbe(ctx context.Context, root, relative string) (SnapshotProtection, error) {
	if s == nil || s.Runner == nil || strings.TrimSpace(root) == "" || strings.TrimSpace(relative) == "" || filepath.IsAbs(relative) || !filepath.IsLocal(relative) {
		return SnapshotProtection{}, errors.New("snapshot probe unavailable")
	}
	root = filepath.Clean(root)
	env := s.snapshotGitEnv()
	rootResult, rootErr := s.runSnapshotGit(ctx, root, env, "rev-parse", "--show-toplevel")
	if rootErr != nil || rootResult.ExitCode != 0 {
		return SnapshotProtection{}, errors.New("snapshot probe unavailable")
	}
	resolvedRoot, err := filepath.Abs(filepath.Clean(strings.TrimSpace(rootResult.Stdout)))
	if err != nil || !SameRepositoryPath(resolvedRoot, root) {
		return SnapshotProtection{}, errors.New("snapshot probe unavailable")
	}
	trackedResult, trackedErr := s.runSnapshotGit(ctx, root, env, "ls-files", "--", filepath.ToSlash(relative))
	if trackedErr != nil || trackedResult.ExitCode != 0 {
		return SnapshotProtection{}, errors.New("snapshot probe unavailable")
	}
	if strings.TrimSpace(trackedResult.Stdout) != "" {
		return SnapshotProtection{Tracked: true}, nil
	}
	probePath := filepath.ToSlash(filepath.Join(relative, ".roborev-ignore-check"))
	ignoreResult, ignoreErr := s.runSnapshotGit(ctx, root, env, "check-ignore", "--quiet", "--no-index", "--", probePath)
	if ignoreResult.ExitCode == 0 {
		if ignoreErr != nil || strings.TrimSpace(ignoreResult.Stdout) != "" || strings.TrimSpace(ignoreResult.Stderr) != "" {
			return SnapshotProtection{}, errors.New("snapshot probe unavailable")
		}
		return SnapshotProtection{Ignored: true}, nil
	}
	if ignoreResult.ExitCode == 1 && ignoreErr != nil && strings.TrimSpace(ignoreResult.Stdout) == "" && strings.TrimSpace(ignoreResult.Stderr) == "" {
		return SnapshotProtection{}, nil
	}
	return SnapshotProtection{}, errors.New("snapshot probe unavailable")
}

func (s *Service) runSnapshotGit(ctx context.Context, root string, env []string, args ...string) (proc.Result, error) {
	commandArgs := append([]string{"-C", root}, args...)
	result, runErr := s.Runner.Run(ctx, proc.Command{Executable: s.Git, Args: commandArgs, Env: env, Timeout: 10 * time.Second, OutputLimit: 64 << 10})
	if result.StdoutTruncated || result.StderrTruncated || !utf8.ValidString(result.Stdout) || !utf8.ValidString(result.Stderr) {
		return result, errors.New("snapshot probe unavailable")
	}
	return result, runErr
}

// snapshotGitEnv removes all inherited GIT_* routing/configuration variables
// while retaining HOME so persistent global/system ignore rules remain active.
func (s *Service) snapshotGitEnv() []string {
	env := minimalHookGitEnv(os.Environ())
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
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

// ResolveHookLayout resolves Git's effective hook directory and metadata with
// one sanitized environment. It is deliberately read-only: unlike the
// upstream install-hook command it never invokes EnsureAbsoluteHooksPath or
// writes repository configuration.
func (s *Service) ResolveHookLayout(ctx context.Context, path string) (HookLayout, error) {
	if strings.TrimSpace(path) == "" {
		return HookLayout{}, errors.New("repository path is required")
	}
	input, err := filepath.Abs(path)
	if err != nil {
		return HookLayout{}, errors.New("repository path is malformed")
	}
	env := s.sanitizedHookGitEnv(ctx, input)
	rootValue, err := s.hookCommandWithEnv(ctx, input, env, "rev-parse", "--show-toplevel")
	if err != nil {
		return HookLayout{}, errors.New("resolve Git repository root")
	}
	root := absoluteHookPath(input, normalizeHookPath(rootValue))
	if root == "" {
		return HookLayout{}, errors.New("Git repository root is malformed")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = filepath.Clean(resolved)
	}
	gitDirValue, err := s.hookCommandWithEnv(ctx, root, env, "rev-parse", "--git-dir")
	if err != nil {
		return HookLayout{}, errors.New("resolve Git directory")
	}
	commonDirValue, err := s.hookCommandWithEnv(ctx, root, env, "rev-parse", "--git-common-dir")
	if err != nil {
		return HookLayout{}, errors.New("resolve Git common directory")
	}
	hooksValue, err := s.hookCommandWithEnv(ctx, root, env, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return HookLayout{}, errors.New("resolve Git hooks path")
	}
	gitDir := absoluteHookPath(root, normalizeHookPath(gitDirValue))
	commonDir := absoluteHookPath(root, normalizeHookPath(commonDirValue))
	hooksPath := absoluteHookPath(root, normalizeHookPath(hooksValue))
	if gitDir == "" || commonDir == "" || hooksPath == "" {
		return HookLayout{}, errors.New("Git metadata path is malformed")
	}
	// rev-parse --git-path hooks returns a relative path rooted in the main
	// checkout for linked worktrees, matching kit v0.1.8 HooksPath.
	if !filepath.IsAbs(normalizeHookPath(hooksValue)) {
		mainRoot, mainErr := s.mainRootForHooks(ctx, root, env)
		if mainErr != nil {
			return HookLayout{}, mainErr
		}
		hooksPath = filepath.Clean(filepath.Join(mainRoot, normalizeHookPath(hooksValue)))
	}
	effectiveValue, effectiveSet, configErr := s.effectiveHooksPath(ctx, root, env)
	if configErr != nil {
		return HookLayout{}, configErr
	}
	lower := strings.TrimSpace(effectiveValue)
	relative := effectiveSet && lower != "" && !filepath.IsAbs(normalizeHookPath(lower)) && !isTildePath(lower)
	canonicalGit, gitAmbiguous := strictCanonicalPath(gitDir)
	canonicalCommon, commonAmbiguous := strictCanonicalPath(commonDir)
	canonicalHooks, hooksAmbiguous := strictCanonicalPath(hooksPath)
	if canonicalGit == "" || canonicalCommon == "" || canonicalHooks == "" {
		gitAmbiguous, commonAmbiguous, hooksAmbiguous = true, true, true
	}
	safe := false
	if !gitAmbiguous && !commonAmbiguous && !hooksAmbiguous {
		safe = pathWithin(canonicalHooks, canonicalGit) || pathWithin(canonicalHooks, canonicalCommon)
	}
	return HookLayout{Root: root, GitDir: canonicalGit, CommonDir: canonicalCommon, HooksPath: canonicalHooks, EffectiveHooksPath: effectiveValue, EffectiveHooksPathSet: effectiveSet, EffectiveHooksPathIsRelative: relative, Safe: safe, Ambiguous: gitAmbiguous || commonAmbiguous || hooksAmbiguous}, nil
}

// effectiveHooksPath mirrors the exact read performed by kit v0.1.8's
// EnsureAbsoluteHooksPath: `git config core.hooksPath`, not --local/--get. Git exit
// status 1 means the key is unset; every other command failure is an
// inspection error and therefore fails closed instead of being treated as a
// safe default.
func (s *Service) effectiveHooksPath(ctx context.Context, root string, env []string) (string, bool, error) {
	args := []string{"-C", root, "config", "core.hooksPath"}
	result, err := s.Runner.Run(ctx, proc.Command{Executable: s.Git, Args: args, Env: env, Timeout: 10 * time.Second, OutputLimit: 64 << 10})
	if err != nil || result.StdoutTruncated {
		if !result.StdoutTruncated && result.ExitCode == 1 {
			return "", false, nil
		}
		return "", false, errors.New("read Git hooks path configuration")
	}
	value := strings.TrimSpace(result.Stdout)
	if value == "" {
		return "", false, nil
	}
	return value, true, nil
}

func isTildePath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		return true
	}
	return len(value) > 1 && value[0] == '~' && (isASCIIAlpha(value[1]) || value[1] == '_')
}

// strictCanonicalPath resolves every existing component and fails closed on
// symlink/reparse ambiguity. A not-yet-created final hooks directory is safe
// when all existing parents are ordinary directories.
func strictCanonicalPath(path string) (string, bool) {
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", true
	}
	root := filepath.VolumeName(path) + string(filepath.Separator)
	if root == string(filepath.Separator) {
		root = string(filepath.Separator)
	}
	current := root
	remainder := strings.TrimPrefix(path, root)
	for _, component := range strings.FieldsFunc(remainder, func(r rune) bool { return r == '/' || r == '\\' }) {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return filepath.Clean(path), false
			}
			return "", true
		}
		if pathComponentAmbiguous(info) {
			return "", true
		}
		if current == path && !info.IsDir() {
			return "", true
		}
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return filepath.Clean(path), false
	}
	return filepath.Clean(resolved), false
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
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

// InspectCIWorkflow performs the fixed-target, read-only Git inspection used
// by CI Workflow Management. Every invocation uses one sanitized environment
// and typed argument arrays; in particular, no inherited GIT_* routing is
// allowed to redirect the repository or config lookup.
//
// The method deliberately does not inspect a caller-supplied target path. The
// target is always CIWorkflowTarget, resolved relative to the canonical Git
// root returned by rev-parse.
func (s *Service) InspectCIWorkflow(ctx context.Context, path string) (CIWorkflowRepositoryState, error) {
	if strings.TrimSpace(path) == "" {
		return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
	}
	input, err := filepath.Abs(path)
	if err != nil {
		return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
	}
	if resolved, evalErr := filepath.EvalSymlinks(input); evalErr == nil {
		input = filepath.Clean(resolved)
	} else {
		input = filepath.Clean(input)
	}
	env, err := s.sanitizedCIWorkflowGitEnv(ctx, input)
	if err != nil {
		return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
	}

	rootResult, err := s.runCIWorkflowGit(ctx, input, env, "rev-parse", "--show-toplevel")
	if err != nil || rootResult.ExitCode != 0 {
		return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
	}
	root, err := canonicalCIWorkflowPath(rootResult.Stdout)
	if err != nil || !SameRepositoryPath(root, input) {
		return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
	}

	headResult, err := s.runCIWorkflowGit(ctx, root, env, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || headResult.ExitCode != 0 {
		return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
	}
	head, ok := fullCommitSHA(headResult.Stdout)
	if !ok {
		return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
	}

	branchResult, branchErr := s.runCIWorkflowGit(ctx, root, env, "symbolic-ref", "--quiet", "--short", "HEAD")
	detached := false
	branch := ""
	if branchResult.ExitCode == 1 {
		// Git uses exit 1 specifically for a detached HEAD. Keep this separate
		// from a branch literally named "detached".
		if branchErr == nil || strings.TrimSpace(branchResult.Stdout) != "" {
			return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
		}
		detached = true
		branch = "detached"
	} else {
		if branchErr != nil || branchResult.ExitCode != 0 {
			return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
		}
		branch, ok = singleLineGitOutput(branchResult.Stdout)
		if !ok {
			return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
		}
	}

	statusResult, err := s.runCIWorkflowGit(ctx, root, env, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil || statusResult.ExitCode != 0 {
		return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
	}

	ignoreResult, ignoreErr := s.runCIWorkflowGit(ctx, root, env, "check-ignore", "--quiet", "--no-index", "--", CIWorkflowTarget)
	ignored := false
	if ignoreResult.ExitCode == 0 {
		if ignoreErr != nil || strings.TrimSpace(ignoreResult.Stdout) != "" {
			return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
		}
		ignored = true
	} else if ignoreResult.ExitCode == 1 {
		if ignoreErr == nil || strings.TrimSpace(ignoreResult.Stdout) != "" {
			return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
		}
	} else {
		return CIWorkflowRepositoryState{}, errors.New("Git CI workflow inspection unavailable")
	}

	return CIWorkflowRepositoryState{
		Root:     root,
		Branch:   branch,
		Detached: detached,
		Head:     head,
		Dirty:    strings.TrimSpace(statusResult.Stdout) != "",
		Ignored:  ignored,
	}, nil
}

// InspectCIWorkflowTarget is an explicit alias for callers that prefer to
// name the fixed-target nature of InspectCIWorkflow. Both methods retain the
// same server-owned path and read-only semantics.
func (s *Service) InspectCIWorkflowTarget(ctx context.Context, path string) (CIWorkflowRepositoryState, error) {
	return s.InspectCIWorkflow(ctx, path)
}

func (s *Service) runCIWorkflowGit(ctx context.Context, path string, env []string, args ...string) (proc.Result, error) {
	commandArgs := append([]string{"-C", path}, args...)
	result, runErr := s.Runner.Run(ctx, proc.Command{
		Executable:  s.Git,
		Args:        commandArgs,
		Env:         env,
		Timeout:     10 * time.Second,
		OutputLimit: 1 << 20,
	})
	if result.StdoutTruncated || result.StderrTruncated || !utf8.ValidString(result.Stdout) || !utf8.ValidString(result.Stderr) {
		return result, errors.New("Git CI workflow inspection unavailable")
	}
	return result, runErr
}

// sanitizedCIWorkflowGitEnv is the strict counterpart to the broader hook
// inspection environment helper. A missing safe.directory key is expected
// (Git exits 1); every other config-probe failure is rejected so an unknown
// or truncated probe cannot silently broaden repository authority.
func (s *Service) sanitizedCIWorkflowGitEnv(ctx context.Context, path string) ([]string, error) {
	base := minimalHookGitEnv(os.Environ())
	base = append(base, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	safeDirectories := make([]string, 0, 4)
	for _, scope := range []string{"--system", "--global"} {
		result, runErr := s.Runner.Run(ctx, proc.Command{
			Executable:  s.Git,
			Args:        []string{"-C", path, "config", scope, "--includes", "-z", "--get-all", "safe.directory"},
			Env:         base,
			Timeout:     10 * time.Second,
			OutputLimit: 64 << 10,
		})
		if result.StdoutTruncated || result.StderrTruncated || !utf8.ValidString(result.Stdout) || !utf8.ValidString(result.Stderr) {
			return nil, errors.New("Git CI workflow inspection unavailable")
		}
		if result.ExitCode == 1 {
			if strings.TrimSpace(result.Stdout) != "" {
				return nil, errors.New("Git CI workflow inspection unavailable")
			}
			continue
		}
		if runErr != nil || result.ExitCode != 0 {
			return nil, errors.New("Git CI workflow inspection unavailable")
		}
		if result.Stdout == "" {
			continue
		}
		if !strings.HasSuffix(result.Stdout, "\x00") {
			return nil, errors.New("Git CI workflow inspection unavailable")
		}
		for _, value := range strings.Split(strings.TrimSuffix(result.Stdout, "\x00"), "\x00") {
			safeDirectories = append(safeDirectories, value)
		}
	}
	env := minimalHookGitEnv(os.Environ())
	env = append(env, "GIT_CONFIG_GLOBAL=", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(safeDirectories)))
	for index, value := range safeDirectories {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=safe.directory", index), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", index, value))
	}
	return env, nil
}

func canonicalCIWorkflowPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("Git CI workflow inspection unavailable")
	}
	path := filepath.FromSlash(value)
	if !filepath.IsAbs(path) {
		return "", errors.New("Git CI workflow inspection unavailable")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("Git CI workflow inspection unavailable")
	}
	if resolved, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func fullCommitSHA(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || (len(value) != 40 && len(value) != 64) {
		return "", false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return "", false
		}
	}
	return value, true
}

func singleLineGitOutput(value string) (string, bool) {
	if !utf8.ValidString(value) {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", false
	}
	return value, true
}
