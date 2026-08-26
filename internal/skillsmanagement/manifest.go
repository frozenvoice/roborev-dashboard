// Package skillsmanagement contains the server-side safety foundation for
// single-agent RoboRev bundled skill mutations. It does not execute RoboRev.
package skillsmanagement

const Version = "v0.64.0"

type AgentSpec struct {
	ID         string
	Label      string
	Prefix     string
	Invocation string
}

type SkillSpec struct {
	Name            string
	CodexPolicyFile string
}

var agentSpecs = [...]AgentSpec{
	{ID: "claude", Label: "Claude Code", Prefix: "/", Invocation: "claude"},
	{ID: "codex", Label: "Codex", Prefix: "$", Invocation: "codex"},
	{ID: "droid", Label: "Factory Droid", Prefix: "/", Invocation: "droid"},
	{ID: "grok", Label: "Grok Build", Prefix: "/", Invocation: "grok"},
}

// This is the exact embedded v0.64.0 catalog order. Every supported agent
// contains the ten SKILL.md files; Codex additionally contains openai.yaml.
var skillSpecs = [...]SkillSpec{
	{Name: "roborev-design-review", CodexPolicyFile: "agents/openai.yaml"},
	{Name: "roborev-design-review-branch", CodexPolicyFile: "agents/openai.yaml"},
	{Name: "roborev-fix", CodexPolicyFile: "agents/openai.yaml"},
	{Name: "roborev-lookahead-review", CodexPolicyFile: "agents/openai.yaml"},
	{Name: "roborev-lookahead-review-branch", CodexPolicyFile: "agents/openai.yaml"},
	{Name: "roborev-refine", CodexPolicyFile: "agents/openai.yaml"},
	{Name: "roborev-respond", CodexPolicyFile: "agents/openai.yaml"},
	{Name: "roborev-review", CodexPolicyFile: "agents/openai.yaml"},
	{Name: "roborev-review-branch", CodexPolicyFile: "agents/openai.yaml"},
	{Name: "roborev-snooze", CodexPolicyFile: "agents/openai.yaml"},
}

func Agents() []AgentSpec {
	return append([]AgentSpec(nil), agentSpecs[:]...)
}

func Agent(id string) (AgentSpec, bool) {
	for _, spec := range agentSpecs {
		if spec.ID == id {
			return spec, true
		}
	}
	return AgentSpec{}, false
}

func Skills() []SkillSpec {
	return append([]SkillSpec(nil), skillSpecs[:]...)
}

func SkillNames() []string {
	names := make([]string, 0, len(skillSpecs))
	for _, spec := range skillSpecs {
		names = append(names, spec.Name)
	}
	return names
}

func SkillCount() int { return len(skillSpecs) }

func IsSupportedAgent(id string) bool {
	_, ok := Agent(id)
	return ok
}

// ManagedRelativePaths is the exact path set the custom-path v0.64 installer
// can write for one selected agent. It intentionally excludes legacy cleanup
// and all unrelated user files/directories.
func ManagedRelativePaths(agent string) ([]string, error) {
	if !IsSupportedAgent(agent) {
		return nil, ErrUnsupportedAgent
	}
	paths := make([]string, 0, len(skillSpecs)+len(skillSpecs))
	for _, skill := range skillSpecs {
		paths = append(paths, skill.Name+"/SKILL.md")
		if agent == "codex" && skill.CodexPolicyFile != "" {
			paths = append(paths, skill.Name+"/"+skill.CodexPolicyFile)
		}
	}
	return paths, nil
}

func ManagedSkillDirectories(agent string) ([]string, error) {
	if !IsSupportedAgent(agent) {
		return nil, ErrUnsupportedAgent
	}
	dirs := make([]string, 0, len(skillSpecs)+1)
	for _, skill := range skillSpecs {
		dirs = append(dirs, skill.Name)
		if agent == "codex" {
			dirs = append(dirs, skill.Name+"/agents")
		}
	}
	return dirs, nil
}
