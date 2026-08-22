package roborev

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

// ConfigScope selects the RoboRev configuration file a command targets.
type ConfigScope string

const (
	ConfigScopeGlobal ConfigScope = "global"
	ConfigScopeLocal  ConfigScope = "local"
)

const (
	configListTimeout = 10 * time.Second
	configGetTimeout  = 10 * time.Second
	configSetTimeout  = 15 * time.Second
	// The merged effective list is a human-readable table for every known
	// key. Real v0.64 output is a few KiB; 256 KiB is a conservative bound.
	configListOutputLimit = 256 << 10
	configValueLimit      = 8 << 10
)

// ConfigRow is one parsed `config list --show-origin` line. Value keeps the
// upstream text verbatim: sensitive values arrive already masked by RoboRev
// and are never unmasked or re-fetched by the Dashboard.
type ConfigRow struct {
	Origin string `json:"origin"`
	Key    string `json:"key"`
	Value  string `json:"value"`
}

// ConfigService is the optional typed CLI surface for bounded configuration
// introspection and allowlisted mutation. It is deliberately separate from
// Service so existing transports that do not expose it keep compiling.
type ConfigService interface {
	ConfigList(ctx context.Context, repoPath string) ([]ConfigRow, error)
	ConfigGet(ctx context.Context, repoPath string, key string, scope ConfigScope) (string, error)
	ConfigSet(ctx context.Context, repoPath string, key string, value string, scope ConfigScope) error
}

var configKeyPattern = regexp.MustCompile(`^[a-z0-9_]+$`)
var configOriginPattern = regexp.MustCompile(`^(global|local|default)$`)

func validateConfigKey(key string) error {
	if !configKeyPattern.MatchString(key) || len(key) > 128 {
		return fmt.Errorf("invalid config key %q", key)
	}
	return nil
}

func validateConfigScope(scope ConfigScope) error {
	if scope != ConfigScopeGlobal && scope != ConfigScopeLocal {
		return fmt.Errorf("invalid config scope %q", scope)
	}
	return nil
}

// ConfigListArgs builds the exact argv for the merged origin list.
func ConfigListArgs() []string {
	return []string{"config", "list", "--show-origin"}
}

// ConfigGetArgs builds the exact argv for an explicit-scope get. Upstream
// errors when the key is not set in the requested scope file.
func ConfigGetArgs(key string, scope ConfigScope) ([]string, error) {
	if err := validateConfigKey(key); err != nil {
		return nil, err
	}
	if err := validateConfigScope(scope); err != nil {
		return nil, err
	}
	return []string{"config", "get", key, "--" + string(scope)}, nil
}

// ConfigSetArgs builds the exact argv for an explicit-scope set.
func ConfigSetArgs(key string, value string, scope ConfigScope) ([]string, error) {
	if err := validateConfigKey(key); err != nil {
		return nil, err
	}
	if err := validateConfigScope(scope); err != nil {
		return nil, err
	}
	if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
		return nil, errors.New("invalid config value")
	}
	return []string{"config", "set", key, value, "--" + string(scope)}, nil
}

// ParseConfigList parses the human-readable tabwriter output of
// `roborev config list --show-origin`. Each line is
// `<origin><pad><key><pad><value>` where <pad> is at least one space emitted
// by upstream's tabwriter. The first two columns are split off; the entire
// remainder is preserved as the value so embedded spaces survive parsing.
// Parsing fails closed on unknown origins, duplicate keys, malformed rows,
// control characters, and oversized rows.
func ParseConfigList(output string) ([]ConfigRow, error) {
	const (
		maxRows    = 512
		maxLineLen = 4096
	)
	rows := make([]ConfigRow, 0, 64)
	seen := make(map[string]bool, 64)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) > maxLineLen {
			return nil, fmt.Errorf("config list row exceeds %d bytes", maxLineLen)
		}
		for _, r := range line {
			if r < 0x20 || r == 0x7f {
				return nil, fmt.Errorf("config list row contains control character %q", r)
			}
		}
		origin, rest, found := cutConfigColumn(line)
		if !found || !configOriginPattern.MatchString(origin) {
			return nil, fmt.Errorf("malformed config list row: unknown or missing origin in %q", truncateConfigSnippet(line))
		}
		key, valueRest, found := cutConfigColumn(rest)
		if !found || !configKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("malformed config list row: invalid key in %q", truncateConfigSnippet(line))
		}
		value := strings.TrimRight(valueRest, " \t")
		if seen[key] {
			return nil, fmt.Errorf("duplicate config key %q", key)
		}
		seen[key] = true
		rows = append(rows, ConfigRow{Origin: origin, Key: key, Value: value})
		if len(rows) > maxRows {
			return nil, fmt.Errorf("config list exceeds %d rows", maxRows)
		}
	}
	return rows, nil
}

func truncateConfigSnippet(line string) string {
	if len(line) <= 96 {
		return line
	}
	return line[:96] + "..."
}

// cutConfigColumn splits off the first whitespace-delimited column and returns
// the remainder with the separating whitespace run removed exactly once so a
// value's own internal spacing stays intact.
func cutConfigColumn(line string) (column, rest string, found bool) {
	end := 0
	for end < len(line) && line[end] != ' ' && line[end] != '\t' {
		end++
	}
	if end == 0 {
		return "", "", false
	}
	column = line[:end]
	i := end
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return column, line[i:], true
}

func (c *Client) requireConfigCapability(ctx context.Context) error {
	version, err := c.detectVersion(ctx)
	if err != nil {
		return err
	}
	caps := capabilities(version)
	if !caps.ConfigIntrospection || !caps.ConfigMutation {
		return fmt.Errorf("RoboRev %s does not support the bounded configuration contract", version)
	}
	return nil
}

// ConfigList returns the merged effective configuration with origins for one
// registered repository. The repository path is used as the process working
// directory only; upstream resolves the global file independently, so this
// works while the daemon is offline.
func (c *Client) ConfigList(ctx context.Context, repoPath string) ([]ConfigRow, error) {
	if err := c.requireConfigCapability(ctx); err != nil {
		return nil, err
	}
	result, err := c.runInDir(ctx, repoPath, ConfigListArgs(), configListTimeout, configListOutputLimit)
	if err != nil {
		return nil, fmt.Errorf("config list: %w", err)
	}
	rows, err := ParseConfigList(result.Stdout)
	if err != nil {
		return nil, fmt.Errorf("config list: %w", err)
	}
	return rows, nil
}

// ConfigGet reads one key from an explicit scope file. Upstream fails when the
// key is absent from that scope; the Dashboard treats that as an empty value.
func (c *Client) ConfigGet(ctx context.Context, repoPath string, key string, scope ConfigScope) (string, error) {
	if err := c.requireConfigCapability(ctx); err != nil {
		return "", err
	}
	args, err := ConfigGetArgs(key, scope)
	if err != nil {
		return "", err
	}
	result, err := c.runInDir(ctx, repoPath, args, configGetTimeout, configValueLimit)
	if err != nil {
		// Upstream fails with `key "<key>" is not set in global|local config`
		// when the requested scope file does not carry an explicit value.
		if strings.Contains(result.Stderr, "is not set in") {
			return "", nil
		}
		return "", fmt.Errorf("config get %s: %w", key, err)
	}
	if result.StdoutTruncated {
		return "", fmt.Errorf("config get %s: response exceeded bounded output", key)
	}
	value := strings.TrimRight(result.Stdout, "\r\n")
	if len(strings.Fields(value)) > 1 {
		return "", fmt.Errorf("config get %s: unexpected multi-token response", key)
	}
	return value, nil
}

// ConfigSet writes one allowlisted key through the official CLI. Scope and
// type validation belong to the caller; the adapter only enforces argv shape.
func (c *Client) ConfigSet(ctx context.Context, repoPath string, key string, value string, scope ConfigScope) error {
	if err := c.requireConfigCapability(ctx); err != nil {
		return err
	}
	args, err := ConfigSetArgs(key, value, scope)
	if err != nil {
		return err
	}
	if _, err := c.runInDir(ctx, repoPath, args, configSetTimeout, configValueLimit); err != nil {
		return fmt.Errorf("config set %s: %w", key, err)
	}
	return nil
}

// runInDir executes a bounded RoboRev command inside a registered repository.
func (c *Client) runInDir(ctx context.Context, repoPath string, args []string, timeout time.Duration, limit int) (proc.Result, error) {
	binary, _, _ := c.snapshot()
	if binary == "" {
		return proc.Result{}, errors.New("roborev is not installed")
	}
	command := proc.Command{Executable: binary, Args: args, Dir: repoPath, Timeout: timeout, OutputLimit: limit}
	result, err := c.Runner.Run(ctx, command)
	if err != nil {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = err.Error()
		}
		return result, fmt.Errorf("%s: %w", message, err)
	}
	return result, nil
}
