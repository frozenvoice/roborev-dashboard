//go:build !windows

package snapshotignoremanagement

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendAbsentBoundAuthoritySurvivesRootRenameWithoutWritingReplacement(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
		t.Fatal(err)
	}
	moved := root + "-moved"
	fs := &scriptedFS{OSFileSystem: OSFileSystem{}}
	fs.beforeBoundCreate = func() error {
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
	if result.Status != StatusFailedUnverified || result.Reason != ReasonStale {
		t.Fatalf("bound root rename result=%#v", result)
	}
	if fs.createCalls != 1 || fs.writeCalls != 0 {
		t.Fatalf("bound create/write counts=%d/%d", fs.createCalls, fs.writeCalls)
	}
	if fs.rootAuthorityCloseCalls != 1 {
		t.Fatalf("root authority close calls=%d want 1", fs.rootAuthorityCloseCalls)
	}
	if _, err := os.Stat(filepath.Join(root, Target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement root received .gitignore: stat err=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(moved, Target))
	if err != nil {
		t.Fatalf("bound original .gitignore: %v", err)
	}
	if len(data) != 0 || !bytes.Equal(data, nil) {
		t.Fatalf("bound original was unexpectedly written: %q", data)
	}
}
