package roborev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

// ConfigScope selects the RoboRev configuration file a command targets.
type ConfigScope string

const (
	ConfigScopeGlobal ConfigScope = "global"
	ConfigScopeLocal  ConfigScope = "local"
)

var (
	errNoLocalConfig     = errors.New("no local config (.roborev.toml) found")
	errSnapshotDirNotSet = errors.New(`key "snapshot_dir" is not set in local config`)
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
}

// v0.64 flattens nested TOML tables into lowercase dot-separated keys (for
// example, cost.timeout and agent_hook.turn_threshold). Keep each segment
// strict so malformed separators and shell-like key text fail closed.
var configKeyPattern = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*$`)
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

func (c *Client) requireConfigIntrospection(ctx context.Context) error {
	version, err := c.detectVersion(ctx)
	if err != nil {
		return err
	}
	if !capabilities(version).ConfigIntrospection {
		return fmt.Errorf("RoboRev %s does not support configuration introspection", version)
	}
	return nil
}

// ConfigList returns the merged effective configuration with origins for one
// registered repository. The repository path is used as the process working
// directory only; upstream resolves the global file independently, so this
// works while the daemon is offline.
func (c *Client) ConfigList(ctx context.Context, repoPath string) ([]ConfigRow, error) {
	if err := c.requireConfigIntrospection(ctx); err != nil {
		return nil, err
	}
	result, err := c.runInDir(ctx, repoPath, ConfigListArgs(), configListTimeout, configListOutputLimit)
	if err != nil {
		return nil, fmt.Errorf("config list: %w", err)
	}
	if result.ExitCode != 0 || result.Stderr != "" || result.StdoutTruncated || result.StderrTruncated || !utf8.ValidString(result.Stdout) || !utf8.ValidString(result.Stderr) {
		return nil, errors.New("config list: response unavailable")
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
	if err := c.requireConfigIntrospection(ctx); err != nil {
		return "", err
	}
	args, err := ConfigGetArgs(key, scope)
	if err != nil {
		return "", err
	}
	result, err := c.runInDir(ctx, repoPath, args, configGetTimeout, configValueLimit)
	if result.StdoutTruncated || result.StderrTruncated || !utf8.ValidString(result.Stdout) || !utf8.ValidString(result.Stderr) {
		return "", fmt.Errorf("config get %s: response exceeded bounded output or was not valid UTF-8", key)
	}
	if err != nil {
		// Upstream fails with `key "<key>" is not set in global|local config`
		// when the requested scope file does not carry an explicit value.
		if strings.Contains(result.Stderr, "is not set in") {
			return "", nil
		}
		return "", fmt.Errorf("config get %s: %w", key, err)
	}
	if result.ExitCode != 0 || result.Stderr != "" {
		return "", fmt.Errorf("config get %s: response unavailable", key)
	}
	value := strings.TrimRight(result.Stdout, "\r\n")
	if len(strings.Fields(value)) > 1 {
		return "", fmt.Errorf("config get %s: unexpected multi-token response", key)
	}
	return value, nil
}

const configurationBindingProbeTimeout = 5 * time.Second

var errConfigurationUnavailable = errors.New("configuration mutation is unavailable")

// ProbeConfigurationBinding is intentionally uncached. It validates the
// exact v0.64 executable and its current file identity before every mutation
// and readback boundary.
func (c *Client) ProbeConfigurationBinding(ctx context.Context) (ConfigurationBinding, error) {
	if c == nil || c.Runner == nil {
		return ConfigurationBinding{}, errConfigurationUnavailable
	}
	binary, _, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return ConfigurationBinding{}, errConfigurationUnavailable
	}
	abs, err := filepath.Abs(binary)
	if err != nil {
		return ConfigurationBinding{}, errConfigurationUnavailable
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		resolved = abs
	}
	if !sameExecutablePath(abs, resolved) {
		return ConfigurationBinding{}, errConfigurationUnavailable
	}
	resolved = filepath.Clean(resolved)
	info, err := os.Lstat(resolved)
	if err != nil || info.IsDir() || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ConfigurationBinding{}, errConfigurationUnavailable
	}
	fileID, err := pathsafety.FileIdentity(resolved, info)
	if err != nil || strings.TrimSpace(fileID) == "" {
		return ConfigurationBinding{}, errConfigurationUnavailable
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{Executable: resolved, Args: []string{"version", "--json"}, Env: agentConfigurationEnvFromProcess(), Timeout: configurationBindingProbeTimeout, OutputLimit: 64 << 10})
	if runErr != nil || !result.Started || result.ExitCode != 0 || result.Stderr != "" || result.StdoutTruncated || result.StderrTruncated || !utf8.ValidString(result.Stdout) || !utf8.ValidString(result.Stderr) {
		return ConfigurationBinding{}, errConfigurationUnavailable
	}
	decoder := json.NewDecoder(strings.NewReader(result.Stdout))
	decoder.DisallowUnknownFields()
	var response struct{ Name, Version string }
	if err := decoder.Decode(&response); err != nil {
		return ConfigurationBinding{}, errConfigurationUnavailable
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || roborevcompat.ValidateVersionIdentity(response.Name, response.Version) != nil || response.Version != "v0.64.0" {
		return ConfigurationBinding{}, errConfigurationUnavailable
	}
	if !c.generationCurrent(generation) {
		return ConfigurationBinding{}, ErrBindingChanged
	}
	return ConfigurationBinding{Binary: resolved, Version: response.Version, Generation: generation, FileID: fileID}, nil
}

func (c *Client) configurationListWithBinary(ctx context.Context, binary, repoPath string) ([]ConfigRow, error) {
	if strings.TrimSpace(binary) == "" || strings.TrimSpace(repoPath) == "" || !filepath.IsAbs(repoPath) {
		return nil, errConfigurationUnavailable
	}
	result, err := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: ConfigListArgs(), Dir: filepath.Clean(repoPath), Env: agentConfigurationEnvFromProcess(), Timeout: configListTimeout, OutputLimit: configListOutputLimit})
	if err != nil || !result.Started || result.ExitCode != 0 || result.Stderr != "" || result.StdoutTruncated || result.StderrTruncated || !utf8.ValidString(result.Stdout) || !utf8.ValidString(result.Stderr) {
		return nil, errConfigurationUnavailable
	}
	rows, err := ParseConfigList(result.Stdout)
	if err != nil || len(rows) == 0 {
		return nil, errConfigurationUnavailable
	}
	return rows, nil
}

// ConfigurationPreflight captures one fresh binding and effective list.
func (c *Client) ConfigurationPreflight(ctx context.Context, repoPath string) (ConfigurationPreflight, error) {
	binding, err := c.ProbeConfigurationBinding(ctx)
	if err != nil {
		return ConfigurationPreflight{}, err
	}
	rows, err := c.configurationListWithBinary(ctx, binding.Binary, repoPath)
	if err != nil {
		return ConfigurationPreflight{}, err
	}
	fresh, err := c.ProbeConfigurationBinding(ctx)
	if err != nil || !fresh.Equal(binding) {
		return ConfigurationPreflight{}, ErrBindingChanged
	}
	return ConfigurationPreflight{Binding: binding, Rows: rows}, nil
}

func (c *Client) ConfigurationSet(ctx context.Context, repoPath, key, value string, scope ConfigScope, expected ConfigurationBinding, guard ConfigurationPreLaunchGuard) (ConfigurationResult, error) {
	args, err := ConfigSetArgs(key, value, scope)
	if err != nil || !expected.Valid() || guard == nil {
		return ConfigurationResult{}, errConfigurationUnavailable
	}
	binary, _, generation := c.snapshot()
	if generation != expected.Generation || !sameExecutablePath(binary, expected.Binary) {
		return ConfigurationResult{}, ErrBindingChanged
	}
	binding, err := c.ProbeConfigurationBinding(ctx)
	if err != nil || !binding.Equal(expected) {
		return ConfigurationResult{}, ErrBindingChanged
	}
	if err := guard(ctx); err != nil {
		return ConfigurationResult{}, err
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{Executable: expected.Binary, Args: args, Dir: filepath.Clean(repoPath), Env: agentConfigurationEnvFromProcess(), Timeout: configSetTimeout, OutputLimit: configValueLimit})
	mutation := ConfigurationResult{Started: result.Started, ExitCode: result.ExitCode}
	if runErr != nil {
		return mutation, errConfigurationUnavailable
	}
	if !c.generationCurrent(expected.Generation) {
		return mutation, ErrBindingChanged
	}
	return mutation, nil
}

func (c *Client) ConfigurationReadback(ctx context.Context, repoPath, key string, scope ConfigScope, expected ConfigurationBinding) (ConfigurationReadback, error) {
	if !expected.Valid() {
		return ConfigurationReadback{}, errConfigurationUnavailable
	}
	binding, err := c.ProbeConfigurationBinding(ctx)
	if err != nil || !binding.Equal(expected) {
		return ConfigurationReadback{}, ErrBindingChanged
	}
	args, err := ConfigGetArgs(key, scope)
	if err != nil {
		return ConfigurationReadback{}, errConfigurationUnavailable
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{Executable: expected.Binary, Args: args, Dir: filepath.Clean(repoPath), Env: agentConfigurationEnvFromProcess(), Timeout: configGetTimeout, OutputLimit: configValueLimit})
	if runErr != nil || !result.Started || result.ExitCode != 0 || result.Stderr != "" || result.StdoutTruncated || result.StderrTruncated || !utf8.ValidString(result.Stdout) || !utf8.ValidString(result.Stderr) {
		return ConfigurationReadback{}, errConfigurationUnavailable
	}
	value := strings.TrimRight(result.Stdout, "\r\n")
	if strings.ContainsAny(value, "\x00\r\n") || strings.TrimSpace(value) != value || len(value) > 4096 {
		return ConfigurationReadback{}, errConfigurationUnavailable
	}
	rows, err := c.configurationListWithBinary(ctx, expected.Binary, repoPath)
	if err != nil {
		return ConfigurationReadback{}, err
	}
	fresh, err := c.ProbeConfigurationBinding(ctx)
	if err != nil || !fresh.Equal(expected) {
		return ConfigurationReadback{}, ErrBindingChanged
	}
	return ConfigurationReadback{Value: value, Rows: rows, Binding: fresh}, nil
}

// SnapshotDirectory resolves the repository-local snapshot_dir used by the
// Setup Readiness read seam. It deliberately does not reuse ConfigGet's
// single-token normalization: spaces are valid in a configured directory.
// ConfigList remains the effective-configuration gate, so malformed global or
// local TOML fails closed even though only the local value is consumed.
func (c *Client) SnapshotDirectory(ctx context.Context, repoPath string) (string, error) {
	if err := c.requireConfigCapability(ctx); err != nil {
		return "", err
	}
	listResult, listErr := c.runInDir(ctx, repoPath, ConfigListArgs(), configListTimeout, configListOutputLimit)
	if listErr != nil {
		return "", errors.New("effective configuration is unavailable")
	}
	if listResult.StdoutTruncated || listResult.StderrTruncated || !utf8.ValidString(listResult.Stdout) || !utf8.ValidString(listResult.Stderr) {
		return "", errors.New("effective configuration is unavailable")
	}
	args, err := ConfigGetArgs("snapshot_dir", ConfigScopeLocal)
	if err != nil {
		return "", err
	}
	result, runErr := c.runInDir(ctx, repoPath, args, configGetTimeout, configValueLimit)
	if result.StdoutTruncated || result.StderrTruncated || !utf8.ValidString(result.Stdout) || !utf8.ValidString(result.Stderr) {
		return "", errors.New("snapshot_dir response exceeded bounded output or was not valid UTF-8")
	}
	if runErr != nil {
		message := strings.TrimSpace(result.Stderr)
		message = strings.TrimSpace(strings.TrimPrefix(message, "Error:"))
		if message == errNoLocalConfig.Error() || message == errSnapshotDirNotSet.Error() {
			return ".roborev", nil
		}
		return "", errors.New("snapshot_dir could not be resolved")
	}
	// Remove exactly one terminal EOL while preserving every other space.
	value := result.Stdout
	if strings.HasSuffix(value, "\r\n") {
		value = strings.TrimSuffix(value, "\r\n")
	} else if strings.HasSuffix(value, "\n") {
		value = strings.TrimSuffix(value, "\n")
	} else if strings.HasSuffix(value, "\r") {
		value = strings.TrimSuffix(value, "\r")
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

var _ ConfigService = (*Client)(nil)
var _ ConfigurationMutationTransport = (*Client)(nil)
