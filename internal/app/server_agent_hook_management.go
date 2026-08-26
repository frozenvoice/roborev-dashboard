package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/agenthookmanagement"
	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	agentHookManagementPreviewTTL = 3 * time.Minute
	agentHookManagementPreviewMax = 128
	agentHookPreviewBinary        = "<current-v0.64.0-binary>"
)

type agentHookPreviewBody struct {
	Agent string `json:"agent"`
}

type agentHookExecuteBody struct {
	Token string `json:"token"`
}

type agentHookPreviewRecord struct {
	Agent    string
	Binding  roborev.GitHookBinding
	DryRun   roborev.AgentHookDryRun
	Snapshot agenthookmanagement.Snapshot
	Expires  time.Time
}

type agentHookPreviewStore struct {
	mu      sync.Mutex
	pending map[string]agentHookPreviewRecord
}

func newAgentHookPreviewStore() *agentHookPreviewStore {
	return &agentHookPreviewStore{pending: make(map[string]agentHookPreviewRecord)}
}

func (s *agentHookPreviewStore) put(record agentHookPreviewRecord) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.expireLocked(now)
	for len(s.pending) >= agentHookManagementPreviewMax {
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

func (s *agentHookPreviewStore) claim(token string) (agentHookPreviewRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(time.Now())
	record, ok := s.pending[token]
	if ok {
		delete(s.pending, token)
	}
	return record, ok
}

func (s *agentHookPreviewStore) expireLocked(now time.Time) {
	for token, record := range s.pending {
		if now.After(record.Expires) {
			delete(s.pending, token)
		}
	}
}

type agentHookCommandDTO struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type agentHookPreviewDTO struct {
	Token     string              `json:"token"`
	Agent     string              `json:"agent"`
	Action    string              `json:"action"`
	Summary   string              `json:"summary"`
	Command   agentHookCommandDTO `json:"command"`
	ExpiresAt time.Time           `json:"expiresAt"`
}

type agentHookResultDTO struct {
	Status    string `json:"status"`
	Agent     string `json:"agent"`
	ErrorCode string `json:"errorCode,omitempty"`
}

func (s *Server) agentHookManagementPreview(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeHookMutation(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body agentHookPreviewBody
	if !decodeUniqueBody(w, r, &body, "agent") {
		return
	}
	agent := body.Agent
	if !agentHookManagementAgentAllowed(agent) {
		writeError(w, http.StatusBadRequest, "unsupported agent", "request")
		return
	}
	transport, ok := s.RoboRev.(roborev.AgentHookManagementTransport)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "agent hook management is unavailable", "agent-hook-management")
		return
	}
	binding, err := transport.ProbeAgentHookBinding(r.Context())
	if err != nil || !roborevcompat.IsSupportedVersion(binding.Version) {
		writeError(w, http.StatusServiceUnavailable, "RoboRev v0.64.0 binding is unavailable", "version")
		return
	}
	dryRun, err := transport.AgentHookDryRun(r.Context(), agent, binding)
	if err != nil || dryRun.Agent != agent || dryRun.Status != roborev.AgentHookReadinessNeedsUpdate || strings.TrimSpace(dryRun.ConfigPath) == "" {
		writeError(w, http.StatusConflict, "agent hook action is unavailable", "action")
		return
	}
	configFS := s.AgentHookConfigFS
	if configFS == nil {
		configFS = agenthookmanagement.OSFileSystem{}
	}
	snapshot, err := agenthookmanagement.Capture(configFS, dryRun.ConfigPath)
	if err != nil {
		writeError(w, http.StatusConflict, "agent hook configuration is unavailable", "config")
		return
	}
	if s.agentHookPreviews == nil {
		writeError(w, http.StatusServiceUnavailable, "agent hook management is unavailable", "agent-hook-management")
		return
	}
	record := agentHookPreviewRecord{Agent: agent, Binding: binding, DryRun: dryRun, Snapshot: snapshot, Expires: time.Now().Add(agentHookManagementPreviewTTL)}
	token, err := s.agentHookPreviews.put(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "prepare agent hook action", "agent-hook-management")
		return
	}
	writeJSON(w, http.StatusOK, agentHookPreviewDTO{
		Token: token, Agent: agent, Action: "install-update", Summary: agentHookManagementSummary(agent),
		Command:   agentHookCommandDTO{Executable: "roborev", Args: []string{"agent-hook", "install", "--agent", agent, "--binary", agentHookPreviewBinary}},
		ExpiresAt: record.Expires,
	})
}

func (s *Server) agentHookManagementExecute(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeHookMutation(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body agentHookExecuteBody
	if !decodeUniqueBody(w, r, &body, "token") {
		return
	}
	token := body.Token
	if token == "" || strings.TrimSpace(token) != token {
		writeError(w, http.StatusBadRequest, "confirmation token is required", "request")
		return
	}
	if s.agentHookPreviews == nil {
		writeError(w, http.StatusServiceUnavailable, "agent hook management is unavailable", "agent-hook-management")
		return
	}
	record, ok := s.agentHookPreviews.claim(token)
	if !ok {
		writeError(w, http.StatusConflict, "confirmation token is missing or expired", "stale")
		return
	}
	transport, ok := s.RoboRev.(roborev.AgentHookManagementTransport)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "agent hook management is unavailable", "agent-hook-management")
		return
	}
	binding, err := transport.ProbeAgentHookBinding(r.Context())
	if err != nil || !roborevcompat.IsSupportedVersion(binding.Version) {
		writeError(w, http.StatusConflict, "agent hook preview is stale", "stale")
		return
	}
	if binding != record.Binding {
		writeError(w, http.StatusConflict, "agent hook preview is stale", "stale")
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
	configFS := s.AgentHookConfigFS
	if configFS == nil {
		configFS = agenthookmanagement.OSFileSystem{}
	}
	snapshot, err := agenthookmanagement.Capture(configFS, record.Snapshot.Path)
	if err != nil || !snapshot.Equal(record.Snapshot) {
		writeError(w, http.StatusConflict, "agent hook preview is stale", "stale")
		return
	}
	dryRun, err := transport.AgentHookDryRun(r.Context(), record.Agent, binding)
	if err != nil || dryRun.Agent != record.Agent || dryRun.Status != roborev.AgentHookReadinessNeedsUpdate || !sameAgentHookConfigPath(dryRun.ConfigPath, record.DryRun.ConfigPath) {
		writeError(w, http.StatusConflict, "agent hook preview is stale", "stale")
		return
	}
	binding, err = transport.ProbeAgentHookBinding(r.Context())
	if err != nil || binding != record.Binding {
		writeError(w, http.StatusConflict, "agent hook preview is stale", "stale")
		return
	}
	execution, runErr := transport.RunAgentHookInstall(r.Context(), record.Agent, binding)
	readCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	postBinding, postBindingErr := transport.ProbeAgentHookBinding(readCtx)
	postBindingStable := postBindingErr == nil && postBinding == record.Binding
	var postDryRun roborev.AgentHookDryRun
	var postErr error
	if postBindingStable {
		postDryRun, postErr = transport.AgentHookDryRun(readCtx, record.Agent, postBinding)
	} else {
		postErr = roborev.ErrBindingChanged
	}
	postSnapshot, postSnapshotErr := agenthookmanagement.Capture(configFS, record.Snapshot.Path)
	verified := execution.Started && postBindingStable && postErr == nil && postDryRun.Agent == record.Agent && postDryRun.Status == roborev.AgentHookReadinessCurrent && sameAgentHookConfigPath(postDryRun.ConfigPath, record.DryRun.ConfigPath) && postSnapshotErr == nil && postSnapshot.Exists && sameAgentHookConfigPath(postSnapshot.Path, record.Snapshot.Path)
	result := agentHookResultDTO{Status: "failed-unverified", Agent: record.Agent}
	if verified {
		result.Status = "verified-success"
	}
	switch {
	case !execution.Started:
		result.ErrorCode = "cli-not-started"
	case runErr != nil:
		result.ErrorCode = "cli-failed"
	case !postBindingStable:
		result.ErrorCode = "binding-drift"
	case postErr != nil || postSnapshotErr != nil:
		result.ErrorCode = "readback-unavailable"
	case !verified:
		result.ErrorCode = "readback-not-current"
	}
	writeJSON(w, http.StatusOK, result)
}

func agentHookManagementAgentAllowed(agent string) bool {
	switch agent {
	case "codex", "claude", "grok", "droid":
		return true
	default:
		return false
	}
}

func agentHookManagementSummary(agent string) string {
	labels := map[string]string{"codex": "Codex", "claude": "Claude", "grok": "Grok Build", "droid": "Factory Droid"}
	return "Install or update RoboRev hooks for " + labels[agent]
}

func sameAgentHookConfigPath(left, right string) bool {
	return gitx.SameRepositoryPath(filepath.Clean(left), filepath.Clean(right))
}
