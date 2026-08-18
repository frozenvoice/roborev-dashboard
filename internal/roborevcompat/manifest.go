// Package roborevcompat contains the version-pinned, security-sensitive
// RoboRev bootstrap adapter. It deliberately has no dependency on the HTTP
// server or Dashboard state machine.
package roborevcompat

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
)

var (
	ErrUnsupportedPlatform = errors.New("RoboRev bootstrap is unsupported on this platform")
	ErrUnsupportedVersion  = errors.New("installed RoboRev version is unsupported")
	ErrExplicitOverride    = errors.New("RoboRev path was explicitly configured")
	ErrExistingBinary      = errors.New("managed RoboRev binary already exists")
	ErrDownloadContract    = errors.New("RoboRev download contract failed")
	ErrArchiveContract     = errors.New("RoboRev archive contract failed")
	ErrDownloadSize        = errors.New("RoboRev download size mismatch")
	ErrDownloadHash        = errors.New("RoboRev download hash mismatch")
	ErrRedirectPolicy      = errors.New("RoboRev download redirect policy rejected")
	ErrArchiveCRC          = errors.New("RoboRev archive checksum mismatch")
	ErrInstallPermission   = errors.New("RoboRev installation permission denied")
	ErrInstallPolicy       = errors.New("RoboRev installation policy rejected")
)

type Manifest struct {
	Version        string
	Name           string
	URL            string
	CompressedSize int64
	SHA256         string
	ArchiveEntry   string
	TargetName     string
}

var manifest064 = Manifest{
	Version:        "v0.64.0",
	Name:           "roborev",
	URL:            "https://github.com/kenn-io/roborev/releases/download/v0.64.0/roborev_0.64.0_windows_amd64.zip",
	CompressedSize: 16325029,
	SHA256:         "157b008086a2ad1c107fe4aa70f172275400dd508c4c9761db2d604f9e56e056",
	ArchiveEntry:   "roborev.exe",
	TargetName:     "roborev.exe",
}

// Manifest064 returns a value copy. The release policy is intentionally not
// mutable by callers; all policy checks use this same private manifest.
func Manifest064() Manifest { return manifest064 }

var canonicalVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

func ValidateVersionIdentity(name, version string) error {
	if name != manifest064.Name || !canonicalVersion.MatchString(version) {
		return fmt.Errorf("%w: expected name %q and canonical version", ErrUnsupportedVersion, manifest064.Name)
	}
	return nil
}

func IsSupportedVersion(version string) bool { return version == manifest064.Version }

func isPinnedManifest(value Manifest) bool { return value == manifest064 }

func SupportedPlatform(goos, goarch string) bool { return goos == "windows" && goarch == "amd64" }

func RequireSupportedPlatform(goos, goarch string) error {
	if !SupportedPlatform(goos, goarch) {
		return fmt.Errorf("%w: %s/%s (supported: windows/amd64)", ErrUnsupportedPlatform, goos, goarch)
	}
	return nil
}

func RuntimePlatformSupported() bool { return SupportedPlatform(runtime.GOOS, runtime.GOARCH) }

func ManagedTarget(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".roborev", "bin", manifest064.TargetName)
}
