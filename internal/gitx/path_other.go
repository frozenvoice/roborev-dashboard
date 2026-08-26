//go:build !windows

package gitx

import "os"

func pathComponentAmbiguous(info os.FileInfo) bool {
	return info == nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() && info.Mode().Perm()&0o111 == 0
}
