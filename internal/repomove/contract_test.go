package repomove

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
)

type fakeFileSystem struct {
	info os.FileInfo
	err  error
}

func (f fakeFileSystem) Lstat(string) (os.FileInfo, error) { return f.info, f.err }

func TestBuildCommandUsesDestinationCwdAndExactSource(t *testing.T) {
	source, _ := filepath.Abs(filepath.Join("old", "project"))
	destination, _ := filepath.Abs(filepath.Join("new", "project"))
	command, err := BuildCommand("roborev", Request{SourcePath: source, SourceName: "project", DestinationPath: destination})
	if err != nil {
		t.Fatal(err)
	}
	if command.Dir != destination {
		t.Fatalf("dir=%q want %q", command.Dir, destination)
	}
	want := []string{"repo", "move", source, "."}
	if len(command.Args) != len(want) {
		t.Fatalf("args=%#v", command.Args)
	}
	for i := range want {
		if command.Args[i] != want[i] {
			t.Fatalf("args[%d]=%q want %q", i, command.Args[i], want[i])
		}
	}
}

func TestBuildCommandPinsUpstreamDotDestinationResolutionOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("upstream filepath and Git-root representation contract is Windows-specific")
	}

	canonicalRoot := filepath.Join(t.TempDir(), "canonical-repository")
	if err := os.MkdirAll(filepath.Join(canonicalRoot, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(filepath.Dir(canonicalRoot), "stale-repository")

	for _, cwd := range []string{canonicalRoot, filepath.Join(canonicalRoot, "nested")} {
		t.Run(filepath.Base(cwd), func(t *testing.T) {
			// This mirrors upstream cmd/roborev/repo.go's resolveMoveTargetPath:
			// filepath.Abs(".") uses the process cwd, gitrepo.Root resolves that
			// path to the canonical Git root, and the resolved storage spelling uses
			// filepath.ToSlash(root). The resolver is deliberately fake here.
			t.Chdir(cwd)
			absolutizedTarget, err := filepath.Abs(".")
			if err != nil {
				t.Fatal(err)
			}
			if !gitx.SameRepositoryPath(absolutizedTarget, cwd) {
				t.Fatalf("filepath.Abs(\".\")=%q does not resolve to cwd %q", absolutizedTarget, cwd)
			}
			resolvedRoot, err := fakeGitRootForMove(absolutizedTarget, canonicalRoot)
			if err != nil {
				t.Fatal(err)
			}
			storedDestination := filepath.ToSlash(resolvedRoot)

			command, err := BuildCommand("roborev", Request{
				SourcePath: source,
				// The Dashboard registry supplies the native canonical Git root;
				// only the upstream storage/argv resolver representation above is
				// slash-normalized.
				DestinationPath: canonicalRoot,
			})
			if err != nil {
				t.Fatal(err)
			}
			if storedDestination != filepath.ToSlash(canonicalRoot) {
				t.Fatalf("stored destination=%q want filepath.ToSlash(root)=%q", storedDestination, filepath.ToSlash(canonicalRoot))
			}
			if command.Dir != canonicalRoot {
				t.Fatalf("dir=%q want native canonical destination %q", command.Dir, canonicalRoot)
			}
			wantArgs := []string{"repo", "move", source, "."}
			if len(command.Args) != len(wantArgs) {
				t.Fatalf("args=%#v want %#v", command.Args, wantArgs)
			}
			for i := range wantArgs {
				if command.Args[i] != wantArgs[i] {
					t.Fatalf("args[%d]=%q want %q", i, command.Args[i], wantArgs[i])
				}
			}
		})
	}
}

func fakeGitRootForMove(absolutizedTarget, canonicalRoot string) (string, error) {
	nested := filepath.Join(canonicalRoot, "nested")
	if !gitx.SameRepositoryPath(absolutizedTarget, canonicalRoot) && !gitx.SameRepositoryPath(absolutizedTarget, nested) {
		return "", fmt.Errorf("fake Git-root resolver received unexpected absolute target %q", absolutizedTarget)
	}
	return canonicalRoot, nil
}

func TestValidateRequestAllowsEmptySourceName(t *testing.T) {
	source, _ := filepath.Abs("old")
	destination, _ := filepath.Abs("new")
	if _, err := ValidateRequest(Request{SourcePath: source, DestinationPath: destination}); err != nil {
		t.Fatalf("empty source name rejected: %v", err)
	}
}

func TestValidateRequestRequiresAbsoluteDistinctPaths(t *testing.T) {
	source, _ := filepath.Abs("old")
	destination, _ := filepath.Abs("new")
	base := Request{SourcePath: source, SourceName: "old", DestinationPath: destination}
	for _, test := range []struct {
		name string
		edit func(*Request)
		want error
	}{
		{name: "relative source", edit: func(r *Request) { r.SourcePath = "old" }, want: ErrSourcePathNotAbsolute},
		{name: "relative destination", edit: func(r *Request) { r.DestinationPath = "new" }, want: ErrDestinationNotAbsolute},
		{name: "same", edit: func(r *Request) { r.DestinationPath = r.SourcePath }, want: ErrSourceDestinationSame},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.edit(&request)
			if _, err := ValidateRequest(request); !errors.Is(err, test.want) {
				t.Fatalf("err=%v want errors.Is(...,%v)", err, test.want)
			}
		})
	}
}

func TestSourceAbsentFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		fs   fakeFileSystem
		want error
	}{
		{name: "missing", fs: fakeFileSystem{err: os.ErrNotExist}},
		{name: "exists file", fs: fakeFileSystem{info: fakeInfo{}}, want: ErrSourceExists},
		{name: "exists directory", fs: fakeFileSystem{info: fakeInfo{mode: os.ModeDir}}, want: ErrSourceExists},
		{name: "exists symlink", fs: fakeFileSystem{info: fakeInfo{mode: os.ModeSymlink}}, want: ErrSourceExists},
		{name: "permission", fs: fakeFileSystem{err: os.ErrPermission}, want: ErrReadSource},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := SourceAbsent(test.fs, filepath.Join(string(filepath.Separator), "old"))
			if test.want == nil {
				if err != nil {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v want errors.Is(...,%v)", err, test.want)
			}
		})
	}
}

type fakeInfo struct{ mode os.FileMode }

func (fakeInfo) Name() string        { return "entry" }
func (fakeInfo) Size() int64         { return 0 }
func (f fakeInfo) Mode() os.FileMode { return f.mode }
func (fakeInfo) ModTime() time.Time  { return time.Time{} }
func (fakeInfo) IsDir() bool         { return false }
func (fakeInfo) Sys() any            { return nil }

func TestSourceFallbackKeyPreservesNativeWindowsResolution(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows filepath resolver contract")
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	volume := filepath.VolumeName(workingDirectory)
	if volume == "" {
		t.Skip("test drive unavailable")
	}
	forward := volume + "/old/repository"
	backslash := volume + `\old\repository`
	for _, source := range []string{forward, backslash} {
		want, err := filepath.Abs(source)
		if err != nil {
			t.Fatal(err)
		}
		got, err := SourceFallbackKey(source)
		if err != nil || got != want || strings.Contains(got, "/") {
			t.Fatalf("source=%q got=%q want native %q err=%v", source, got, want, err)
		}
	}
}
