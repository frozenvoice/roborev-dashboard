//go:build !windows

package snapshotignoremanagement

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPlanRejectsNonRegularGitignore(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, Target)
	if err := syscall.Mkfifo(target, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := New(OSFileSystem{}).Plan(context.Background(), root, ".roborev"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("FIFO target err=%v", err)
	}
}
