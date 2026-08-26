package roborev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	agentHookReadinessVersion        = "v0.64.0"
	agentHookReadinessProbeTimeout   = 5 * time.Second
	agentHookReadinessOverallTimeout = 15 * time.Second
	agentHookReadinessOutputLimit    = 64 << 10
)

var ErrAgentHookReadinessBinding = errors.New("agent hook readiness binding is unavailable")

type agentHookReadinessSpec struct {
	agent   string
	display string
}

var agentHookReadinessSpecs = [...]agentHookReadinessSpec{
	{agent: "codex", display: "Codex"},
	{agent: "claude", display: "Claude"},
	{agent: "grok", display: "Grok Build"},
	{agent: "droid", display: "Factory Droid"},
}

// AgentHookReadiness performs the deliberately narrow, read-only readiness
// probe for the four fixed v0.64.0 agent integrations. It never asks the
// upstream client to inspect status, dump config, or manage a daemon.
func (c *Client) AgentHookReadiness(parent context.Context) (AgentHookReadiness, error) {
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return AgentHookReadiness{}, err
	}
	ctx, cancel := context.WithTimeout(parent, agentHookReadinessOverallTimeout)
	defer cancel()

	env := readinessProbeEnv(os.Environ())
	binding, err := c.probeAgentHookReadinessBinding(ctx, env)
	if err != nil {
		if parentErr := parent.Err(); parentErr != nil {
			return AgentHookReadiness{}, parentErr
		}
		return AgentHookReadiness{}, fmt.Errorf("%w: initial executable verification", ErrAgentHookReadinessBinding)
	}

	statuses := make([]AgentHookReadinessStatus, len(agentHookReadinessSpecs))
	var wait sync.WaitGroup
	for index, spec := range agentHookReadinessSpecs {
		index, spec := index, spec
		wait.Add(1)
		go func() {
			defer wait.Done()
			probeCtx, probeCancel := context.WithTimeout(ctx, agentHookReadinessProbeTimeout)
			defer probeCancel()
			statuses[index] = c.runAgentHookReadinessProbe(probeCtx, binding, spec, env)
		}()
	}
	wait.Wait()
	if parentErr := parent.Err(); parentErr != nil {
		return AgentHookReadiness{}, parentErr
	}
	if err := ctx.Err(); err != nil {
		return AgentHookReadiness{}, err
	}

	finalBinding, err := c.probeAgentHookReadinessBinding(ctx, env)
	if err != nil {
		if parentErr := parent.Err(); parentErr != nil {
			return AgentHookReadiness{}, parentErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return AgentHookReadiness{}, ctxErr
		}
		return AgentHookReadiness{}, fmt.Errorf("%w: final executable verification", ErrAgentHookReadinessBinding)
	}
	if finalBinding != binding {
		return AgentHookReadiness{}, ErrBindingChanged
	}

	agents := make([]AgentHookReadinessAgent, len(agentHookReadinessSpecs))
	for index, spec := range agentHookReadinessSpecs {
		agents[index] = AgentHookReadinessAgent{Agent: spec.agent, Status: string(statuses[index])}
	}
	return AgentHookReadiness{Version: agentHookReadinessVersion, Agents: agents}, nil
}

func (c *Client) runAgentHookReadinessProbe(ctx context.Context, binding GitHookBinding, spec agentHookReadinessSpec, env []string) AgentHookReadinessStatus {
	result, err := c.Runner.Run(ctx, proc.Command{
		Executable:  binding.Binary,
		Args:        []string{"agent-hook", "install", "--dry-run", "--agent", spec.agent, "--binary", binding.Binary},
		Env:         append([]string(nil), env...),
		Timeout:     agentHookReadinessProbeTimeout,
		OutputLimit: agentHookReadinessOutputLimit,
	})
	if err != nil || !result.Started || result.ExitCode != 0 || result.StdoutTruncated || result.StderrTruncated || result.Stderr != "" {
		return AgentHookReadinessUnavailable
	}
	status, ok := parseAgentHookReadinessOutput(result.Stdout, spec.display)
	if !ok {
		return AgentHookReadinessUnavailable
	}
	return status
}

func (c *Client) probeAgentHookReadinessBinding(ctx context.Context, env []string) (GitHookBinding, error) {
	binary, _, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return GitHookBinding{}, errors.New("RoboRev executable is unavailable")
	}
	abs, err := filepath.Abs(binary)
	if err != nil {
		return GitHookBinding{}, errors.New("RoboRev executable path is malformed")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		resolved = abs
	}
	if !sameExecutablePath(abs, resolved) {
		return GitHookBinding{}, errors.New("RoboRev executable path is not canonical")
	}
	binary = filepath.Clean(resolved)
	info, err := os.Lstat(binary)
	if err != nil || !safeExecutableInfo(info) {
		return GitHookBinding{}, errors.New("RoboRev executable is not a regular file")
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{
		Executable:  binary,
		Args:        []string{"version", "--json"},
		Env:         append([]string(nil), env...),
		Timeout:     agentHookReadinessProbeTimeout,
		OutputLimit: agentHookReadinessOutputLimit,
	})
	if runErr != nil || !result.Started || result.ExitCode != 0 || result.StdoutTruncated || result.StderrTruncated || result.Stderr != "" || !utf8.ValidString(result.Stdout) {
		return GitHookBinding{}, errors.New("RoboRev version probe failed")
	}
	var version struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &version); err != nil || roborevcompat.ValidateVersionIdentity(version.Name, version.Version) != nil || version.Version != agentHookReadinessVersion {
		return GitHookBinding{}, errors.New("unsupported RoboRev version")
	}
	return GitHookBinding{Binary: binary, Version: version.Version, Generation: generation, FileID: fmt.Sprintf("%d:%d:%o", info.Size(), info.ModTime().UnixNano(), info.Mode())}, nil
}

func parseAgentHookReadinessOutput(output, display string) (AgentHookReadinessStatus, bool) {
	status, _, ok := parseAgentHookInstallOutput(output, display)
	return status, ok
}

func parseAgentHookInstallOutput(output, display string) (AgentHookReadinessStatus, string, bool) {
	if !utf8.ValidString(output) || !strings.HasSuffix(output, "\n") {
		return "", "", false
	}
	body := strings.TrimSuffix(output, "\n")
	if strings.HasSuffix(body, "\r") {
		body = strings.TrimSuffix(body, "\r")
	}
	if body == "" || strings.ContainsAny(body, "\r\n") {
		return "", "", false
	}
	for _, expected := range []struct {
		prefix string
		status AgentHookReadinessStatus
	}{
		{prefix: "would update " + display + " agent hooks in ", status: AgentHookReadinessNeedsUpdate},
		{prefix: display + " agent hooks already installed in ", status: AgentHookReadinessCurrent},
	} {
		if !strings.HasPrefix(body, expected.prefix) {
			continue
		}
		path := strings.TrimPrefix(body, expected.prefix)
		if strings.TrimSpace(path) == "" || strings.TrimSpace(path) != path || strings.ContainsAny(path, "\r\n") {
			return "", "", false
		}
		return expected.status, path, true
	}
	return "", "", false
}

func agentHookReadinessSpecFor(agent string) (agentHookReadinessSpec, bool) {
	for _, spec := range agentHookReadinessSpecs {
		if spec.agent == agent {
			return spec, true
		}
	}
	return agentHookReadinessSpec{}, false
}

// readinessProbeEnv deliberately starts with the process package's platform
// runtime allowlist and adds only the five upstream v0.64 location variables
// needed to locate per-agent config. It is never built from an arbitrary
// environment map and intentionally excludes Git routing and credentials.
func readinessProbeEnv(source []string) []string {
	env := proc.MinimalProbeEnv(source)
	seen := make(map[string]struct{}, len(env))
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok {
			seen[strings.ToUpper(name)] = struct{}{}
		}
	}
	for _, entry := range source {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || !readinessLocationEnv(name) {
			continue
		}
		key := strings.ToUpper(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		env = append(env, entry)
	}
	return env
}

func readinessLocationEnv(name string) bool {
	switch strings.ToUpper(name) {
	case "CODEX_HOME", "CLAUDE_CONFIG_DIR", "GROK_HOME", "HOME", "USERPROFILE":
		return true
	default:
		return false
	}
}
