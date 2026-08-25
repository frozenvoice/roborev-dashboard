//go:build !windows

package hookstatus

import "os"

func safeRegular(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}
