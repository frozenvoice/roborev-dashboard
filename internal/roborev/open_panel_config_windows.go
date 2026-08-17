//go:build windows

package roborev

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func openPanelConfigNoFollow(path string) (*os.File, error) {
	extendedPath, err := windowsExtendedPath(path)
	if err != nil {
		return nil, err
	}
	pathPtr, err := windows.UTF16PtrFromString(extendedPath)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, os.ErrInvalid
	}
	return file, nil
}

// windowsExtendedPath converts ordinary Windows paths to the extended-length
// namespace used by Win32 APIs. Extended paths are preserved verbatim so this
// helper never adds a second namespace prefix or changes caller spelling.
func windowsExtendedPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("panel config path is required")
	}
	if strings.HasPrefix(path, `\\?\`) {
		return path, nil
	}
	if strings.HasPrefix(path, `\\.\`) {
		return "", errors.New("device namespace paths are not supported")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if strings.HasPrefix(absolute, `\\?\`) {
		return absolute, nil
	}
	if strings.HasPrefix(absolute, `\\.\`) {
		return "", errors.New("device namespace paths are not supported")
	}
	if strings.HasPrefix(absolute, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(absolute, `\\`), nil
	}
	if !filepath.IsAbs(absolute) {
		return "", errors.New("panel config path is not absolute")
	}
	return `\\?\` + absolute, nil
}
