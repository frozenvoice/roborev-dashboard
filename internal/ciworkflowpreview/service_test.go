package ciworkflowpreview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type fixtureGenerator struct {
	content []byte
	err     error
	root    string
	output  string
}

func (g *fixtureGenerator) GenerateCIWorkflow(ctx context.Context, root, output string, guard roborev.CIWorkflowPreLaunchGuard) error {
	g.root, g.output = root, output
	if guard != nil {
		if err := guard(ctx); err != nil {
			return err
		}
	}
	if g.err != nil {
		return g.err
	}
	return os.WriteFile(output, g.content, 0o600)
}

type recordingTemp struct {
	created, removed string
	base             string
	mkdirCalls       int
	preexisting      bool
	removeErr        error
}

func (t *recordingTemp) TempDir() (string, error) {
	if t.base != "" {
		return t.base, nil
	}
	return os.TempDir(), nil
}

func (t *recordingTemp) MkdirTemp(dir, pattern string) (string, error) {
	t.mkdirCalls++
	path, err := os.MkdirTemp(dir, pattern)
	if err == nil {
		t.created = path
		if t.preexisting {
			_ = os.WriteFile(filepath.Join(path, WorkflowName), []byte("existing"), 0o600)
		}
	}
	return path, err
}

func (t *recordingTemp) RemoveAll(path string) error {
	t.removed = path
	_ = os.RemoveAll(path)
	return t.removeErr
}

func TestServiceGeneratesExactBytesHashAndCleansOutsideRepository(t *testing.T) {
	repo := t.TempDir()
	content := []byte("name: RoboRev\n\n# 한글 preview\n# ${{ secrets.OPENAI_API_KEY }}\n")
	generator := &fixtureGenerator{content: content}
	temp := &recordingTemp{}
	service := NewServiceWithOptions(generator, pathsafety.OSFileSystem{}, temp)
	got, err := service.Generate(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(content)
	if got.Content != string(content) || got.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("workflow=%#v", got)
	}
	if generator.root != repo || filepath.Dir(generator.output) == repo || pathWithin(repo, filepath.Dir(generator.output)) {
		t.Fatalf("generator paths root=%q output=%q repo=%q", generator.root, generator.output, repo)
	}
	if temp.created == "" || temp.removed != temp.created {
		t.Fatalf("temp lifecycle created=%q removed=%q", temp.created, temp.removed)
	}
	if _, err := os.Stat(temp.created); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary directory remains: %v", err)
	}
	entries, err := os.ReadDir(repo)
	if err != nil || len(entries) != 0 {
		t.Fatalf("repository was mutated: entries=%v err=%v", entries, err)
	}
}

func TestServiceRejectsEmptyControlAndOversizedOutput(t *testing.T) {
	repo := t.TempDir()
	tests := []struct {
		name    string
		content []byte
		want    error
	}{
		{name: "empty", content: nil, want: ErrInvalidWorkflow},
		{name: "del", content: []byte("name: x\x7f\n"), want: ErrInvalidWorkflow},
		{name: "too large", content: make([]byte, MaxBytes+1), want: ErrTemporaryOutput},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(&fixtureGenerator{content: test.content})
			_, err := service.Generate(context.Background(), repo)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}

func TestServiceRejectsRepositoryInsideTemporaryBaseBeforeMkdirTemp(t *testing.T) {
	repo := t.TempDir()
	base := filepath.Join(repo, "temporary-base")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	temp := &recordingTemp{base: base}
	service := NewServiceWithOptions(&fixtureGenerator{content: []byte("name: x\n")}, pathsafety.OSFileSystem{}, temp)
	if _, err := service.Generate(context.Background(), repo); !errors.Is(err, ErrUnsafeTemporary) {
		t.Fatalf("error=%v", err)
	}
	if temp.mkdirCalls != 0 {
		t.Fatalf("MkdirTemp calls=%d", temp.mkdirCalls)
	}
}

type broadTemp struct {
	base    string
	removed string
}

func (t *broadTemp) TempDir() (string, error)                 { return t.base, nil }
func (t *broadTemp) MkdirTemp(string, string) (string, error) { return t.base, nil }
func (t *broadTemp) RemoveAll(path string) error {
	t.removed = path
	return nil
}

func TestServiceNeverCleansBroadPathFromAbnormalMkdirTemp(t *testing.T) {
	repo := t.TempDir()
	base := t.TempDir()
	temp := &broadTemp{base: base}
	service := NewServiceWithOptions(&fixtureGenerator{content: []byte("name: x\n")}, pathsafety.OSFileSystem{}, temp)
	if _, err := service.Generate(context.Background(), repo); !errors.Is(err, ErrUnsafeTemporary) {
		t.Fatalf("error=%v", err)
	}
	if temp.removed != "" {
		t.Fatalf("broad cleanup path=%q", temp.removed)
	}
}

func TestServiceGeneratorErrorCleansValidatedTemporaryDirectory(t *testing.T) {
	repo := t.TempDir()
	generator := &fixtureGenerator{content: []byte("name: x\n"), err: errors.New(`private C:\repo\error`)}
	temp := &recordingTemp{}
	service := NewServiceWithOptions(generator, pathsafety.OSFileSystem{}, temp)
	_, err := service.Generate(context.Background(), repo)
	if !errors.Is(err, ErrUnavailable) || temp.created == "" || temp.removed != temp.created {
		t.Fatalf("error=%v created=%q removed=%q", err, temp.created, temp.removed)
	}
}

type cancelingGenerator struct{ cancel context.CancelFunc }

func (g cancelingGenerator) GenerateCIWorkflow(ctx context.Context, _, _ string, guard roborev.CIWorkflowPreLaunchGuard) error {
	if guard != nil {
		if err := guard(ctx); err != nil {
			return err
		}
	}
	g.cancel()
	return ctx.Err()
}

func TestServiceCanceledDuringGeneratorCleansValidatedTemporaryDirectory(t *testing.T) {
	repo := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	temp := &recordingTemp{}
	service := NewServiceWithOptions(cancelingGenerator{cancel: cancel}, pathsafety.OSFileSystem{}, temp)
	_, err := service.Generate(ctx, repo)
	if !errors.Is(err, context.Canceled) || temp.created == "" || temp.removed != temp.created {
		t.Fatalf("error=%v created=%q removed=%q", err, temp.created, temp.removed)
	}
}

func TestServiceCleanupFailureIsSanitizedAndPreexistingOutputBlocksGenerator(t *testing.T) {
	repo := t.TempDir()
	cleanupErr := errors.New(`private cleanup C:\repo\temp`)
	temp := &recordingTemp{removeErr: cleanupErr}
	service := NewServiceWithOptions(&fixtureGenerator{content: []byte("name: x\n")}, pathsafety.OSFileSystem{}, temp)
	_, err := service.Generate(context.Background(), repo)
	if !errors.Is(err, ErrCleanup) || strings.Contains(err.Error(), cleanupErr.Error()) {
		t.Fatalf("cleanup error=%v", err)
	}

	generator := &fixtureGenerator{content: []byte("name: x\n")}
	preexisting := &recordingTemp{preexisting: true}
	service = NewServiceWithOptions(generator, pathsafety.OSFileSystem{}, preexisting)
	_, err = service.Generate(context.Background(), repo)
	if !errors.Is(err, ErrTemporaryOutput) || generator.root != "" || preexisting.removed != preexisting.created {
		t.Fatalf("preexisting error=%v generator=%#v temp=%#v", err, generator, preexisting)
	}
}

func TestServiceRejectsInvalidUTF8C0AndC1WorkflowOutput(t *testing.T) {
	repo := t.TempDir()
	for _, test := range []struct {
		name    string
		content []byte
	}{
		{name: "invalid utf8", content: []byte{'n', 'a', 'm', 'e', ':', ' ', 0xff, '\n'}},
		{name: "nul", content: []byte("name: x\x00\n")},
		{name: "c0", content: []byte("name: x\x1f\n")},
		{name: "c1", content: []byte("name: x\xc2\x80\n")},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewService(&fixtureGenerator{content: test.content}).Generate(context.Background(), repo)
			if !errors.Is(err, ErrInvalidWorkflow) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

type ciFixtureInfo struct {
	name string
	size int64
	mode os.FileMode
	mod  time.Time
}

func (i ciFixtureInfo) Name() string       { return i.name }
func (i ciFixtureInfo) Size() int64        { return i.size }
func (i ciFixtureInfo) Mode() os.FileMode  { return i.mode }
func (i ciFixtureInfo) ModTime() time.Time { return i.mod }
func (i ciFixtureInfo) IsDir() bool        { return i.mode.IsDir() }
func (i ciFixtureInfo) Sys() any           { return nil }

type ciFixtureFile struct {
	*bytes.Reader
	info ciFixtureInfo
	id   string
}

func (f *ciFixtureFile) Close() error               { return nil }
func (f *ciFixtureFile) Stat() (os.FileInfo, error) { return f.info, nil }
func (f *ciFixtureFile) Identity() (string, error)  { return f.id, nil }

type ciFixtureEntry struct {
	info    ciFixtureInfo
	data    []byte
	id      string
	reparse bool
}

type ciFixtureFS struct {
	entries map[string]ciFixtureEntry
}

func newCIWorkflowFixtureFS() *ciFixtureFS {
	fs := &ciFixtureFS{entries: make(map[string]ciFixtureEntry)}
	fs.addDir(`C:\`, "volume")
	fs.addDir(`C:\repo`, "repo")
	fs.addDir(`C:\temp`, "temp")
	return fs
}

func (f *ciFixtureFS) addDir(path, id string) {
	path = filepath.Clean(path)
	f.entries[path] = ciFixtureEntry{info: ciFixtureInfo{name: filepath.Base(path), mode: os.ModeDir, mod: time.Unix(1, 0)}, id: id}
}

func (f *ciFixtureFS) addFile(path string, data []byte, id string) {
	path = filepath.Clean(path)
	f.entries[path] = ciFixtureEntry{info: ciFixtureInfo{name: filepath.Base(path), size: int64(len(data)), mode: 0o600, mod: time.Unix(1, 0)}, data: append([]byte(nil), data...), id: id}
}

func (f *ciFixtureFS) addUnsafeFile(path string, reparse bool) {
	f.addFile(path, []byte("name: x\n"), "unsafe")
	entry := f.entries[filepath.Clean(path)]
	entry.reparse = reparse
	if !reparse {
		entry.info.mode = os.ModeSymlink | 0o600
	}
	f.entries[filepath.Clean(path)] = entry
}

func (f *ciFixtureFS) Lstat(path string) (os.FileInfo, error) {
	entry, ok := f.entries[filepath.Clean(path)]
	if !ok {
		return nil, os.ErrNotExist
	}
	return entry.info, nil
}

func (f *ciFixtureFS) Open(path string) (pathsafety.File, error) {
	entry, ok := f.entries[filepath.Clean(path)]
	if !ok || entry.info.IsDir() {
		return nil, os.ErrNotExist
	}
	return &ciFixtureFile{Reader: bytes.NewReader(entry.data), info: entry.info, id: entry.id}, nil
}

func (f *ciFixtureFS) EvalSymlinks(path string) (string, error) {
	if _, ok := f.entries[filepath.Clean(path)]; !ok {
		return "", os.ErrNotExist
	}
	return filepath.Clean(path), nil
}

func (f *ciFixtureFS) Identity(path string, _ os.FileInfo) (string, error) {
	entry, ok := f.entries[filepath.Clean(path)]
	if !ok {
		return "", os.ErrNotExist
	}
	return entry.id, nil
}

func (f *ciFixtureFS) IsReparse(path string, _ os.FileInfo) bool {
	return f.entries[filepath.Clean(path)].reparse
}

type ciFixtureTemp struct {
	fs      *ciFixtureFS
	created string
	removed string
	unsafe  string
	mode    string
}

func (t *ciFixtureTemp) TempDir() (string, error) { return `C:\temp`, nil }
func (t *ciFixtureTemp) MkdirTemp(dir, pattern string) (string, error) {
	path := t.unsafe
	if path == "" {
		path = filepath.Join(dir, pattern+"fixture")
	}
	t.created = path
	t.fs.addDir(path, "temp-child")
	return path, nil
}
func (t *ciFixtureTemp) RemoveAll(path string) error {
	t.removed = path
	return nil
}

type ciFixtureOutputGenerator struct {
	fs      *ciFixtureFS
	content []byte
	mode    string
	calls   int
	writes  int
}

func (g *ciFixtureOutputGenerator) GenerateCIWorkflow(ctx context.Context, _, output string, guard roborev.CIWorkflowPreLaunchGuard) error {
	g.calls++
	root := filepath.Dir(output)
	switch g.mode {
	case "replace-root-identity":
		entry := g.fs.entries[root]
		entry.id = "replacement"
		g.fs.entries[root] = entry
	case "root-symlink":
		entry := g.fs.entries[root]
		entry.info.mode |= os.ModeSymlink
		g.fs.entries[root] = entry
	case "root-reparse":
		entry := g.fs.entries[root]
		entry.reparse = true
		g.fs.entries[root] = entry
	case "output-created":
		g.fs.addFile(output, []byte("created before guard\n"), "preexisting-output")
	}
	if guard != nil {
		if err := guard(ctx); err != nil {
			return err
		}
	}
	if g.mode == "symlink" || g.mode == "reparse" {
		g.writes++
		g.fs.addUnsafeFile(output, g.mode == "reparse")
		return nil
	}
	g.writes++
	g.fs.addFile(output, g.content, "output")
	return nil
}

func TestServiceRejectsOutputSymlinkAndReparseWithFakeFS(t *testing.T) {
	for _, mode := range []string{"symlink", "reparse"} {
		t.Run(mode, func(t *testing.T) {
			fs := newCIWorkflowFixtureFS()
			temp := &ciFixtureTemp{fs: fs}
			generator := &ciFixtureOutputGenerator{fs: fs, mode: mode}
			service := NewServiceWithOptions(generator, fs, temp)
			_, err := service.Generate(context.Background(), `C:\repo`)
			if !errors.Is(err, ErrTemporaryOutput) || generator.calls != 1 || temp.removed != temp.created {
				t.Fatalf("mode=%s error=%v calls=%d created=%q removed=%q", mode, err, generator.calls, temp.created, temp.removed)
			}
		})
	}
}

func TestServiceFinalTempGuardRejectsRacesAndCleansOnlyValidatedChild(t *testing.T) {
	for _, mode := range []string{"replace-root-identity", "root-symlink", "root-reparse", "output-created"} {
		t.Run(mode, func(t *testing.T) {
			fs := newCIWorkflowFixtureFS()
			temp := &ciFixtureTemp{fs: fs}
			generator := &ciFixtureOutputGenerator{fs: fs, mode: mode, content: []byte("name: x\n")}
			service := NewServiceWithOptions(generator, fs, temp)
			_, err := service.Generate(context.Background(), `C:\repo`)
			if !errors.Is(err, ErrTemporaryOutput) {
				t.Fatalf("error=%v want=%v", err, ErrTemporaryOutput)
			}
			if generator.calls != 1 || generator.writes != 0 {
				t.Fatalf("generator calls=%d writes=%d want calls=1 writes=0", generator.calls, generator.writes)
			}
			if temp.created == "" || temp.removed != temp.created {
				t.Fatalf("cleanup created=%q removed=%q", temp.created, temp.removed)
			}
			if filepath.Dir(temp.removed) != `C:\temp` || filepath.Base(temp.removed) != "roborev-dashboard-ci-fixture" {
				t.Fatalf("cleanup escaped unique child: %q", temp.removed)
			}
			if _, ok := fs.entries[`C:\repo\roborev.yml`]; ok {
				t.Fatal("repository received workflow output")
			}
		})
	}
}
