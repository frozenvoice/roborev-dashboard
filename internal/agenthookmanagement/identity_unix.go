//go:build !windows

package agenthookmanagement

import (
	"fmt"
	"os"
	"syscall"
)

func handleIdentity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	return fileIdentity("", info)
}

func fileIdentity(_ string, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Sprintf("%d:%d:%o", info.Size(), info.ModTime().UnixNano(), info.Mode()), nil
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}

func isReparsePoint(_ string, info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }
