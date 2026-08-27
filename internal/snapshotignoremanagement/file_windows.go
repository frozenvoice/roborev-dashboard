//go:build windows

package snapshotignoremanagement

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"golang.org/x/sys/windows"
)

func openRead(path string) (*os.File, error) {
	return openExistingRead(path)
}

func openExistingRead(path string) (*os.File, error) {
	extended, err := windowsExtendedPath(path)
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(extended)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, os.ErrInvalid
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = file.Close()
		if err == nil {
			err = errors.New("reparse point target")
		}
		return nil, err
	}
	return file, nil
}

// Windows does not expose O_NOFOLLOW through os.OpenFile.  pathsafety's
// final Lstat/EvalSymlinks checks and the handle identity check below provide
// the same no-reparse boundary; CreateFile OPEN_EXISTING semantics are
// represented by this non-creating open.
func openAppend(path string) (*os.File, error) {
	extended, err := windowsExtendedPath(path)
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(extended)
	if err != nil {
		return nil, err
	}
	// FILE_APPEND_DATA gives each Write append semantics at the kernel boundary
	// without seeking a path-derived offset between the final guard and write.
	handle, err := windows.CreateFile(name, windows.FILE_APPEND_DATA|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, os.ErrInvalid
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = file.Close()
		if err == nil {
			err = errors.New("reparse point target")
		}
		return nil, err
	}
	return file, nil
}

type windowsRootCreateAuthority struct {
	file *os.File
	path string
}

func openRootCreateAuthority(path string) (RootCreateAuthority, error) {
	extended, err := windowsExtendedPath(path)
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(extended)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, os.ErrInvalid
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		_ = file.Close()
		if err == nil {
			err = errors.New("unsafe root handle")
		}
		return nil, err
	}
	return &windowsRootCreateAuthority{file: file, path: filepath.Clean(path)}, nil
}

func (a *windowsRootCreateAuthority) Stat() (os.FileInfo, error) {
	if a == nil || a.file == nil {
		return nil, os.ErrInvalid
	}
	return a.file.Stat()
}

func (a *windowsRootCreateAuthority) Identity() (string, error) {
	if a == nil || a.file == nil {
		return "", os.ErrInvalid
	}
	return pathsafety.HandleIdentity(a.file)
}

func (a *windowsRootCreateAuthority) CreateGitignoreExclusive() (File, error) {
	if a == nil || a.file == nil {
		return nil, os.ErrInvalid
	}
	name, err := windows.NewNTUnicodeString(Target)
	if err != nil {
		return nil, err
	}
	// NT object lookup is normally case-insensitive on Windows. Preserve that
	// normal NTFS semantics while keeping the child name fixed and
	// RootDirectory-bound (so a differently-cased existing .GITIGNORE is still
	// observed by FILE_CREATE rather than creating a second logical file).
	object := &windows.OBJECT_ATTRIBUTES{ObjectName: name, RootDirectory: windows.Handle(a.file.Fd()), Attributes: windows.OBJ_CASE_INSENSITIVE}
	object.Length = uint32(unsafe.Sizeof(*object))
	var status windows.IO_STATUS_BLOCK
	var allocationSize int64
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, windows.FILE_APPEND_DATA|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, object, &status, &allocationSize, windows.FILE_ATTRIBUTE_NORMAL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), filepath.Join(a.path, Target))
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, os.ErrInvalid
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		_ = file.Close()
		if err == nil {
			err = errors.New("unsafe target handle")
		}
		return nil, err
	}
	return &osFile{File: file}, nil
}

func (a *windowsRootCreateAuthority) Close() error {
	if a == nil || a.file == nil {
		return nil
	}
	err := a.file.Close()
	a.file = nil
	return err
}

func windowsExtendedPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	if strings.HasPrefix(path, `\\?\`) {
		return path, nil
	}
	if strings.HasPrefix(strings.ReplaceAll(path, "/", `\`), `\\.\`) {
		return "", errors.New("device namespace paths are not supported")
	}
	abs, err := filepath.Abs(path)
	if err != nil || !filepath.IsAbs(abs) {
		return "", errors.New("path is not absolute")
	}
	abs = filepath.Clean(abs)
	if strings.HasPrefix(abs, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(abs, `\\`), nil
	}
	return `\\?\` + abs, nil
}
