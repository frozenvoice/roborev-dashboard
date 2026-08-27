//go:build windows

package snapshotignoremanagement

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsRootCreateAuthorityPreventsRenameWhileBound(t *testing.T) {
	root := t.TempDir()
	authority, err := (OSFileSystem{}).OpenRootCreateAuthority(root)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()

	moved := root + "-moved"
	if err := os.Rename(root, moved); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("root rename error=%v want sharing violation", err)
	}
	if _, err := os.Stat(moved); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rename unexpectedly created moved root: stat err=%v", err)
	}

	first, err := authority.CreateGitignoreExclusive()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := authority.CreateGitignoreExclusive()
	if err == nil {
		_ = second.Close()
		t.Fatal("second FILE_CREATE unexpectedly succeeded")
	}
}

func TestWindowsRootCreateAuthorityUsesCaseInsensitiveNoReplace(t *testing.T) {
	root := t.TempDir()
	caseVariant := filepath.Join(root, strings.ToUpper(Target))
	if err := os.WriteFile(caseVariant, []byte("legacy"), 0o640); err != nil {
		t.Fatal(err)
	}
	authority, err := (OSFileSystem{}).OpenRootCreateAuthority(root)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()

	handle, err := authority.CreateGitignoreExclusive()
	if err == nil {
		_ = handle.Close()
		t.Fatal("case-variant .gitignore was not treated as existing")
	}
	data, err := os.ReadFile(caseVariant)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "legacy" {
		t.Fatalf("case-variant bytes changed: %q", data)
	}
}
