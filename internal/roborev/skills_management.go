package roborev

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/skillsmanagement"
)

const skillsInstallTimeout = 30 * time.Second

// ProbeSkillsBinding deliberately shares the exact uncached v0.64 executable
// probe used by Agent Hook Management. A preview token therefore binds the
// executable path, version, generation, and file identity before launch.
func (c *Client) ProbeSkillsBinding(ctx context.Context) (GitHookBinding, error) {
	return c.ProbeAgentHookBinding(ctx)
}

// SkillsTargetEnvFromProcess returns the same tightly filtered location
// environment used by the read-only skills probe. It is exported only so the
// Dashboard server can derive the exact v0.64 target without accepting a
// browser-supplied path.
func SkillsTargetEnvFromProcess() []string { return readinessProbeEnvFromProcess() }

// RunSkillsInstall launches exactly one custom-path, single-agent v0.64
// install. The destination is already fixed by the server-derived --path;
// no working directory, shell, inherited environment, broad install, update,
// retry, rollback, or detached child is permitted here.
func (c *Client) RunSkillsInstall(ctx context.Context, agent, skillsDir string, expected GitHookBinding) (GitHookExecution, error) {
	if !skillsmanagement.IsSupportedAgent(agent) || expected.Version != skillsmanagement.Version || strings.TrimSpace(expected.Binary) == "" || expected.Generation == 0 || strings.TrimSpace(expected.FileID) == "" {
		return GitHookExecution{}, errors.New("skills install is unavailable")
	}
	args, err := SkillsInstallArgs(agent, skillsDir)
	if err != nil {
		return GitHookExecution{}, err
	}
	fresh, err := c.ProbeSkillsBinding(ctx)
	if err != nil || fresh != expected {
		return GitHookExecution{}, ErrBindingChanged
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{
		Executable:  expected.Binary,
		Args:        args,
		Dir:         "",
		Env:         proc.MinimalProbeEnv(os.Environ()),
		Timeout:     skillsInstallTimeout,
		OutputLimit: 1 << 20,
	})
	execution := GitHookExecution{Started: result.Started, ExitCode: result.ExitCode}
	if runErr != nil {
		return execution, errors.New("RoboRev skills install failed")
	}
	return execution, nil
}
