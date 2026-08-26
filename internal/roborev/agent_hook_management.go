package roborev

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

const agentHookInstallTimeout = 30 * time.Second

func (c *Client) ProbeAgentHookBinding(ctx context.Context) (GitHookBinding, error) {
	return c.probeAgentHookReadinessBinding(ctx, readinessProbeEnvFromProcess())
}

func (c *Client) AgentHookDryRun(ctx context.Context, agent string, binding GitHookBinding) (AgentHookDryRun, error) {
	spec, ok := agentHookReadinessSpecFor(agent)
	if !ok || binding.Version != agentHookReadinessVersion || strings.TrimSpace(binding.Binary) == "" {
		return AgentHookDryRun{}, errors.New("agent hook dry-run is unavailable")
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{
		Executable:  binding.Binary,
		Args:        []string{"agent-hook", "install", "--dry-run", "--agent", spec.agent, "--binary", binding.Binary},
		Env:         readinessProbeEnvFromProcess(),
		Timeout:     agentHookReadinessProbeTimeout,
		OutputLimit: agentHookReadinessOutputLimit,
	})
	if runErr != nil || !result.Started || result.ExitCode != 0 || result.StdoutTruncated || result.StderrTruncated || result.Stderr != "" || !utf8.ValidString(result.Stdout) {
		return AgentHookDryRun{}, errors.New("agent hook dry-run failed")
	}
	status, path, ok := parseAgentHookInstallOutput(result.Stdout, spec.display)
	if !ok {
		return AgentHookDryRun{}, errors.New("agent hook dry-run output is invalid")
	}
	return AgentHookDryRun{Agent: spec.agent, Status: status, ConfigPath: path}, nil
}

func (c *Client) RunAgentHookInstall(ctx context.Context, agent string, binding GitHookBinding) (GitHookExecution, error) {
	spec, ok := agentHookReadinessSpecFor(agent)
	if !ok || binding.Version != agentHookReadinessVersion || strings.TrimSpace(binding.Binary) == "" {
		return GitHookExecution{}, errors.New("agent hook install is unavailable")
	}
	freshBinding, err := c.ProbeAgentHookBinding(ctx)
	if err != nil || freshBinding != binding {
		return GitHookExecution{}, ErrBindingChanged
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{
		Executable:  binding.Binary,
		Args:        []string{"agent-hook", "install", "--agent", spec.agent, "--binary", binding.Binary},
		Env:         readinessProbeEnvFromProcess(),
		Timeout:     agentHookInstallTimeout,
		OutputLimit: agentHookReadinessOutputLimit,
	})
	execution := GitHookExecution{Started: result.Started, ExitCode: result.ExitCode}
	if runErr != nil {
		return execution, errors.New("agent hook install failed")
	}
	return execution, nil
}

func readinessProbeEnvFromProcess() []string { return readinessProbeEnv(os.Environ()) }
