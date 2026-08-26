//go:build !windows

package roborev

import "os"

func safeExecutableInfo(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}
