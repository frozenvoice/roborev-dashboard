package agenthookmanagement

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeInfo struct {
	name string
	size int64
	mode os.FileMode
	mod  time.Time
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return i.size }
func (i fakeInfo) Mode() os.FileMode  { return i.mode }
func (i fakeInfo) ModTime() time.Time { return i.mod }
func (i fakeInfo) IsDir() bool        { return i.mode.IsDir() }
func (i fakeInfo) Sys() any           { return nil }

type fakeFile struct {
	*bytes.Reader
	info     fakeInfo
	identity string
}

func (f *fakeFile) Close() error               { return nil }
func (f *fakeFile) Stat() (os.FileInfo, error) { return f.info, nil }
func (f *fakeFile) Identity() (string, error)  { return f.identity, nil }

type fakeEntry struct {
	info     fakeInfo
	data     []byte
	identity string
	reparse  bool
}

type fakeConfigFS struct {
	entries      map[string]fakeEntry
	aliases      map[string]string
	mutateOnOpen bool
}

func newFakeConfigFS() *fakeConfigFS {
	fs := &fakeConfigFS{entries: map[string]fakeEntry{}, aliases: map[string]string{}}
	for _, path := range []string{`C:\`, `C:\fake-home`, `C:\fake-home\.codex`} {
		fs.addDir(path, path)
	}
	return fs
}

func (f *fakeConfigFS) addDir(path, identity string) {
	f.entries[path] = fakeEntry{info: fakeInfo{name: filepath.Base(path), mode: os.ModeDir, mod: time.Unix(1, 0)}, identity: identity}
}

func (f *fakeConfigFS) addFile(path string, data []byte, identity string) {
	f.entries[path] = fakeEntry{info: fakeInfo{name: filepath.Base(path), size: int64(len(data)), mode: 0o600, mod: time.Unix(1, 0)}, data: append([]byte(nil), data...), identity: identity}
}

func (f *fakeConfigFS) Lstat(path string) (os.FileInfo, error) {
	entry, ok := f.entries[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return entry.info, nil
}

func (f *fakeConfigFS) Open(path string) (File, error) {
	entry, ok := f.entries[path]
	if !ok || entry.info.IsDir() {
		return nil, os.ErrNotExist
	}
	handleIdentity := entry.identity
	if f.mutateOnOpen {
		entry.identity = entry.identity + "-changed"
		f.entries[path] = entry
	}
	return &fakeFile{Reader: bytes.NewReader(entry.data), info: entry.info, identity: handleIdentity}, nil
}

func (f *fakeConfigFS) EvalSymlinks(path string) (string, error) {
	if alias, ok := f.aliases[path]; ok {
		return alias, nil
	}
	if _, ok := f.entries[path]; !ok {
		return "", os.ErrNotExist
	}
	return path, nil
}

func (f *fakeConfigFS) Identity(path string, _ os.FileInfo) (string, error) {
	entry, ok := f.entries[path]
	if !ok {
		return "", os.ErrNotExist
	}
	return entry.identity, nil
}

func (f *fakeConfigFS) IsReparse(path string, _ os.FileInfo) bool {
	return f.entries[path].reparse
}

func TestCaptureCStylePresentAndAbsentSnapshots(t *testing.T) {
	fs := newFakeConfigFS()
	path := `C:\fake-home\.codex\hooks.json`
	absent, err := Capture(fs, path)
	if err != nil {
		t.Fatal(err)
	}
	if absent.Exists || absent.Path != path || absent.AncestorPath != `C:\fake-home\.codex` || absent.AncestorIdentity == "" {
		t.Fatalf("absent snapshot=%#v", absent)
	}
	fs.addFile(path, []byte(`{"hooks":[]}`), "config-1")
	present, err := Capture(fs, path)
	if err != nil {
		t.Fatal(err)
	}
	if !present.Exists || present.FileIdentity != "config-1" || present.Fingerprint == "" || present.AncestorIdentity == "" {
		t.Fatalf("present snapshot=%#v", present)
	}
	if absent.Equal(present) {
		t.Fatal("absent and present snapshots compared equal")
	}
}

func TestCaptureRejectsLargeAndUnsafeConfig(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*fakeConfigFS, string)
		want  error
	}{
		{name: "large", setup: func(fs *fakeConfigFS, path string) {
			fs.addFile(path, bytes.Repeat([]byte("x"), int(MaxConfigBytes)+1), "large")
		}, want: ErrConfigTooLarge},
		{name: "final reparse", setup: func(fs *fakeConfigFS, path string) {
			fs.addFile(path, []byte("{}"), "file")
			entry := fs.entries[path]
			entry.reparse = true
			fs.entries[path] = entry
		}, want: ErrUnsafePath},
		{name: "parent reparse", setup: func(fs *fakeConfigFS, path string) {
			entry := fs.entries[`C:\fake-home\.codex`]
			entry.reparse = true
			fs.entries[`C:\fake-home\.codex`] = entry
		}, want: ErrUnsafePath},
	} {
		t.Run(test.name, func(t *testing.T) {
			fs := newFakeConfigFS()
			path := `C:\fake-home\.codex\hooks.json`
			test.setup(fs, path)
			if _, err := Capture(fs, path); !errors.Is(err, test.want) {
				t.Fatalf("Capture error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestCaptureRejectsSymlinkAndReadRace(t *testing.T) {
	fs := newFakeConfigFS()
	path := `C:\fake-home\.codex\hooks.json`
	fs.aliases[path] = `C:\private\other.json`
	fs.addFile(path, []byte("{}"), "config")
	if _, err := Capture(fs, path); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink error=%v", err)
	}
	delete(fs.aliases, path)
	fs.mutateOnOpen = true
	if _, err := Capture(fs, path); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("race error=%v", err)
	}
}

func TestCaptureRejectsRelativeAndWhitespacePaths(t *testing.T) {
	fs := newFakeConfigFS()
	for _, path := range []string{"", " ", "relative/hooks.json"} {
		if _, err := Capture(fs, path); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("path %q error=%v", path, err)
		}
	}
}
