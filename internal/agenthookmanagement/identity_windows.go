//go:build windows

package agenthookmanagement

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func handleIdentity(file *os.File) (string, error) {
	var data windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &data); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d", data.VolumeSerialNumber, data.FileIndexHigh, data.FileIndexLow), nil
}

func fileIdentity(path string, _ os.FileInfo) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return handleIdentity(file)
}

func isReparsePoint(path string, info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attrs, err := windows.GetFileAttributes(name)
	return err != nil || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
