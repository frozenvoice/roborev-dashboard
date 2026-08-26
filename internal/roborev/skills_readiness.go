package roborev

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

const (
	skillsReadinessVersion        = "v0.64.0"
	skillsReadinessProbeTimeout   = 5 * time.Second
	skillsReadinessOverallTimeout = 20 * time.Second
	skillsReadinessOutputLimit    = 256 << 10
	skillsReadinessTotal          = 10
	skillsReadinessGuidance       = "Run 'roborev skills install' to install or update."
)

var (
	ErrSkillsReadinessBinding         = errors.New("skills readiness binding is unavailable")
	ErrSkillsReadinessUnavailable     = errors.New("skills readiness is unavailable")
	ErrMalformedSkillsReadinessOutput = errors.New("malformed RoboRev skills output")
)

// skillsReadinessCatalog is the exact v0.64.0 embedded catalog audited from
// internal/skills. All ten catalog entries are present for each supported
// agent; the parser nevertheless keeps the membership and invocation details
// in one allowlist rather than accepting arbitrary roborev-* names.
var skillsReadinessCatalog = [...]string{
	"roborev-design-review",
	"roborev-design-review-branch",
	"roborev-fix",
	"roborev-lookahead-review",
	"roborev-lookahead-review-branch",
	"roborev-refine",
	"roborev-respond",
	"roborev-review",
	"roborev-review-branch",
	"roborev-snooze",
}

type skillsReadinessOutputAgent struct {
	agent  string
	label  string
	prefix string
}

// v0.64's human formatter emits rows in this order. The API intentionally
// returns the dashboard-wide agent order separately below.
var skillsReadinessOutputAgents = [...]skillsReadinessOutputAgent{
	{agent: "claude", label: "Claude Code", prefix: "/"},
	{agent: "codex", label: "Codex", prefix: "$"},
	{agent: "droid", label: "Factory Droid", prefix: "/"},
	{agent: "grok", label: "Grok Build", prefix: "/"},
}

var skillsReadinessDashboardAgents = [...]string{"codex", "claude", "grok", "droid"}

type skillsReadinessCounts struct {
	current  int
	outdated int
	missing  int
	noAgent  int
}

// SkillsReadiness performs the only production read transport for Skills
// Readiness v1: a fresh version/binding probe, one exact `skills` process, and
// a fresh binding/version probe. It never calls skills install or update.
func (c *Client) SkillsReadiness(parent context.Context) (SkillsReadiness, error) {
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return SkillsReadiness{}, err
	}
	ctx, cancel := context.WithTimeout(parent, skillsReadinessOverallTimeout)
	defer cancel()

	env := readinessProbeEnv(os.Environ())
	binding, err := c.probeAgentHookReadinessBinding(ctx, env)
	if err != nil {
		if parentErr := parent.Err(); parentErr != nil {
			return SkillsReadiness{}, parentErr
		}
		return SkillsReadiness{}, fmt.Errorf("%w: initial executable verification", ErrSkillsReadinessBinding)
	}

	result, runErr := c.Runner.Run(ctx, proc.Command{
		Executable:  binding.Binary,
		Args:        []string{"skills"},
		Dir:         "",
		Env:         append([]string(nil), env...),
		Timeout:     skillsReadinessProbeTimeout,
		OutputLimit: skillsReadinessOutputLimit,
	})
	if runErr != nil || !result.Started || result.ExitCode != 0 || result.StdoutTruncated || result.StderrTruncated || result.Stderr != "" || !utf8.ValidString(result.Stdout) {
		if parentErr := parent.Err(); parentErr != nil {
			return SkillsReadiness{}, parentErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return SkillsReadiness{}, ctxErr
		}
		if errors.Is(runErr, context.DeadlineExceeded) {
			return SkillsReadiness{}, context.DeadlineExceeded
		}
		if errors.Is(runErr, context.Canceled) {
			return SkillsReadiness{}, context.Canceled
		}
		return SkillsReadiness{}, ErrSkillsReadinessUnavailable
	}
	counts, err := parseSkillsReadinessOutput(result.Stdout)
	if err != nil {
		return SkillsReadiness{}, err
	}

	finalBinding, err := c.probeAgentHookReadinessBinding(ctx, env)
	if err != nil {
		if parentErr := parent.Err(); parentErr != nil {
			return SkillsReadiness{}, parentErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return SkillsReadiness{}, ctxErr
		}
		return SkillsReadiness{}, fmt.Errorf("%w: final executable verification", ErrSkillsReadinessBinding)
	}
	if finalBinding != binding {
		return SkillsReadiness{}, ErrBindingChanged
	}

	readiness := skillsReadinessFromCounts(counts)
	if err := ValidateSkillsReadiness(readiness); err != nil {
		return SkillsReadiness{}, fmt.Errorf("%w: %v", ErrMalformedSkillsReadinessOutput, err)
	}
	return readiness, nil
}

func skillsReadinessFromCounts(counts map[string]skillsReadinessCounts) SkillsReadiness {
	readiness := SkillsReadiness{Version: skillsReadinessVersion, Agents: make([]SkillsReadinessAgent, 0, len(skillsReadinessDashboardAgents))}
	for _, agent := range skillsReadinessDashboardAgents {
		count := counts[agent]
		status := SkillsReadinessNeedsInstallUpdate
		current, outdated, missing := count.current, count.outdated, count.missing
		if count.noAgent == skillsReadinessTotal {
			status = SkillsReadinessConfigUnavailable
			current, outdated, missing = 0, 0, 0
		} else if outdated == 0 && missing == 0 && current == skillsReadinessTotal {
			status = SkillsReadinessCurrent
		}
		readiness.Agents = append(readiness.Agents, SkillsReadinessAgent{
			Agent: agent, Status: string(status), Current: current, Outdated: outdated, Missing: missing, Total: skillsReadinessTotal,
		})
	}
	return readiness
}

// ValidateSkillsReadiness is the typed DTO invariant gate used by the HTTP
// handler before serialization and by the production adapter before return.
func ValidateSkillsReadiness(readiness SkillsReadiness) error {
	if readiness.Version != skillsReadinessVersion {
		return errors.New("unsupported skills readiness version")
	}
	if len(readiness.Agents) != len(skillsReadinessDashboardAgents) {
		return errors.New("skills readiness must contain exactly four agents")
	}
	for index, expectedAgent := range skillsReadinessDashboardAgents {
		agent := readiness.Agents[index]
		if agent.Agent != expectedAgent {
			return errors.New("skills readiness agents are out of order or unknown")
		}
		if agent.Total != skillsReadinessTotal || agent.Current < 0 || agent.Outdated < 0 || agent.Missing < 0 || agent.Current > agent.Total || agent.Outdated > agent.Total || agent.Missing > agent.Total {
			return errors.New("skills readiness counts are invalid")
		}
		switch SkillsReadinessStatus(agent.Status) {
		case SkillsReadinessCurrent:
			if agent.Current != agent.Total || agent.Outdated != 0 || agent.Missing != 0 {
				return errors.New("current skills readiness counts are inconsistent")
			}
		case SkillsReadinessNeedsInstallUpdate:
			if int64(agent.Current)+int64(agent.Outdated)+int64(agent.Missing) != int64(agent.Total) || agent.Outdated+agent.Missing <= 0 {
				return errors.New("needs-install-update counts are inconsistent")
			}
		case SkillsReadinessConfigUnavailable:
			if agent.Current != 0 || agent.Outdated != 0 || agent.Missing != 0 || agent.Total <= 0 {
				return errors.New("config-unavailable counts are inconsistent")
			}
		default:
			return errors.New("unknown skills readiness status")
		}
	}
	return nil
}

func parseSkillsReadinessOutput(output string) (map[string]skillsReadinessCounts, error) {
	if !utf8.ValidString(output) || !strings.HasSuffix(output, "\n") {
		return nil, fmt.Errorf("%w: output is not valid UTF-8 or is truncated", ErrMalformedSkillsReadinessOutput)
	}
	normalized := strings.ReplaceAll(output, "\r\n", "\n")
	if strings.Contains(normalized, "\r") {
		return nil, fmt.Errorf("%w: unexpected carriage return", ErrMalformedSkillsReadinessOutput)
	}
	lines := strings.Split(normalized, "\n")
	lines = lines[:len(lines)-1]
	if len(lines) < 1 || lines[0] != "Skills:" {
		return nil, fmt.Errorf("%w: missing Skills header", ErrMalformedSkillsReadinessOutput)
	}
	if len(lines) < 2 || lines[1] != "" {
		return nil, fmt.Errorf("%w: missing header separator", ErrMalformedSkillsReadinessOutput)
	}

	counts := map[string]skillsReadinessCounts{}
	for _, agent := range skillsReadinessDashboardAgents {
		counts[agent] = skillsReadinessCounts{}
	}
	lineIndex := 2
	for skillIndex, skillName := range skillsReadinessCatalog {
		if lineIndex >= len(lines) || lines[lineIndex] != "  "+skillName {
			return nil, fmt.Errorf("%w: unexpected or missing skill %q", ErrMalformedSkillsReadinessOutput, skillName)
		}
		lineIndex++
		if lineIndex >= len(lines) || !validSkillsDescriptionLine(lines[lineIndex]) {
			return nil, fmt.Errorf("%w: missing description for %q", ErrMalformedSkillsReadinessOutput, skillName)
		}
		lineIndex++

		for _, agent := range skillsReadinessOutputAgents {
			if lineIndex >= len(lines) {
				return nil, fmt.Errorf("%w: truncated agent rows for %q", ErrMalformedSkillsReadinessOutput, skillName)
			}
			badge, ok := parseSkillsReadinessRow(lines[lineIndex], agent, skillName)
			if !ok {
				return nil, fmt.Errorf("%w: invalid %s row for %q", ErrMalformedSkillsReadinessOutput, agent.label, skillName)
			}
			lineIndex++
			current := counts[agent.agent]
			switch badge {
			case "installed":
				current.current++
			case "outdated":
				current.outdated++
			case "not installed":
				current.missing++
			case "no agent":
				current.noAgent++
			default:
				return nil, fmt.Errorf("%w: unknown badge", ErrMalformedSkillsReadinessOutput)
			}
			counts[agent.agent] = current
		}
		if skillIndex < len(skillsReadinessCatalog)-1 {
			if lineIndex >= len(lines) || lines[lineIndex] != "" {
				return nil, fmt.Errorf("%w: missing skill separator", ErrMalformedSkillsReadinessOutput)
			}
			lineIndex++
		}
	}

	if lineIndex < len(lines) {
		if lines[lineIndex] != "" {
			return nil, fmt.Errorf("%w: unexpected trailing output", ErrMalformedSkillsReadinessOutput)
		}
		lineIndex++
		if lineIndex >= len(lines) || lines[lineIndex] != skillsReadinessGuidance {
			return nil, fmt.Errorf("%w: unexpected trailing output", ErrMalformedSkillsReadinessOutput)
		}
		lineIndex++
	}
	if lineIndex != len(lines) {
		return nil, fmt.Errorf("%w: unexpected trailing output", ErrMalformedSkillsReadinessOutput)
	}

	for _, agent := range skillsReadinessOutputAgents {
		count := counts[agent.agent]
		if count.noAgent != 0 && count.noAgent != skillsReadinessTotal {
			return nil, fmt.Errorf("%w: no-agent rows are mixed with installed rows", ErrMalformedSkillsReadinessOutput)
		}
	}
	return counts, nil
}

func validSkillsDescriptionLine(line string) bool {
	if len(line) < 3 || line[:2] != "  " || line[2] == ' ' {
		return false
	}
	description := line[2:]
	if description == "" || description != strings.TrimSpace(description) {
		return false
	}
	for _, char := range description {
		if unicode.IsControl(char) {
			return false
		}
	}
	if description == "" || strings.HasPrefix(description, "Run 'roborev skills") {
		return false
	}
	for _, badge := range []string{"installed", "outdated", "not installed", "no agent"} {
		if description == badge {
			return false
		}
	}
	for _, agent := range skillsReadinessOutputAgents {
		if strings.HasPrefix(description, agent.label+" (") {
			return false
		}
	}
	return true
}

func parseSkillsReadinessRow(line string, agent skillsReadinessOutputAgent, skillName string) (string, bool) {
	for _, badge := range []string{"installed", "outdated", "not installed", "no agent"} {
		expected := fmt.Sprintf("    %s %-12s  %s%s", agent.label, "("+badge+")", agent.prefix, skillName)
		if line == expected {
			return badge, true
		}
	}
	return "", false
}
