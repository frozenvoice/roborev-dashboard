//go:build !windows

package process

import "os/exec"

func prepareCommand(_ *exec.Cmd) {}

func attachProcessTree(_ *exec.Cmd) (processTree, error) { return nil, nil }
