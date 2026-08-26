package roborev

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

const (
	ciWorkflowVersion       = "v0.64.0"
	ciWorkflowCLIVersion    = "0.64.0"
	ciWorkflowOutputName    = "roborev.yml"
	ciWorkflowConfigTimeout = 10 * time.Second
	ciWorkflowRunTimeout    = 30 * time.Second
	ciWorkflowOutputLimit   = 64 << 10
	ciWorkflowConfigLimit   = 256 << 10
)

var (
	ErrCIWorkflowUnavailable = errors.New("CI workflow generation is unavailable")
	ErrCIWorkflowConfig      = errors.New("CI workflow configuration is unavailable")
	ErrCIWorkflowOutput      = errors.New("CI workflow output path is unsafe")
)

// CIWorkflowPreviewArgs is the sole typed builder for the preview generation
// subprocess. The output must already be the server-owned canonical absolute
// path to the one fixed filename; callers cannot add flags or select a
// version, agent, force mode, or repository target.
func CIWorkflowPreviewArgs(privateOutput string) ([]string, error) {
	if strings.TrimSpace(privateOutput) == "" || !filepath.IsAbs(privateOutput) || filepath.Clean(privateOutput) != privateOutput || filepath.Base(privateOutput) != ciWorkflowOutputName {
		return nil, ErrCIWorkflowOutput
	}
	return []string{"init", "gh-action", "--output", privateOutput, "--roborev-version", ciWorkflowCLIVersion}, nil
}

// GenerateCIWorkflow executes the one supported v0.64.0 CI workflow
// generation command in a server-owned temporary output location. It keeps
// all version-probe/configuration/process details inside the adapter and
// returns no upstream output to callers.
func (c *Client) GenerateCIWorkflow(ctx context.Context, canonicalRepoRoot, privateOutput string, guard CIWorkflowPreLaunchGuard) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if guard == nil {
		return ErrCIWorkflowOutput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(canonicalRepoRoot) || filepath.Clean(canonicalRepoRoot) != canonicalRepoRoot {
		return ErrCIWorkflowUnavailable
	}
	root := canonicalRepoRoot
	args, err := CIWorkflowPreviewArgs(privateOutput)
	if err != nil {
		return ErrCIWorkflowOutput
	}
	output := privateOutput
	if c == nil || c.Runner == nil {
		return ErrCIWorkflowUnavailable
	}
	if info, statErr := os.Lstat(output); statErr == nil || !errors.Is(statErr, os.ErrNotExist) {
		// The service proves absence immediately before this call. Keep this
		// independent check so direct adapter callers cannot overwrite a path.
		_ = info
		return ErrCIWorkflowOutput
	}
	env := ciWorkflowEnvFromProcess()
	initial, err := c.probeCIWorkflowBinding(ctx, env)
	if err != nil {
		return ErrCIWorkflowUnavailable
	}
	// Build every command and its environment before the fresh binding
	// acceptance boundary. No caller-controlled value enters a shell string.
	configCommand := proc.Command{
		Executable:  initial.Binary,
		Args:        ConfigListArgs(),
		Dir:         root,
		Env:         append([]string(nil), env...),
		Timeout:     ciWorkflowConfigTimeout,
		OutputLimit: ciWorkflowConfigLimit,
	}
	generationCommand := proc.Command{
		Executable:  initial.Binary,
		Args:        args,
		Dir:         root,
		Env:         append([]string(nil), env...),
		Timeout:     ciWorkflowRunTimeout,
		OutputLimit: ciWorkflowOutputLimit,
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	configResult, configErr := c.Runner.Run(ctx, configCommand)
	_, parseErr := ParseConfigList(configResult.Stdout)
	if configErr != nil || !configResult.Started || configResult.ExitCode != 0 || configResult.StdoutTruncated || configResult.StderrTruncated || !utf8.ValidString(configResult.Stdout) || parseErr != nil {
		return ErrCIWorkflowConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fresh, err := c.probeCIWorkflowBinding(ctx, env)
	if err != nil || fresh != initial {
		return ErrBindingChanged
	}
	if err := guard(ctx); err != nil {
		// Guard failures may contain private filesystem details. Collapse every
		// callback error to the existing safe output-path error and do not
		// inspect it or launch generation.
		return ErrCIWorkflowOutput
	}
	// The guard closes the Dashboard-observable race. The OS does not provide
	// atomic binding between this callback and CreateProcess/WriteWorkflow, so
	// a same-user replacement in that residual window is outside this contract.
	result, runErr := c.Runner.Run(ctx, generationCommand)
	if runErr != nil || !result.Started || result.ExitCode != 0 || result.StdoutTruncated || result.StderrTruncated {
		return ErrCIWorkflowUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	final, err := c.probeCIWorkflowBinding(ctx, env)
	if err != nil || final != initial {
		return ErrBindingChanged
	}
	return nil
}

func (c *Client) probeCIWorkflowBinding(ctx context.Context, env []string) (GitHookBinding, error) {
	binding, err := c.probeAgentHookReadinessBinding(ctx, env)
	if err != nil {
		return GitHookBinding{}, err
	}
	if binding.Version != ciWorkflowVersion {
		return GitHookBinding{}, ErrCIWorkflowUnavailable
	}
	return binding, nil
}

func ciWorkflowEnvFromProcess() []string {
	source := os.Environ()
	env := proc.MinimalProbeEnv(source)
	seen := make(map[string]struct{}, len(env))
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok {
			seen[strings.ToUpper(name)] = struct{}{}
		}
	}
	for _, entry := range source {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(name)
		switch upper {
		case "HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH":
			if strings.TrimSpace(value) == "" {
				continue
			}
		case "ROBOREV_DATA_DIR":
			if strings.TrimSpace(value) == "" || !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
				continue
			}
		default:
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
