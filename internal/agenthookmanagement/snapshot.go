// Package agenthookmanagement contains the narrow, server-side safety boundary
// for one RoboRev agent-hook configuration file. The filesystem primitives are
// shared with Skills Management through internal/pathsafety.
package agenthookmanagement

import "github.com/froze/roborev-dashboard/internal/pathsafety"

const MaxConfigBytes = pathsafety.MaxFileBytes

var (
	ErrInvalidPath       = pathsafety.ErrInvalidPath
	ErrUnsafePath        = pathsafety.ErrUnsafePath
	ErrUnsafeConfig      = pathsafety.ErrUnsafeFile
	ErrConfigChanged     = pathsafety.ErrChanged
	ErrConfigTooLarge    = pathsafety.ErrTooLarge
	ErrConfigUnavailable = pathsafety.ErrUnavailable
)

// File and FileSystem remain aliases so existing Agent Hook Management test
// doubles and callers retain their source-compatible behavior.
type File = pathsafety.File
type FileSystem = pathsafety.FileSystem
type OSFileSystem = pathsafety.OSFileSystem
type Snapshot = pathsafety.Snapshot

// Capture preserves the historical Agent Hook API while using the shared
// bounded, identity-bound regular-file capture implementation.
func Capture(fs FileSystem, rawPath string) (Snapshot, error) {
	return pathsafety.CaptureFile(fs, rawPath)
}
