package gitx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type recordingRunner struct {
	commands []proc.Command
	stdout   string
	err      error
}

type hooksPathRunner struct {
	commands []proc.Command
	outputs  map[string]string
}

func (r *hooksPathRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	key := strings.Join(command.Args[2:], " ")
	return proc.Result{Stdout: r.outputs[key]}, nil
}

func TestHooksPathUsesGitPathAndSanitizedEnvironment(t *testing.T) {
	t.Setenv("HOME", `C:\Users\hook-home`)
	t.Setenv("USERPROFILE", `C:\Users\hook-user`)
	t.Setenv("HOMEDRIVE", `C:`)
	t.Setenv("HOMEPATH", `\Users\hook-user`)
	t.Setenv("GIT_DIR", `C:\attacker\git`)
	t.Setenv("GIT_CONFIG_GLOBAL", `C:\attacker\config`)
	t.Setenv("HOOK_STATUS_SECRET", "must-not-leak")
	root := filepath.Join(t.TempDir(), "repo")
	runner := &hooksPathRunner{outputs: map[string]string{
		"rev-parse --git-path hooks": ".githooks",
		"rev-parse --git-dir":        filepath.Join(root, ".git"),
		"rev-parse --git-common-dir": filepath.Join(root, ".git"),
		"rev-parse --show-toplevel":  root,
	}}
	service := New(runner)
	got, err := service.HooksPath(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".githooks"); got != want {
		t.Fatalf("hooks path=%q want=%q", got, want)
	}
	if len(runner.commands) != 6 {
		t.Fatalf("commands=%#v", runner.commands)
	}
	var command proc.Command
	for _, candidate := range runner.commands {
		if strings.Contains(strings.Join(candidate.Args, " "), "rev-parse --git-path hooks") {
			command = candidate
			break
		}
	}
	if command.Executable == "" {
		t.Fatalf("hooks path command missing: %#v", runner.commands)
	}
	if !reflect.DeepEqual(command.Args, []string{"-C", root, "rev-parse", "--git-path", "hooks"}) {
		t.Fatalf("unexpected args=%#v", command.Args)
	}
	for _, entry := range command.Env {
		if strings.HasPrefix(strings.ToUpper(entry), "GIT_DIR=") || strings.HasPrefix(strings.ToUpper(entry), "GIT_WORK_TREE=") {
			t.Fatalf("unsafe inherited Git environment: %q", entry)
		}
	}
	if !containsEnv(command.Env, "GIT_CONFIG_NOSYSTEM=1") || !containsEnv(command.Env, "GIT_CONFIG_GLOBAL=") || !containsEnv(command.Env, "GIT_CONFIG_COUNT=0") {
		t.Fatalf("missing config isolation env: %#v", command.Env)
	}
	for _, expected := range []string{
		`HOME=C:\Users\hook-home`,
		`USERPROFILE=C:\Users\hook-user`,
		`HOMEDRIVE=C:`,
		`HOMEPATH=\Users\hook-user`,
	} {
		if !containsEnv(command.Env, expected) {
			t.Fatalf("home-location variable %q missing from resolver env: %#v", expected, command.Env)
		}
	}
	for _, entry := range command.Env {
		upper := strings.ToUpper(entry)
		if strings.HasPrefix(upper, "GIT_DIR=") || strings.HasPrefix(upper, "GIT_CONFIG_GLOBAL=C:") || strings.HasPrefix(upper, "HOOK_STATUS_SECRET=") {
			t.Fatalf("unsafe resolver environment entry: %q", entry)
		}
	}
}

func TestHooksPathForwardsSafeDirectoryResetValuesAtCommandScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	runner := &hooksPathRunner{outputs: map[string]string{
		"config --system --includes -z --get-all safe.directory": "C:/trusted\x00\x00",
		"config --global --includes -z --get-all safe.directory": "D:/trusted\x00",
		"rev-parse --git-path hooks":                             filepath.Join(root, ".git", "hooks"),
	}}
	if _, err := New(runner).HooksPath(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	var resolverCommand *proc.Command
	for index := range runner.commands {
		if strings.Contains(strings.Join(runner.commands[index].Args, " "), "rev-parse --git-path hooks") {
			resolverCommand = &runner.commands[index]
			break
		}
	}
	if resolverCommand == nil {
		t.Fatalf("resolver command missing: %#v", runner.commands)
	}
	if !containsEnv(resolverCommand.Env, "GIT_CONFIG_COUNT=3") ||
		!containsEnv(resolverCommand.Env, "GIT_CONFIG_KEY_0=safe.directory") ||
		!containsEnv(resolverCommand.Env, "GIT_CONFIG_VALUE_0=C:/trusted") ||
		!containsEnv(resolverCommand.Env, "GIT_CONFIG_VALUE_1=") ||
		!containsEnv(resolverCommand.Env, "GIT_CONFIG_VALUE_2=D:/trusted") {
		t.Fatalf("safe.directory forwarding=%#v", resolverCommand.Env)
	}
}

func TestHooksPathResolvesLinkedWorktreeMainRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "main")
	common := filepath.Join(root, ".git")
	worktree := filepath.Join(common, "worktrees", "feature")
	runner := &hooksPathRunner{outputs: map[string]string{
		"rev-parse --git-path hooks": ".githooks",
		"rev-parse --git-dir":        worktree,
		"rev-parse --git-common-dir": common,
	}}
	got, err := New(runner).HooksPath(context.Background(), filepath.Join(root, "feature"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".githooks"); got != want {
		t.Fatalf("hooks path=%q want=%q", got, want)
	}
}

func TestHooksPathResolvesSeparateGitDirThroughCoreWorktree(t *testing.T) {
	base := t.TempDir()
	worktree := filepath.Join(base, "checkout")
	mainRoot := filepath.Join(base, "main")
	common := filepath.Join(base, "metadata")
	gitDir := filepath.Join(common, "worktrees", "checkout")
	configFile := filepath.Join(common, "config")
	runner := &hooksPathRunner{outputs: map[string]string{
		"rev-parse --git-path hooks":                        ".githooks",
		"rev-parse --git-dir":                               gitDir,
		"rev-parse --git-common-dir":                        common,
		"config --file " + configFile + " --bool core.bare": "false",
		"config --file " + configFile + " core.worktree":    filepath.Join("..", "main"),
	}}
	got, err := New(runner).HooksPath(context.Background(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(mainRoot, ".githooks"); got != want {
		t.Fatalf("hooks path=%q want=%q", got, want)
	}
}

func TestHooksPathNormalizesMSYSAbsoluteOutput(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("MSYS path conversion is Windows-specific")
	}
	runner := &hooksPathRunner{outputs: map[string]string{"rev-parse --git-path hooks": "/c/Workspace/hooks"}}
	got, err := New(runner).HooksPath(context.Background(), `C:\Workspace`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `C:\Workspace\hooks` {
		t.Fatalf("hooks path=%q", got)
	}
}

func containsEnv(env []string, expected string) bool {
	for _, entry := range env {
		if entry == expected {
			return true
		}
	}
	return false
}

func (r *recordingRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	return proc.Result{Stdout: r.stdout}, r.err
}

type inspectRunner struct {
	commands []proc.Command
	root     string
	branch   string
}

func (r *inspectRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	args := command.Args
	if len(args) >= 2 && args[len(args)-2] == "rev-parse" && args[len(args)-1] == "--show-toplevel" {
		return proc.Result{Stdout: r.root}, nil
	}
	if len(args) >= 2 && args[len(args)-2] == "rev-parse" && args[len(args)-1] == "HEAD" {
		return proc.Result{Stdout: "abcdef0123456789"}, nil
	}
	if len(args) >= 2 && args[len(args)-2] == "branch" && args[len(args)-1] == "--show-current" {
		return proc.Result{Stdout: r.branch}, nil
	}
	if len(args) >= 3 && args[len(args)-3] == "log" && args[len(args)-2] == "-1" {
		return proc.Result{Stdout: "subject"}, nil
	}
	if len(args) >= 3 && args[len(args)-3] == "status" && args[len(args)-2] == "--porcelain=v1" {
		return proc.Result{}, nil
	}
	return proc.Result{}, errors.New("unexpected Git command")
}

func TestIsLinkedWorktreeUsesTypedMetadataCommandAndPreservesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "한글 repository with spaces")
	common := filepath.Join(t.TempDir(), "common")
	gitDir := filepath.Join(t.TempDir(), "worktrees", "feature")
	runner := &recordingRunner{stdout: gitDir + "\n" + common}
	service := New(runner)
	linked, err := service.IsLinkedWorktree(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !linked {
		t.Fatal("different canonical git-dir/common-dir was not classified as linked")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	command := runner.commands[0]
	wantArgs := []string{"-C", abs, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir"}
	if command.Executable != service.Git || !reflect.DeepEqual(command.Args, wantArgs) {
		t.Fatalf("unexpected Git command: executable=%q args=%#v want=%#v", command.Executable, command.Args, wantArgs)
	}
}

func TestIsLinkedWorktreeKeepsOrdinarySubmoduleAndSeparateGitDirLayouts(t *testing.T) {
	for _, name := range []string{"ordinary", "submodule", "separate-git-dir"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			metadata := filepath.Join(root, "metadata", name)
			linked, err := New(&recordingRunner{stdout: metadata + "\n" + metadata}).IsLinkedWorktree(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if linked {
				t.Fatalf("layout %s was classified as linked", name)
			}
		})
	}
}

func TestIsLinkedWorktreeDoesNotSuppressOnMetadataFailureOrMalformedOutput(t *testing.T) {
	for _, test := range []struct {
		name   string
		stdout string
		err    error
	}{
		{name: "malformed", stdout: "/only/one/path"},
		{name: "relative", stdout: "relative/git\nrelative/common"},
		{name: "command-error", err: errors.New("git failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			linked, err := New(&recordingRunner{stdout: test.stdout, err: test.err}).IsLinkedWorktree(context.Background(), t.TempDir())
			if err == nil || linked {
				t.Fatalf("metadata failure did not fail closed: linked=%v err=%v", linked, err)
			}
		})
	}
}

func TestCanonicalRootRejectsBlankWithoutRunningGit(t *testing.T) {
	runner := &recordingRunner{}
	service := New(runner)
	if _, err := service.CanonicalRoot(context.Background(), " \t\r\n"); err == nil {
		t.Fatal("expected blank path rejection")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("git unexpectedly invoked: %#v", runner.commands)
	}
}

func TestCanonicalRootRejectsSubdirectory(t *testing.T) {
	root := t.TempDir()
	subdirectory := filepath.Join(root, "nested")
	if err := os.Mkdir(subdirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{stdout: root}
	if _, err := New(runner).CanonicalRoot(context.Background(), subdirectory); err == nil {
		t.Fatal("expected subdirectory rejection")
	}
}

func TestCanonicalRootUsesExactExecutableAndArgumentArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "한글 project with spaces; $(not-shell)")
	runner := &recordingRunner{}
	service := New(runner)
	service.Git = `C:\Program Files\Git\cmd\git.exe`
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	runner.stdout = abs
	got, err := service.CanonicalRoot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(abs) {
		t.Fatalf("canonical root = %q, want %q", got, filepath.Clean(abs))
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(runner.commands))
	}
	command := runner.commands[0]
	if command.Executable != service.Git {
		t.Fatalf("executable = %q", command.Executable)
	}
	wantArgs := []string{"-C", abs, "rev-parse", "--show-toplevel"}
	if !reflect.DeepEqual(command.Args, wantArgs) {
		t.Fatalf("argument boundaries changed\n got: %#v\nwant: %#v", command.Args, wantArgs)
	}
}

func TestCanonicalRootAcceptsWindowsStyleSpacesAndUTF8AsOneArgument(t *testing.T) {
	paths := []string{`C:\Workspace With Spaces\repository`, `C:\개발\로보 리뷰`}
	for _, input := range paths {
		t.Run(input, func(t *testing.T) {
			abs, err := filepath.Abs(input)
			if err != nil {
				t.Fatal(err)
			}
			runner := &recordingRunner{stdout: abs}
			if _, err := New(runner).CanonicalRoot(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			if got := runner.commands[0].Args; len(got) != 4 || got[1] != abs {
				t.Fatalf("path was not preserved as one argument: %#v", got)
			}
		})
	}
}

func TestCanonicalRootResolvesSymlinkWhenSupported(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(filepath.Dir(target), filepath.Base(target)+"-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	runner := &recordingRunner{stdout: target}
	got, err := New(runner).CanonicalRoot(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if !SameRepositoryPath(got, resolved) {
		t.Fatalf("root = %q, want resolved target %q", got, resolved)
	}
	if runner.commands[0].Args[1] != resolved {
		t.Fatalf("git received unresolved path: %#v", runner.commands[0].Args)
	}
}

func TestCanonicalRootCaseSemanticsFollowPlatform(t *testing.T) {
	input := t.TempDir()
	alternate := strings.ToUpper(input)
	if alternate == input {
		alternate = strings.ToLower(input)
	}
	runner := &recordingRunner{stdout: alternate}
	_, err := New(runner).CanonicalRoot(context.Background(), input)
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("Windows path casing should share identity: %v", err)
		}
	} else if err == nil {
		t.Fatal("case-sensitive platform accepted differently-cased canonical root")
	}
}

func TestInspectDistinguishesActualDetachedFromLiteralDetachedBranch(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name         string
		branchOutput string
		wantBranch   string
		wantDetached bool
	}{
		{name: "actual detached", branchOutput: "", wantBranch: "detached", wantDetached: true},
		{name: "literal detached branch", branchOutput: "detached", wantBranch: "detached"},
		{name: "main", branchOutput: "main", wantBranch: "main"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &inspectRunner{root: root, branch: test.branchOutput}
			repo, err := New(runner).Inspect(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if repo.Branch != test.wantBranch || repo.Detached != test.wantDetached {
				t.Fatalf("branch=%q detached=%v, want branch=%q detached=%v", repo.Branch, repo.Detached, test.wantBranch, test.wantDetached)
			}
			encoded, err := json.Marshal(repo)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if _, ok := fields["detached"]; ok {
				t.Fatalf("browser repository JSON leaked detached safety field: %s", encoded)
			}
			if len(runner.commands) != 5 {
				t.Fatalf("commands=%d, want 5", len(runner.commands))
			}
		})
	}
}
