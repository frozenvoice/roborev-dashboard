package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/agentconfiguration"
	"github.com/froze/roborev-dashboard/internal/configuration"
	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

const agentConfigurationVerificationTimeout = 10 * time.Second

type repositoryAgentConfigurationStatusBody struct {
	RepositoryIdentity string `json:"repositoryIdentity"`
}

type repositoryAgentConfigurationPreviewBody struct {
	RepositoryIdentity string `json:"repositoryIdentity"`
	Scope              string `json:"scope"`
	Agent              string `json:"agent"`
}

type repositoryAgentConfigurationExecuteBody struct {
	Token string `json:"token"`
}

type repositoryAgentConfigurationRowDTO struct {
	Status string `json:"status"`
	Agent  string `json:"agent,omitempty"`
}

type repositoryAgentConfigurationStatusDTO struct {
	Version            string                             `json:"version"`
	RepositoryIdentity string                             `json:"repositoryIdentity"`
	Global             repositoryAgentConfigurationRowDTO `json:"global"`
	Repository         repositoryAgentConfigurationRowDTO `json:"repository"`
}

type repositoryAgentConfigurationCommandDTO struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type repositoryAgentConfigurationPreviewDTO struct {
	Version            string                                  `json:"version"`
	RepositoryIdentity string                                  `json:"repositoryIdentity"`
	Scope              string                                  `json:"scope"`
	Agent              string                                  `json:"agent"`
	CurrentStatus      string                                  `json:"currentStatus"`
	CurrentAgent       string                                  `json:"currentAgent,omitempty"`
	Action             string                                  `json:"action"`
	Target             string                                  `json:"target"`
	Summary            string                                  `json:"summary"`
	Token              string                                  `json:"token,omitempty"`
	ExpiresAt          *time.Time                              `json:"expiresAt,omitempty"`
	Command            *repositoryAgentConfigurationCommandDTO `json:"command,omitempty"`
}

type repositoryAgentConfigurationResultDTO struct {
	Status string `json:"status"`
}

func (s *Server) authorizeRepositoryAgentConfiguration(w http.ResponseWriter, r *http.Request) bool {
	if !s.authorizeSameOrigin(w, r) {
		return false
	}
	if !exactHeaderValue(r.Header, "Sec-Fetch-Site", "same-origin") ||
		!exactHeaderValue(r.Header, "Sec-Fetch-Mode", "cors") ||
		!exactHeaderValue(r.Header, "Sec-Fetch-Dest", "empty") {
		writeError(w, http.StatusForbidden, "same-origin browser request required", "origin")
		return false
	}
	if !exactHeaderValue(r.Header, "X-CSRF-Token", s.csrfToken) {
		writeError(w, http.StatusForbidden, "invalid CSRF token", "csrf")
		return false
	}
	return true
}

func (s *Server) agentConfigurationTransport(w http.ResponseWriter) (roborev.RepositoryAgentConfigurationTransport, bool) {
	if s.RepositoryAgentConfiguration == nil {
		writeError(w, http.StatusServiceUnavailable, "agent configuration is unavailable", "agent-configuration")
		return nil, false
	}
	return s.RepositoryAgentConfiguration, true
}

func (s *Server) repositoryAgentConfigurationStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorizeRepositoryAgentConfiguration(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body repositoryAgentConfigurationStatusBody
	if !decodeUniqueBody(w, r, &body, "repositoryIdentity") {
		return
	}
	if body.RepositoryIdentity == "" || strings.TrimSpace(body.RepositoryIdentity) != body.RepositoryIdentity {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is invalid", "repository")
		return
	}
	transport, ok := s.agentConfigurationTransport(w)
	if !ok {
		return
	}
	release, root, err := s.Registry.AcquireSelected(body.RepositoryIdentity)
	if err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return
	}
	defer release()
	if err := s.verifyAgentRoot(r.Context(), body.RepositoryIdentity, root); err != nil {
		writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
		return
	}
	rootSnapshot, err := s.captureAgentConfigurationRoot(root)
	if err != nil {
		writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
		return
	}
	statusSnapshot, err := transport.RepositoryAgentConfigurationStatus(r.Context(), root)
	if err != nil || !statusSnapshot.Binding.Valid() {
		writeError(w, http.StatusBadGateway, "agent configuration is unavailable", "agent-configuration")
		return
	}
	global := agentConfigurationRow(statusSnapshot.Global)
	repository := agentConfigurationRow(statusSnapshot.Repository)
	if s.verifyAgentRoot(r.Context(), body.RepositoryIdentity, root) != nil {
		writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
		return
	}
	latestSnapshot, snapshotErr := s.captureAgentConfigurationRoot(root)
	if snapshotErr != nil || !latestSnapshot.Equal(rootSnapshot) {
		writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
		return
	}
	writeJSON(w, http.StatusOK, repositoryAgentConfigurationStatusDTO{
		Version: statusSnapshot.Binding.Version, RepositoryIdentity: s.repositoryMutationAlias(root), Global: global, Repository: repository,
	})
}

func agentConfigurationRow(value roborev.RepositoryAgentConfigurationScopedValue) repositoryAgentConfigurationRowDTO {
	status, agent := agentconfiguration.Classify(agentconfiguration.ScopedValue{Set: value.Set, Value: value.Value})
	row := repositoryAgentConfigurationRowDTO{Status: string(status)}
	if status == agentconfiguration.StatusCanonical {
		row.Agent = agent
	}
	return row
}

func (s *Server) repositoryAgentConfigurationPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorizeRepositoryAgentConfiguration(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body repositoryAgentConfigurationPreviewBody
	if !decodeUniqueBody(w, r, &body, "repositoryIdentity", "scope", "agent") {
		return
	}
	if body.RepositoryIdentity == "" || strings.TrimSpace(body.RepositoryIdentity) != body.RepositoryIdentity || !agentconfiguration.ValidateAgent(body.Agent) {
		writeError(w, http.StatusBadRequest, "invalid agent configuration request", "request")
		return
	}
	scope := agentconfiguration.Scope(body.Scope)
	if !scope.Valid() {
		writeError(w, http.StatusBadRequest, "invalid agent configuration scope", "request")
		return
	}
	legacy, selectedRoot, ok := s.resolveRepositoryMutationAlias(body.RepositoryIdentity)
	if !ok {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return
	}
	release, leasedRoot, err := s.Registry.AcquireSelected(legacy)
	if err != nil || !gitx.SameRepositoryPath(leasedRoot, selectedRoot) {
		if release != nil {
			release()
		}
		writeError(w, http.StatusConflict, "repository selection changed", "repository")
		return
	}
	defer release()
	transport, ok := s.agentConfigurationTransport(w)
	if !ok {
		return
	}
	if err := s.verifyAgentRoot(r.Context(), legacy, selectedRoot); err != nil {
		writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
		return
	}
	var repo gitx.Repository
	if scope == agentconfiguration.ScopeRepository {
		repo, err = s.Registry.InspectIdentity(r.Context(), legacy)
		if err != nil || !gitx.SameRepositoryPath(repo.Path, selectedRoot) {
			writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
			return
		}
		if repo.Dirty {
			writeError(w, http.StatusConflict, "repository must be clean before local agent configuration changes", "dirty")
			return
		}
	}
	rootSnapshot, err := s.captureAgentConfigurationRoot(selectedRoot)
	if err != nil {
		writeError(w, http.StatusConflict, "agent configuration preview is unavailable", "agent-configuration")
		return
	}
	preflight, err := transport.RepositoryAgentConfigurationPreflight(r.Context(), selectedRoot, agentconfiguration.RoboRevScope(scope))
	if err != nil || !preflight.Binding.Valid() {
		writeError(w, http.StatusBadGateway, "agent configuration is unavailable", "agent-configuration")
		return
	}
	binding := preflight.Binding
	rows := preflight.Rows
	value := preflight.Value
	currentStatus, currentAgent := agentconfiguration.Classify(agentconfiguration.ScopedValue{Set: value.Set, Value: value.Value})
	base := repositoryAgentConfigurationPreviewDTO{
		Version: binding.Version, RepositoryIdentity: body.RepositoryIdentity, Scope: string(scope), Agent: body.Agent,
		CurrentStatus: string(currentStatus), CurrentAgent: currentAgent, Target: agentConfigurationTarget(scope), Summary: agentConfigurationSummary(scope),
	}
	if currentStatus == agentconfiguration.StatusCanonical && currentAgent == body.Agent {
		base.Action = "already-configured"
		base.CurrentAgent = currentAgent
		writeJSON(w, http.StatusOK, base)
		return
	}
	expires := time.Now().Add(agentconfiguration.TokenTTL)
	record := agentconfiguration.Record{
		Alias: body.RepositoryIdentity, LegacyIdentity: legacy, Root: selectedRoot, RootSnapshot: rootSnapshot,
		Scope: scope, Agent: body.Agent, Binding: binding, ConfigDigest: configuration.SnapshotDigest(rows),
		CurrentStatus: currentStatus, CurrentAgent: currentAgent, CurrentSet: value.Set, CurrentValue: value.Value, ExpiresAt: expires,
	}
	if scope == agentconfiguration.ScopeRepository {
		record.Head, record.Branch, record.Detached, record.Dirty = repo.Head, repo.Branch, repo.Detached, repo.Dirty
	}
	if s.agentConfiguration == nil {
		writeError(w, http.StatusServiceUnavailable, "agent configuration is unavailable", "agent-configuration")
		return
	}
	token, err := s.agentConfiguration.Put(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "agent configuration preview is unavailable", "agent-configuration")
		return
	}
	base.Action = "set-agent"
	base.Token = token
	base.ExpiresAt = &expires
	base.Command = &repositoryAgentConfigurationCommandDTO{Executable: "roborev", Args: []string{"config", "set", agentconfiguration.Key(scope), body.Agent, "--" + string(agentconfiguration.RoboRevScope(scope))}}
	writeJSON(w, http.StatusOK, base)
}

func agentConfigurationTarget(scope agentconfiguration.Scope) string {
	if scope == agentconfiguration.ScopeGlobal {
		return "Global default agent"
	}
	return "Repository agent"
}

func agentConfigurationSummary(scope agentconfiguration.Scope) string {
	if scope == agentconfiguration.ScopeGlobal {
		return "Set the global default RoboRev agent."
	}
	return "Set the selected repository RoboRev agent."
}

func (s *Server) repositoryAgentConfigurationExecute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorizeRepositoryAgentConfiguration(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body repositoryAgentConfigurationExecuteBody
	if !decodeUniqueBody(w, r, &body, "token") {
		return
	}
	if body.Token == "" || strings.TrimSpace(body.Token) != body.Token {
		writeError(w, http.StatusBadRequest, "confirmation token is required", "request")
		return
	}
	if s.agentConfiguration == nil {
		writeError(w, http.StatusServiceUnavailable, "agent configuration is unavailable", "agent-configuration")
		return
	}
	record, ok := s.agentConfiguration.Claim(body.Token)
	if !ok {
		writeError(w, http.StatusConflict, "confirmation token is missing or expired", "stale")
		return
	}
	transport, ok := s.agentConfigurationTransport(w)
	if !ok {
		return
	}
	legacy, selectedRoot, ok := s.resolveRepositoryMutationAlias(record.Alias)
	if !ok || legacy != record.LegacyIdentity || !gitx.SameRepositoryPath(selectedRoot, record.Root) {
		writeError(w, http.StatusConflict, "agent configuration preview is stale", "stale")
		return
	}
	if s.Gate == nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	releaseGlobal, err := s.Gate.AcquireFix()
	if err != nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	defer releaseGlobal()
	if record.Scope == agentconfiguration.ScopeRepository {
		releaseRepo, repoErr := s.Gate.AcquireRepository(gitx.RepositoryKey(record.Root))
		if repoErr != nil {
			writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
			return
		}
		defer releaseRepo()
	}
	releaseSelected, leasedRoot, err := s.Registry.AcquireSelected(record.LegacyIdentity)
	if err != nil || !gitx.SameRepositoryPath(leasedRoot, record.Root) {
		if releaseSelected != nil {
			releaseSelected()
		}
		writeError(w, http.StatusConflict, "repository selection changed", "repository")
		return
	}
	defer releaseSelected()
	if err := s.revalidateAgentConfiguration(r.Context(), transport, record); err != nil {
		writeError(w, http.StatusConflict, "agent configuration preview is stale", "stale")
		return
	}
	finalGuard := func(ctx context.Context) error {
		if s.verifyAgentRoot(ctx, record.LegacyIdentity, record.Root) != nil {
			return errors.New("repository root changed")
		}
		snapshot, guardErr := s.captureAgentConfigurationRoot(record.Root)
		if guardErr != nil || !snapshot.Equal(record.RootSnapshot) {
			return errors.New("repository root changed")
		}
		return nil
	}
	result, setErr := transport.RepositoryAgentConfigurationSet(r.Context(), record.Root, agentconfiguration.RoboRevScope(record.Scope), record.Agent, record.Binding, finalGuard)
	if !result.Started {
		writeJSON(w, http.StatusOK, repositoryAgentConfigurationResultDTO{Status: "failed-unverified"})
		return
	}
	// Verification is deliberately detached from browser cancellation and runs
	// while the global/repository/selection leases remain held.
	verifyCtx, cancel := context.WithTimeout(context.Background(), agentConfigurationVerificationTimeout)
	defer cancel()
	verified := s.verifyAgentConfigurationResult(verifyCtx, transport, record)
	if verified {
		writeJSON(w, http.StatusOK, repositoryAgentConfigurationResultDTO{Status: "verified-success"})
		return
	}
	_ = setErr // process errors are intentionally not exposed; readback decides.
	writeJSON(w, http.StatusOK, repositoryAgentConfigurationResultDTO{Status: "failed-unverified"})
}

func (s *Server) revalidateAgentConfiguration(ctx context.Context, transport roborev.RepositoryAgentConfigurationTransport, record agentconfiguration.Record) error {
	if err := s.verifyAgentRoot(ctx, record.LegacyIdentity, record.Root); err != nil {
		return err
	}
	snapshot, err := s.captureAgentConfigurationRoot(record.Root)
	if err != nil || !snapshot.Equal(record.RootSnapshot) {
		return errors.New("root changed")
	}
	if record.Scope == agentconfiguration.ScopeRepository {
		repo, inspectErr := s.Registry.InspectIdentity(ctx, record.LegacyIdentity)
		if inspectErr != nil || !gitx.SameRepositoryPath(repo.Path, record.Root) || repo.Dirty || repo.Head != record.Head || repo.Branch != record.Branch || repo.Detached != record.Detached {
			return errors.New("repository changed")
		}
	}
	preflight, err := transport.RepositoryAgentConfigurationPreflight(ctx, record.Root, agentconfiguration.RoboRevScope(record.Scope))
	if err != nil || !preflight.Binding.Equal(record.Binding) || configuration.SnapshotDigest(preflight.Rows) != record.ConfigDigest {
		return errors.New("configuration changed")
	}
	value := preflight.Value
	status, agent := agentconfiguration.Classify(agentconfiguration.ScopedValue{Set: value.Set, Value: value.Value})
	if status != record.CurrentStatus || agent != record.CurrentAgent || value.Set != record.CurrentSet || value.Value != record.CurrentValue {
		return errors.New("current value changed")
	}
	return nil
}

func (s *Server) verifyAgentConfigurationResult(ctx context.Context, transport roborev.RepositoryAgentConfigurationTransport, record agentconfiguration.Record) bool {
	if err := s.verifyAgentRoot(ctx, record.LegacyIdentity, record.Root); err != nil {
		return false
	}
	snapshot, err := s.captureAgentConfigurationRoot(record.Root)
	if err != nil || !snapshot.Equal(record.RootSnapshot) {
		return false
	}
	if transport.RepositoryAgentConfigurationReadback(ctx, record.Root, agentconfiguration.RoboRevScope(record.Scope), record.Agent, record.Binding) != nil {
		return false
	}
	if record.Scope == agentconfiguration.ScopeRepository {
		repo, inspectErr := s.Registry.InspectIdentity(ctx, record.LegacyIdentity)
		if inspectErr != nil || !gitx.SameRepositoryPath(repo.Path, record.Root) || repo.Head != record.Head || repo.Branch != record.Branch || repo.Detached != record.Detached {
			return false
		}
	}
	if s.verifyAgentRoot(ctx, record.LegacyIdentity, record.Root) != nil {
		return false
	}
	after, err := s.captureAgentConfigurationRoot(record.Root)
	return err == nil && after.Equal(record.RootSnapshot)
}

func (s *Server) verifyAgentRoot(ctx context.Context, identity, root string) error {
	if authorized, err := s.Registry.ResolveIdentity(identity); err != nil || !gitx.SameRepositoryPath(authorized, root) {
		return errors.New("repository identity changed")
	}
	inspected, err := s.Registry.InspectRoot(ctx, root)
	if err != nil || !gitx.SameRepositoryPath(inspected, root) {
		return errors.New("repository root changed")
	}
	return nil
}

func (s *Server) captureAgentConfigurationRoot(root string) (pathsafety.Snapshot, error) {
	fs := s.RepositoryAgentConfigurationFS
	if fs == nil {
		fs = pathsafety.OSFileSystem{}
	}
	return pathsafety.CaptureDirectory(fs, root)
}
