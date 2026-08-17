package gitx

import (
	"context"
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
}

func (r *recordingRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	return proc.Result{Stdout: r.stdout}, nil
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
