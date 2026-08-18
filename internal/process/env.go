package process

import (
	"runtime"
	"strings"
)

// The identity probes run third-party agent executables. Keep their
// environment fail-closed: the probe only needs the standard variables used
// to locate an executable and, on Windows, the variables required by the
// process runtime. In particular, do not pass through user profiles,
// provider/cloud credentials, forge credentials, or preload hooks.
var (
	windowsProbeEnvAllowlist = map[string]struct{}{
		"SystemRoot": {},
		"WINDIR":     {},
		"TEMP":       {},
		"TMP":        {},
		"PATH":       {},
		"PATHEXT":    {},
		"ComSpec":    {},
	}
	nonWindowsProbeEnvAllowlist = map[string]struct{}{
		"PATH":   {},
		"TMPDIR": {},
		"TMP":    {},
		"TEMP":   {},
		"LANG":   {},
		"LC_ALL": {},
		"TZ":     {},
	}
)

// MinimalProbeEnv returns only the platform's explicitly allowed environment
// variables for an agent identity probe. Values and their original spelling
// are preserved, but unknown variables are dropped. The returned slice is
// always non-nil, including when env is empty or no variable is allowed.
func MinimalProbeEnv(env []string) []string {
	result := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if probeEnvKeyAllowed(key) {
			result = append(result, entry)
		}
	}
	return result
}

func probeEnvKeyAllowed(key string) bool {
	if runtime.GOOS == "windows" {
		for allowed := range windowsProbeEnvAllowlist {
			if strings.EqualFold(key, allowed) {
				return true
			}
		}
		return false
	}
	_, ok := nonWindowsProbeEnvAllowlist[key]
	return ok
}

// StripUntrustedEnv is retained as a compatibility shim for callers that
// have not yet migrated to MinimalProbeEnv. It now uses the same fail-closed
// allowlist and does not implement a credential denylist.
//
// Deprecated: use MinimalProbeEnv.
func StripUntrustedEnv(env []string) []string {
	return MinimalProbeEnv(env)
}
