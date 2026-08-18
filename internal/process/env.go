package process

import (
	"runtime"
	"strings"
)

var untrustedProbeEnvKeys = []string{
	"GITLAB_TOKEN", "GL_TOKEN", "CI_JOB_TOKEN", "CI_REGISTRY_PASSWORD",
	"CI_REPOSITORY_URL", "CI_DEPLOY_PASSWORD", "CI_DEPENDENCY_PROXY_PASSWORD",
	"CI_JOB_JWT", "CI_JOB_JWT_V1", "CI_JOB_JWT_V2", "CI_BUILD_TOKEN", "CI_BUILD_REPO",
	"GH_TOKEN", "GITHUB_TOKEN", "NODE_OPTIONS", "LD_PRELOAD", "LD_AUDIT", "DYLD_INSERT_LIBRARIES",
}

// StripUntrustedEnv returns a copy without forge credentials and runtime
// preload hooks. It is used for capability probes, which run before any
// review and must not inherit credentials or code-injection variables.
func StripUntrustedEnv(env []string) []string {
	result := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		remove := false
		for _, blocked := range untrustedProbeEnvKeys {
			if key == blocked || (runtime.GOOS == "windows" && strings.EqualFold(key, blocked)) {
				remove = true
				break
			}
		}
		if !remove {
			result = append(result, entry)
		}
	}
	return result
}
