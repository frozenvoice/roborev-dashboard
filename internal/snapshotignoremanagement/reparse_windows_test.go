//go:build windows

package snapshotignoremanagement

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPlanRejectsWindowsReparseGitignore(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("private"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, Target)); err != nil {
		t.Skipf("creating a Windows reparse-point symlink is unavailable: %v", err)
	}
	if _, err := New(OSFileSystem{}).Plan(context.Background(), root, ".roborev"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("reparse target err=%v", err)
	}
}
