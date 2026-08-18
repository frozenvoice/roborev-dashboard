//go:build windows

package roborevcompat

import (
	"golang.org/x/sys/windows"
	"os"
)

func isUnsafeDirectory(path string, info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attrs, err := windows.GetFileAttributes(name)
	return err != nil || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
