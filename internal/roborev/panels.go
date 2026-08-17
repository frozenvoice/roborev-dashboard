package roborev

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"
)

const PanelNone = "none"

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
	Resolve(string) (PanelCatalog, error)
}

type PanelResolver struct {
	GlobalConfigPath string
}

type panelConfigFile struct {
	Review panelReviewConfig `toml:"review"`
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

func NewPanelResolver() *PanelResolver { return &PanelResolver{} }

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

func loadPanelConfig(path string) (panelReviewConfig, error) {
	var file panelConfigFile
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return panelReviewConfig{}, nil
	} else if err != nil {
		return panelReviewConfig{}, fmt.Errorf("read panel config: %w", err)
	}
	if _, err := toml.DecodeFile(path, &file); err != nil {
		return panelReviewConfig{}, errors.New("panel config contains invalid TOML syntax or types")
	}
	return file.Review, nil
}

func (r *PanelResolver) Resolve(repoPath string) (PanelCatalog, error) {
	if strings.TrimSpace(repoPath) == "" {
		return PanelCatalog{}, errors.New("repository path is required")
	}
	localPath := filepath.Join(repoPath, ".roborev.toml")
	suppressGlobalNamed := linkedWorktreeWithoutDirectConfig(repoPath, localPath)
	globalPath, err := r.globalPath()
	if err != nil {
		return PanelCatalog{}, err
	}
	global, err := loadPanelConfig(globalPath)
	if err != nil {
		return PanelCatalog{}, fmt.Errorf("global RoboRev config: %w", err)
	}
	local, err := loadPanelConfig(localPath)
	if err != nil {
		return PanelCatalog{}, fmt.Errorf("repository RoboRev config: %w", err)
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

func builtinNoneCatalog() PanelCatalog {
	return PanelCatalog{Panels: []PanelInfo{{Name: PanelNone, Kind: "single", Members: []string{}, Origin: "builtin", Valid: true}}}
}

func linkedWorktreeWithoutDirectConfig(repoPath, localPath string) bool {
	if _, err := os.Stat(localPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return false
	}
	gitInfo, err := os.Stat(filepath.Join(repoPath, ".git"))
	return err == nil && !gitInfo.IsDir()
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
