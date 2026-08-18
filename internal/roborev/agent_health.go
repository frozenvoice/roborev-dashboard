package roborev

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

// AgentDescriptor is the small, version-gated description of a built-in
// RoboRev v0.64 agent that the dashboard is allowed to expose.  It is
// deliberately separate from RoboRev's runtime Agent interface: the
// dashboard must never turn arbitrary config text into an executable command.
type AgentDescriptor struct {
	Name           string   `json:"name"`
	Aliases        []string `json:"aliases,omitempty"`
	DefaultCommand string   `json:"defaultCommand"`
	CommandKey     string   `json:"commandKey,omitempty"`
}

// v064AgentDescriptors is the single allowlist used by discovery, config
// projection and check-agents health validation.  ACP and test agents are
// intentionally absent: their command/argument shape is user supplied.
var v064AgentDescriptors = []AgentDescriptor{
	{Name: "codex", DefaultCommand: "codex", CommandKey: "codex_cmd"},
	{Name: "claude-code", Aliases: []string{"claude"}, DefaultCommand: "claude", CommandKey: "claude_code_cmd"},
	{Name: "gemini", DefaultCommand: "gemini", CommandKey: "gemini_cmd"},
	{Name: "copilot", DefaultCommand: "copilot"},
	{Name: "opencode", DefaultCommand: "opencode", CommandKey: "opencode_cmd"},
	{Name: "cursor", Aliases: []string{"agent"}, DefaultCommand: "agent", CommandKey: "cursor_cmd"},
	{Name: "kiro", DefaultCommand: "kiro-cli"},
	{Name: "kilo", DefaultCommand: "kilo"},
	{Name: "droid", DefaultCommand: "droid"},
	{Name: "pi", DefaultCommand: "pi", CommandKey: "pi_cmd"},
	{Name: "grok", Aliases: []string{"grok-build"}, DefaultCommand: "grok", CommandKey: "grok_cmd"},
}

// AgentDescriptors returns a defensive copy of the supported v0.64
// descriptors.  Callers may sort or modify it without changing the allowlist.
func AgentDescriptors() []AgentDescriptor {
	result := make([]AgentDescriptor, len(v064AgentDescriptors))
	for i, descriptor := range v064AgentDescriptors {
		result[i] = descriptor
		result[i].Aliases = append([]string(nil), descriptor.Aliases...)
	}
	return result
}

// CanonicalAgentName resolves one of the v0.64 built-in names or aliases.  A
// false result means the name is not safe for detection or health checks.
func CanonicalAgentName(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, descriptor := range v064AgentDescriptors {
		if name == descriptor.Name {
			return descriptor.Name, true
		}
		for _, alias := range descriptor.Aliases {
			if name == alias {
				return descriptor.Name, true
			}
		}
	}
	return "", false
}

// AgentHealthStatus is the deliberately small result vocabulary emitted by
// the v0.64 check-agents adapter.
type AgentHealthStatus string

const (
	AgentHealthPassed   AgentHealthStatus = "passed"
	AgentHealthFailed   AgentHealthStatus = "failed"
	AgentHealthSkipped  AgentHealthStatus = "skipped"
	AgentHealthCanceled AgentHealthStatus = "canceled"
)

type AgentHealthResult struct {
	Agent  string            `json:"agent"`
	Status AgentHealthStatus `json:"status"`
}

var agentHealthSummaryRE = regexp.MustCompile(`^([0-9]+) passed, ([0-9]+) failed, ([0-9]+) skipped$`)
var agentHealthRowRE = regexp.MustCompile(`^([?-])\s+([A-Za-z0-9][A-Za-z0-9_-]*)(?:\s|$)(.*)$`)
var agentHealthPassRE = regexp.MustCompile(`\.\.\.\s+OK\s+\([0-9]+\s+bytes\)$`)

const agentHealthOuterTimeout = 65 * time.Second

// ParseAgentHealthOutput parses only the exact summary emitted by RoboRev
// v0.64 check-agents.  The parser intentionally does not return command text:
// stderr may contain credentials or provider diagnostics.
func ParseAgentHealthOutput(agent, output string) (AgentHealthResult, error) {
	canonical, ok := CanonicalAgentName(agent)
	if !ok {
		return AgentHealthResult{}, errors.New("agent is not allowed")
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	var summary string
	var rowStatus AgentHealthStatus
	var rowSeen, rowResolved bool
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if rowSeen && rowResolved && rowStatus == AgentHealthFailed && strings.HasPrefix(line, "-") {
			// Failure diagnostics are indented by v0.64 before trimming. A
			// provider diagnostic may itself begin with '-'; it is not a
			// second result row once the selected row is already failed.
			continue
		}
		if row := agentHealthRowRE.FindStringSubmatch(line); row != nil {
			if rowSeen {
				return AgentHealthResult{}, errors.New("health output contains multiple agent rows")
			}
			rowSeen = true
			if row[2] != canonical {
				return AgentHealthResult{}, errors.New("health output agent does not match request")
			}
			switch row[1] {
			case "-":
				rowStatus, rowResolved = AgentHealthSkipped, true
			case "?":
				if agentHealthPassRE.MatchString(line) {
					rowStatus, rowResolved = AgentHealthPassed, true
				} else if strings.HasSuffix(strings.TrimSpace(row[3]), "FAIL") {
					rowStatus, rowResolved = AgentHealthFailed, true
				}
			}
			continue
		}
		if agentHealthSummaryRE.MatchString(line) {
			if summary != "" {
				return AgentHealthResult{}, errors.New("health output contains multiple summaries")
			}
			summary = line
		}
	}
	if summary == "" {
		return AgentHealthResult{}, errors.New("health output has no v0.64 summary")
	}
	if !rowSeen || !rowResolved {
		return AgentHealthResult{}, errors.New("health output has no exact agent result row")
	}
	matches := agentHealthSummaryRE.FindStringSubmatch(summary)
	passed, _ := strconv.Atoi(matches[1])
	failed, _ := strconv.Atoi(matches[2])
	skipped, _ := strconv.Atoi(matches[3])
	if passed+failed+skipped != 1 {
		return AgentHealthResult{}, errors.New("health summary is not for exactly one agent")
	}
	if (rowStatus == AgentHealthPassed && (passed != 1 || failed != 0 || skipped != 0)) ||
		(rowStatus == AgentHealthFailed && (passed != 0 || failed != 1 || skipped != 0)) ||
		(rowStatus == AgentHealthSkipped && (passed != 0 || failed != 0 || skipped != 1)) {
		return AgentHealthResult{}, errors.New("health row and summary disagree")
	}
	return AgentHealthResult{Agent: canonical, Status: rowStatus}, nil
}

// CheckAgent runs the user-requested, manual v0.64 agent smoke test.  It is
// intentionally not called by discovery: discovery must remain zero-quota.
func (c *Client) CheckAgent(ctx context.Context, agent string) (AgentHealthResult, error) {
	return c.checkAgent(ctx, "", agent)
}

// CheckAgentInRepo runs the same explicit health check with the selected
// repository as the process working directory. The repository path must come
// from the app's registered-repository resolver; this adapter accepts no
// browser-supplied command fields or arguments.
func (c *Client) CheckAgentInRepo(ctx context.Context, repoPath, agent string) (AgentHealthResult, error) {
	if strings.TrimSpace(repoPath) == "" {
		return AgentHealthResult{}, errors.New("repository path is required")
	}
	return c.checkAgent(ctx, repoPath, agent)
}

// CheckAgentForRepository is a descriptive alias for callers that prefer the
// repository-qualified method name.
func (c *Client) CheckAgentForRepository(ctx context.Context, repoPath, agent string) (AgentHealthResult, error) {
	return c.CheckAgentInRepo(ctx, repoPath, agent)
}

func (c *Client) checkAgent(ctx context.Context, repoPath, agent string) (AgentHealthResult, error) {
	canonical, ok := CanonicalAgentName(agent)
	if !ok {
		return AgentHealthResult{}, errors.New("agent is not allowed")
	}
	binary, _, _ := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return AgentHealthResult{}, errors.New("RoboRev executable is not bound")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	command := procCommandForAgentHealth(binary, canonical)
	command.Dir = repoPath
	result, runErr := c.Runner.Run(ctx, command)
	if result.StdoutTruncated || result.StderrTruncated {
		return AgentHealthResult{}, errors.New("agent health output exceeded the maximum size")
	}
	if runErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) ||
			errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return AgentHealthResult{Agent: canonical, Status: AgentHealthCanceled}, nil
		}
		// v0.64 returns a non-zero exit code when one or more agents fail, but
		// still emits the exact summary. Parse it before treating the process
		// error as a transport failure.
		parsed, parseErr := ParseAgentHealthOutput(canonical, result.Stdout)
		if parseErr == nil {
			return parsed, nil
		}
		return AgentHealthResult{}, errors.New("agent health command failed")
	}
	return ParseAgentHealthOutput(canonical, result.Stdout)
}

// procCommandForAgentHealth is kept as a helper so tests can assert the exact
// argv and limits without executing RoboRev.
func procCommandForAgentHealth(binary, canonical string) proc.Command {
	return proc.Command{
		Executable:  binary,
		Args:        []string{"check-agents", "--agent", canonical, "--timeout", "60"},
		Timeout:     agentHealthOuterTimeout,
		OutputLimit: 64 << 10,
	}
}
