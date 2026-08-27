package roborev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	agentConfigurationProbeTimeout = 5 * time.Second
	agentConfigurationListTimeout  = 10 * time.Second
	agentConfigurationGetTimeout   = 10 * time.Second
	agentConfigurationSetTimeout   = 15 * time.Second
	agentConfigurationOutputLimit  = 256 << 10
	agentConfigurationValueLimit   = 8 << 10
)

var errAgentConfigurationUnavailable = errors.New("agent configuration is unavailable")

// ProbeRepositoryAgentConfigurationBinding performs an uncached version and
// executable identity check. It is intentionally independent from
// Installation(), which may use a cached version and daemon health probes.
func (c *Client) probeRepositoryAgentConfigurationBinding(ctx context.Context) (RepositoryAgentConfigurationBinding, error) {
	if c == nil || c.Runner == nil {
		return RepositoryAgentConfigurationBinding{}, errAgentConfigurationUnavailable
	}
	binary, _, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return RepositoryAgentConfigurationBinding{}, errAgentConfigurationUnavailable
	}
	abs, err := filepath.Abs(binary)
	if err != nil {
		return RepositoryAgentConfigurationBinding{}, errAgentConfigurationUnavailable
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		resolved = abs
	}
	if !sameExecutablePath(abs, resolved) {
		return RepositoryAgentConfigurationBinding{}, errAgentConfigurationUnavailable
	}
	resolved = filepath.Clean(resolved)
	info, err := os.Lstat(resolved)
	if err != nil || !safeExecutableInfo(info) {
		return RepositoryAgentConfigurationBinding{}, errAgentConfigurationUnavailable
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{
		Executable:  resolved,
		Args:        []string{"version", "--json"},
		Env:         agentConfigurationEnvFromProcess(),
		Timeout:     agentConfigurationProbeTimeout,
		OutputLimit: 64 << 10,
	})
	if runErr != nil || !result.Started || result.ExitCode != 0 || result.Stderr != "" || result.StdoutTruncated || result.StderrTruncated || !utf8.ValidString(result.Stdout) || !utf8.ValidString(result.Stderr) {
		return RepositoryAgentConfigurationBinding{}, errAgentConfigurationUnavailable
	}
	var response struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	decoder := json.NewDecoder(strings.NewReader(result.Stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return RepositoryAgentConfigurationBinding{}, errAgentConfigurationUnavailable
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || roborevcompat.ValidateVersionIdentity(response.Name, response.Version) != nil || !roborevcompat.IsSupportedVersion(response.Version) {
		return RepositoryAgentConfigurationBinding{}, errAgentConfigurationUnavailable
	}
	if !c.generationCurrent(generation) {
		return RepositoryAgentConfigurationBinding{}, ErrBindingChanged
	}
	return RepositoryAgentConfigurationBinding{
		Binary: resolved, Version: response.Version, Generation: generation,
		FileID: fmt.Sprintf("%d:%d:%o", info.Size(), info.ModTime().UnixNano(), info.Mode()),
	}, nil
}

func (c *Client) agentConfigurationList(ctx context.Context, repoPath string) ([]ConfigRow, error) {
	binary, _, _ := c.snapshot()
	return c.agentConfigurationListWithBinary(ctx, binary, repoPath)
}

func (c *Client) agentConfigurationListWithBinary(ctx context.Context, binary, repoPath string) ([]ConfigRow, error) {
	result, err := c.runAgentConfigurationWithBinary(ctx, binary, repoPath, []string{"config", "list", "--show-origin"}, agentConfigurationListTimeout, agentConfigurationOutputLimit)
	if err != nil || !result.Started || result.ExitCode != 0 || result.StdoutTruncated || result.StderrTruncated || result.Stderr != "" || !utf8.ValidString(result.Stdout) || !utf8.ValidString(result.Stderr) {
		return nil, errAgentConfigurationUnavailable
	}
	rows, parseErr := ParseConfigList(result.Stdout)
	if parseErr != nil || len(rows) == 0 {
		return nil, errAgentConfigurationUnavailable
	}
	return rows, nil
}

func (c *Client) agentConfigurationGet(ctx context.Context, repoPath string, scope ConfigScope) (RepositoryAgentConfigurationScopedValue, error) {
	binary, _, _ := c.snapshot()
	return c.agentConfigurationGetWithBinary(ctx, binary, repoPath, scope)
}

func (c *Client) agentConfigurationGetWithBinary(ctx context.Context, binary, repoPath string, scope ConfigScope) (RepositoryAgentConfigurationScopedValue, error) {
	key, err := agentConfigurationKey(scope)
	if err != nil {
		return RepositoryAgentConfigurationScopedValue{}, err
	}
	result, runErr := c.runAgentConfigurationWithBinary(ctx, binary, repoPath, []string{"config", "get", key, "--" + string(scope)}, agentConfigurationGetTimeout, agentConfigurationValueLimit)
	if result.StdoutTruncated || result.StderrTruncated || !utf8.ValidString(result.Stdout) || !utf8.ValidString(result.Stderr) {
		return RepositoryAgentConfigurationScopedValue{}, errAgentConfigurationUnavailable
	}
	if runErr != nil {
		if result.Started && result.ExitCode != 0 && exactUnsetAgentConfigurationError(result.Stderr, key, scope) {
			return RepositoryAgentConfigurationScopedValue{}, nil
		}
		return RepositoryAgentConfigurationScopedValue{}, errAgentConfigurationUnavailable
	}
	value := result.Stdout
	if strings.HasSuffix(value, "\r\n") {
		value = strings.TrimSuffix(value, "\r\n")
	} else if strings.HasSuffix(value, "\n") || strings.HasSuffix(value, "\r") {
		value = value[:len(value)-1]
	}
	if !result.Started || result.ExitCode != 0 || result.Stderr != "" || strings.ContainsAny(value, "\x00\r\n") || strings.TrimSpace(value) != value || len(value) > 4096 {
		return RepositoryAgentConfigurationScopedValue{}, errAgentConfigurationUnavailable
	}
	return RepositoryAgentConfigurationScopedValue{Set: true, Value: value}, nil
}

func (c *Client) ProbeRepositoryAgentConfigurationBinding(ctx context.Context) (RepositoryAgentConfigurationBinding, error) {
	return c.probeRepositoryAgentConfigurationBinding(ctx)
}

// RepositoryAgentConfigurationStatus reads both dedicated settings against
// one executable binding and one complete config-list snapshot. The exact
// command order is version, list, global get, local get, version.
func (c *Client) RepositoryAgentConfigurationStatus(ctx context.Context, repoPath string) (RepositoryAgentConfigurationStatusSnapshot, error) {
	binding, err := c.probeRepositoryAgentConfigurationBinding(ctx)
	if err != nil {
		return RepositoryAgentConfigurationStatusSnapshot{}, err
	}
	rows, err := c.agentConfigurationListWithBinary(ctx, binding.Binary, repoPath)
	if err != nil {
		return RepositoryAgentConfigurationStatusSnapshot{}, err
	}
	global, err := c.agentConfigurationGetWithBinary(ctx, binding.Binary, repoPath, ConfigScopeGlobal)
	if err != nil {
		return RepositoryAgentConfigurationStatusSnapshot{}, err
	}
	repository, err := c.agentConfigurationGetWithBinary(ctx, binding.Binary, repoPath, ConfigScopeLocal)
	if err != nil {
		return RepositoryAgentConfigurationStatusSnapshot{}, err
	}
	fresh, err := c.probeRepositoryAgentConfigurationBinding(ctx)
	if err != nil || !fresh.Equal(binding) {
		return RepositoryAgentConfigurationStatusSnapshot{}, ErrBindingChanged
	}
	return RepositoryAgentConfigurationStatusSnapshot{
		Binding: binding, Rows: rows, Global: global, Repository: repository,
	}, nil
}

func (c *Client) RepositoryAgentConfigurationPreflight(ctx context.Context, repoPath string, scope ConfigScope) (RepositoryAgentConfigurationPreflight, error) {
	if !scopeIsAgentConfiguration(scope) {
		return RepositoryAgentConfigurationPreflight{}, errAgentConfigurationUnavailable
	}
	binding, err := c.probeRepositoryAgentConfigurationBinding(ctx)
	if err != nil {
		return RepositoryAgentConfigurationPreflight{}, err
	}
	rows, err := c.agentConfigurationListWithBinary(ctx, binding.Binary, repoPath)
	if err != nil {
		return RepositoryAgentConfigurationPreflight{}, err
	}
	value, err := c.agentConfigurationGetWithBinary(ctx, binding.Binary, repoPath, scope)
	if err != nil {
		return RepositoryAgentConfigurationPreflight{}, err
	}
	fresh, err := c.probeRepositoryAgentConfigurationBinding(ctx)
	if err != nil || !fresh.Equal(binding) {
		return RepositoryAgentConfigurationPreflight{}, ErrBindingChanged
	}
	return RepositoryAgentConfigurationPreflight{Binding: binding, Rows: rows, Value: value}, nil
}

func (c *Client) RepositoryAgentConfigurationSet(ctx context.Context, repoPath string, scope ConfigScope, value string, expected RepositoryAgentConfigurationBinding, guard RepositoryAgentConfigurationPreLaunchGuard) (RepositoryAgentConfigurationResult, error) {
	key, err := agentConfigurationKey(scope)
	if err != nil || !isCanonicalAgentConfigurationValue(value) {
		return RepositoryAgentConfigurationResult{}, errAgentConfigurationUnavailable
	}
	if !expected.Valid() || guard == nil {
		return RepositoryAgentConfigurationResult{}, errAgentConfigurationUnavailable
	}
	args := []string{"config", "set", key, value, "--" + string(scope)}
	binary, _, generation := c.snapshot()
	if binary == "" || generation != expected.Generation || !sameExecutablePath(binary, expected.Binary) {
		return RepositoryAgentConfigurationResult{}, ErrBindingChanged
	}
	binding, err := c.probeRepositoryAgentConfigurationBinding(ctx)
	if err != nil || !binding.Equal(expected) {
		return RepositoryAgentConfigurationResult{}, ErrBindingChanged
	}
	if err := guard(ctx); err != nil {
		return RepositoryAgentConfigurationResult{}, err
	}
	// Use the path captured by the successful binding probe directly. A
	// re-snapshot here could select a newly rebound executable in the gap after
	// the final root guard.
	result, runErr := c.runAgentConfigurationWithBinary(ctx, expected.Binary, repoPath, args, agentConfigurationSetTimeout, agentConfigurationValueLimit)
	mutation := RepositoryAgentConfigurationResult{Started: result.Started, ExitCode: result.ExitCode}
	if runErr != nil {
		if !c.generationCurrent(expected.Generation) {
			return mutation, ErrBindingChanged
		}
		return mutation, errAgentConfigurationUnavailable
	}
	if !c.generationCurrent(expected.Generation) {
		return mutation, ErrBindingChanged
	}
	return mutation, nil
}

func (c *Client) RepositoryAgentConfigurationReadback(ctx context.Context, repoPath string, scope ConfigScope, desired string, expected RepositoryAgentConfigurationBinding) error {
	if !expected.Valid() || !isCanonicalAgentConfigurationValue(desired) {
		return errAgentConfigurationUnavailable
	}
	fresh, err := c.probeRepositoryAgentConfigurationBinding(ctx)
	if err != nil || !fresh.Equal(expected) {
		return ErrBindingChanged
	}
	value, err := c.agentConfigurationGetWithBinary(ctx, expected.Binary, repoPath, scope)
	if err != nil || !value.Set || value.Value != desired {
		return errAgentConfigurationUnavailable
	}
	rows, err := c.agentConfigurationListWithBinary(ctx, expected.Binary, repoPath)
	if err != nil {
		return err
	}
	key, err := agentConfigurationKey(scope)
	if err != nil {
		return err
	}
	expectedOrigin := "global"
	if scope == ConfigScopeLocal {
		expectedOrigin = "local"
	}
	matched := 0
	for _, row := range rows {
		if row.Key != key {
			continue
		}
		matched++
		if row.Origin != expectedOrigin || row.Value != desired {
			return errAgentConfigurationUnavailable
		}
	}
	if matched != 1 {
		return errAgentConfigurationUnavailable
	}
	fresh, err = c.probeRepositoryAgentConfigurationBinding(ctx)
	if err != nil || !fresh.Equal(expected) {
		return ErrBindingChanged
	}
	return nil
}

func agentConfigurationKey(scope ConfigScope) (string, error) {
	switch scope {
	case ConfigScopeGlobal:
		return "default_agent", nil
	case ConfigScopeLocal:
		return "agent", nil
	default:
		return "", errAgentConfigurationUnavailable
	}
}

func scopeIsAgentConfiguration(scope ConfigScope) bool {
	return scope == ConfigScopeGlobal || scope == ConfigScopeLocal
}

func isCanonicalAgentConfigurationValue(value string) bool {
	switch value {
	case "codex", "claude-code", "gemini", "copilot", "opencode", "cursor", "kiro", "kilo", "droid", "pi", "grok":
		return true
	default:
		return false
	}
}

func exactUnsetAgentConfigurationError(stderr string, key string, scope ConfigScope) bool {
	message := stderr
	if strings.HasSuffix(message, "\r\n") {
		message = strings.TrimSuffix(message, "\r\n")
	} else if strings.HasSuffix(message, "\n") || strings.HasSuffix(message, "\r") {
		message = message[:len(message)-1]
	}
	if message == fmt.Sprintf("Error: key %q is not set in %s config", key, scope) {
		return true
	}
	return scope == ConfigScopeLocal && message == "Error: no local config (.roborev.toml) found"
}

func (c *Client) runAgentConfiguration(ctx context.Context, repoPath string, args []string, timeout time.Duration, limit int) (proc.Result, error) {
	binary, _, _ := c.snapshot()
	return c.runAgentConfigurationWithBinary(ctx, binary, repoPath, args, timeout, limit)
}

func (c *Client) runAgentConfigurationWithBinary(ctx context.Context, binary, repoPath string, args []string, timeout time.Duration, limit int) (proc.Result, error) {
	if c == nil || c.Runner == nil || strings.TrimSpace(repoPath) == "" || !filepath.IsAbs(repoPath) {
		return proc.Result{}, errAgentConfigurationUnavailable
	}
	if strings.TrimSpace(binary) == "" {
		return proc.Result{}, errAgentConfigurationUnavailable
	}
	result, err := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: args, Dir: filepath.Clean(repoPath), Env: agentConfigurationEnvFromProcess(), Timeout: timeout, OutputLimit: limit})
	if err != nil {
		return result, errAgentConfigurationUnavailable
	}
	return result, nil
}

func agentConfigurationEnvFromProcess() []string {
	source := os.Environ()
	env := proc.MinimalProbeEnv(source)
	seen := make(map[string]struct{}, len(env))
	for _, entry := range env {
		if name, _, ok := strings.Cut(entry, "="); ok {
			seen[strings.ToUpper(name)] = struct{}{}
		}
	}
	for _, entry := range source {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		upper := strings.ToUpper(name)
		allowed := upper == "HOME" || upper == "USERPROFILE" || upper == "HOMEDRIVE" || upper == "HOMEPATH"
		if upper == "ROBOREV_DATA_DIR" {
			allowed = filepath.IsAbs(value) && !strings.ContainsAny(value, "\x00\r\n")
		}
		if !allowed {
			continue
		}
		if _, exists := seen[upper]; exists {
			continue
		}
		seen[upper] = struct{}{}
		env = append(env, entry)
	}
	return env
}

var _ RepositoryAgentConfigurationTransport = (*Client)(nil)
