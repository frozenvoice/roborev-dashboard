package snapshotignoremanagement

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
)

func TestAppendExactUpstreamBytesAndPreservesLegacyEncoding(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
		t.Fatal(err)
	}
	fs := OSFileSystem{}
	service := New(fs)
	plan, err := service.Plan(context.Background(), root, ".roborev")
	if err != nil {
		t.Fatal(err)
	}
	result := service.Append(context.Background(), plan)
	if result.Status != StatusVerifiedSuccess {
		t.Fatalf("result=%#v", result)
	}
	got, err := os.ReadFile(filepath.Join(root, Target))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("# roborev snapshots\n/.roborev/\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("bytes=%q want=%q", got, want)
	}

	for _, existing := range [][]byte{
		{},
		[]byte("# existing\n"),
		[]byte("# existing"),
		[]byte("# existing\r\n"),
		[]byte{0xff, '\n', '#', ' ', 'x'},
	} {
		t.Run(string(existing), func(t *testing.T) {
			caseRoot := t.TempDir()
			if err := os.WriteFile(filepath.Join(caseRoot, Target), existing, 0o640); err != nil {
				t.Fatal(err)
			}
			p, err := service.Plan(context.Background(), caseRoot, "cache/my snapshots")
			if err != nil {
				t.Fatal(err)
			}
			if p.Status() != TargetExists {
				t.Fatalf("status=%q", p.Status())
			}
			wantSuffix := appendSuffix(existing, "/cache/my snapshots/")
			want := append(append([]byte(nil), existing...), wantSuffix...)
			got := service.Append(context.Background(), p)
			if got.Status != StatusVerifiedSuccess {
				t.Fatalf("result=%#v", got)
			}
			data, err := os.ReadFile(filepath.Join(caseRoot, Target))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, want) {
				t.Fatalf("bytes=%q want=%q", data, want)
			}
		})
	}
}

func TestShortWriteNeverGetsSuccessCredit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, Target), []byte("existing"), 0o640); err != nil {
		t.Fatal(err)
	}
	fs := &scriptedFS{OSFileSystem: OSFileSystem{}, writeN: 0}
	plan, err := New(fs).Plan(context.Background(), root, ".roborev")
	if err != nil {
		t.Fatal(err)
	}
	result := New(fs).Append(context.Background(), plan)
	if result.Status != StatusFailedUnverified || result.Reason != ReasonZeroProgress {
		t.Fatalf("result=%#v", result)
	}
	if fs.writeCalls != 1 {
		t.Fatalf("zero write calls=%d", fs.writeCalls)
	}
	data, _ := os.ReadFile(filepath.Join(root, Target))
	if string(data) != "existing" {
		t.Fatalf("short write changed bytes=%q", data)
	}

	fs = &scriptedFS{OSFileSystem: OSFileSystem{}, writeN: -1, writeErr: errors.New("reported write error")}
	plan, err = New(fs).Plan(context.Background(), root, ".roborev")
	if err != nil {
		t.Fatal(err)
	}
	result = New(fs).Append(context.Background(), plan)
	if result.Status != StatusVerifiedSuccess || result.Warning != WarningWrite {
		t.Fatalf("full write error result=%#v", result)
	}
	if fs.writeCalls != 1 {
		t.Fatalf("full write calls=%d", fs.writeCalls)
	}
}

func TestAppendSyncAndCloseErrorsMayBeVerifiedButStatAndIdentityErrorsCannot(t *testing.T) {
	for _, test := range []struct {
		name       string
		configure  func(*scriptedFS)
		wantStatus ResultStatus
		wantWarn   WarningCode
	}{
		{name: "sync", configure: func(fs *scriptedFS) { fs.syncErr = errors.New("sync") }, wantStatus: StatusVerifiedSuccess, wantWarn: WarningSync},
		{name: "close", configure: func(fs *scriptedFS) { fs.closeErr = errors.New("close") }, wantStatus: StatusVerifiedSuccess, wantWarn: WarningClose},
		{name: "stat", configure: func(fs *scriptedFS) { fs.statErr = errors.New("stat"); fs.statErrAfter = 3 }, wantStatus: StatusFailedUnverified},
		{name: "identity", configure: func(fs *scriptedFS) { fs.identityAfter = "wrong-identity" }, wantStatus: StatusFailedUnverified},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, Target), []byte("existing"), 0o640); err != nil {
				t.Fatal(err)
			}
			fs := &scriptedFS{OSFileSystem: OSFileSystem{}, writeN: -1}
			test.configure(fs)
			service := New(fs)
			plan, err := service.Plan(context.Background(), root, ".roborev")
			if err != nil {
				t.Fatal(err)
			}
			result := service.Append(context.Background(), plan)
			if result.Status != test.wantStatus || (test.wantWarn != "" && result.Warning != test.wantWarn) {
				t.Fatalf("result=%#v want status=%q warning=%q", result, test.wantStatus, test.wantWarn)
			}
			if fs.writeCalls > 1 {
				t.Fatalf("write calls=%d", fs.writeCalls)
			}
		})
	}
}

func TestPlanRejectsDuplicateAndFinalArtifactOverLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, Target), []byte("/.roborev/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := New(OSFileSystem{})
	plan, err := service.Plan(context.Background(), root, ".roborev")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status() != TargetDuplicate || plan.WriteAvailable() {
		t.Fatalf("duplicate plan status=%q available=%v", plan.Status(), plan.WriteAvailable())
	}

	large := bytes.Repeat([]byte{'x'}, int(MaxBytes))
	if err := os.WriteFile(filepath.Join(root, Target), large, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Plan(context.Background(), root, ".roborev"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized plan err=%v", err)
	}
}

func TestPlanRejectsUnsafeTargetAndBindsExactBytes(t *testing.T) {
	if runtime.GOOS != "windows" {
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, []byte("not a gitignore"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, Target)); err != nil {
			t.Fatal(err)
		}
		if _, err := New(OSFileSystem{}).Plan(context.Background(), root, ".roborev"); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("symlink target err=%v", err)
		}
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, Target), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := New(OSFileSystem{}).Plan(context.Background(), root, ".roborev"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("directory target err=%v", err)
	}

	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, Target), []byte("legacy"), 0o640); err != nil {
		t.Fatal(err)
	}
	service := New(OSFileSystem{})
	plan, err := service.Plan(context.Background(), root, ".roborev")
	if err != nil {
		t.Fatal(err)
	}
	copyPlan := plan
	copyPlan.existing = append([]byte(nil), plan.existing...)
	copyPlan.existing[0] ^= 1
	if plan.Equal(copyPlan) {
		t.Fatal("plan equality ignored exact existing bytes")
	}
	copyPlan = plan
	copyPlan.expected = append([]byte(nil), plan.expected...)
	copyPlan.expected[len(copyPlan.expected)-1] ^= 1
	if plan.Equal(copyPlan) {
		t.Fatal("plan equality ignored exact expected bytes")
	}
}

type scriptedFS struct {
	OSFileSystem
	writeN                  int
	writeErr                error
	writeOverride           func(*os.File, []byte) (int, error)
	afterWrite              func(*os.File, []byte)
	syncErr                 error
	statErr                 error
	closeErr                error
	identityOverride        string
	identityAfter           string
	beforeOpenAppend        func(string) error
	beforeCreate            func(string) error
	beforeBoundCreate       func() error
	identityCalls           int
	writeCalls              int
	openAppendCalls         int
	createCalls             int
	rootAuthorityCloseCalls int
	statCalls               int
	statErrAfter            int
}

func (f *scriptedFS) OpenAppend(path string) (File, error) {
	f.openAppendCalls++
	if f.beforeOpenAppend != nil {
		if err := f.beforeOpenAppend(path); err != nil {
			return nil, err
		}
	}
	handle, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return nil, err
	}
	return &scriptedFile{File: handle, fs: f}, nil
}
func (f *scriptedFS) CreateExclusive(path string, mode os.FileMode) (File, error) {
	f.createCalls++
	if f.beforeCreate != nil {
		if err := f.beforeCreate(path); err != nil {
			return nil, err
		}
	}
	handle, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return nil, err
	}
	return &scriptedFile{File: handle, fs: f}, nil
}

func (f *scriptedFS) OpenRootCreateAuthority(path string) (RootCreateAuthority, error) {
	if f.beforeCreate != nil {
		if err := f.beforeCreate(filepath.Join(path, Target)); err != nil {
			return nil, err
		}
	}
	authority, err := f.OSFileSystem.OpenRootCreateAuthority(path)
	if err != nil {
		return nil, err
	}
	return &scriptedRootAuthority{RootCreateAuthority: authority, fs: f}, nil
}

type scriptedRootAuthority struct {
	RootCreateAuthority
	fs *scriptedFS
}

func (a *scriptedRootAuthority) CreateGitignoreExclusive() (File, error) {
	if a.fs.beforeBoundCreate != nil {
		if err := a.fs.beforeBoundCreate(); err != nil {
			return nil, err
		}
	}
	a.fs.createCalls++
	handle, err := a.RootCreateAuthority.CreateGitignoreExclusive()
	if err != nil {
		return nil, err
	}
	return handle, nil
}

func (a *scriptedRootAuthority) Close() error {
	a.fs.rootAuthorityCloseCalls++
	return a.RootCreateAuthority.Close()
}

type scriptedFile struct {
	*os.File
	fs *scriptedFS
}

func (f *scriptedFile) Write(data []byte) (int, error) {
	f.fs.writeCalls++
	if f.fs.writeOverride != nil {
		n, err := f.fs.writeOverride(f.File, data)
		if f.fs.afterWrite != nil {
			f.fs.afterWrite(f.File, data)
		}
		return n, err
	}
	if f.fs.writeN == 0 {
		if f.fs.afterWrite != nil {
			f.fs.afterWrite(f.File, data)
		}
		return 0, nil
	}
	n, err := f.File.Write(data)
	if f.fs.writeN > 0 && n > f.fs.writeN {
		n = f.fs.writeN
	}
	if f.fs.writeErr != nil {
		if f.fs.afterWrite != nil {
			f.fs.afterWrite(f.File, data)
		}
		return n, f.fs.writeErr
	}
	if f.fs.afterWrite != nil {
		f.fs.afterWrite(f.File, data)
	}
	return n, err
}
func (f *scriptedFile) Sync() error {
	if f.fs.syncErr != nil {
		return f.fs.syncErr
	}
	return f.File.Sync()
}
func (f *scriptedFile) Stat() (os.FileInfo, error) {
	f.fs.statCalls++
	if f.fs.statErr != nil && (f.fs.statErrAfter == 0 || f.fs.statCalls >= f.fs.statErrAfter) {
		return nil, f.fs.statErr
	}
	return f.File.Stat()
}
func (f *scriptedFile) Close() error {
	err := f.File.Close()
	if f.fs.closeErr != nil {
		return f.fs.closeErr
	}
	return err
}
func (f *scriptedFile) Identity() (string, error) {
	f.fs.identityCalls++
	if f.fs.identityAfter != "" && f.fs.identityCalls >= 3 {
		return f.fs.identityAfter, nil
	}
	if f.fs.identityOverride != "" {
		return f.fs.identityOverride, nil
	}
	return pathsafety.HandleIdentity(f.File)
}

var _ io.Writer = (*scriptedFile)(nil)
