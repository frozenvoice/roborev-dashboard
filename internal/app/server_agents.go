package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

type configuredAgentDTO struct {
	Name            string `json:"name"`
	Agent           string `json:"agent"`
	Model           string `json:"model,omitempty"`
	Reasoning       string `json:"reasoning,omitempty"`
	Command         string `json:"command"`
	CommandSource   string `json:"commandSource,omitempty"`
	Detected        bool   `json:"detected"`
	DetectionReason string `json:"detectionReason,omitempty"`
	ResolvedPath    string `json:"resolvedPath,omitempty"`
	Health          string `json:"health"`
}

type agentsDTO struct {
	RoboRevVersion string               `json:"roborevVersion,omitempty"`
	Configured     []configuredAgentDTO `json:"configured"`
}

func (s *Server) agentStatuses(w http.ResponseWriter, r *http.Request) {
	if s.Agents == nil {
		writeError(w, http.StatusServiceUnavailable, "agent status is unavailable", "agents")
		return
	}
	if err := requireV064(s.RoboRev, r.Context(), "agent discovery"); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error(), "agents")
		return
	}
	repo, err := s.Registry.Resolve("")
	if err != nil {
		writeError(w, http.StatusConflict, err.Error(), "repository")
		return
	}
	statuses, err := s.Agents.Discover(r.Context(), repo)
	if err != nil {
		writeError(w, http.StatusConflict, "agent configuration is unavailable: "+err.Error(), "agents")
		return
	}
	response := agentsDTO{Configured: []configuredAgentDTO{}}
	installation := s.RoboRev.Installation(r.Context())
	response.RoboRevVersion = installation.Version
	for _, status := range statuses {
		for _, configured := range status.Subagents {
			health := string(status.Health)
			if health == "" {
				health = "not_checked"
			}
			reason := "Executable was not found."
			if status.Available {
				reason = "Executable detected without sending an agent request."
			} else if commandIsCustom(status.Name, status.Command) {
				reason = "Custom command could not be resolved or identified safely."
			}
			response.Configured = append(response.Configured, configuredAgentDTO{
				Name: configured.Name, Agent: configured.CanonicalAgent, Model: configured.Model,
				Reasoning: configured.Reasoning, Command: status.Command,
				CommandSource: configured.Origin, Detected: status.Available,
				DetectionReason: reason, ResolvedPath: status.Path, Health: health,
			})
		}
		for _, configured := range status.UnresolvedSubagents {
			response.Configured = append(response.Configured, configuredAgentDTO{
				Name: configured.Name, Agent: "inherited/default", Model: configured.Model,
				Reasoning: configured.Reasoning, Command: "Detection unavailable",
				CommandSource: configured.Origin, Detected: false,
				DetectionReason: "The effective agent is inherited and cannot be resolved from the safe configuration subset.",
				Health:          "not_checked",
			})
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func commandIsCustom(agent, command string) bool {
	for _, descriptor := range roborev.AgentDescriptors() {
		if descriptor.Name == agent {
			return strings.TrimSpace(command) != descriptor.DefaultCommand
		}
	}
	return false
}

type agentCheckBody struct {
	Agent string `json:"agent"`
}

func (s *Server) checkAgent(w http.ResponseWriter, r *http.Request) {
	if s.Agents == nil {
		writeError(w, http.StatusServiceUnavailable, "agent health check is unavailable", "agents")
		return
	}
	if err := requireV064(s.RoboRev, r.Context(), "agent health check"); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error(), "agents")
		return
	}
	var body agentCheckBody
	if !decodeBody(w, r, &body) {
		return
	}
	if _, ok := roborev.CanonicalAgentName(body.Agent); !ok {
		writeError(w, http.StatusBadRequest, "agent is not allowed", "agent")
		return
	}
	repo, err := s.Registry.Resolve("")
	if err != nil {
		writeError(w, http.StatusConflict, err.Error(), "repository")
		return
	}
	result, err := s.Agents.CheckInRepo(r.Context(), repo, body.Agent)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "agent-check")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func requireV064(service roborev.Service, ctx context.Context, action string) error {
	if service == nil {
		return fmt.Errorf("%s is unavailable: RoboRev service is not configured", action)
	}
	installation := service.Installation(ctx)
	if !installation.Installed || !roborevcompat.IsSupportedVersion(installation.Version) {
		version := strings.TrimSpace(installation.Version)
		if version == "" {
			version = "unknown"
		}
		return fmt.Errorf("%s requires RoboRev v0.64.0 (detected %s)", action, version)
	}
	return nil
}
