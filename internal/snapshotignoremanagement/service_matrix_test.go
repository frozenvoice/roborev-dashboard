package snapshotignoremanagement

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIgnorePatternMatchesV0640ForDefaultNestedSpacesAndWindowsSeparators(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name     string
		relative string
		want     string
	}{
		{name: "default", relative: ".roborev", want: "/.roborev/"},
		{name: "nested", relative: "cache/snapshots", want: "/cache/snapshots/"},
		{name: "spaces", relative: "cache/my snapshots", want: "/cache/my snapshots/"},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases, struct {
			name     string
			relative string
			want     string
		}{name: "windows separators", relative: `cache\my snapshots\nested`, want: "/cache/my snapshots/nested/"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IgnorePatternForDir(root, tc.relative)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("pattern=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestAppendByteMatrixPreservesBytesAndExistingPermissions(t *testing.T) {
	cases := []struct {
		name     string
		existing []byte
		absent   bool
	}{
		{name: "absent", absent: true},
		{name: "empty", existing: []byte{}},
		{name: "trailing LF", existing: []byte("# existing\n")},
		{name: "no final newline", existing: []byte("# existing")},
		{name: "lone CR", existing: []byte("# existing\r")},
		{name: "CRLF", existing: []byte("# existing\r\n")},
		{name: "BOM", existing: append([]byte{0xef, 0xbb, 0xbf}, []byte("# existing\n")...)},
		{name: "invalid UTF-8", existing: []byte{0xff, 0xfe, '#', ' ', 'x'}},
	}
	const mode = 0o640
	pattern := "/cache/my snapshots/"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "cache"), 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, Target)
			var want []byte
			if tc.absent {
				want = []byte("# roborev snapshots\n" + pattern + "\n")
			} else {
				if err := os.WriteFile(target, tc.existing, mode); err != nil {
					t.Fatal(err)
				}
				want = append(append([]byte(nil), tc.existing...), []byte("# roborev snapshots\n"+pattern+"\n")...)
				if len(tc.existing) > 0 && tc.existing[len(tc.existing)-1] != '\n' {
					want = append(append([]byte(nil), tc.existing...), append([]byte{'\n'}, []byte("# roborev snapshots\n"+pattern+"\n")...)...)
				}
			}

			service := New(OSFileSystem{})
			plan, err := service.Plan(context.Background(), root, filepath.FromSlash("cache/my snapshots"))
			if err != nil {
				t.Fatal(err)
			}
			result := service.Append(context.Background(), plan)
			if result.Status != StatusVerifiedSuccess {
				t.Fatalf("result=%#v", result)
			}
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("bytes=%q want=%q", got, want)
			}
			if !tc.absent && runtime.GOOS != "windows" {
				info, err := os.Stat(target)
				if err != nil {
					t.Fatal(err)
				}
				if gotMode := info.Mode().Perm(); gotMode != mode {
					t.Fatalf("mode=%#o want=%#o", gotMode, mode)
				}
			}
		})
	}
}

func TestAppendDuplicateTrimSpaceCRLFAndCommentOnly(t *testing.T) {
	cases := []struct {
		name      string
		existing  []byte
		duplicate bool
	}{
		{name: "trimmed CRLF", existing: []byte("  /.roborev/  \r\n"), duplicate: true},
		{name: "comment only", existing: []byte("# /.roborev/\n")},
		{name: "different rule", existing: []byte("/.roborev-other/\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, Target)
			if err := os.WriteFile(target, tc.existing, 0o640); err != nil {
				t.Fatal(err)
			}
			service := New(OSFileSystem{})
			plan, err := service.Plan(context.Background(), root, ".roborev")
			if err != nil {
				t.Fatal(err)
			}
			if tc.duplicate {
				if plan.Status() != TargetDuplicate || plan.WriteAvailable() {
					t.Fatalf("duplicate plan status=%q available=%v", plan.Status(), plan.WriteAvailable())
				}
				before, _ := os.ReadFile(target)
				result := service.Append(context.Background(), plan)
				if result.Status != StatusPreMutationStale || result.Reason != ReasonInvalidPlan {
					t.Fatalf("duplicate append result=%#v", result)
				}
				after, _ := os.ReadFile(target)
				if !bytes.Equal(before, after) {
					t.Fatalf("duplicate changed bytes=%q", after)
				}
				return
			}
			result := service.Append(context.Background(), plan)
			if result.Status != StatusVerifiedSuccess {
				t.Fatalf("result=%#v", result)
			}
			got, _ := os.ReadFile(target)
			if !bytes.HasPrefix(got, tc.existing) || !bytes.HasSuffix(got, []byte("/.roborev/\n")) {
				t.Fatalf("append did not preserve/comment-only bytes=%q", got)
			}
		})
	}
}

func TestPlanRejectsSymlinkSnapshotAncestor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires platform-specific privileges on Windows")
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "cache")); err != nil {
		t.Fatal(err)
	}
	if _, err := New(OSFileSystem{}).Plan(context.Background(), root, filepath.FromSlash("cache/snapshots")); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("symlink snapshot ancestor err=%v", err)
	}
}

func TestAppendFinalArtifactOneMiBBoundary(t *testing.T) {
	pattern := "/.roborev/"
	suffix := []byte("# roborev snapshots\n" + pattern + "\n")
	existingSize := int(MaxBytes) - len(suffix)

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := bytes.Repeat([]byte{'x'}, existingSize-1)
	existing = append(existing, '\n')
	if err := os.WriteFile(filepath.Join(root, Target), existing, 0o640); err != nil {
		t.Fatal(err)
	}
	service := New(OSFileSystem{})
	plan, err := service.Plan(context.Background(), root, ".roborev")
	if err != nil {
		t.Fatal(err)
	}
	result := service.Append(context.Background(), plan)
	if result.Status != StatusVerifiedSuccess {
		t.Fatalf("boundary result=%#v", result)
	}
	data, err := os.ReadFile(filepath.Join(root, Target))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != int(MaxBytes) || !bytes.Equal(data[len(data)-len(suffix):], suffix) {
		t.Fatalf("boundary size=%d suffix=%q", len(data), data[len(data)-len(suffix):])
	}

	overRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(overRoot, ".roborev"), 0o755); err != nil {
		t.Fatal(err)
	}
	over := bytes.Repeat([]byte{'x'}, existingSize)
	over = append(over, '\n')
	if err := os.WriteFile(filepath.Join(overRoot, Target), over, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Plan(context.Background(), overRoot, ".roborev"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over-limit plan err=%v", err)
	}
}

func TestAppendRejectsRootAncestorAndFileDriftBeforeWrite(t *testing.T) {
	t.Run("root", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
			t.Fatal(err)
		}
		service := New(OSFileSystem{})
		plan, err := service.Plan(context.Background(), root, ".roborev")
		if err != nil {
			t.Fatal(err)
		}
		moved := root + "-moved"
		if err := os.Rename(root, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
		result := service.Append(context.Background(), plan)
		if result.Status != StatusPreMutationStale || result.Reason != ReasonStale {
			t.Fatalf("root drift result=%#v", result)
		}
		if _, err := os.Stat(filepath.Join(root, Target)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("root drift wrote target, stat err=%v", err)
		}
	})

	t.Run("ancestor", func(t *testing.T) {
		root := t.TempDir()
		snapshotDir := filepath.Join(root, "cache", "nested")
		if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
			t.Fatal(err)
		}
		service := New(OSFileSystem{})
		plan, err := service.Plan(context.Background(), root, filepath.FromSlash("cache/nested"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(root, "cache"), filepath.Join(root, "cache-moved")); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
			t.Fatal(err)
		}
		result := service.Append(context.Background(), plan)
		if result.Status != StatusPreMutationStale || result.Reason != ReasonStale {
			t.Fatalf("ancestor drift result=%#v", result)
		}
		if _, err := os.Stat(filepath.Join(root, Target)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("ancestor drift wrote target, stat err=%v", err)
		}
	})

	t.Run("file bytes", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, Target)
		if err := os.WriteFile(target, []byte("before"), 0o640); err != nil {
			t.Fatal(err)
		}
		service := New(OSFileSystem{})
		plan, err := service.Plan(context.Background(), root, ".roborev")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("drifted"), 0o640); err != nil {
			t.Fatal(err)
		}
		result := service.Append(context.Background(), plan)
		if result.Status != StatusPreMutationStale || result.Reason != ReasonStale {
			t.Fatalf("file drift result=%#v", result)
		}
		got, _ := os.ReadFile(target)
		if string(got) != "drifted" {
			t.Fatalf("file drift changed bytes=%q", got)
		}
	})
}

func TestAppendExistingHandleIdentityMismatchWritesZero(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, Target)
	if err := os.WriteFile(target, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	fs := &scriptedFS{OSFileSystem: OSFileSystem{}, writeN: -1}
	fs.beforeOpenAppend = func(path string) error {
		if err := os.Rename(path, path+".replaced"); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("racer"), 0o640)
	}
	service := New(fs)
	plan, err := service.Plan(context.Background(), root, ".roborev")
	if err != nil {
		t.Fatal(err)
	}
	result := service.Append(context.Background(), plan)
	if result.Status != StatusPreMutationStale || result.Reason != ReasonStale {
		t.Fatalf("identity mismatch result=%#v", result)
	}
	if fs.openAppendCalls != 1 || fs.writeCalls != 0 {
		t.Fatalf("open=%d writes=%d", fs.openAppendCalls, fs.writeCalls)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "racer" {
		t.Fatalf("replacement was modified: %q", got)
	}
	original, _ := os.ReadFile(target + ".replaced")
	if string(original) != "original" {
		t.Fatalf("original was modified: %q", original)
	}
}

func TestAppendAbsentCreateExclusiveRacerPreservesRacer(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, Target)
	fs := &scriptedFS{OSFileSystem: OSFileSystem{}}
	fs.beforeCreate = func(path string) error {
		return os.WriteFile(path, []byte("racer-created"), 0o640)
	}
	service := New(fs)
	plan, err := service.Plan(context.Background(), root, ".roborev")
	if err != nil {
		t.Fatal(err)
	}
	result := service.Append(context.Background(), plan)
	if result.Status != StatusPreMutationStale || result.Reason != ReasonCreate {
		t.Fatalf("create racer result=%#v", result)
	}
	if fs.createCalls != 1 || fs.writeCalls != 0 {
		t.Fatalf("creates=%d writes=%d", fs.createCalls, fs.writeCalls)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "racer-created" {
		t.Fatalf("racer bytes changed: %q", got)
	}
}

func TestAppendAbsentRootReplacementBeforeAuthorityOpenDoesNotCreateInReplacement(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
		t.Fatal(err)
	}
	moved := root + "-moved"
	fs := &scriptedFS{OSFileSystem: OSFileSystem{}}
	fs.beforeCreate = func(_ string) error {
		if err := os.Rename(root, moved); err != nil {
			return err
		}
		return os.Mkdir(root, 0o755)
	}
	service := New(fs)
	plan, err := service.Plan(context.Background(), root, ".roborev")
	if err != nil {
		t.Fatal(err)
	}
	result := service.Append(context.Background(), plan)
	if result.Status != StatusPreMutationStale || result.Reason != ReasonStale {
		t.Fatalf("root replacement result=%#v", result)
	}
	if fs.createCalls != 0 || fs.writeCalls != 0 {
		t.Fatalf("authority opened replacement or wrote: creates=%d writes=%d", fs.createCalls, fs.writeCalls)
	}
	if fs.rootAuthorityCloseCalls != 1 {
		t.Fatalf("replacement authority close calls=%d want 1", fs.rootAuthorityCloseCalls)
	}
	if _, err := os.Stat(filepath.Join(root, Target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement root received .gitignore: stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(moved, Target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old root unexpectedly created .gitignore: stat err=%v", err)
	}
}

func TestAppendShortAndZeroWriteNeverRetryOrReceiveSuccessCredit(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
	}{
		{name: "zero", n: 0},
		{name: "short", n: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, Target)
			if err := os.WriteFile(target, []byte("existing"), 0o640); err != nil {
				t.Fatal(err)
			}
			fs := &scriptedFS{OSFileSystem: OSFileSystem{}}
			fs.writeOverride = func(file *os.File, data []byte) (int, error) {
				// Make the final bytes appear even though the single Write reports
				// zero/short progress. This must remain failed-unverified.
				if err := os.WriteFile(file.Name(), append([]byte("existing"), data...), 0o640); err != nil {
					return 0, err
				}
				if tc.n == 0 {
					return 0, nil
				}
				return len(data) - 1, nil
			}
			service := New(fs)
			plan, err := service.Plan(context.Background(), root, ".roborev")
			if err != nil {
				t.Fatal(err)
			}
			result := service.Append(context.Background(), plan)
			if result.Status != StatusFailedUnverified {
				t.Fatalf("result=%#v", result)
			}
			if fs.writeCalls != 1 {
				t.Fatalf("write calls=%d", fs.writeCalls)
			}
			got, _ := os.ReadFile(target)
			if !bytes.Equal(got, plan.expected) {
				t.Fatalf("final bytes=%q want=%q", got, plan.expected)
			}
		})
	}
}

func TestAppendExternalAppendFailsPostReadback(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, Target)
	if err := os.WriteFile(target, []byte("existing"), 0o640); err != nil {
		t.Fatal(err)
	}
	fs := &scriptedFS{OSFileSystem: OSFileSystem{}, writeN: -1}
	fs.afterWrite = func(file *os.File, _ []byte) {
		other, err := os.OpenFile(file.Name(), os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return
		}
		_, _ = other.Write([]byte("external"))
		_ = other.Close()
	}
	service := New(fs)
	plan, err := service.Plan(context.Background(), root, ".roborev")
	if err != nil {
		t.Fatal(err)
	}
	result := service.Append(context.Background(), plan)
	if result.Status != StatusFailedUnverified {
		t.Fatalf("result=%#v", result)
	}
	if fs.writeCalls != 1 {
		t.Fatalf("write calls=%d", fs.writeCalls)
	}
	got, _ := os.ReadFile(target)
	if !bytes.HasPrefix(got, plan.expected) || !bytes.HasSuffix(got, []byte("external")) {
		t.Fatalf("external append bytes=%q expected prefix=%q", got, plan.expected)
	}
}

func TestVerifyRejectsReceiptIdentityAndBytesMismatch(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
		t.Fatal(err)
	}
	service := New(OSFileSystem{})
	plan, err := service.Plan(context.Background(), root, ".roborev")
	if err != nil {
		t.Fatal(err)
	}
	result := service.Append(context.Background(), plan)
	if result.Status != StatusVerifiedSuccess || result.Receipt == nil {
		t.Fatalf("append result=%#v", result)
	}

	bytesReceipt := *result.Receipt
	bytesReceipt.expected = append([]byte(nil), bytesReceipt.expected...)
	bytesReceipt.expected[len(bytesReceipt.expected)-1] ^= 1
	if verified := service.Verify(context.Background(), bytesReceipt); verified.Status != StatusFailedUnverified || verified.Reason != ReasonReadbackMismatch {
		t.Fatalf("bytes mismatch verify=%#v", verified)
	}

	identityReceipt := *result.Receipt
	identityReceipt.targetIdentity = "different-handle"
	if verified := service.Verify(context.Background(), identityReceipt); verified.Status != StatusFailedUnverified || verified.Reason != ReasonReadbackMismatch {
		t.Fatalf("identity mismatch verify=%#v", verified)
	}
}
