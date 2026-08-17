//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package roborev

import (
	"os"

	"golang.org/x/sys/unix"
)

func openPanelConfigNoFollow(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	handle := os.NewFile(uintptr(fd), path)
	if handle == nil {
		_ = unix.Close(fd)
		return nil, os.ErrInvalid
	}
	return handle, nil
}
