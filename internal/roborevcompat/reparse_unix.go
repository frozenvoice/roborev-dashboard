//go:build !windows

package roborevcompat

import "os"

func isUnsafeDirectory(_ string, info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }
