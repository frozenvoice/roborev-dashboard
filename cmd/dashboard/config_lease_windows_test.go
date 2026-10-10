//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestConfigLeasePathsAndLifecycle(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "미생성 Config", "config.json")
	lease, err := acquireConfigLease(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("lease created configuration directory: %v", err)
	}
	for _, equivalent := range []string{strings.ToUpper(path), `\\?\` + path} {
		if duplicate, err := acquireConfigLease(equivalent); !errors.Is(err, errConfigInUse) {
			if duplicate != nil {
				_ = duplicate.Close()
			}
			t.Fatalf("equivalent path %q did not conflict: %v", equivalent, err)
		}
	}
	separate, err := acquireConfigLease(filepath.Join(root, "Separate Config", "config.json"))
	if err != nil {
		t.Fatalf("separate config: %v", err)
	}
	_ = separate.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	const content = "existing settings stay intact"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := acquireConfigLease(path); !errors.Is(err, errConfigInUse) {
		if duplicate != nil {
			_ = duplicate.Close()
		}
		t.Fatalf("materialized path did not conflict: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
	reacquired, err := acquireConfigLease(path)
	if err != nil {
		t.Fatalf("reacquire after close: %v", err)
	}
	_ = reacquired.Close()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != content {
		t.Fatalf("configuration changed: %q, %v", data, err)
	}
}

func TestConfigLeaseDirectoryAlias(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := createConfigTestJunction(alias, target); err != nil {
		t.Fatalf("directory junction: %v", err)
	}
	lease, err := acquireConfigLease(filepath.Join(target, "missing", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	duplicate, err := acquireConfigLease(filepath.Join(alias, "missing", "config.json"))
	if duplicate != nil {
		_ = duplicate.Close()
	}
	if !errors.Is(err, errConfigInUse) {
		t.Fatalf("directory alias did not conflict: %v", err)
	}
}

func TestConfigLeaseMissingSpaceRefusesWithoutWriting(t *testing.T) {
	for _, extended := range []bool{false, true} {
		t.Run(fmt.Sprintf("extended_%v", extended), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "missing ", "config.json")
			if extended {
				path = `\\?\` + path
			}
			lease, err := acquireConfigLease(path)
			if lease != nil {
				_ = lease.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "ends with a space") {
				t.Fatalf("ambiguous missing directory was not rejected: %v", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("rejected lease wrote files or directories: %v, %v", entries, err)
			}
		})
	}
}

func TestConfigLeaseMissingDotRemainsStable(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "missing.", "config.json")
	ordinary := filepath.Join(root, "missing", "config.json")
	lease, err := acquireConfigLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("settings"), 0600); err != nil {
		t.Fatal(err)
	}
	duplicate, err := acquireConfigLease(ordinary)
	if duplicate != nil {
		_ = duplicate.Close()
	}
	if !errors.Is(err, errConfigInUse) {
		t.Fatalf("normalized trailing-dot directory did not conflict: %v", err)
	}
}

func TestConfigLeaseFileAtomicReplacement(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprintf("symlink_%v", symlink), func(t *testing.T) {
			testConfigLeaseFileAtomicReplacement(t, symlink)
		})
	}
}

func testConfigLeaseFileAtomicReplacement(t *testing.T, symlink bool) {
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte("target content"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.json")
	if symlink {
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("file symlink unavailable: %v", err)
		}
	} else if err := os.WriteFile(path, []byte("old configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := acquireConfigLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	temp := path + ".tmp"
	if err := os.WriteFile(temp, []byte("replaced configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temp, path); err != nil {
		t.Fatal(err)
	}
	duplicate, err := acquireConfigLease(path)
	if duplicate != nil {
		_ = duplicate.Close()
	}
	if !errors.Is(err, errConfigInUse) {
		t.Fatalf("atomic config symlink replacement changed lease identity: %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "target content" {
		t.Fatalf("symlink target changed: %q, %v", content, err)
	}
}

// Directory mount-point junctions do not require symbolic-link privilege.
// This helper changes only a newly created directory inside t.TempDir.
func createConfigTestJunction(alias, target string) error {
	if err := os.Mkdir(alias, 0700); err != nil {
		return err
	}
	substitute, err := windows.UTF16FromString(`\??\` + target)
	if err != nil {
		return err
	}
	printName, err := windows.UTF16FromString(target)
	if err != nil {
		return err
	}
	pathBytes := 2 * (len(substitute) + len(printName))
	buffer := make([]byte, 16+pathBytes)
	binary.LittleEndian.PutUint32(buffer[0:4], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buffer[4:6], uint16(8+pathBytes))
	binary.LittleEndian.PutUint16(buffer[10:12], uint16(2*(len(substitute)-1)))
	binary.LittleEndian.PutUint16(buffer[12:14], uint16(2*len(substitute)))
	binary.LittleEndian.PutUint16(buffer[14:16], uint16(2*(len(printName)-1)))
	for index, code := range append(substitute, printName...) {
		binary.LittleEndian.PutUint16(buffer[16+index*2:], code)
	}
	ptr, err := windows.UTF16PtrFromString(alias)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(ptr, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var returned uint32
	return windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil)
}

func TestConfigLeaseChildProcessAndCrashRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConfigLeaseHelper$")
	command.Env = append(os.Environ(), "DASHBOARD_CONFIG_LEASE_HELPER="+path)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "leased\n" {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("child readiness = %q, %v: %s", line, err, stderr.String())
	}
	duplicate, err := acquireConfigLease(path)
	if duplicate != nil {
		_ = duplicate.Close()
	}
	if !errors.Is(err, errConfigInUse) {
		t.Fatalf("another process did not conflict: %v", err)
	}
	// Termination skips deferred Close; Windows must reclaim the kernel lease.
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	reacquired, err := acquireConfigLease(path)
	if err != nil {
		t.Fatalf("reacquire after process exit: %v", err)
	}
	_ = reacquired.Close()
}

func TestConfigLeaseHelper(t *testing.T) {
	path := os.Getenv("DASHBOARD_CONFIG_LEASE_HELPER")
	if path == "" {
		return
	}
	lease, err := acquireConfigLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	fmt.Println("leased")
	_, _ = io.Copy(io.Discard, os.Stdin)
}
