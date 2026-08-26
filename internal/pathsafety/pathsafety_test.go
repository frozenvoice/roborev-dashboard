package pathsafety

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testInfo struct {
	name string
	size int64
	mode os.FileMode
}

func (i testInfo) Name() string       { return i.name }
func (i testInfo) Size() int64        { return i.size }
func (i testInfo) Mode() os.FileMode  { return i.mode }
func (i testInfo) ModTime() time.Time { return time.Unix(1, 0) }
func (i testInfo) IsDir() bool        { return i.mode.IsDir() }
func (i testInfo) Sys() any           { return nil }

type testFile struct {
	*bytes.Reader
	info testInfo
	id   string
}

func (f *testFile) Close() error               { return nil }
func (f *testFile) Stat() (os.FileInfo, error) { return f.info, nil }
func (f *testFile) Identity() (string, error)  { return f.id, nil }

type testEntry struct {
	info    testInfo
	data    []byte
	id      string
	reparse bool
}

type testFS struct {
	entries       map[string]testEntry
	aliases       map[string]string
	mutateOnOpen  bool
	mutateLstatAt map[string]int
	lstatCounts   map[string]int
}

func newTestFS() *testFS {
	f := &testFS{entries: map[string]testEntry{}, aliases: map[string]string{}, mutateLstatAt: map[string]int{}, lstatCounts: map[string]int{}}
	for _, path := range []string{`C:\`, `C:\fake-home`, `C:\fake-home\.codex`} {
		f.addDir(path, path)
	}
	return f
}

func (f *testFS) addDir(path, id string) {
	f.entries[path] = testEntry{info: testInfo{name: filepath.Base(path), mode: os.ModeDir}, id: id}
}
func (f *testFS) addFile(path string, data []byte, id string) {
	f.entries[path] = testEntry{info: testInfo{name: filepath.Base(path), size: int64(len(data)), mode: 0o600}, data: append([]byte(nil), data...), id: id}
}
func (f *testFS) Lstat(path string) (os.FileInfo, error) {
	f.lstatCounts[path]++
	if at := f.mutateLstatAt[path]; at > 0 && f.lstatCounts[path] == at {
		entry := f.entries[path]
		entry.id += "-changed"
		f.entries[path] = entry
	}
	entry, ok := f.entries[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return entry.info, nil
}
func (f *testFS) Open(path string) (File, error) {
	entry, ok := f.entries[path]
	if !ok || entry.info.IsDir() {
		return nil, os.ErrNotExist
	}
	id := entry.id
	if f.mutateOnOpen {
		entry.id += "-changed"
		f.entries[path] = entry
	}
	return &testFile{Reader: bytes.NewReader(entry.data), info: entry.info, id: id}, nil
}
func (f *testFS) EvalSymlinks(path string) (string, error) {
	if alias, ok := f.aliases[path]; ok {
		return alias, nil
	}
	if _, ok := f.entries[path]; !ok {
		return "", os.ErrNotExist
	}
	return path, nil
}
func (f *testFS) Identity(path string, _ os.FileInfo) (string, error) {
	entry, ok := f.entries[path]
	if !ok {
		return "", os.ErrNotExist
	}
	return entry.id, nil
}
func (f *testFS) IsReparse(path string, _ os.FileInfo) bool { return f.entries[path].reparse }

func TestCaptureFileIncludesFingerprintAndAbsenceWitness(t *testing.T) {
	f := newTestFS()
	path := `C:\fake-home\.codex\skills\roborev-fix\SKILL.md`
	absent, err := CaptureFile(f, path)
	if err != nil {
		t.Fatal(err)
	}
	if absent.Exists || absent.Absence.Path != path || absent.Absence.AncestorPath != `C:\fake-home\.codex` || absent.Absence.AncestorIdentity == "" {
		t.Fatalf("absence witness=%#v", absent)
	}
	f.addDir(`C:\fake-home\.codex\skills`, "skills")
	f.addDir(`C:\fake-home\.codex\skills\roborev-fix`, "skill")
	f.addFile(path, []byte("managed"), "file-1")
	present, err := CaptureFile(f, path)
	if err != nil {
		t.Fatal(err)
	}
	if !present.Exists || present.Size != 7 || present.FileIdentity != "file-1" || present.Fingerprint == "" || present.Absence != (AbsenceWitness{}) {
		t.Fatalf("present snapshot=%#v", present)
	}
}

func TestCaptureDirectoryIdentityAndRaces(t *testing.T) {
	f := newTestFS()
	if got, err := CaptureDirectory(f, `C:\fake-home\.codex`); err != nil || got.FileIdentity == "" || got.Kind != Directory {
		t.Fatalf("directory snapshot=%#v err=%v", got, err)
	}
	f = newTestFS()
	f.addDir(`C:\fake-home\.codex\skills`, "skills")
	f.mutateLstatAt[`C:\fake-home\.codex`] = 3
	if _, err := CaptureDirectory(f, `C:\fake-home\.codex`); !errors.Is(err, ErrChanged) {
		t.Fatalf("directory race error=%v count=%d id=%q", err, f.lstatCounts[`C:\fake-home\.codex`], f.entries[`C:\fake-home\.codex`].id)
	}
}

func TestCaptureRejectsSymlinkReparseAndOpenRace(t *testing.T) {
	f := newTestFS()
	path := `C:\fake-home\.codex\hooks.json`
	f.addFile(path, []byte("{}"), "file")
	f.aliases[path] = `C:\other\hooks.json`
	if _, err := CaptureFile(f, path); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink error=%v", err)
	}
	delete(f.aliases, path)
	f.mutateOnOpen = true
	if _, err := CaptureFile(f, path); !errors.Is(err, ErrChanged) {
		t.Fatalf("open race error=%v", err)
	}
}

func TestCaptureRejectsRelativeAndOversizedFiles(t *testing.T) {
	f := newTestFS()
	if _, err := CaptureFile(f, `relative\hooks.json`); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("relative error=%v", err)
	}
	path := `C:\fake-home\.codex\hooks.json`
	f.addFile(path, bytes.Repeat([]byte("x"), int(MaxFileBytes)+1), "large")
	if _, err := CaptureFile(f, path); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("large error=%v", err)
	}
}

func TestReadStableFileReturnsExactBytesAndFingerprint(t *testing.T) {
	f := newTestFS()
	path := `C:\fake-home\.codex\workflow.yml`
	content := []byte("name: RoboRev\r\n\t# exact bytes\n")
	f.addFile(path, content, "workflow")
	got, snapshot, err := ReadStableFile(f, path, 256)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) || snapshot.Size != int64(len(content)) || snapshot.FileIdentity != "workflow" {
		t.Fatalf("content=%q snapshot=%#v", got, snapshot)
	}
	want := sha256.Sum256(content)
	if snapshot.Fingerprint != hex.EncodeToString(want[:]) {
		t.Fatalf("fingerprint=%q", snapshot.Fingerprint)
	}
}

func TestReadStableFileRejectsBoundAndPathRaces(t *testing.T) {
	path := `C:\fake-home\.codex\workflow.yml`
	for _, test := range []struct {
		name  string
		setup func(*testFS)
		want  error
	}{
		{name: "oversized", setup: func(f *testFS) { f.addFile(path, bytes.Repeat([]byte("x"), 5), "large") }, want: ErrTooLarge},
		{name: "symlink", setup: func(f *testFS) { f.addFile(path, []byte("x"), "link"); f.aliases[path] = `C:\other\workflow.yml` }, want: ErrUnsafePath},
		{name: "reparse", setup: func(f *testFS) {
			f.addFile(path, []byte("x"), "reparse")
			entry := f.entries[path]
			entry.reparse = true
			f.entries[path] = entry
		}, want: ErrUnsafePath},
		{name: "open identity race", setup: func(f *testFS) { f.addFile(path, []byte("x"), "race"); f.mutateOnOpen = true }, want: ErrChanged},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTestFS()
			test.setup(f)
			_, _, err := ReadStableFile(f, path, 4)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}
