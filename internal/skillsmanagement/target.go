package skillsmanagement

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
)

// Target is entirely server-derived. It must never be populated from a
// browser request other than the allowlisted Agent identifier.
type Target struct {
	Agent     string
	ConfigDir string
	SkillsDir string
}

type TargetResolver struct {
	FS         pathsafety.FileSystem
	UserHome   string
	HomeLookup func() (string, error)
}

// ResolveTarget is the stateless convenience form used by transport code and
// deterministic tests.
func ResolveTarget(fs pathsafety.FileSystem, agent string, env []string, userHome string) (Target, error) {
	return NewTargetResolver(fs, userHome).Resolve(agent, env)
}

func NewTargetResolver(fs pathsafety.FileSystem, userHome string) TargetResolver {
	return TargetResolver{FS: fs, UserHome: userHome, HomeLookup: os.UserHomeDir}
}

// Resolve applies the exact v0.64 agent config semantics. Environment
// overrides are selected by nonempty value; no path is accepted unless the
// final config and skills paths are absolute.
func (r TargetResolver) Resolve(agent string, env []string) (Target, error) {
	if !IsSupportedAgent(agent) {
		return Target{}, ErrUnsupportedAgent
	}
	var configDir string
	switch agent {
	case "claude":
		configDir = nonemptyEnv(env, "CLAUDE_CONFIG_DIR")
	case "codex":
		configDir = nonemptyEnv(env, "CODEX_HOME")
	case "grok":
		configDir = nonemptyEnv(env, "GROK_HOME")
	case "droid":
		droidHome := nonemptyEnv(env, "HOME")
		if droidHome != "" {
			configDir = filepath.Join(droidHome, ".factory")
		}
	}
	if configDir == "" {
		home := r.UserHome
		if home == "" {
			lookup := r.HomeLookup
			if lookup == nil {
				lookup = os.UserHomeDir
			}
			var err error
			home, err = lookup()
			if err != nil {
				return Target{}, ErrConfigUnavailable
			}
		}
		switch agent {
		case "claude":
			configDir = filepath.Join(home, ".claude")
		case "codex":
			configDir = filepath.Join(home, ".codex")
		case "grok":
			configDir = filepath.Join(home, ".grok")
		case "droid":
			configDir = filepath.Join(home, ".factory")
		}
	}
	configDir = filepath.Clean(configDir)
	skillsDir := filepath.Clean(filepath.Join(configDir, "skills"))
	if !filepath.IsAbs(configDir) || !filepath.IsAbs(skillsDir) {
		return Target{}, ErrRelativeTarget
	}
	// Resolve is deliberately read-only. The root existence/type/safety proof
	// is performed by ValidateConfigRoot immediately before preview/execute.
	return Target{Agent: agent, ConfigDir: configDir, SkillsDir: skillsDir}, nil
}

func (r TargetResolver) ValidateConfigRoot(target Target) (pathsafety.Snapshot, error) {
	if !IsSupportedAgent(target.Agent) || !filepath.IsAbs(target.ConfigDir) || !filepath.IsAbs(target.SkillsDir) {
		return pathsafety.Snapshot{}, ErrRelativeTarget
	}
	fs := r.FS
	if fs == nil {
		fs = pathsafety.OSFileSystem{}
	}
	snapshot, err := pathsafety.CaptureDirectory(fs, target.ConfigDir)
	if err != nil {
		if err == pathsafety.ErrMissing {
			return pathsafety.Snapshot{}, ErrConfigUnavailable
		}
		return pathsafety.Snapshot{}, ErrTreeUnsafe
	}
	if !snapshot.Exists || snapshot.Kind != pathsafety.Directory {
		return pathsafety.Snapshot{}, ErrConfigUnavailable
	}
	return snapshot, nil
}

func nonemptyEnv(env []string, name string) string {
	return nonemptyEnvForOS(env, name, runtime.GOOS)
}

// os.Getenv is case-insensitive on Windows and case-sensitive on Unix. Keep
// the distinction explicit instead of relying on the casing returned by
// os.Environ, so injected deterministic environments follow the same contract.
func nonemptyEnvForOS(env []string, name, goos string) string {
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && envKeyEqual(key, name, goos) && value != "" {
			return value
		}
	}
	return ""
}

func envKeyEqual(key, name, goos string) bool {
	if goos == "windows" {
		return strings.EqualFold(key, name)
	}
	return key == name
}
