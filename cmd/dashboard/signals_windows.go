//go:build windows

package main

import (
	"os"
	"syscall"
)

func shutdownSignals() []os.Signal {
	// On Windows os.Interrupt covers both CTRL+C and CTRL+BREAK. SIGTERM is
	// delivered for console close/logoff/shutdown events.
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
