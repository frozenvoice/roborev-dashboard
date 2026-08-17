//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris

package roborev

import (
	"errors"
	"os"
)

// Unsupported platforms fail closed for existing files. A missing config is
// still distinguishable so callers can use the built-in defaults.
func openPanelConfigNoFollow(path string) (*os.File, error) {
	if _, err := os.Lstat(path); err != nil {
		return nil, err
	}
	return nil, errors.New("safe panel config opening is unsupported on this platform")
}
