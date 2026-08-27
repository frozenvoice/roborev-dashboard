package ciworkflowmanagement

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
)

type fakeInfo struct {
	name string
	size int64
	mode os.FileMode
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return i.size }
func (i fakeInfo) Mode() os.FileMode  { return i.mode }
func (i fakeInfo) ModTime() time.Time { return time.Unix(1, 0) }
func (i fakeInfo) IsDir() bool        { return i.mode.IsDir() }
func (i fakeInfo) Sys() any           { return nil }

type fakeEntry struct {
	info    fakeInfo
	data    []byte
	id      string
	reparse bool
}

type fakeFS struct {
	entries      map[string]fakeEntry
	aliases      map[string]string
	lstatCalls   map[string]int
	openCalls    map[string]int
	openErr      error
	openHook     func(string)
	mkdirCalls   []string
	createCalls  int
	createMode   os.FileMode
	mkdirMode    []os.FileMode
	mkdirHook    func(string)
	createHook   func(string)
	writePlan    []writeStep
	syncErr      error
	statErr      error
	identityErr  error
	closeErr     error
	readbackSwap func(string)
}

type writeStep struct {
	n   int
	err error
}

func newFakeFS() *fakeFS {
	f := &fakeFS{entries: map[string]fakeEntry{}, aliases: map[string]string{}, lstatCalls: map[string]int{}, openCalls: map[string]int{}}
	f.addDir(`C:\`, "volume")
	f.addDir(`C:\repo`, "repo")
	return f
}

func (f *fakeFS) addDir(path, id string) {
	path = filepath.Clean(path)
	f.entries[path] = fakeEntry{info: fakeInfo{name: filepath.Base(path), mode: os.ModeDir}, id: id}
}

func (f *fakeFS) addFile(path string, content []byte, id string) {
	path = filepath.Clean(path)
	f.entries[path] = fakeEntry{info: fakeInfo{name: filepath.Base(path), size: int64(len(content)), mode: 0o644}, data: append([]byte(nil), content...), id: id}
}

func (f *fakeFS) Lstat(path string) (os.FileInfo, error) {
	path = filepath.Clean(path)
	f.lstatCalls[path]++
	if f.createCalls > 0 && filepath.Base(path) == "roborev.yml" && f.readbackSwap != nil {
		f.readbackSwap(path)
		f.readbackSwap = nil
	}
	e, ok := f.entries[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return e.info, nil
}

func (f *fakeFS) Open(path string) (pathsafety.File, error) {
	path = filepath.Clean(path)
	f.openCalls[path]++
	if f.openHook != nil {
		f.openHook(path)
	}
	if f.openErr != nil {
		return nil, f.openErr
	}
	e, ok := f.entries[path]
	if !ok || e.info.IsDir() {
		return nil, os.ErrNotExist
	}
	return &fakeReadHandle{Reader: bytes.NewReader(e.data), info: e.info, id: e.id}, nil
}

func (f *fakeFS) EvalSymlinks(path string) (string, error) {
	path = filepath.Clean(path)
	if alias, ok := f.aliases[path]; ok {
		return alias, nil
	}
	if _, ok := f.entries[path]; !ok {
		return "", os.ErrNotExist
	}
	return path, nil
}

func (f *fakeFS) Identity(path string, _ os.FileInfo) (string, error) {
	e, ok := f.entries[filepath.Clean(path)]
	if !ok {
		return "", os.ErrNotExist
	}
	return e.id, nil
}

func (f *fakeFS) IsReparse(path string, _ os.FileInfo) bool {
	return f.entries[filepath.Clean(path)].reparse
}

func (f *fakeFS) Mkdir(path string, mode os.FileMode) error {
	path = filepath.Clean(path)
	f.mkdirCalls = append(f.mkdirCalls, path)
	f.mkdirMode = append(f.mkdirMode, mode)
	if f.mkdirHook != nil {
		f.mkdirHook(path)
	}
	if _, ok := f.entries[path]; ok {
		return os.ErrExist
	}
	parent := filepath.Dir(path)
	if e, ok := f.entries[parent]; !ok || !e.info.IsDir() {
		return os.ErrNotExist
	}
	f.addDir(path, "dir-"+filepath.Base(path))
	return nil
}

func (f *fakeFS) CreateExclusive(path string, mode os.FileMode) (File, error) {
	path = filepath.Clean(path)
	f.createCalls++
	f.createMode = mode
	if f.createHook != nil {
		f.createHook(path)
	}
	if _, ok := f.entries[path]; ok {
		return nil, os.ErrExist
	}
	parent := filepath.Dir(path)
	if e, ok := f.entries[parent]; !ok || !e.info.IsDir() {
		return nil, os.ErrNotExist
	}
	f.addFile(path, nil, "file-created")
	return &fakeWriteHandle{fs: f, path: path, id: "file-created", info: f.entries[path].info, plan: append([]writeStep(nil), f.writePlan...), syncErr: f.syncErr, statErr: f.statErr, identityErr: f.identityErr, closeErr: f.closeErr}, nil
}

type fakeReadHandle struct {
	*bytes.Reader
	info fakeInfo
	id   string
}

func (f *fakeReadHandle) Close() error               { return nil }
func (f *fakeReadHandle) Stat() (os.FileInfo, error) { return f.info, nil }
func (f *fakeReadHandle) Identity() (string, error)  { return f.id, nil }

type fakeWriteHandle struct {
	fs          *fakeFS
	path        string
	id          string
	info        fakeInfo
	plan        []writeStep
	syncErr     error
	statErr     error
	identityErr error
	closeErr    error
	closed      bool
}

func (f *fakeWriteHandle) Read([]byte) (int, error) { return 0, io.EOF }

func (f *fakeWriteHandle) Write(p []byte) (int, error) {
	n := len(p)
	var err error
	if len(f.plan) > 0 {
		step := f.plan[0]
		f.plan = f.plan[1:]
		n, err = step.n, step.err
	}
	if n < 0 {
		n = 0
	}
	if n > len(p) {
		n = len(p)
	}
	e := f.fs.entries[f.path]
	e.data = append(e.data, p[:n]...)
	e.info.size = int64(len(e.data))
	f.fs.entries[f.path] = e
	return n, err
}
func (f *fakeWriteHandle) Sync() error { return f.syncErr }
func (f *fakeWriteHandle) Stat() (os.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	e := f.fs.entries[f.path]
	return e.info, nil
}
func (f *fakeWriteHandle) Identity() (string, error) {
	if f.identityErr != nil {
		return "", f.identityErr
	}
	return f.id, nil
}
func (f *fakeWriteHandle) Close() error {
	if f.closed {
		return errors.New("closed twice")
	}
	f.closed = true
	return f.closeErr
}

func planFor(t *testing.T, f *fakeFS) (Plan, *Service) {
	t.Helper()
	s := New(f)
	p, err := s.Plan(context.Background(), `C:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	return p, s
}

func TestPlanClassifiesAbsentExistingAndUnsafeTargets(t *testing.T) {
	content := []byte("name: x\n")
	tests := []struct {
		name   string
		setup  func(*fakeFS)
		status TargetStatus
	}{
		{name: "absent safe", status: TargetAbsentSafe},
		{name: "exists", setup: func(f *fakeFS) {
			f.addDir(`C:\repo\.github`, "gh")
			f.addDir(`C:\repo\.github\workflows`, "wf")
			f.addFile(`C:\repo\.github\workflows\roborev.yml`, content, "old")
		}, status: TargetExists},
		{name: "file instead of directory", setup: func(f *fakeFS) { f.addFile(`C:\repo\.github`, nil, "not-dir") }, status: TargetUnsafe},
		{name: "target symlink", setup: func(f *fakeFS) {
			f.addDir(`C:\repo\.github`, "gh")
			f.addDir(`C:\repo\.github\workflows`, "wf")
			f.addFile(`C:\repo\.github\workflows\roborev.yml`, content, "link")
			f.aliases[`C:\repo\.github\workflows\roborev.yml`] = `C:\elsewhere\roborev.yml`
		}, status: TargetUnsafe},
		{name: "target reparse", setup: func(f *fakeFS) {
			f.addDir(`C:\repo\.github`, "gh")
			f.addDir(`C:\repo\.github\workflows`, "wf")
			f.addFile(`C:\repo\.github\workflows\roborev.yml`, content, "reparse")
			e := f.entries[`C:\repo\.github\workflows\roborev.yml`]
			e.reparse = true
			f.entries[`C:\repo\.github\workflows\roborev.yml`] = e
		}, status: TargetUnsafe},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFakeFS()
			if test.setup != nil {
				test.setup(f)
			}
			p, _ := planFor(t, f)
			if p.Status() != test.status || p.Target() != WorkflowTarget {
				t.Fatalf("status=%q target=%q", p.Status(), p.Target())
			}
		})
	}
}

func existingPlanFor(t *testing.T, content []byte) (Plan, *Service, *fakeFS) {
	t.Helper()
	f := newFakeFS()
	f.addDir(`C:\repo\.github`, "gh")
	f.addDir(`C:\repo\.github\workflows`, "wf")
	f.addFile(`C:\repo\.github\workflows\roborev.yml`, content, "workflow")
	p, s := planFor(t, f)
	return p, s, f
}

func TestCompareExistingUsesExactBytesAndAllowlistedResults(t *testing.T) {
	tests := []struct {
		name     string
		existing []byte
		preview  []byte
		want     ExistingComparison
	}{
		{name: "exact", existing: []byte("name: roborev\n"), preview: []byte("name: roborev\n"), want: ExistingExactMatch},
		{name: "one character", existing: []byte("name: roborev\n"), preview: []byte("name: RoboRev\n"), want: ExistingDifferent},
		{name: "trailing newline", existing: []byte("name: roborev"), preview: []byte("name: roborev\n"), want: ExistingDifferent},
		{name: "LF CRLF", existing: []byte("name: roborev\r\n"), preview: []byte("name: roborev\n"), want: ExistingDifferent},
		{name: "comment only", existing: []byte("name: roborev\n# existing\n"), preview: []byte("name: roborev\n# generated\n"), want: ExistingDifferent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, service, f := existingPlanFor(t, test.existing)
			beforeOpen := f.openCalls[plan.target.path]
			if got := service.CompareExisting(context.Background(), plan, test.preview); got != test.want {
				t.Fatalf("comparison=%q want=%q", got, test.want)
			}
			if got := f.openCalls[plan.target.path] - beforeOpen; got != 1 {
				t.Fatalf("target opened %d times beyond Plan", got)
			}
		})
	}
}

func TestCompareExistingFailsClosedForBoundedReadErrorsAndRaces(t *testing.T) {
	content := []byte("name: roborev\n")
	for _, test := range []struct {
		name  string
		setup func(*fakeFS)
	}{
		{name: "oversized", setup: func(f *fakeFS) {
			f.entries[`C:\repo\.github\workflows\roborev.yml`] = fakeEntry{info: fakeInfo{name: "roborev.yml", size: MaxBytes + 1, mode: 0o644}, data: bytes.Repeat([]byte("x"), int(MaxBytes)+1), id: "workflow"}
		}},
		{name: "read error", setup: func(f *fakeFS) { f.openErr = errors.New("read failed") }},
		{name: "target identity replacement", setup: func(f *fakeFS) {
			f.openHook = func(path string) { e := f.entries[path]; e.id = "replacement"; f.entries[path] = e; f.openHook = nil }
		}},
		{name: "content fingerprint drift", setup: func(f *fakeFS) {
			f.openHook = func(path string) {
				e := f.entries[path]
				e.data = []byte("name: changed\n")
				e.info.size = int64(len(e.data))
				f.entries[path] = e
				f.openHook = nil
			}
		}},
		{name: "workflows identity drift", setup: func(f *fakeFS) {
			f.openHook = func(string) {
				e := f.entries[`C:\repo\.github\workflows`]
				e.id = "wf-replaced"
				f.entries[`C:\repo\.github\workflows`] = e
				f.openHook = nil
			}
		}},
		{name: "root identity drift", setup: func(f *fakeFS) {
			f.openHook = func(string) {
				e := f.entries[`C:\repo`]
				e.id = "root-replaced"
				f.entries[`C:\repo`] = e
				f.openHook = nil
			}
		}},
		{name: "github identity drift", setup: func(f *fakeFS) {
			f.openHook = func(string) {
				e := f.entries[`C:\repo\.github`]
				e.id = "github-replaced"
				f.entries[`C:\repo\.github`] = e
				f.openHook = nil
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, service, f := existingPlanFor(t, content)
			test.setup(f)
			if got := service.CompareExisting(context.Background(), plan, content); got != ExistingUnavailable {
				t.Fatalf("comparison=%q", got)
			}
		})
	}
}

func TestCompareExistingDoesNotReadAbsentUnsafeOrCanceledTargets(t *testing.T) {
	for _, test := range []struct {
		name   string
		setup  func(*fakeFS)
		cancel bool
	}{
		{name: "absent", setup: func(*fakeFS) {}},
		{name: "unsafe target", setup: func(f *fakeFS) {
			f.addDir(`C:\repo\.github`, "gh")
			f.addDir(`C:\repo\.github\workflows`, "wf")
			f.addFile(`C:\repo\.github\workflows\roborev.yml`, []byte("x"), "link")
			f.aliases[`C:\repo\.github\workflows\roborev.yml`] = `C:\elsewhere\workflow.yml`
		}},
		{name: "target directory", setup: func(f *fakeFS) {
			f.addDir(`C:\repo\.github`, "gh")
			f.addDir(`C:\repo\.github\workflows`, "wf")
			f.addDir(`C:\repo\.github\workflows\roborev.yml`, "target-dir")
		}},
		{name: "target reparse", setup: func(f *fakeFS) {
			f.addDir(`C:\repo\.github`, "gh")
			f.addDir(`C:\repo\.github\workflows`, "wf")
			f.addFile(`C:\repo\.github\workflows\roborev.yml`, []byte("x"), "target-reparse")
			e := f.entries[`C:\repo\.github\workflows\roborev.yml`]
			e.reparse = true
			f.entries[`C:\repo\.github\workflows\roborev.yml`] = e
		}},
		{name: "unsafe workflows ancestor", setup: func(f *fakeFS) {
			f.addDir(`C:\repo\.github`, "gh")
			f.addDir(`C:\repo\.github\workflows`, "wf")
			e := f.entries[`C:\repo\.github\workflows`]
			e.reparse = true
			f.entries[`C:\repo\.github\workflows`] = e
			f.addFile(`C:\repo\.github\workflows\roborev.yml`, []byte("x"), "workflow")
		}},
		{name: "unsafe ancestor", setup: func(f *fakeFS) { f.addFile(`C:\repo\.github`, nil, "not-dir") }},
		{name: "canceled", setup: func(f *fakeFS) {
			f.addDir(`C:\repo\.github`, "gh")
			f.addDir(`C:\repo\.github\workflows`, "wf")
			f.addFile(`C:\repo\.github\workflows\roborev.yml`, []byte("x"), "workflow")
		}, cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFakeFS()
			test.setup(f)
			plan, service := planFor(t, f)
			ctx := context.Background()
			if test.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if got := service.CompareExisting(ctx, plan, []byte("x")); got != ExistingUnavailable {
				t.Fatalf("comparison=%q", got)
			}
			if !test.cancel && f.openCalls[`C:\repo\.github\workflows\roborev.yml`] != 0 {
				t.Fatalf("target opened %d times", f.openCalls[`C:\repo\.github\workflows\roborev.yml`])
			}
		})
	}
}

func TestCreateMakesDirectoriesInOrderAndExactBytes(t *testing.T) {
	f := newFakeFS()
	p, s := planFor(t, f)
	content := []byte("name: RoboRev\n# exact 한글\n")
	result := s.Create(context.Background(), p, content)
	if result.Status != StatusVerifiedCreated {
		t.Fatalf("result=%#v", result)
	}
	wantDirs := []string{`C:\repo\.github`, `C:\repo\.github\workflows`}
	if strings.Join(f.mkdirCalls, "|") != strings.Join(wantDirs, "|") {
		t.Fatalf("mkdir order=%v", f.mkdirCalls)
	}
	if len(f.mkdirMode) != 2 || f.mkdirMode[0] != directoryMode || f.mkdirMode[1] != directoryMode {
		t.Fatalf("modes=%v", f.mkdirMode)
	}
	if f.createCalls != 1 || f.createMode != fileMode {
		t.Fatalf("create calls=%d mode=%o", f.createCalls, f.createMode)
	}
	if !bytes.Equal(f.entries[`C:\repo\.github\workflows\roborev.yml`].data, content) {
		t.Fatal("bytes changed")
	}
}

func TestCreateExistingTargetNeverOverwrites(t *testing.T) {
	f := newFakeFS()
	f.addDir(`C:\repo\.github`, "gh")
	f.addDir(`C:\repo\.github\workflows`, "wf")
	f.addFile(`C:\repo\.github\workflows\roborev.yml`, []byte("existing"), "old")
	p, s := planFor(t, f)
	if got := s.Create(context.Background(), p, []byte("new")).Status; got != StatusPreMutationStale {
		t.Fatalf("status=%q", got)
	}
	if f.createCalls != 0 || string(f.entries[`C:\repo\.github\workflows\roborev.yml`].data) != "existing" {
		t.Fatal("existing target changed")
	}
}

func TestCreateTargetRaceUsesExclusiveFailureAndNoOverwrite(t *testing.T) {
	f := newFakeFS()
	f.addDir(`C:\repo\.github`, "gh")
	f.addDir(`C:\repo\.github\workflows`, "wf")
	p, s := planFor(t, f)
	f.createHook = func(path string) { f.addFile(path, []byte("racer"), "racer") }
	result := s.Create(context.Background(), p, []byte("new"))
	if result.Status != StatusFailedUnverified || f.createCalls != 1 || string(f.entries[`C:\repo\.github\workflows\roborev.yml`].data) != "racer" {
		t.Fatalf("result=%#v entries=%#v", result, f.entries)
	}
}

func TestCreateRevalidatesPlanAndParentDriftBeforeMutation(t *testing.T) {
	f := newFakeFS()
	f.addDir(`C:\repo\.github`, "gh")
	f.addDir(`C:\repo\.github\workflows`, "wf")
	p, s := planFor(t, f)
	e := f.entries[`C:\repo\.github`]
	e.id = "changed"
	f.entries[`C:\repo\.github`] = e
	result := s.Create(context.Background(), p, []byte("x"))
	if result.Status != StatusPreMutationStale || f.createCalls != 0 {
		t.Fatalf("result=%#v create=%d", result, f.createCalls)
	}

	f = newFakeFS()
	p, s = planFor(t, f)
	f.mkdirHook = func(path string) {
		if filepath.Base(path) == ".github" {
			e := f.entries[`C:\repo`]
			e.id = "root-drift"
			f.entries[`C:\repo`] = e
		}
	}
	result = s.Create(context.Background(), p, []byte("x"))
	if result.Status != StatusFailedUnverified || f.createCalls != 0 {
		t.Fatalf("result=%#v create=%d", result, f.createCalls)
	}
}

func TestCreateCancellationBeforeMutationAndIgnoresAfterStart(t *testing.T) {
	f := newFakeFS()
	p, s := planFor(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := s.Create(ctx, p, []byte("x"))
	if result.Status != StatusPreMutationCanceled || len(f.mkdirCalls) != 0 {
		t.Fatalf("result=%#v mkdir=%v", result, f.mkdirCalls)
	}

	f = newFakeFS()
	p, s = planFor(t, f)
	ctx, cancel = context.WithCancel(context.Background())
	f.mkdirHook = func(path string) {
		if filepath.Base(path) == ".github" {
			cancel()
		}
	}
	result = s.Create(ctx, p, []byte("x"))
	if result.Status != StatusVerifiedCreated || len(f.mkdirCalls) != 2 || f.createCalls != 1 {
		t.Fatalf("result=%#v mkdir=%v create=%d", result, f.mkdirCalls, f.createCalls)
	}
}

func TestCreateShortZeroPartialAndHandleErrorsRemainHonest(t *testing.T) {
	tests := []struct {
		name string
		plan []writeStep
		set  func(*fakeFS)
		want ResultStatus
	}{
		{name: "short writes", plan: []writeStep{{n: 1}, {n: 1}, {n: 1}, {n: 1}}, want: StatusVerifiedCreated},
		{name: "zero progress", plan: []writeStep{{n: 0}}, want: StatusFailedUnverified},
		{name: "write error", plan: []writeStep{{n: 1, err: errors.New("write")}}, want: StatusFailedUnverified},
		{name: "sync error", set: func(f *fakeFS) { f.syncErr = errors.New("sync") }, want: StatusVerifiedCreated},
		{name: "stat error", set: func(f *fakeFS) { f.statErr = errors.New("stat") }, want: StatusVerifiedCreated},
		{name: "identity error", set: func(f *fakeFS) { f.identityErr = errors.New("identity") }, want: StatusFailedUnverified},
		{name: "close error", set: func(f *fakeFS) { f.closeErr = errors.New("close") }, want: StatusVerifiedCreated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFakeFS()
			if test.set != nil {
				test.set(f)
			}
			f.writePlan = test.plan
			p, s := planFor(t, f)
			result := s.Create(context.Background(), p, []byte("abcd"))
			if result.Status != test.want {
				t.Fatalf("result=%#v", result)
			}
			if result.Receipt == nil {
				t.Fatal("missing opaque receipt")
			}
		})
	}
}

func TestCreateReadbackMismatchReplacementSymlinkAndReparseFail(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fakeFS, string)
		want   ReasonCode
	}{
		{name: "mismatch", mutate: func(f *fakeFS, path string) {
			e := f.entries[path]
			e.data = []byte("other")
			e.info.size = 5
			f.entries[path] = e
		}, want: ReasonReadbackMismatch},
		{name: "replacement", mutate: func(f *fakeFS, path string) { e := f.entries[path]; e.id = "replacement"; f.entries[path] = e }, want: ReasonReadbackReplacement},
		{name: "symlink", mutate: func(f *fakeFS, path string) { f.aliases[path] = `C:\other\workflow.yml` }, want: ReasonReadback},
		{name: "reparse", mutate: func(f *fakeFS, path string) { e := f.entries[path]; e.reparse = true; f.entries[path] = e }, want: ReasonReadback},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFakeFS()
			p, s := planFor(t, f)
			f.readbackSwap = func(path string) { test.mutate(f, path) }
			result := s.Create(context.Background(), p, []byte("exact"))
			if result.Status != StatusFailedUnverified || result.Reason != test.want {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestVerifyCreatedCanRunAfterSlowInspectionAndDetectsRootParentDrift(t *testing.T) {
	f := newFakeFS()
	p, s := planFor(t, f)
	content := []byte("exact")
	result := s.Create(context.Background(), p, content)
	if result.Status != StatusVerifiedCreated || result.Receipt == nil {
		t.Fatalf("create=%#v", result)
	}
	// This call models a slow, external read-only Git inspection between the
	// HTTP response and the independent filesystem verification.
	if got := s.VerifyCreated(context.Background(), *result.Receipt); got.Status != StatusVerifiedCreated {
		t.Fatalf("verify=%#v", got)
	}
	e := f.entries[`C:\repo`]
	e.id = "root-drift"
	f.entries[`C:\repo`] = e
	if got := s.VerifyCreated(context.Background(), *result.Receipt); got.Status != StatusFailedUnverified || got.Reason != ReasonRepositoryDrift {
		t.Fatalf("root drift=%#v", got)
	}
}

func TestFailedDirectoryMutationLeavesPartialDirectoriesWithoutRollback(t *testing.T) {
	f := newFakeFS()
	f.mkdirHook = func(path string) {
		if filepath.Base(path) == "workflows" {
			// The parent exists, but the one-component operation fails before
			// adding the child. The already-created .github remains.
		}
	}
	p, s := planFor(t, f)
	// A file in the path is introduced immediately before the second mkdir.
	f.mkdirHook = func(path string) {
		if filepath.Base(path) == "workflows" {
			f.addFile(path, nil, "file-race")
		}
	}
	result := s.Create(context.Background(), p, []byte("x"))
	_, githubExists := f.entries[`C:\repo\.github`]
	if result.Status != StatusFailedUnverified || !githubExists {
		t.Fatalf("result=%#v entries=%#v", result, f.entries)
	}
}

func TestCreateRejectsInvalidBytesBeforeMutation(t *testing.T) {
	for _, content := range [][]byte{nil, {0xff}, []byte("x\x00")} {
		f := newFakeFS()
		p, s := planFor(t, f)
		result := s.Create(context.Background(), p, content)
		if result.Status != StatusPreMutationStale || result.Reason != ReasonInvalidContent || len(f.mkdirCalls) != 0 {
			t.Fatalf("result=%#v mkdir=%v", result, f.mkdirCalls)
		}
	}
}

var _ io.ReadCloser = (*fakeReadHandle)(nil)
var _ File = (*fakeWriteHandle)(nil)
