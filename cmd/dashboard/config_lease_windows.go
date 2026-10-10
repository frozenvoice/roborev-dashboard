//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errConfigInUse = errors.New("Dashboard configuration is already in use; stop the existing Dashboard or use a separate --config-dir")

type configLease struct {
	handle windows.Handle
	once   sync.Once
	err    error
}

func (lease *configLease) Close() error {
	lease.once.Do(func() { lease.err = windows.CloseHandle(lease.handle) })
	return lease.err
}

// A per-user, per-config existence lease prevents installed and portable copies
// from racing registry writes. No ownership wait is used: Go goroutines can move
// between OS threads, while Windows mutex ownership is thread-affine.
func acquireConfigLease(configFile string) (io.Closer, error) {
	canonical, err := canonicalConfigPath(configFile)
	if err != nil {
		return nil, fmt.Errorf("resolve Dashboard configuration lease: %w", err)
	}
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		if err == nil {
			err = errors.New("current process token has no user SID")
		}
		return nil, fmt.Errorf("identify configuration user: %w", err)
	}
	sid := user.User.Sid.String()
	digest := sha256.Sum256([]byte(sid + "\x00" + strings.ToLower(canonical)))
	name, err := windows.UTF16PtrFromString(`Global\RoboRevDashboard-Config-` + hex.EncodeToString(digest[:]))
	if err != nil {
		return nil, err
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + sid + ")")
	if err != nil {
		return nil, fmt.Errorf("secure configuration lease: %w", err)
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	handle, createErr := windows.CreateMutex(&attributes, false, name)
	if handle == 0 {
		return nil, fmt.Errorf("create configuration lease: %w", createErr)
	}
	if createErr != nil {
		_ = windows.CloseHandle(handle)
		if errors.Is(createErr, windows.ERROR_ALREADY_EXISTS) {
			return nil, errConfigInUse
		}
		return nil, fmt.Errorf("create configuration lease: %w", createErr)
	}
	return &configLease{handle: handle}, nil
}

// Resolve the parent write namespace, not a config file symlink's target:
// registry persistence atomically replaces the leaf. Resolve the nearest
// existing parent ancestor with a read-only handle and append missing suffixes.
// This creates no files and normalizes directory aliases and path spelling.
func canonicalConfigPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("configuration path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	ancestor := filepath.Dir(abs)
	suffix := []string{filepath.Base(abs)}
	for {
		resolved, err := existingConfigPath(ancestor)
		if err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return strings.ToLower(filepath.Clean(resolved)), nil
		}
		if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", err
		}
		component := filepath.Base(ancestor)
		// Win32 directory creation can strip a trailing space even though
		// GetFullPathName preserves it. Refuse that unstable missing name.
		if strings.HasSuffix(component, " ") {
			return "", errors.New("missing configuration directory component ends with a space; choose a directory without trailing spaces")
		}
		suffix = append(suffix, component)
		ancestor = parent
	}
}

func existingConfigPath(path string) (string, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(ptr, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 512)
	for {
		size, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if size >= uint32(len(buffer)) {
			buffer = make([]uint16, size+1)
			continue
		}
		resolved := windows.UTF16ToString(buffer[:size])
		if strings.HasPrefix(resolved, `\\?\UNC\`) {
			resolved = `\\` + resolved[len(`\\?\UNC\`):]
		} else {
			resolved = strings.TrimPrefix(resolved, `\\?\`)
		}
		return resolved, nil
	}
}
