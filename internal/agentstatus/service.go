// Package agentstatus provides zero-quota discovery and explicitly-triggered
// health state for the built-in RoboRev v0.64 agents.
package agentstatus

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

const (
	identityProbeTimeout   = 2 * time.Second
	identityProbeMaxBytes  = 4096
	identityProbeWaitDelay = 250 * time.Millisecond
)

// ExecutableResolver is the only filesystem/PATH seam used by discovery.
// Implementations must return a regular executable file, or an error.
type ExecutableResolver interface {
	LookPath(string) (string, error)
	SameFile(string, string) bool
}

// ProbeResult is the bounded output of one version probe. Output is combined
// stdout/stderr because v0.64's identity check treats either stream as proof.
type ProbeResult struct {
	Output    string
	Err       error
	Truncated bool
}

// IdentityProbeRunner runs only a fixed version flag and receives no stdin.
// It is intentionally narrower than the generic process runner.
type IdentityProbeRunner interface {
	Run(context.Context, string, []string) ProbeResult
}

type systemExecutableResolver struct{}

func (systemExecutableResolver) LookPath(command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", os.ErrNotExist
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", os.ErrNotExist
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return absolute, nil
}

func (systemExecutableResolver) SameFile(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftResolved, leftErr := filepath.EvalSymlinks(left)
	rightResolved, rightErr := filepath.EvalSymlinks(right)
	if leftErr == nil {
		left = leftResolved
	}
	if rightErr == nil {
		right = rightResolved
	}
	if left == right {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

// SystemExecutableResolver is the production LookPath/regular-file
// resolver. It is exported so integration wiring can use the same behavior
// explicitly, while tests can provide a fake.
var SystemExecutableResolver ExecutableResolver = systemExecutableResolver{}

type processIdentityProbeRunner struct{ runner proc.Runner }

func (r processIdentityProbeRunner) Run(ctx context.Context, executable string, args []string) ProbeResult {
	result, err := r.runner.Run(ctx, proc.Command{
		Executable: executable,
		Args:       append([]string(nil), args...),
		Dir:        os.TempDir(),
		Env:        proc.StripUntrustedEnv(os.Environ()),
		Timeout:    identityProbeTimeout,
		WaitDelay:  identityProbeWaitDelay,
		// ExecRunner bounds each stream independently. The combined length
		// check below is the authoritative 4096-byte cap.
		OutputLimit: identityProbeMaxBytes,
	})
	combined := result.Stdout + result.Stderr
	return ProbeResult{
		Output:    combined,
		Err:       err,
		Truncated: result.StdoutTruncated || result.StderrTruncated || len(combined) > identityProbeMaxBytes,
	}
}

// SystemIdentityProbeRunner is the production bounded version probe runner.
var SystemIdentityProbeRunner IdentityProbeRunner = processIdentityProbeRunner{runner: proc.ExecRunner{}}

type configuredHealth struct {
	result roborev.AgentHealthResult
	set    bool
}

// Status is the safe GET-equivalent representation of one built-in agent.
// Discover never invokes HealthChecker; Health is only populated from a
// previously completed explicit Check call.
type Status struct {
	Name                string                       `json:"name"`
	Aliases             []string                     `json:"aliases,omitempty"`
	Command             string                       `json:"command"`
	Path                string                       `json:"path,omitempty"`
	Available           bool                         `json:"available"`
	Configured          bool                         `json:"configured"`
	Subagents           []roborev.ConfiguredSubagent `json:"subagents,omitempty"`
	UnresolvedSubagents []roborev.ConfiguredSubagent `json:"unresolvedSubagents,omitempty"`
	Health              roborev.AgentHealthStatus    `json:"health,omitempty"`
}

// HealthChecker is implemented by roborev.Client and by deterministic fakes.
type HealthChecker interface {
	CheckAgent(context.Context, string) (roborev.AgentHealthResult, error)
}

// RepositoryHealthChecker is implemented by health adapters that need the
// selected repository as the check-agents working directory.
type RepositoryHealthChecker interface {
	CheckAgentInRepo(context.Context, string, string) (roborev.AgentHealthResult, error)
}

// Service owns discovery seams and in-memory health state. It does not read
// RoboRev storage and does not poll or invoke an agent during discovery.
type Service struct {
	Panels        *roborev.PanelResolver
	Resolver      ExecutableResolver
	Probe         IdentityProbeRunner
	HealthChecker HealthChecker

	mu     sync.RWMutex
	health map[string]configuredHealth
}

func NewService(panels *roborev.PanelResolver, checker HealthChecker) *Service {
	return &Service{
		Panels: panels, Resolver: SystemExecutableResolver,
		Probe: SystemIdentityProbeRunner, HealthChecker: checker,
		health: make(map[string]configuredHealth),
	}
}

// NewServiceWithSeams is intended for tests and controlled embedding.
func NewServiceWithSeams(panels *roborev.PanelResolver, checker HealthChecker, resolver ExecutableResolver, probe IdentityProbeRunner) *Service {
	service := NewService(panels, checker)
	if resolver != nil {
		service.Resolver = resolver
	}
	if probe != nil {
		service.Probe = probe
	}
	return service
}

// Discover performs bounded PATH/config discovery only. It never calls the
// health checker and never sends a prompt to an agent.
func (s *Service) Discover(ctx context.Context, repoPath string) ([]Status, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return nil, errors.New("agent status service is nil")
	}
	resolver := s.Resolver
	if resolver == nil {
		resolver = SystemExecutableResolver
	}
	probe := s.Probe
	if probe == nil {
		probe = SystemIdentityProbeRunner
	}
	panels := s.Panels
	if panels == nil {
		panels = roborev.NewPanelResolver()
	}
	configuration, err := panels.ResolveAgentConfiguration(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	configured := make(map[string][]roborev.ConfiguredSubagent)
	var unresolved []roborev.ConfiguredSubagent
	for _, entry := range configuration.Subagents {
		if entry.DetectionUnavailable || entry.CanonicalAgent == "" {
			unresolved = append(unresolved, entry)
			continue
		}
		configured[entry.CanonicalAgent] = append(configured[entry.CanonicalAgent], entry)
	}
	statuses := make([]Status, 0, len(roborev.AgentDescriptors()))
	for _, descriptor := range roborev.AgentDescriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		command := descriptor.DefaultCommand
		if descriptor.CommandKey != "" {
			if override := configuration.Commands[descriptor.CommandKey]; override != "" {
				command = override
			}
		}
		path, pathErr := resolver.LookPath(command)
		available := pathErr == nil && strings.TrimSpace(path) != ""
		if available && descriptor.Name == "cursor" {
			available = cursorIdentityUsable(ctx, path, configuration.Commands["grok_cmd"], resolver, probe)
		}
		status := Status{
			Name: descriptor.Name, Aliases: append([]string(nil), descriptor.Aliases...),
			Command: command, Path: path, Available: available,
			Configured: len(configured[descriptor.Name]) > 0,
			Subagents:  append([]roborev.ConfiguredSubagent(nil), configured[descriptor.Name]...),
		}
		s.mu.RLock()
		if state, ok := s.health[descriptor.Name]; ok && state.set && state.result.Status != "" {
			status.Health = state.result.Status
		}
		s.mu.RUnlock()
		statuses = append(statuses, status)
	}
	if len(unresolved) > 0 && len(statuses) > 0 {
		// Inherited entries have no safe canonical agent to attach to. Keep
		// them as an explicit projection on the first descriptor so API
		// callers can flatten them without inventing a runnable identity.
		statuses[0].UnresolvedSubagents = append([]roborev.ConfiguredSubagent(nil), unresolved...)
	}
	return statuses, nil
}

// Check runs one explicit manual check-agents request and stores its small
// status in memory for later discovery responses.
func (s *Service) Check(ctx context.Context, agent string) (roborev.AgentHealthResult, error) {
	if s == nil || s.HealthChecker == nil {
		return roborev.AgentHealthResult{}, errors.New("agent health checker is unavailable")
	}
	canonical, ok := roborev.CanonicalAgentName(agent)
	if !ok {
		return roborev.AgentHealthResult{}, errors.New("agent is not allowed")
	}
	result, err := s.HealthChecker.CheckAgent(ctx, canonical)
	if err != nil {
		return roborev.AgentHealthResult{}, err
	}
	s.mu.Lock()
	if s.health == nil {
		s.health = make(map[string]configuredHealth)
	}
	s.health[canonical] = configuredHealth{result: result, set: true}
	s.mu.Unlock()
	return result, nil
}

// CheckInRepo is the repository-aware form used by the app handler after it
// has resolved a registered repository. Fakes that only implement the legacy
// HealthChecker interface remain usable for tests and embedding.
func (s *Service) CheckInRepo(ctx context.Context, repoPath, agent string) (roborev.AgentHealthResult, error) {
	if s == nil || s.HealthChecker == nil {
		return roborev.AgentHealthResult{}, errors.New("agent health checker is unavailable")
	}
	if strings.TrimSpace(repoPath) == "" {
		return roborev.AgentHealthResult{}, errors.New("repository path is required")
	}
	canonical, ok := roborev.CanonicalAgentName(agent)
	if !ok {
		return roborev.AgentHealthResult{}, errors.New("agent is not allowed")
	}
	checker, ok := s.HealthChecker.(RepositoryHealthChecker)
	if !ok {
		return s.Check(ctx, canonical)
	}
	result, err := checker.CheckAgentInRepo(ctx, repoPath, canonical)
	if err != nil {
		return roborev.AgentHealthResult{}, err
	}
	s.mu.Lock()
	if s.health == nil {
		s.health = make(map[string]configuredHealth)
	}
	s.health[canonical] = configuredHealth{result: result, set: true}
	s.mu.Unlock()
	return result, nil
}

// Health returns the last explicit health result without invoking a runner.
func (s *Service) Health(agent string) (roborev.AgentHealthResult, bool) {
	canonical, ok := roborev.CanonicalAgentName(agent)
	if !ok || s == nil {
		return roborev.AgentHealthResult{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.health[canonical]
	if !ok || !state.set {
		return roborev.AgentHealthResult{}, false
	}
	return state.result, true
}

func cursorIdentityUsable(parent context.Context, cursorPath, grokCommand string, resolver ExecutableResolver, probe IdentityProbeRunner) bool {
	if parent == nil {
		parent = context.Background()
	}
	if grokCommand == "" {
		grokCommand = "grok"
	}
	if grokPath, err := resolver.LookPath(grokCommand); err == nil && resolver.SameFile(cursorPath, grokPath) {
		return false
	}
	ctx, cancel := context.WithTimeout(parent, identityProbeTimeout)
	defer cancel()
	for _, arg := range []string{"--version", "-v"} {
		if ctx.Err() != nil {
			return false
		}
		result := probe.Run(ctx, cursorPath, []string{arg})
		if result.Truncated || ctx.Err() != nil {
			return false
		}
		if result.Err != nil || strings.TrimSpace(result.Output) == "" {
			continue
		}
		if strings.Contains(strings.ToLower(result.Output), "grok") {
			return false
		}
		return true
	}
	return false
}
