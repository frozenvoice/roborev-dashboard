//go:build windows

// Synthetic installer payload. --serve holds this image open until a second
// process of the same file is started with --stop-installed. The installer UI
// test is the only caller. A launch with no arguments exits immediately.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--stop-installed" {
		os.Exit(signal())
	}
	if len(os.Args) > 1 && os.Args[1] == "--serve" {
		os.Exit(serve())
	}
	os.Exit(0)
}

func names() (eventName, mutexName string, err error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(strings.ToLower(abs)))
	base := `Local\RoboRevDashboardSetupUiStop.` + hex.EncodeToString(sum[:8])
	return base + ".evt", base + ".mtx", nil
}

func serve() int {
	eventName, mutexName, err := names()
	if err != nil {
		return 1
	}
	eventPtr, err := windows.UTF16PtrFromString(eventName)
	if err != nil {
		return 1
	}
	mutexPtr, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return 1
	}
	mutex, err := windows.CreateMutex(nil, true, mutexPtr)
	if err != nil {
		return 1
	}
	defer windows.CloseHandle(mutex)
	event, err := windows.CreateEvent(nil, 0, 0, eventPtr)
	if err != nil {
		return 1
	}
	defer windows.CloseHandle(event)
	_, _ = windows.WaitForSingleObject(event, windows.INFINITE)
	return 0
}

func signal() int {
	eventName, mutexName, err := names()
	if err != nil {
		return 1
	}
	eventPtr, err := windows.UTF16PtrFromString(eventName)
	if err != nil {
		return 1
	}
	mutexPtr, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return 1
	}
	mutex, err := windows.OpenMutex(windows.SYNCHRONIZE, false, mutexPtr)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(mutex)
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, eventPtr)
	if err != nil {
		return 1
	}
	defer windows.CloseHandle(event)
	if err := windows.SetEvent(event); err != nil {
		return 1
	}
	_, _ = windows.WaitForSingleObject(mutex, 5000)
	return 0
}
