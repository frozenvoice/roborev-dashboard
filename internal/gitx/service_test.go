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
