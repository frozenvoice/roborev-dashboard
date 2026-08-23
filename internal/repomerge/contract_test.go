package repomerge

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/repomove"
)

func TestBuildCommandUsesExactYesArgsAndEmptyWorkingDirectory(t *testing.T) {
	source, _ := filepath.Abs(filepath.Join("stale", "source"))
	target, _ := filepath.Abs(filepath.Join("tracked", "target"))
	command, err := BuildCommand("roborev.exe", Request{SourcePath: source, TargetPath: target})
	if err != nil {
		t.Fatal(err)
	}
	if command.Executable != "roborev.exe" || command.WorkingDirectory != "" {
		t.Fatalf("command=%#v", command)
	}
	want := []string{"repo", "merge", "--yes", source, target}
	if strings.Join(command.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args=%#v want %#v", command.Args, want)
	}
}

func TestValidateRequestMatrix(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	target, _ := filepath.Abs("tracked/target")
	base := Request{SourcePath: source, TargetPath: target}
	tests := []struct {
		name    string
		request Request
		want    error
	}{
		{name: "missing source", request: Request{TargetPath: target}, want: ErrSourcePathRequired},
		{name: "relative source", request: Request{SourcePath: "stale/source", TargetPath: target}, want: ErrSourcePathNotAbsolute},
		{name: "missing target", request: Request{SourcePath: source}, want: ErrTargetPathRequired},
		{name: "relative target", request: Request{SourcePath: source, TargetPath: "tracked/target"}, want: ErrTargetPathNotAbsolute},
		{name: "same path", request: Request{SourcePath: source, TargetPath: source}, want: ErrSourceTargetSame},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ValidateRequest(test.request); !errors.Is(err, test.want) {
				t.Fatalf("err=%v want errors.Is(...,%v)", err, test.want)
			}
		})
	}
	if got, err := ValidateRequest(base); err != nil || got != base {
		t.Fatalf("valid request=%#v err=%v", got, err)
	}
}

func TestSourceAbsentReusesMovePrimitiveAndFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "missing", err: os.ErrNotExist},
		{name: "exists", err: nil, want: repomove.ErrSourceExists},
		{name: "permission", err: os.ErrPermission, want: repomove.ErrReadSource},
	} {
		t.Run(test.name, func(t *testing.T) {
			fs := fakeFS{err: test.err}
			if test.err == nil {
				fs.info = fakeInfo{}
			}
			err := SourceAbsent(fs, filepath.Join(string(filepath.Separator), "stale"))
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

func TestFallbackKeysPreserveUpstreamPlatformSpelling(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native separator and drive spelling are Windows-specific")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	volume := filepath.VolumeName(wd)
	if volume == "" {
		t.Skip("no drive volume")
	}
	forward := volume + "/stale/project"
	backslash := volume + `\stale\project`
	sourceWant, _ := filepath.Abs(backslash)
	if got, _ := SourceFallbackKey(forward); got != sourceWant {
		t.Fatalf("source key=%q want native %q", got, sourceWant)
	}
	targetWant, _ := filepath.Abs(backslash)
	if got, _ := TargetFallbackKey(forward); got != targetWant {
		t.Fatalf("target key=%q want native %q", got, targetWant)
	}
	if got, _ := SourceFallbackKey(backslash); got != sourceWant {
		t.Fatalf("backslash source key=%q want %q", got, sourceWant)
	}
}

type fakeFS struct {
	info os.FileInfo
	err  error
}

func (f fakeFS) Lstat(string) (os.FileInfo, error) { return f.info, f.err }

type fakeInfo struct{}

func (fakeInfo) Name() string       { return "stale" }
func (fakeInfo) Size() int64        { return 0 }
func (fakeInfo) Mode() os.FileMode  { return 0 }
func (fakeInfo) ModTime() time.Time { return time.Time{} }
func (fakeInfo) IsDir() bool        { return false }
func (fakeInfo) Sys() any           { return nil }
