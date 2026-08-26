package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/skillsmanagement"
)

const (
	skillsManagementPreviewTTL      = 3 * time.Minute
	skillsManagementPreviewMax      = 128
	skillsManagementPreviewPath     = "<agent-skills-directory>"
	skillsManagementReadbackTimeout = 10 * time.Second
)

type skillsManagementPreviewBody struct {
	Agent string `json:"agent"`
}

type skillsManagementExecuteBody struct {
	Token string `json:"token"`
}

type skillsPreviewRecord struct {
	Agent     string
	Binding   roborev.GitHookBinding
	Target    skillsmanagement.Target
	Snapshot  skillsmanagement.TreeSnapshot
	PreCounts skillsmanagement.ReadinessCounts
	Expires   time.Time
}

type skillsPreviewStore struct {
	mu      sync.Mutex
	pending map[string]skillsPreviewRecord
}

func newSkillsPreviewStore() *skillsPreviewStore {
	return &skillsPreviewStore{pending: make(map[string]skillsPreviewRecord)}
}

func (s *skillsPreviewStore) put(record skillsPreviewRecord) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.expireLocked(now)
	for len(s.pending) >= skillsManagementPreviewMax {
		var oldest string
		var oldestExpiry time.Time
		for candidate, pending := range s.pending {
			if oldest == "" || pending.Expires.Before(oldestExpiry) {
				oldest, oldestExpiry = candidate, pending.Expires
			}
		}
		if oldest == "" {
			break
		}
		delete(s.pending, oldest)
	}
	s.pending[token] = record
	return token, nil
}

func (s *skillsPreviewStore) claim(token string) (skillsPreviewRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(time.Now())
	record, ok := s.pending[token]
	if ok {
		delete(s.pending, token)
	}
	return record, ok
}

func (s *skillsPreviewStore) expireLocked(now time.Time) {
	for token, record := range s.pending {
		if now.After(record.Expires) {
			delete(s.pending, token)
		}
	}
}

type skillsManagementCommandDTO struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type skillsManagementPreviewDTO struct {
	Token     string                     `json:"token"`
	Agent     string                     `json:"agent"`
	Action    string                     `json:"action"`
	Summary   string                     `json:"summary"`
	Command   skillsManagementCommandDTO `json:"command"`
	ExpiresAt time.Time                  `json:"expiresAt"`
}

type skillsManagementResultDTO struct {
	Status    string `json:"status"`
	Agent     string `json:"agent"`
	ErrorCode string `json:"errorCode,omitempty"`
}

func (s *Server) skillsPlanner() skillsmanagement.Planner {
	fs := s.SkillsManagementFS
	if fs == nil {
		fs = pathsafety.OSFileSystem{}
	}
	return skillsmanagement.NewPlanner(fs, s.SkillsManagementHome)
}

func (s *Server) skillsTargetEnv() []string {
	if s.SkillsManagementEnv != nil {
		return append([]string(nil), s.SkillsManagementEnv()...)
	}
	return roborev.SkillsTargetEnvFromProcess()
}

func skillsManagementTransport(service roborev.Service) (roborev.SkillsManagementTransport, bool) {
	transport, ok := service.(roborev.SkillsManagementTransport)
	return transport, ok
}

func (s *Server) skillsManagementPreview(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeHookMutation(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body skillsManagementPreviewBody
	if !decodeUniqueBody(w, r, &body, "agent") {
		return
	}
	if !skillsmanagement.IsSupportedAgent(body.Agent) {
		writeError(w, http.StatusBadRequest, "unsupported agent", "request")
		return
	}
	transport, ok := skillsManagementTransport(s.RoboRev)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "skills management is unavailable", "skills-management")
		return
	}
	readiness, err := transport.SkillsReadiness(r.Context())
	if err != nil || roborev.ValidateSkillsReadiness(readiness) != nil {
		writeError(w, http.StatusServiceUnavailable, "skills readiness is unavailable", "skills-readiness")
		return
	}
	preCounts, ok := skillsReadinessCounts(readiness, body.Agent)
	if !ok {
		writeError(w, http.StatusConflict, "skills action is unavailable", "action")
		return
	}
	if readiness.Agents[skillsReadinessAgentIndex(body.Agent)].Status != string(roborev.SkillsReadinessNeedsInstallUpdate) {
		writeError(w, http.StatusConflict, "skills action is unavailable", "action")
		return
	}
	planner := s.skillsPlanner()
	target, err := planner.Resolver.Resolve(body.Agent, s.skillsTargetEnv())
	if err != nil {
		writeError(w, http.StatusConflict, "skills target is unavailable", "target")
		return
	}
	if _, err := planner.Resolver.ValidateConfigRoot(target); err != nil {
		writeError(w, http.StatusConflict, "skills target is unavailable", "target")
		return
	}
	snapshot, err := planner.CaptureStable(target)
	if err != nil {
		writeError(w, http.StatusConflict, "skills target is unavailable", "target")
		return
	}
	binding, err := transport.ProbeSkillsBinding(r.Context())
	if err != nil || !validSkillsBinding(binding) {
		writeError(w, http.StatusServiceUnavailable, "RoboRev v0.64.0 binding is unavailable", "version")
		return
	}
	if s.skillsPreviews == nil {
		writeError(w, http.StatusServiceUnavailable, "skills management is unavailable", "skills-management")
		return
	}
	record := skillsPreviewRecord{Agent: body.Agent, Binding: binding, Target: target, Snapshot: snapshot, PreCounts: preCounts, Expires: time.Now().Add(skillsManagementPreviewTTL)}
	token, err := s.skillsPreviews.put(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "prepare skills action", "skills-management")
		return
	}
	// The browser receives only a fixed semantic placeholder. The real target
	// and binding remain in the opaque server-side token record.
	writeJSON(w, http.StatusOK, skillsManagementPreviewDTO{
		Token: token, Agent: body.Agent, Action: "install-update", Summary: skillsManagementSummary(body.Agent),
		Command: skillsManagementCommandDTO{Executable: "roborev", Args: []string{"skills", "install", "--path", skillsManagementPreviewPath, "--agent", body.Agent}}, ExpiresAt: record.Expires,
	})
}

func (s *Server) skillsManagementExecute(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeHookMutation(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body skillsManagementExecuteBody
	if !decodeUniqueBody(w, r, &body, "token") {
		return
	}
	if body.Token == "" || strings.TrimSpace(body.Token) != body.Token {
		writeError(w, http.StatusBadRequest, "confirmation token is required", "request")
		return
	}
	if s.skillsPreviews == nil {
		writeError(w, http.StatusServiceUnavailable, "skills management is unavailable", "skills-management")
		return
	}
	record, ok := s.skillsPreviews.claim(body.Token)
	if !ok {
		writeError(w, http.StatusConflict, "confirmation token is missing or expired", "stale")
		return
	}
	transport, ok := skillsManagementTransport(s.RoboRev)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "skills management is unavailable", "skills-management")
		return
	}
	binding, err := transport.ProbeSkillsBinding(r.Context())
	if err != nil || !validSkillsBinding(binding) || binding != record.Binding {
		writeError(w, http.StatusConflict, "skills preview is stale", "stale")
		return
	}
	if s.Gate == nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	release, err := s.Gate.AcquireFix()
	if err != nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	defer release()
	planner := s.skillsPlanner()
	target, err := planner.Resolver.Resolve(record.Agent, s.skillsTargetEnv())
	if err != nil || !sameSkillsTarget(target, record.Target) {
		writeError(w, http.StatusConflict, "skills preview is stale", "stale")
		return
	}
	if _, err := planner.Revalidate(record.Snapshot, target); err != nil {
		writeError(w, http.StatusConflict, "skills preview is stale", "stale")
		return
	}
	readiness, err := transport.SkillsReadiness(r.Context())
	if err != nil || roborev.ValidateSkillsReadiness(readiness) != nil {
		writeError(w, http.StatusConflict, "skills preview is stale", "stale")
		return
	}
	_, ok = skillsReadinessCounts(readiness, record.Agent)
	if !ok || readiness.Agents[skillsReadinessAgentIndex(record.Agent)].Status != string(roborev.SkillsReadinessNeedsInstallUpdate) {
		writeError(w, http.StatusConflict, "skills preview is stale", "stale")
		return
	}
	binding, err = transport.ProbeSkillsBinding(r.Context())
	if err != nil || !validSkillsBinding(binding) || binding != record.Binding {
		writeError(w, http.StatusConflict, "skills preview is stale", "stale")
		return
	}
	execution, runErr := transport.RunSkillsInstall(r.Context(), record.Agent, record.Target.SkillsDir, binding)
	if !execution.Started {
		result := skillsManagementResultDTO{Status: "failed-unverified", Agent: record.Agent, ErrorCode: "cli-not-started"}
		writeJSON(w, http.StatusOK, result)
		return
	}
	result := s.skillsDetachedResult(record, execution, runErr, transport, planner)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) skillsDetachedResult(record skillsPreviewRecord, execution roborev.GitHookExecution, runErr error, transport roborev.SkillsManagementTransport, planner skillsmanagement.Planner) skillsManagementResultDTO {
	result := skillsManagementResultDTO{Status: "failed-unverified", Agent: record.Agent}
	readCtx, cancel := context.WithTimeout(context.Background(), skillsManagementReadbackTimeout)
	defer cancel()
	postReadiness, readinessErr := transport.SkillsReadiness(readCtx)
	postTarget, targetErr := planner.Resolver.Resolve(record.Agent, s.skillsTargetEnv())
	if targetErr != nil || !sameSkillsTarget(postTarget, record.Target) {
		result.ErrorCode = "target-drift"
		return result
	}
	postSnapshot, snapshotErr := planner.Capture(postTarget)
	postBinding, bindingErr := transport.ProbeSkillsBinding(readCtx)

	if bindingErr != nil || postBinding != record.Binding {
		result.ErrorCode = "binding-drift"
		return result
	}
	if snapshotErr != nil {
		if errors.Is(snapshotErr, skillsmanagement.ErrLegacyPresent) {
			result.ErrorCode = "target-drift"
		} else {
			result.ErrorCode = "readback-unavailable"
		}
		return result
	}
	if err := skillsmanagement.ValidatePostState(record.Snapshot, postSnapshot); err != nil {
		result.ErrorCode = "target-drift"
		return result
	}
	if readinessErr != nil || roborev.ValidateSkillsReadiness(postReadiness) != nil {
		result.ErrorCode = "readback-unavailable"
		return result
	}
	postCounts, ok := skillsReadinessCounts(postReadiness, record.Agent)
	if !ok {
		result.ErrorCode = "readback-unavailable"
		return result
	}
	cliFailed := runErr != nil || execution.ExitCode != 0
	postStatus := postReadiness.Agents[skillsReadinessAgentIndex(record.Agent)].Status
	if postStatus == string(roborev.SkillsReadinessCurrent) {
		result.Status = "verified-success"
		if cliFailed {
			result.ErrorCode = "cli-failed"
		}
		return result
	}
	if postStatus == string(roborev.SkillsReadinessNeedsInstallUpdate) && skillsmanagement.HasMeasurableImprovement(record.PreCounts, postCounts) {
		result.Status = "partial"
		if cliFailed {
			result.ErrorCode = "cli-failed"
		}
		return result
	}
	if postStatus != string(roborev.SkillsReadinessNeedsInstallUpdate) {
		result.ErrorCode = "readback-not-current"
	} else if cliFailed {
		result.ErrorCode = "cli-failed"
	} else {
		result.ErrorCode = "no-improvement"
	}
	return result
}

func skillsReadinessCounts(readiness roborev.SkillsReadiness, agent string) (skillsmanagement.ReadinessCounts, bool) {
	for _, candidate := range readiness.Agents {
		if candidate.Agent == agent {
			return skillsmanagement.ReadinessCounts{Current: candidate.Current, Outdated: candidate.Outdated, Missing: candidate.Missing}, true
		}
	}
	return skillsmanagement.ReadinessCounts{}, false
}

func skillsReadinessAgentIndex(agent string) int {
	switch agent {
	case "codex":
		return 0
	case "claude":
		return 1
	case "grok":
		return 2
	case "droid":
		return 3
	default:
		return -1
	}
}

func skillsManagementSummary(agent string) string {
	spec, ok := skillsmanagement.Agent(agent)
	if !ok {
		return "Install or update RoboRev skills"
	}
	return "Install or update RoboRev skills for " + spec.Label
}

func sameSkillsTarget(left, right skillsmanagement.Target) bool {
	return left.Agent == right.Agent && gitx.SameRepositoryPath(left.ConfigDir, right.ConfigDir) && gitx.SameRepositoryPath(left.SkillsDir, right.SkillsDir)
}

func validSkillsBinding(binding roborev.GitHookBinding) bool {
	return binding.Version == skillsmanagement.Version && strings.TrimSpace(binding.Binary) != "" && binding.Generation != 0 && strings.TrimSpace(binding.FileID) != ""
}
