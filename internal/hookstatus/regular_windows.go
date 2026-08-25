//go:build windows

package hookstatus

import (
	"os"
	"syscall"
)

func safeRegular(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	// Some deterministic injected filesystems do not expose Windows native
	// identity data. Real os.FileInfo values do; when absent, retain the
	// regular-file checks and let the opened-handle revalidation provide the
	// strongest identity available from that adapter.
	return !ok || attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT == 0
}
