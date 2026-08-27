//go:build !windows

package snapshotignoremanagement

import (
	"os"
	"path/filepath"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"golang.org/x/sys/unix"
)

func openRead(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
}

func openAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
}

type unixRootCreateAuthority struct {
	file *os.File
	path string
}

func openRootCreateAuthority(path string) (RootCreateAuthority, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, os.ErrInvalid
	}
	return &unixRootCreateAuthority{file: file, path: filepath.Clean(path)}, nil
}

func (a *unixRootCreateAuthority) Stat() (os.FileInfo, error) {
	if a == nil || a.file == nil {
		return nil, os.ErrInvalid
	}
	return a.file.Stat()
}

func (a *unixRootCreateAuthority) Identity() (string, error) {
	if a == nil || a.file == nil {
		return "", os.ErrInvalid
	}
	return pathsafety.HandleIdentity(a.file)
}

func (a *unixRootCreateAuthority) CreateGitignoreExclusive() (File, error) {
	if a == nil || a.file == nil {
		return nil, os.ErrInvalid
	}
	fd, err := unix.Openat(int(a.file.Fd()), Target, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o644)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), filepath.Join(a.path, Target))
	if file == nil {
		_ = unix.Close(fd)
		return nil, os.ErrInvalid
	}
	return &osFile{File: file}, nil
}

func (a *unixRootCreateAuthority) Close() error {
	if a == nil || a.file == nil {
		return nil
	}
	err := a.file.Close()
	a.file = nil
	return err
}
