package roborev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"
)

const PanelNone = "none"

// maxPanelConfigBytes keeps repository-controlled and global panel discovery
// bounded while leaving ample room for the small TOML configuration RoboRev
// actually consumes.
const maxPanelConfigBytes = 1 << 20

type PanelInfo struct {
	Name                 string   `json:"name"`
	Kind                 string   `json:"kind"`
	Members              []string `json:"members"`
	SynthesisAgent       string   `json:"synthesisAgent,omitempty"`
	SynthesisBackupAgent string   `json:"synthesisBackupAgent,omitempty"`
	Origin               string   `json:"origin"`
	Valid                bool     `json:"valid"`
	Error                string   `json:"error,omitempty"`
}

type PanelCatalog struct {
	Panels []PanelInfo
}

func (c PanelCatalog) Find(name string) (PanelInfo, bool) {
	for _, panel := range c.Panels {
		if panel.Name == name && panel.Valid {
			return panel, true
		}
	}
	return PanelInfo{}, false
}

type PanelCatalogResolver interface {
	Resolve(context.Context, string) (PanelCatalog, error)
}

// GitMetadataResolver supplies the small amount of Git metadata needed to
// distinguish a linked worktree from other checkout layouts whose .git entry
// is a file. Implementations must execute typed Git arguments without a shell.
type GitMetadataResolver interface {
	IsLinkedWorktree(context.Context, string) (bool, error)
}

type PanelResolver struct {
	GlobalConfigPath string
	Metadata         GitMetadataResolver
}

type panelConfigFile struct {
	Review        panelReviewConfig `toml:"review"`
	CodexCmd      string            `toml:"codex_cmd"`
	ClaudeCodeCmd string            `toml:"claude_code_cmd"`
	GeminiCmd     string            `toml:"gemini_cmd"`
	OpenCodeCmd   string            `toml:"opencode_cmd"`
	CursorCmd     string            `toml:"cursor_cmd"`
	PiCmd         string            `toml:"pi_cmd"`
	GrokCmd       string            `toml:"grok_cmd"`
}

type panelAgentCommands struct {
	Codex      string
	ClaudeCode string
	Gemini     string
	OpenCode   string
	Cursor     string
	Pi         string
	Grok       string
}

// ConfiguredSubagent is the safe projection of one [review.subagents.NAME]
// entry.  Provider, instructions, timeout and all other raw TOML fields are
// intentionally omitted because they can contain secrets or executable
// policy. Agent preserves the spelling from config (including an alias),
// while CanonicalAgent supplies the v0.64 identity used by health checks.
type ConfiguredSubagent struct {
	Name                 string `json:"name"`
	Agent                string `json:"agent"`
	CanonicalAgent       string `json:"canonicalAgent,omitempty"`
	Model                string `json:"model,omitempty"`
	Reasoning            string `json:"reasoning,omitempty"`
	Origin               string `json:"origin"`
	DetectionUnavailable bool   `json:"detectionUnavailable,omitempty"`
}

// AgentConfiguration contains only safe, machine-readable configuration
// needed by agent discovery. Command overrides come from the global config;
// repository configs never override command executable selection.
type AgentConfiguration struct {
	Commands  map[string]string    `json:"commands,omitempty"`
	Subagents []ConfiguredSubagent `json:"subagents,omitempty"`
}

type panelReviewConfig struct {
	DefaultPanel string                       `toml:"default_panel"`
	HookPanel    string                       `toml:"hook_review_panel"`
	Subagents    map[string]panelSubagentSpec `toml:"subagents"`
	Panels       map[string]panelSpec         `toml:"panels"`
}

type panelSubagentSpec struct {
	Agent        string `toml:"agent"`
	Model        string `toml:"model"`
	Provider     string `toml:"provider"`
	Reasoning    string `toml:"reasoning"`
	ReviewType   string `toml:"review_type"`
	Instructions string `toml:"instructions"`
	AllowFailure bool   `toml:"allow_failure"`
	Timeout      string `toml:"timeout"`
}

type panelSpec struct {
	Members              []string `toml:"members"`
	SynthesisAgent       string   `toml:"synthesis_agent"`
	SynthesisModel       string   `toml:"synthesis_model"`
	SynthesisBackupAgent string   `toml:"synthesis_backup_agent"`
	SynthesisBackupModel string   `toml:"synthesis_backup_model"`
}

func NewPanelResolver(metadata ...GitMetadataResolver) *PanelResolver {
	resolver := &PanelResolver{}
	if len(metadata) > 0 {
		resolver.Metadata = metadata[0]
	}
	return resolver
}

func (r *PanelResolver) globalPath() (string, error) {
	if r != nil && strings.TrimSpace(r.GlobalConfigPath) != "" {
		return r.GlobalConfigPath, nil
	}
	if dataDir := os.Getenv("ROBOREV_DATA_DIR"); dataDir != "" {
		return filepath.Join(dataDir, "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve RoboRev home: %w", err)
	}
	return filepath.Join(home, ".roborev", "config.toml"), nil
}

func loadPanelConfig(path string) (panelReviewConfig, bool, error) {
	review, _, present, err := loadPanelConfigWithCommands(path)
	return review, present, err
}

func loadPanelConfigWithCommands(path string) (panelReviewConfig, panelAgentCommands, bool, error) {
	var file panelConfigFile
	handle, err := openPanelConfigNoFollow(path)
	if errors.Is(err, os.ErrNotExist) || os.IsNotExist(err) {
		return panelReviewConfig{}, panelAgentCommands{}, false, nil
	}
	if err != nil {
		return panelReviewConfig{}, panelAgentCommands{}, true, errors.New("panel config could not be opened")
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		return panelReviewConfig{}, panelAgentCommands{}, true, errors.New("panel config metadata could not be read")
	}
	if !info.Mode().IsRegular() {
		return panelReviewConfig{}, panelAgentCommands{}, true, errors.New("panel config must be a regular file")
	}
	if info.Size() < 0 || info.Size() > maxPanelConfigBytes {
		return panelReviewConfig{}, panelAgentCommands{}, true, errors.New("panel config exceeds the maximum size")
	}
	data, err := io.ReadAll(io.LimitReader(handle, maxPanelConfigBytes+1))
	if err != nil {
		return panelReviewConfig{}, panelAgentCommands{}, true, errors.New("panel config could not be read")
	}
	if len(data) > maxPanelConfigBytes {
		return panelReviewConfig{}, panelAgentCommands{}, true, errors.New("panel config exceeds the maximum size")
	}
	if _, err := toml.Decode(string(data), &file); err != nil {
		return panelReviewConfig{}, panelAgentCommands{}, true, errors.New("panel config contains invalid TOML syntax or types")
	}
	return file.Review, panelAgentCommands{
		Codex: file.CodexCmd, ClaudeCode: file.ClaudeCodeCmd, Gemini: file.GeminiCmd,
		OpenCode: file.OpenCodeCmd, Cursor: file.CursorCmd, Pi: file.PiCmd, Grok: file.GrokCmd,
	}, true, nil
}

func (r *PanelResolver) Resolve(ctx context.Context, repoPath string) (PanelCatalog, error) {
	if strings.TrimSpace(repoPath) == "" {
		return PanelCatalog{}, errors.New("repository path is required")
	}
	localPath := filepath.Join(repoPath, ".roborev.toml")
	local, localPresent, err := loadPanelConfig(localPath)
	if err != nil {
		return PanelCatalog{}, fmt.Errorf("repository RoboRev config: %w", err)
	}
	suppressGlobalNamed := false
	if !localPresent {
		var err error
		suppressGlobalNamed, err = linkedWorktreeWithoutDirectConfig(ctx, repoPath, r.Metadata)
		if err != nil {
			return PanelCatalog{}, err
		}
	}
	globalPath, err := r.globalPath()
	if err != nil {
		return PanelCatalog{}, err
	}
	global, _, err := loadPanelConfig(globalPath)
	if err != nil {
		return PanelCatalog{}, fmt.Errorf("global RoboRev config: %w", err)
	}
	subagents := make(map[string]panelSubagentSpec, len(global.Subagents)+len(local.Subagents))
	for name, spec := range global.Subagents {
		subagents[name] = spec
	}
	for name, spec := range local.Subagents {
		subagents[name] = spec
	}
	panels := make(map[string]panelSpec, len(global.Panels)+len(local.Panels))
	origins := make(map[string]string, len(global.Panels)+len(local.Panels))
	for name, spec := range global.Panels {
		if name == PanelNone {
			continue
		}
		panels[name], origins[name] = spec, "global"
	}
	for name, spec := range local.Panels {
		if name == PanelNone {
			continue
		}
		panels[name], origins[name] = spec, "local"
	}
	defaultPanel := global.DefaultPanel
	if local.DefaultPanel != "" {
		defaultPanel = local.DefaultPanel
	}
	hookPanel := global.HookPanel
	if local.HookPanel != "" {
		hookPanel = local.HookPanel
	}
	for _, reference := range []struct{ field, name string }{{"default_panel", defaultPanel}, {"hook_review_panel", hookPanel}} {
		if reference.name != "" && reference.name != PanelNone {
			if _, ok := panels[reference.name]; !ok {
				return PanelCatalog{}, fmt.Errorf("%s references undefined panel", reference.field)
			}
		}
	}

	names := make([]string, 0, len(panels))
	for name := range panels {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]PanelInfo, 0, len(names)+1)
	for _, name := range names {
		spec := panels[name]
		if err := validateConfiguredPanel(name, spec, subagents); err != nil {
			return PanelCatalog{}, err
		}
		result = append(result, PanelInfo{Name: name, Kind: "panel", Members: append([]string(nil), spec.Members...), SynthesisAgent: spec.SynthesisAgent, SynthesisBackupAgent: spec.SynthesisBackupAgent, Origin: origins[name], Valid: true})
	}
	result = append(result, PanelInfo{Name: PanelNone, Kind: "single", Members: []string{}, Origin: "builtin", Valid: true})
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	if suppressGlobalNamed {
		// RoboRev may inherit an untracked machine-local config from the main
		// checkout. The global config was still fully parsed and validated above;
		// only its named results are suppressed because their effective upstream
		// definitions cannot be reproduced safely here.
		return builtinNoneCatalog(), nil
	}
	return PanelCatalog{Panels: result}, nil
}

// ResolveAgentConfiguration reads the bounded global/repository config files
// and returns the safe agent projection used by the dashboard. Repository
// subagent entries replace global entries as whole TOML entries. Executable
// command overrides are deliberately read from the global file only; local
// command fields are ignored even when present.
func (r *PanelResolver) ResolveAgentConfiguration(ctx context.Context, repoPath string) (AgentConfiguration, error) {
	_ = ctx // kept in the resolver contract for parity with panel discovery
	if r == nil {
		r = &PanelResolver{}
	}
	globalPath, err := r.globalPath()
	if err != nil {
		return AgentConfiguration{}, err
	}
	globalReview, globalCommands, _, err := loadPanelConfigWithCommands(globalPath)
	if err != nil {
		return AgentConfiguration{}, fmt.Errorf("global RoboRev config: %w", err)
	}

	var localReview panelReviewConfig
	if strings.TrimSpace(repoPath) != "" {
		localPath := filepath.Join(repoPath, ".roborev.toml")
		localReview, _, _, err = loadPanelConfigWithCommands(localPath)
		if err != nil {
			return AgentConfiguration{}, fmt.Errorf("repository RoboRev config: %w", err)
		}
	}

	type sourceSpec struct {
		spec   panelSubagentSpec
		origin string
	}
	merged := make(map[string]sourceSpec, len(globalReview.Subagents)+len(localReview.Subagents))
	for name, spec := range globalReview.Subagents {
		merged[name] = sourceSpec{spec: spec, origin: "global"}
	}
	for name, spec := range localReview.Subagents {
		merged[name] = sourceSpec{spec: spec, origin: "local"}
	}

	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)
	result := AgentConfiguration{
		Commands:  make(map[string]string),
		Subagents: make([]ConfiguredSubagent, 0, len(names)),
	}
	for _, name := range names {
		entry := merged[name]
		agent := strings.TrimSpace(entry.spec.Agent)
		if !safeAgentConfigValue(name) {
			continue
		}
		model := safeAgentConfigField(entry.spec.Model, 512)
		reasoning := safeAgentConfigField(entry.spec.Reasoning, 128)
		if agent == "" {
			result.Subagents = append(result.Subagents, ConfiguredSubagent{
				Name: name, Model: model, Reasoning: reasoning, Origin: entry.origin,
				DetectionUnavailable: true,
			})
			continue
		}
		canonical, ok := CanonicalAgentName(agent)
		if !ok || !safeAgentConfigValue(agent) {
			// Unknown names may be dynamic ACP entries. They are not exposed
			// as dashboard agents because their command/args are user-defined.
			continue
		}
		result.Subagents = append(result.Subagents, ConfiguredSubagent{
			Name: name, Agent: agent, CanonicalAgent: canonical,
			Model: model, Reasoning: reasoning, Origin: entry.origin,
		})
	}
	for key, value := range map[string]string{
		"codex_cmd": globalCommands.Codex, "claude_code_cmd": globalCommands.ClaudeCode,
		"gemini_cmd": globalCommands.Gemini, "opencode_cmd": globalCommands.OpenCode,
		"cursor_cmd": globalCommands.Cursor, "pi_cmd": globalCommands.Pi, "grok_cmd": globalCommands.Grok,
	} {
		value = strings.TrimSpace(value)
		if value != "" && safeAgentConfigField(value, 4096) != "" {
			result.Commands[key] = value
		}
	}
	if len(result.Commands) == 0 {
		result.Commands = nil
	}
	if len(result.Subagents) == 0 {
		result.Subagents = nil
	}
	return result, nil
}

func safeAgentConfigValue(value string) bool {
	return strings.TrimSpace(value) == value && value != "" && len(value) <= 128 && !strings.ContainsAny(value, "\x00\r\n")
}

func safeAgentConfigField(value string, max int) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > max || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	return value
}

func builtinNoneCatalog() PanelCatalog {
	return PanelCatalog{Panels: []PanelInfo{{Name: PanelNone, Kind: "single", Members: []string{}, Origin: "builtin", Valid: true}}}
}

func linkedWorktreeWithoutDirectConfig(ctx context.Context, repoPath string, metadata GitMetadataResolver) (bool, error) {
	entry, err := os.Lstat(filepath.Join(repoPath, ".git"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("repository Git metadata could not be read")
	}
	if entry.IsDir() {
		return false, nil
	}
	if !entry.Mode().IsRegular() {
		return false, errors.New("repository Git metadata has unsupported layout")
	}
	if metadata == nil {
		return false, errors.New("repository Git metadata is unavailable")
	}
	linked, err := metadata.IsLinkedWorktree(ctx, repoPath)
	if err != nil {
		return false, errors.New("repository Git metadata could not be resolved")
	}
	return linked, nil
}

func validateConfiguredPanel(name string, panel panelSpec, subagents map[string]panelSubagentSpec) error {
	if err := validatePanelToken("panel name", name); err != nil {
		return err
	}
	if len(panel.Members) == 0 {
		return fmt.Errorf("panel %q has no members", name)
	}
	for field, value := range map[string]string{"synthesis_agent": panel.SynthesisAgent, "synthesis_backup_agent": panel.SynthesisBackupAgent} {
		if value != "" {
			if err := validatePanelToken(field, value); err != nil {
				return fmt.Errorf("panel %q: %w", name, err)
			}
		}
	}
	for _, member := range panel.Members {
		if err := validatePanelToken("subagent name", member); err != nil {
			return fmt.Errorf("panel %q: %w", name, err)
		}
		spec, ok := subagents[member]
		if !ok {
			return fmt.Errorf("panel %q references undefined subagent %q", name, member)
		}
		if err := validateSubagent(member, spec); err != nil {
			return fmt.Errorf("panel %q: %w", name, err)
		}
	}
	return nil
}

func validatePanelToken(field, value string) error {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || len(value) > 128 {
		return fmt.Errorf("invalid %s", field)
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return fmt.Errorf("invalid %s", field)
		}
	}
	return nil
}

func validateSubagent(name string, spec panelSubagentSpec) error {
	if value := spec.ReviewType; value != "" {
		switch value {
		case "default", "general", "review", "security", "design", "lookahead":
		default:
			return fmt.Errorf("subagent %q has invalid review_type", name)
		}
	}
	if value := strings.ToLower(strings.TrimSpace(spec.Reasoning)); value != "" {
		switch value {
		case "maximum", "max", "xhigh", "thorough", "high", "medium", "standard", "fast", "low":
		default:
			return fmt.Errorf("subagent %q has invalid reasoning", name)
		}
	}
	if spec.Timeout != "" {
		duration, err := time.ParseDuration(spec.Timeout)
		if err != nil || duration <= 0 {
			return fmt.Errorf("subagent %q has invalid timeout", name)
		}
	}
	return nil
}
