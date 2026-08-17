//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package roborev

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLoadPanelConfigRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("FIFO creation unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	result := make(chan error, 1)
	go func() {
		_, err := loadPanelConfig(path)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("FIFO config was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO config read blocked")
	}
}
