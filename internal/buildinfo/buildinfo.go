// Package buildinfo owns the small, deterministic provenance contract exposed
// by the Dashboard executable. Values are deliberately supplied through
// linker flags for release builds instead of being derived from the machine
// running the executable.
package buildinfo

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
)

const (
	Name             = "roborev-dashboard"
	SupportedRoboRev = "v0.64.0"
)

// These variables are the only linker-injected values. Keep their defaults
// valid for a local development build and do not add timestamps, paths, or
// machine identity here: the same source and inputs must produce the same
// release bytes.
var (
	version = "dev"
	commit  = "unknown"
	release = "false"
	target  = ""
)

// Metadata is the public version contract. Field order is intentional: the
// encoding/json output is used as a deterministic machine-readable response.
type Metadata struct {
	Name                 string `json:"name"`
	Version              string `json:"version"`
	Commit               string `json:"commit"`
	Release              bool   `json:"release"`
	Target               string `json:"target"`
	RoboRevCompatibility string `json:"roborevCompatibility"`
}

// Current validates linker-provided metadata and returns the executable's
// provenance. Invalid release metadata is an error rather than something to
// silently repair or expose as a misleading release.
func Current() (Metadata, error) {
	releaseValue, err := parseRelease(release)
	if err != nil {
		return Metadata{}, err
	}

	if releaseValue {
		if !IsStrictSemVer(version) || version == "dev" {
			return Metadata{}, fmt.Errorf("release build has invalid version %q", version)
		}
		if !isCommit(commit) {
			return Metadata{}, fmt.Errorf("release build has invalid commit %q", commit)
		}
		if target != "windows/amd64" {
			return Metadata{}, fmt.Errorf("release build has invalid target %q", target)
		}
	} else {
		if version != "dev" {
			return Metadata{}, fmt.Errorf("development build has invalid version %q", version)
		}
		if commit != "unknown" && !isCommit(commit) {
			return Metadata{}, fmt.Errorf("development build has invalid commit %q", commit)
		}
	}

	resolvedTarget := target
	if resolvedTarget == "" {
		resolvedTarget = runtime.GOOS + "/" + runtime.GOARCH
	}
	if !isTarget(resolvedTarget) {
		return Metadata{}, fmt.Errorf("build has invalid target %q", resolvedTarget)
	}

	return Metadata{
		Name:                 Name,
		Version:              version,
		Commit:               commit,
		Release:              releaseValue,
		Target:               resolvedTarget,
		RoboRevCompatibility: SupportedRoboRev,
	}, nil
}

// IsStrictSemVer reports whether value follows SemVer 2.0.0 syntax. Numeric
// identifiers are checked for leading zeroes, while build metadata remains
// allowed by the standard.
func IsStrictSemVer(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(value, "v") {
		return false
	}
	core := value
	if build := strings.IndexByte(core, '+'); build >= 0 {
		if !validIdentifiers(core[build+1:], false) {
			return false
		}
		core = core[:build]
	}
	if prerelease := strings.IndexByte(core, '-'); prerelease >= 0 {
		if !validIdentifiers(core[prerelease+1:], true) {
			return false
		}
		core = core[:prerelease]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if !validNumericIdentifier(part) {
			return false
		}
	}
	return true
}

func validIdentifiers(value string, prerelease bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		for _, char := range identifier {
			if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && char != '-' {
				return false
			}
		}
		if prerelease && allDigits(identifier) && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

func validNumericIdentifier(value string) bool {
	if value == "" || !allDigits(value) {
		return false
	}
	return len(value) == 1 || value[0] != '0'
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func parseRelease(value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("build has invalid release flag %q", value)
	}
}

func isCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func isTarget(value string) bool {
	parts := strings.Split(value, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != "" && !strings.ContainsAny(value, "\\ \t\r\n")
}

// JSON returns strict deterministic JSON without a trailing newline.
func (m Metadata) JSON() ([]byte, error) {
	if m.Name != Name || m.RoboRevCompatibility != SupportedRoboRev || !IsStrictSemVer(m.Version) && m.Version != "dev" || m.Commit != "unknown" && !isCommit(m.Commit) || !isTarget(m.Target) {
		return nil, errors.New("invalid build metadata")
	}
	return json.Marshal(m)
}

// Human returns the stable human-readable version output.
func (m Metadata) Human() string {
	return fmt.Sprintf("%s %s\ncommit %s\nRoboRev compatibility %s\n", m.Name, m.Version, m.Commit, m.RoboRevCompatibility)
}
