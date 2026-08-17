//go:build !windows

package process

import "os/exec"

func prepareCommand(_ *exec.Cmd) {}
