package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/agentconfiguration"
	"github.com/froze/roborev-dashboard/internal/agenthookmanagement"
	"github.com/froze/roborev-dashboard/internal/agentstatus"
	"github.com/froze/roborev-dashboard/internal/analysis"
	"github.com/froze/roborev-dashboard/internal/ciworkflowmanagement"
	"github.com/froze/roborev-dashboard/internal/ciworkflowpreview"
	"github.com/froze/roborev-dashboard/internal/fixoperation"
	"github.com/froze/roborev-dashboard/internal/fixprompt"
	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/hookmanagement"
	"github.com/froze/roborev-dashboard/internal/hookstatus"
	"github.com/froze/roborev-dashboard/internal/insights"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"github.com/froze/roborev-dashboard/internal/refine"
	"github.com/froze/roborev-dashboard/internal/repomove"
	"github.com/froze/roborev-dashboard/internal/repositorytracking"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/runtimecontrol"
	"github.com/froze/roborev-dashboard/internal/setup"
	"github.com/froze/roborev-dashboard/internal/setupreadiness"
	"github.com/froze/roborev-dashboard/internal/snapshotignoremanagement"
	"github.com/froze/roborev-dashboard/internal/streamrelay"
	"github.com/froze/roborev-dashboard/internal/webui"
)

type Server struct {
	RoboRev     roborev.Service
	Registry    *Registry
	Panels      roborev.PanelCatalogResolver
	Refines     *refine.Manager
	Fixes       *fixoperation.Manager
	Setup       *setup.Manager
	Agents      *agentstatus.Service
	Runtime     *runtimecontrol.Manager
	Analyses    *analysis.Manager
	Events      EventRelay
	Gate        *operation.Gate
	AllowedHost string
	csrfToken   string
	mux         *http.ServeMux
	// configPreviews holds single-use, server-side configuration preview
	// bindings. The browser only ever receives the opaque token; every bound
	// field is revalidated fresh before a config set executes.
	configPreviews *configPreviewStore
	renamePreviews *repositoryRenamePreviewStore
	movePreviews   *repositoryMovePreviewStore
	mergePreviews  *repositoryMergePreviewStore
	// repositoryMoveFS is nil in production and exists only to make the
	// read-only stale-source boundary deterministic in app tests.
	repositoryMoveFS             repomove.FileSystem
	Insights                     *insights.Manager
	HookStatus                   *hookstatus.Service
	HookManagement               *hookmanagement.Service
	hookPreviews                 *hookManagementPreviewStore
	AgentHookConfigFS            agenthookmanagement.FileSystem
	agentHookPreviews            *agentHookPreviewStore
	SkillsManagementFS           pathsafety.FileSystem
	SkillsManagementHome         string
	SkillsManagementEnv          func() []string
	skillsPreviews               *skillsPreviewStore
	CIWorkflowPreview            *ciworkflowpreview.Service
	CIWorkflowManagement         *ciworkflowmanagement.Service
	ciWorkflowWrites             *ciWorkflowWriteStore
	SetupReadiness               *setupreadiness.Checker
	SnapshotIgnore               *snapshotignoremanagement.Service
	RepositoryTracking           roborev.RepositoryTrackingTransport
	RepositoryAgentConfiguration roborev.RepositoryAgentConfigurationTransport
	// RepositoryTrackingFS is injectable only for deterministic root-race
	// tests. Production uses pathsafety.OSFileSystem and never accepts a
	// filesystem implementation from the browser.
	RepositoryTrackingFS pathsafety.FileSystem
	// RepositoryAgentConfigurationFS is injectable only for deterministic
	// agent-setting root-race tests. It is deliberately separate from the
	// repository tracking filesystem seam.
	RepositoryAgentConfigurationFS pathsafety.FileSystem
	snapshotIgnoreKey              [32]byte
	snapshotIgnoreWrites           *snapshotIgnoreWriteStore
	repositoryTracking             *repositorytracking.Store
	agentConfiguration             *agentconfiguration.Store
}

// EventRelay is the narrow HTTP-facing surface of the stream manager. The
// server must not depend on manager internals; tests can also use a counting
// relay to prove authorization happens before subscription.
type EventRelay interface {
	Subscribe(context.Context, string) (<-chan streamrelay.Event, func(), error)
	Shutdown(context.Context) error
}

type RepositoryDTO struct {
	gitx.Repository
	Identity string `json:"identity"`
}

type RepositoryPanels struct {
	RepositoryIdentity string              `json:"repositoryIdentity"`
	Panels             []roborev.PanelInfo `json:"panels"`
	SelectedPanel      string              `json:"selectedPanel"`
	SelectionRequired  bool                `json:"selectionRequired"`
	Error              string              `json:"error,omitempty"`
}

func NewServer(service roborev.Service, registry *Registry, allowedHost string) (*Server, error) {
	return NewServerWithSetup(service, registry, allowedHost, operation.NewGate(), nil)
}

func NewServerWithSetup(service roborev.Service, registry *Registry, allowedHost string, gate *operation.Gate, setupManager *setup.Manager) (*Server, error) {
	if service == nil || registry == nil {
		return nil, errors.New("service and repository registry are required")
	}
	if gate == nil {
		gate = operation.NewGate()
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	var snapshotIgnoreKey [32]byte
	if _, err := rand.Read(snapshotIgnoreKey[:]); err != nil {
		return nil, err
	}
	panels := roborev.NewPanelResolver(registry)
	// Ensure the long-lived Panel Refine manager and HTTP mutations share the
	// same lifecycle gate.
	refines, err := refine.NewManager(refine.Dependencies{Registry: registry, Panels: panels, RoboRev: service, Binary: serviceBinary(service), Gate: gate})
	if err != nil {
		return nil, err
	}
	fixes, err := fixoperation.NewManager(fixoperation.Dependencies{Registry: registry, RoboRev: service, Binary: serviceBinary(service), Gate: gate})
	if err != nil {
		return nil, err
	}
	if setupManager != nil {
		setupManager.SetGate(gate)
	}
	var checker agentstatus.HealthChecker
	if candidate, ok := service.(agentstatus.HealthChecker); ok {
		checker = candidate
	}
	runtimeManager, err := runtimecontrol.NewManagerWithGate(service, gate)
	if err != nil {
		return nil, err
	}
	var analyses *analysis.Manager
	if analysisService, ok := service.(analysis.Service); ok {
		analyses, err = analysis.NewManager(analysis.Dependencies{RoboRev: analysisService})
		if err != nil {
			return nil, err
		}
	}
	var guidelineInsights *insights.Manager
	if insightsService, ok := service.(roborev.InsightsService); ok {
		guidelineInsights, err = insights.NewManager(insightsService, registry)
		if err != nil {
			return nil, err
		}
	}
	var events EventRelay
	if source, ok := service.(streamrelay.Source); ok {
		events = streamrelay.NewManager(source, func(ctx context.Context, identity string) (string, error) {
			repo, err := registry.InspectIdentity(ctx, identity)
			if err != nil {
				return "", err
			}
			return repo.Path, nil
		})
	}
	var versionProbe hookstatus.VersionProbe
	if candidate, ok := service.(hookstatus.VersionProbe); ok {
		versionProbe = candidate
	}
	var management *hookmanagement.Service
	if _, ok := registry.inspector.(repositoryHookLayoutResolver); ok {
		management = hookmanagement.New(registry, nil)
	}
	var ciWorkflow *ciworkflowpreview.Service
	if transport, ok := service.(roborev.CIWorkflowPreviewTransport); ok {
		ciWorkflow = ciworkflowpreview.NewService(transport)
	}
	hookStatus := hookstatus.New(versionProbe, registry, nil)
	s := &Server{RoboRev: service, Registry: registry, Panels: panels, Refines: refines, Fixes: fixes, Analyses: analyses, Insights: guidelineInsights, Setup: setupManager, Agents: agentstatus.NewService(panels, checker), Runtime: runtimeManager, Events: events, Gate: gate, AllowedHost: allowedHost, csrfToken: base64.RawURLEncoding.EncodeToString(tokenBytes), snapshotIgnoreKey: snapshotIgnoreKey, snapshotIgnoreWrites: newSnapshotIgnoreWriteStore(), repositoryTracking: repositorytracking.NewStore(), agentConfiguration: agentconfiguration.NewStore(), RepositoryTrackingFS: pathsafety.OSFileSystem{}, RepositoryAgentConfigurationFS: pathsafety.OSFileSystem{}, SnapshotIgnore: snapshotignoremanagement.New(snapshotignoremanagement.OSFileSystem{}), mux: http.NewServeMux(), configPreviews: newConfigPreviewStore(), renamePreviews: newRepositoryRenamePreviewStore(), movePreviews: newRepositoryMovePreviewStore(), mergePreviews: newRepositoryMergePreviewStore(), HookStatus: hookStatus, HookManagement: management, hookPreviews: newHookManagementPreviewStore(), AgentHookConfigFS: agenthookmanagement.OSFileSystem{}, agentHookPreviews: newAgentHookPreviewStore(), SkillsManagementFS: pathsafety.OSFileSystem{}, SkillsManagementEnv: roborev.SkillsTargetEnvFromProcess, skillsPreviews: newSkillsPreviewStore(), CIWorkflowPreview: ciWorkflow, CIWorkflowManagement: ciworkflowmanagement.New(ciworkflowmanagement.OSFileSystem{}), ciWorkflowWrites: newCIWorkflowWriteStore()}
	s.SetupReadiness = newSetupReadinessChecker(service, registry, hookStatus)
	if tracking, ok := service.(roborev.RepositoryTrackingTransport); ok {
		s.RepositoryTracking = tracking
	}
	if agentConfig, ok := service.(roborev.RepositoryAgentConfigurationTransport); ok {
		s.RepositoryAgentConfiguration = agentConfig
	}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") && s.AllowedHost != "" && !strings.EqualFold(r.Host, s.AllowedHost) {
			writeError(w, http.StatusForbidden, "invalid host", "host")
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

// BeginShutdown stops long-lived SSE sources before net/http begins waiting
// for active handlers. It is idempotent with Shutdown; the remaining
// process-scoped managers are handled by Shutdown.
func (s *Server) BeginShutdown(ctx context.Context) error {
	var shutdownErrors []error
	s.Registry.BeginShutdown()
	if s.Events != nil {
		if err := s.Events.Shutdown(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	}
	return errors.Join(shutdownErrors...)
}

// Shutdown closes new acceptance in every process-scoped manager under its own
// mutex before canceling active work and waiting for the gate leases and child
// processes to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	var shutdownErrors []error
	s.Registry.BeginShutdown()
	if s.Events != nil {
		if err := s.Events.Shutdown(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	}
	if s.Runtime != nil {
		if err := s.Runtime.Shutdown(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	}
	if s.Refines != nil {
		if err := s.Refines.Shutdown(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	}
	if s.Fixes != nil {
		if err := s.Fixes.Shutdown(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	}
	if s.Setup != nil {
		if err := s.Setup.Shutdown(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	}
	// Registry admission is closed and cooperative probes were canceled above;
	// wait for detached probes last so a non-cooperative inspector cannot
	// consume the cleanup budget of process-scoped managers.
	if err := s.Registry.Shutdown(ctx); err != nil {
		shutdownErrors = append(shutdownErrors, err)
	}
	return errors.Join(shutdownErrors...)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/bootstrap", s.bootstrap)
	s.mux.HandleFunc("GET /api/runtime", s.runtime)
	s.mux.HandleFunc("GET /api/runtime/diagnostics", s.runtimeDiagnostics)
	s.mux.HandleFunc("GET /api/agent-hooks/readiness", s.agentHookReadiness)
	s.mux.HandleFunc("GET /api/skills/readiness", s.skillsReadiness)
	s.mux.HandleFunc("POST /api/agent-hooks/preview", s.agentHookManagementPreview)
	s.mux.HandleFunc("POST /api/agent-hooks/execute", s.agentHookManagementExecute)
	s.mux.HandleFunc("POST /api/skills/preview", s.skillsManagementPreview)
	s.mux.HandleFunc("POST /api/skills/execute", s.skillsManagementExecute)
	s.mux.HandleFunc("POST /api/ci-workflow/preview", s.ciWorkflowPreview)
	s.mux.HandleFunc("POST /api/ci-workflow/execute", s.ciWorkflowManagementExecute)
	s.mux.HandleFunc("GET /api/exports/reviews", s.reviewMetadataExport)
	s.mux.HandleFunc("GET /api/repositories/hooks", s.repositoryHooks)
	s.mux.HandleFunc("GET /api/repositories/hooks/management", s.repositoryHookManagement)
	s.mux.HandleFunc("POST /api/repositories/hooks/preview", s.mutationAuth(s.repositoryHookManagementPreview))
	s.mux.HandleFunc("POST /api/repositories/hooks/execute", s.mutationAuth(s.repositoryHookManagementExecute))
	s.mux.HandleFunc("GET /api/agents", s.agentStatuses)
	s.mux.HandleFunc("GET /api/runtime-control", s.runtimeControlStatus)
	s.mux.HandleFunc("GET /api/setup/roborev", s.setupStatus)
	s.mux.HandleFunc("GET /api/reviews", s.listReviews)
	s.mux.HandleFunc("GET /api/events", s.events)
	s.mux.HandleFunc("GET /api/reviews/{id}", s.showReview)
	s.mux.HandleFunc("GET /api/reviews/{id}/prompt", s.showPrompt)
	s.mux.HandleFunc("GET /api/reviews/{id}/log", s.showLog)
	s.mux.HandleFunc("GET /api/reviews/{id}/fix-prompt", s.showFixPrompt)
	s.mux.HandleFunc("GET /api/panels/{run}/members", s.panelMembers)
	s.mux.HandleFunc("GET /api/panels/{run}/members/{id}", s.panelMember)
	s.mux.HandleFunc("GET /api/repos/panels", s.repositoryPanels)
	s.mux.HandleFunc("GET /api/panel-refines", s.listPanelRefines)
	s.mux.HandleFunc("GET /api/panel-refines/{id}", s.getPanelRefine)
	s.mux.HandleFunc("GET /api/fixes/preview", s.fixPreview)
	s.mux.HandleFunc("GET /api/configuration", s.configurationSnapshot)
	s.mux.HandleFunc("GET /api/stats/summary", s.statsSummary)
	s.mux.HandleFunc("GET /api/stats/cost", s.statsCost)
	s.mux.HandleFunc("GET /api/repository-inventory", s.repositoryInventory)
	s.mux.HandleFunc("POST /api/repositories/setup-readiness", s.repositorySetupReadiness)
	s.mux.HandleFunc("POST /api/repository-tracking/preview", s.repositoryTrackingPreview)
	s.mux.HandleFunc("POST /api/repository-tracking/execute", s.repositoryTrackingExecute)
	s.mux.HandleFunc("POST /api/repository-agent-configuration/status", s.repositoryAgentConfigurationStatus)
	s.mux.HandleFunc("POST /api/repository-agent-configuration/preview", s.repositoryAgentConfigurationPreview)
	s.mux.HandleFunc("POST /api/repository-agent-configuration/execute", s.repositoryAgentConfigurationExecute)
	s.mux.HandleFunc("POST /api/snapshot-ignore/preview", s.snapshotIgnorePreview)
	s.mux.HandleFunc("POST /api/snapshot-ignore/execute", s.snapshotIgnoreExecute)
	s.mux.HandleFunc("GET /api/insights/{id}/result", s.insightsResult)
	s.mux.HandleFunc("GET /api/insights/{id}", s.insightsStatus)
	s.mux.HandleFunc("POST /api/configuration/preview", s.mutationAuth(s.configurationPreview))
	s.mux.HandleFunc("POST /api/configuration/set", s.mutationAuth(s.configurationSet))
	s.mux.HandleFunc("POST /api/repository-renames/preview", s.mutationAuth(s.repositoryRenamePreview))
	s.mux.HandleFunc("POST /api/repository-renames", s.mutation(s.repositoryRenameConfirm))
	s.mux.HandleFunc("POST /api/repository-moves/preview", s.mutationAuth(s.repositoryMovePreview))
	s.mux.HandleFunc("POST /api/repository-moves/eligibility", s.mutationAuth(s.repositoryMoveEligibility))
	s.mux.HandleFunc("POST /api/repository-moves", s.mutation(s.repositoryMoveConfirm))
	s.mux.HandleFunc("POST /api/repository-merges/eligibility", s.mutationAuth(s.repositoryMergeEligibility))
	s.mux.HandleFunc("POST /api/repository-merges/preview", s.mutationAuth(s.repositoryMergePreview))
	s.mux.HandleFunc("POST /api/repository-merges", s.mutation(s.repositoryMergeConfirm))
	s.mux.HandleFunc("POST /api/insights/preview", s.mutationAuth(s.insightsPreview))
	s.mux.HandleFunc("POST /api/insights", s.mutationAuth(s.startInsights))
	s.mux.HandleFunc("POST /api/tasks/preview", s.mutationAuth(s.taskPreview))
	s.mux.HandleFunc("GET /api/tasks/preview", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "task preview requires POST", "method")
	})
	s.mux.HandleFunc("GET /api/analyses/types", s.analysisTypes)
	s.mux.HandleFunc("GET /api/analyses/prompt", s.analysisPrompt)
	s.mux.HandleFunc("GET /api/analyses/preview", s.analysisPreview)
	s.mux.HandleFunc("GET /api/fixes", s.listFixes)
	s.mux.HandleFunc("GET /api/fixes/{id}", s.getFix)
	s.mux.HandleFunc("POST /api/reviews", s.mutation(s.createReview))
	s.mux.HandleFunc("POST /api/panel-refines", s.mutation(s.startPanelRefine))
	s.mux.HandleFunc("POST /api/panel-refines/{id}/cancel", s.mutation(s.cancelPanelRefine))
	s.mux.HandleFunc("POST /api/fixes", s.mutationAuth(s.startFix))
	s.mux.HandleFunc("POST /api/analyses", s.mutationAuth(s.startAnalysis))
	s.mux.HandleFunc("POST /api/tasks", s.mutationAuth(s.startTask))
	s.mux.HandleFunc("POST /api/fixes/{id}/cancel", s.mutationAuth(s.cancelFix))
	s.mux.HandleFunc("POST /api/reviews/{id}/comment", s.mutation(s.comment))
	s.mux.HandleFunc("POST /api/reviews/{id}/comment-close", s.mutation(s.commentClose))
	s.mux.HandleFunc("POST /api/reviews/{id}/close", s.mutation(s.closeReview))
	s.mux.HandleFunc("POST /api/reviews/{id}/reopen", s.mutation(s.reopenReview))
	s.mux.HandleFunc("POST /api/reviews/{id}/cancel", s.mutation(s.cancelReview))
	s.mux.HandleFunc("POST /api/reviews/{id}/rerun", s.mutation(s.rerunReview))
	s.mux.HandleFunc("POST /api/repos", s.mutation(s.addRepository))
	s.mux.HandleFunc("POST /api/repos/select", s.mutation(s.selectRepository))
	s.mux.HandleFunc("POST /api/repos/panel-selection", s.mutation(s.selectRepositoryPanel))
	s.mux.HandleFunc("POST /api/agents/check", s.setupMutation(s.checkAgent))
	s.mux.HandleFunc("POST /api/runtime-control/queue/pause", s.setupMutation(s.pauseQueue))
	s.mux.HandleFunc("POST /api/runtime-control/queue/resume", s.setupMutation(s.resumeQueue))
	s.mux.HandleFunc("POST /api/daemon/start", s.setupMutation(s.startDaemon))
	s.mux.HandleFunc("POST /api/daemon/stop", s.setupMutation(s.stopDaemon))
	s.mux.HandleFunc("POST /api/daemon/restart", s.setupMutation(s.restartDaemon))
	s.mux.HandleFunc("POST /api/setup/roborev/install", s.setupMutation(s.installRoboRev))
	s.mux.HandleFunc("POST /api/setup/roborev/retry", s.setupMutation(s.retryRoboRev))
	s.mux.Handle("/", spaHandler())
}

// serviceBinary exposes the configured executable for the command preview.
// The concrete client intentionally keeps its transport details private.
func serviceBinary(service roborev.Service) string {
	if client, ok := service.(*roborev.Client); ok {
		return client.BinaryPath()
	}
	if provider, ok := service.(interface{ BinaryPath() string }); ok {
		if binary := strings.TrimSpace(provider.BinaryPath()); binary != "" {
			return binary
		}
	}
	return "roborev"
}

func (s *Server) runtime(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.RoboRev.Installation(r.Context()))
}

// events relays only a repository-scoped jobs-changed invalidation signal.
// The repository path is never accepted from the browser: identity lookup and
// a fresh canonical-root inspection are centralized in Registry.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	query := r.URL.Query()
	identities, hasIdentity := query["repositoryIdentity"]
	if len(query) != 1 || !hasIdentity || len(identities) != 1 {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "repository")
		return
	}
	identity := strings.TrimSpace(identities[0])
	if identity == "" {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "repository")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming responses are unsupported", "stream")
		return
	}
	_, err := s.Registry.InspectIdentity(r.Context(), identity)
	if err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return
	}
	if s.Events == nil {
		writeError(w, http.StatusServiceUnavailable, "live updates are unavailable", "stream")
		return
	}
	updates, unsubscribe, err := s.Events.Subscribe(r.Context(), identity)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "live updates are unavailable", "stream")
		return
	}
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	// A comment establishes the stream without leaking any upstream state.
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-updates:
			if !ok {
				return
			}
			payload, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				return
			}
			if _, writeErr := io.WriteString(w, "data: "+string(payload)+"\n\n"); writeErr != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, writeErr := io.WriteString(w, ": keepalive\n\n"); writeErr != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	if s.Setup == nil {
		writeError(w, http.StatusServiceUnavailable, "RoboRev setup manager is unavailable", "setup")
		return
	}
	writeJSON(w, http.StatusOK, s.Setup.Status(r.Context()))
}

func requireEmptyBody(w http.ResponseWriter, r *http.Request) bool {
	data, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(data) != 0 {
		writeError(w, http.StatusBadRequest, "request body must be empty", "request")
		return false
	}
	return true
}

func (s *Server) installRoboRev(w http.ResponseWriter, r *http.Request) {
	if !requireEmptyBody(w, r) {
		return
	}
	if s.Setup == nil {
		writeError(w, http.StatusServiceUnavailable, "RoboRev setup manager is unavailable", "setup")
		return
	}
	state, err := s.Setup.StartInstall(r.Context())
	if err != nil {
		if errors.Is(err, setup.ErrBusy) || errors.Is(err, setup.ErrIneligible) {
			writeJSON(w, http.StatusConflict, state)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error(), "setup")
		return
	}
	writeJSON(w, http.StatusAccepted, state)
}

func (s *Server) retryRoboRev(w http.ResponseWriter, r *http.Request) {
	if !requireEmptyBody(w, r) {
		return
	}
	if s.Setup == nil {
		writeError(w, http.StatusServiceUnavailable, "RoboRev setup manager is unavailable", "setup")
		return
	}
	state, err := s.Setup.Retry(r.Context())
	if err != nil {
		if errors.Is(err, setup.ErrBusy) || errors.Is(err, setup.ErrIneligible) || errors.Is(err, operation.ErrBusy) {
			writeJSON(w, http.StatusConflict, state)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error(), "setup")
		return
	}
	writeJSON(w, http.StatusAccepted, state)
}

func (s *Server) mutation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeMutation(w, r) {
			return
		}
		release, err := s.Gate.AcquireMutation()
		if err != nil {
			writeError(w, http.StatusConflict, err.Error(), "operation")
			return
		}
		defer release()
		next(w, r)
	}
}

// mutationAuth applies same-origin and CSRF checks without reserving the
// short-lived ordinary mutation slot. Process-scoped agentic operations own
// their Fix lease after fresh validation and must not be blocked by this
// request's handler lifetime.
func (s *Server) mutationAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeMutation(w, r) {
			return
		}
		next(w, r)
	}
}

func (s *Server) setupMutation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeMutation(w, r) {
			return
		}
		next(w, r)
	}
}

func (s *Server) authorizeMutation(w http.ResponseWriter, r *http.Request) bool {
	if !s.authorizeSameOrigin(w, r) {
		return false
	}
	if r.Header.Get("X-CSRF-Token") != s.csrfToken {
		writeError(w, http.StatusForbidden, "invalid CSRF token", "csrf")
		return false
	}
	return true
}

// authorizeSameOrigin protects mutations. Its absent-Origin compatibility is
// intentional for local/non-browser clients; SSE and lazy analysis reads use
// the stricter authorizeStrictBrowserRead policy below.
func (s *Server) authorizeSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	}
	if len(origins) != 1 || !s.sameOrigin(r, origins[0]) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected", "origin")
		return false
	}
	return true
}

// authorizeStrictBrowserRead protects reads that can expose lazy or live
// upstream data. An explicit Origin must be the exact request origin. When
// browsers omit Origin, only the native same-origin Fetch Metadata contract is
// accepted. Every metadata header is required exactly once with its exact
// value, so missing, duplicate, malformed, cross-site, and navigation requests
// fail closed before query, registry, or service side effects.
func (s *Server) authorizeStrictBrowserRead(w http.ResponseWriter, r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) > 0 {
		if len(origins) != 1 || !s.sameOrigin(r, origins[0]) {
			writeError(w, http.StatusForbidden, "cross-origin request rejected", "origin")
			return false
		}
		return true
	}
	if !exactHeaderValue(r.Header, "Sec-Fetch-Site", "same-origin") ||
		!exactHeaderValue(r.Header, "Sec-Fetch-Mode", "cors") ||
		!exactHeaderValue(r.Header, "Sec-Fetch-Dest", "empty") {
		writeError(w, http.StatusForbidden, "same-origin browser request required", "origin")
		return false
	}
	return true
}

func exactHeaderValue(header http.Header, name, expected string) bool {
	values := header.Values(name)
	return len(values) == 1 && values[0] == expected
}

func (s *Server) sameOrigin(r *http.Request, origin string) bool {
	origin = strings.TrimSpace(origin)
	parsed, err := url.Parse(origin)
	if err != nil || strings.EqualFold(origin, "null") || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return false
	}
	expectedScheme := "http"
	if r.TLS != nil {
		expectedScheme = "https"
	}
	if r.URL != nil && r.URL.Scheme != "" {
		expectedScheme = strings.ToLower(r.URL.Scheme)
	}
	if !strings.EqualFold(parsed.Scheme, expectedScheme) || !strings.EqualFold(parsed.Host, r.Host) {
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeRawJSON(w http.ResponseWriter, status int, raw json.RawMessage) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func writeError(w http.ResponseWriter, status int, message, phase string) {
	writeJSON(w, status, map[string]any{"error": message, "phase": phase})
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error(), "request")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			writeError(w, http.StatusBadRequest, "invalid request: trailing JSON value", "request")
		} else {
			writeError(w, http.StatusBadRequest, "invalid request: "+err.Error(), "request")
		}
		return false
	}
	return true
}

func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid job ID", "request")
		return 0, false
	}
	return id, true
}

// parseLazyContentID is deliberately stricter than the legacy review route:
// browser lazy-content paths accept only positive ASCII decimal IDs and no
// query parameters.  The content endpoint does not need a repository path,
// daemon URL, or log offset from the browser.
func parseLazyContentID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return 0, false
	}
	value := r.PathValue("id")
	if value == "" {
		writeError(w, http.StatusBadRequest, "invalid job ID", "request")
		return 0, false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			writeError(w, http.StatusBadRequest, "invalid job ID", "request")
			return 0, false
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid job ID", "request")
		return 0, false
	}
	return id, true
}

func (s *Server) repoForRequest(w http.ResponseWriter, path string) (string, bool) {
	repo, err := s.Registry.Resolve(path)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error(), "repository")
		return "", false
	}
	return repo, true
}

func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	repos := s.Registry.InspectAll(r.Context())
	repositoryDTOs := make([]RepositoryDTO, 0, len(repos))
	for _, repo := range repos {
		if dto, err := s.repositoryDTO(repo); err == nil {
			repositoryDTOs = append(repositoryDTOs, dto)
		}
	}
	selectedIdentity, _ := s.Registry.Identity("")
	installation := s.RoboRev.Installation(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"csrfToken": s.csrfToken, "repositories": repositoryDTOs, "selectedRepository": s.Registry.Selected(), "selectedRepositoryIdentity": selectedIdentity, "roborev": installation})
}

func (s *Server) repositoryDTO(repo gitx.Repository) (RepositoryDTO, error) {
	identity, err := s.Registry.Identity(repo.Path)
	if err != nil {
		return RepositoryDTO{}, err
	}
	return RepositoryDTO{Repository: repo, Identity: identity}, nil
}

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoForRequest(w, r.URL.Query().Get("repo"))
	if !ok {
		return
	}
	// The upstream contract supports bounded limits (1..200). Ordinary polling
	// keeps the small default; the lazy Tasks history view may raise it once
	// per repository switch so older task rows are not silently truncated.
	limit := 100
	if values := r.URL.Query()["limit"]; len(values) > 0 {
		if len(values) != 1 {
			writeError(w, http.StatusBadRequest, "limit must appear at most once", "request")
			return
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(values[0]))
		if err != nil || parsed < 1 || parsed > 200 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 200", "request")
			return
		}
		limit = parsed
	}
	raw, err := s.RoboRev.List(r.Context(), repo, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "list")
		return
	}
	writeRawJSON(w, http.StatusOK, raw)
}

func (s *Server) showReview(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	if len(query) == 0 && r.URL.RawQuery == "" {
		if !s.authorizeJob(w, r, id) {
			return
		}
	} else {
		identities, hasIdentity := query["repositoryIdentity"]
		if len(query) != 1 || !hasIdentity || len(identities) != 1 || strings.TrimSpace(identities[0]) == "" {
			writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "repository")
			return
		}
		resolved, err := s.Registry.ResolveIdentity(strings.TrimSpace(identities[0]))
		if err != nil {
			writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
			return
		}
		metadata, err := s.RoboRev.JobMetadata(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "job not found", "job")
			return
		}
		if metadata.ID != id || !gitx.SameRepositoryPath(metadata.RepoPath, resolved) {
			writeError(w, http.StatusForbidden, "job belongs to a different repository", "repository")
			return
		}
	}
	raw, err := s.RoboRev.Show(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "detail")
		return
	}
	writeRawJSON(w, http.StatusOK, raw)
}

const lazyContentRequestTimeout = 15 * time.Second

func (s *Server) showPrompt(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	id, ok := parseLazyContentID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lazyContentRequestTimeout)
	defer cancel()
	lazy, ok := s.RoboRev.(roborev.LazyContentAuthorizationService)
	if !ok {
		writeLazyContentError(w, roborev.ErrLazyContentUnsupported, "prompt")
		return
	}
	paths, incomplete, err := s.Registry.FreshRegisteredPathsWithStatus(ctx)
	if err != nil {
		writeLazyContentError(w, err, "prompt")
		return
	}
	if err := ctx.Err(); err != nil {
		writeLazyContentError(w, err, "prompt")
		return
	}
	value, err := lazy.AuthorizedPrompt(ctx, id, paths)
	if err != nil {
		if incomplete && errors.Is(err, roborev.ErrLazyContentForbidden) {
			err = roborev.ErrLazyContentWitnessUnavailable
		}
		writeLazyContentError(w, err, "prompt")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": value})
}
func (s *Server) showLog(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	id, ok := parseLazyContentID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lazyContentRequestTimeout)
	defer cancel()
	lazy, ok := s.RoboRev.(roborev.LazyContentAuthorizationService)
	if !ok {
		writeLazyContentError(w, roborev.ErrLazyContentUnsupported, "log")
		return
	}
	paths, incomplete, err := s.Registry.FreshRegisteredPathsWithStatus(ctx)
	if err != nil {
		writeLazyContentError(w, err, "log")
		return
	}
	if err := ctx.Err(); err != nil {
		writeLazyContentError(w, err, "log")
		return
	}
	value, err := lazy.AuthorizedLog(ctx, id, paths)
	if err != nil {
		if incomplete && errors.Is(err, roborev.ErrLazyContentForbidden) {
			err = roborev.ErrLazyContentWitnessUnavailable
		}
		writeLazyContentError(w, err, "log")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": value})
}

func writeLazyContentError(w http.ResponseWriter, err error, phase string) {
	status := http.StatusBadGateway
	message := "lazy content request failed"
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		status = http.StatusGatewayTimeout
		message = "lazy content request timed out"
	case errors.Is(err, roborev.ErrLazyContentUnsupported), errors.Is(err, roborev.ErrLazyContentOffline), errors.Is(err, roborev.ErrLazyContentWitnessUnavailable), errors.Is(err, roborev.ErrDaemonOffline), errors.Is(err, roborev.ErrBindingChanged), errors.Is(err, roborev.ErrUnsupportedJobMetadata):
		status = http.StatusServiceUnavailable
		message = "lazy content is temporarily unavailable"
	case errors.Is(err, roborev.ErrLazyContentNotFound), errors.Is(err, roborev.ErrJobMetadataNotFound):
		status = http.StatusNotFound
		message = "lazy content was not found"
	case errors.Is(err, roborev.ErrLazyContentForbidden):
		status = http.StatusForbidden
		message = "job belongs to an unregistered repository"
	case errors.Is(err, roborev.ErrLazyContentMalformed), errors.Is(err, roborev.ErrLazyContentOversized), errors.Is(err, roborev.ErrMalformedJobMetadata):
		status = http.StatusBadGateway
		message = "lazy content response is invalid"
	}
	writeError(w, status, message, phase)
}

func (s *Server) showFixPrompt(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	requestedRepo := r.URL.Query().Get("repo")
	if strings.TrimSpace(requestedRepo) == "" {
		writeError(w, http.StatusBadRequest, "repository query is required", "request")
		return
	}
	resolved, err := s.Registry.Resolve(requestedRepo)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error(), "repository")
		return
	}
	metadata, err := s.RoboRev.JobMetadata(r.Context(), id)
	if err != nil || metadata.ID != id {
		writeError(w, http.StatusNotFound, "job not found", "job")
		return
	}
	if !gitx.SameRepositoryPath(metadata.RepoPath, resolved) {
		writeError(w, http.StatusForbidden, "job belongs to a different repository", "repository")
		return
	}
	repo, err := s.Registry.Inspect(r.Context(), resolved)
	if err != nil {
		writeError(w, http.StatusConflict, "repository state is unavailable: "+err.Error(), "repository")
		return
	}
	raw, err := s.RoboRev.Show(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "fix-prompt")
		return
	}
	review, err := fixprompt.DecodeShow(raw)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error(), "fix-prompt")
		return
	}
	if review.JobID != id || !gitx.SameRepositoryPath(review.RepositoryPath, metadata.RepoPath) || !gitx.SameRepositoryPath(review.RepositoryPath, repo.Path) {
		writeError(w, http.StatusConflict, "RoboRev review identity does not match job metadata", "fix-prompt")
		return
	}
	if review.PanelRole != "" && review.PanelRole != "synthesis" && review.PanelRole != "member" {
		writeError(w, http.StatusConflict, "RoboRev review has an unsupported panel role", "fix-prompt")
		return
	}
	result, err := fixprompt.Generate(review, fixprompt.RepositoryState{Path: repo.Path, Head: repo.Head})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error(), "fix-prompt")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Text  string `json:"text"`
		JobID int64  `json:"jobId"`
		Stale bool   `json:"stale"`
	}{Text: result.Text, JobID: id, Stale: result.Stale})
}

type panelMembersRequest struct {
	run            string
	parentJobID    int64
	repositoryPath string
}

type panelMemberShow struct {
	JobID int64 `json:"job_id"`
	Job   struct {
		ID           int64  `json:"id"`
		RepoPath     string `json:"repo_path"`
		PanelRunUUID string `json:"panel_run_uuid"`
		PanelRole    string `json:"panel_role"`
	} `json:"job"`
}

func (s *Server) panelMembers(w http.ResponseWriter, r *http.Request) {
	_, _, raw, ok := s.loadPanelMembers(w, r)
	if !ok {
		return
	}
	writeRawJSON(w, http.StatusOK, raw)
}

func (s *Server) panelMember(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	request, jobs, _, ok := s.loadPanelMembers(w, r)
	if !ok {
		return
	}
	var member roborev.JobMetadata
	found := false
	for _, job := range jobs {
		if job.ID == id {
			member = job
			found = true
			break
		}
	}
	if !found || member.PanelRole != "member" {
		writeError(w, http.StatusNotFound, "panel member not found", "panel-members")
		return
	}
	raw, err := s.RoboRev.Show(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "panel-member-detail")
		return
	}
	if err := validatePanelMemberShow(raw, request, member); err != nil {
		status := http.StatusConflict
		if errors.Is(err, errPanelDetailMalformed) {
			status = http.StatusBadGateway
		}
		writeError(w, status, err.Error(), "panel-member-detail")
		return
	}
	writeRawJSON(w, http.StatusOK, raw)
}

func (s *Server) loadPanelMembers(w http.ResponseWriter, r *http.Request) (panelMembersRequest, []roborev.JobMetadata, json.RawMessage, bool) {
	request, ok := s.panelMembersRequest(w, r)
	if !ok {
		return panelMembersRequest{}, nil, nil, false
	}
	raw, err := s.RoboRev.PanelMembers(r.Context(), request.run)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "panel-members")
		return panelMembersRequest{}, nil, nil, false
	}
	jobs, err := decodePanelMemberList(raw)
	if err != nil {
		if errors.Is(err, errPanelRunNotFound) {
			writeError(w, http.StatusNotFound, "panel run not found", "panel-members")
		} else {
			writeError(w, http.StatusBadGateway, err.Error(), "panel-members")
		}
		return panelMembersRequest{}, nil, nil, false
	}
	if _, err := s.validatePanelMemberList(jobs, request); err != nil {
		status := http.StatusConflict
		switch {
		case errors.Is(err, errPanelForeignRepository):
			status = http.StatusForbidden
		case errors.Is(err, errPanelMalformed):
			status = http.StatusBadGateway
		}
		writeError(w, status, err.Error(), "panel-members")
		return panelMembersRequest{}, nil, nil, false
	}
	return request, jobs, raw, true
}

var (
	errPanelRunNotFound       = errors.New("panel run not found")
	errPanelForeignRepository = errors.New("panel belongs to an unregistered repository")
	errPanelMalformed         = errors.New("malformed panel members response")
	errPanelDetailMalformed   = errors.New("malformed panel member detail response")
)

func (s *Server) panelMembersRequest(w http.ResponseWriter, r *http.Request) (panelMembersRequest, bool) {
	values := r.URL.Query()
	if len(values) != 2 || len(values["parentJobId"]) != 1 || len(values["repositoryIdentity"]) != 1 || strings.TrimSpace(values.Get("parentJobId")) == "" || strings.TrimSpace(values.Get("repositoryIdentity")) == "" {
		writeError(w, http.StatusBadRequest, "parentJobId and repositoryIdentity are required", "request")
		return panelMembersRequest{}, false
	}
	parentJobID, err := strconv.ParseInt(values.Get("parentJobId"), 10, 64)
	if err != nil || parentJobID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid parent job ID", "request")
		return panelMembersRequest{}, false
	}
	run := strings.TrimSpace(r.PathValue("run"))
	if !validPanelRunUUID(run) {
		writeError(w, http.StatusBadRequest, "invalid panel run UUID", "request")
		return panelMembersRequest{}, false
	}
	repositoryID := strings.TrimSpace(values.Get("repositoryIdentity"))
	repositoryPath, err := s.Registry.ResolveIdentity(repositoryID)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error(), "repository")
		return panelMembersRequest{}, false
	}
	return panelMembersRequest{run: run, parentJobID: parentJobID, repositoryPath: repositoryPath}, true
}

func validPanelRunUUID(run string) bool {
	if len(run) != 36 {
		return false
	}
	for index, char := range run {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func validatePanelParent(parent roborev.JobMetadata, request panelMembersRequest) error {
	if parent.ID <= 0 || parent.ID != request.parentJobID {
		return errors.New("panel parent job identity does not match request")
	}
	if parent.PanelRole != "synthesis" {
		return errors.New("panel parent job is not the synthesis job")
	}
	if parent.JobType != "synthesis" {
		return errors.New("panel parent job has an invalid synthesis type")
	}
	if strings.TrimSpace(parent.PanelName) == "" || parent.PanelRunUUID != request.run {
		return errors.New("panel parent identity does not match run")
	}
	if !gitx.SameRepositoryPath(parent.RepoPath, request.repositoryPath) {
		return errors.New("panel parent repository does not match identity")
	}
	return nil
}

func decodePanelMemberList(raw json.RawMessage) ([]roborev.JobMetadata, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var response struct {
		Jobs json.RawMessage `json:"jobs"`
	}
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("%w: %v", errPanelMalformed, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing JSON", errPanelMalformed)
	}
	if len(response.Jobs) == 0 || bytes.Equal(bytes.TrimSpace(response.Jobs), []byte("null")) {
		return nil, fmt.Errorf("%w: jobs is required", errPanelMalformed)
	}
	var jobs []roborev.JobMetadata
	if err := json.Unmarshal(response.Jobs, &jobs); err != nil {
		return nil, fmt.Errorf("%w: jobs: %v", errPanelMalformed, err)
	}
	if len(jobs) == 0 {
		return nil, errPanelRunNotFound
	}
	return jobs, nil
}

func (s *Server) validatePanelMemberList(jobs []roborev.JobMetadata, request panelMembersRequest) (roborev.JobMetadata, error) {
	seen := make(map[int64]struct{}, len(jobs))
	parentCount := 0
	var parent roborev.JobMetadata
	for _, job := range jobs {
		if job.ID <= 0 {
			return roborev.JobMetadata{}, errors.New("panel member job ID must be positive")
		}
		if _, ok := seen[job.ID]; ok {
			return roborev.JobMetadata{}, errors.New("panel members response contains duplicate job IDs")
		}
		seen[job.ID] = struct{}{}
		if strings.TrimSpace(job.RepoPath) == "" || strings.TrimSpace(job.PanelName) == "" || strings.TrimSpace(job.PanelRunUUID) == "" || strings.TrimSpace(job.PanelRole) == "" {
			return roborev.JobMetadata{}, errors.New("panel member identity fields do not match the requested panel")
		}
		if job.PanelRole != "synthesis" && job.PanelRole != "member" {
			return roborev.JobMetadata{}, errors.New("panel member has an unsupported panel role")
		}
		if !gitx.SameRepositoryPath(job.RepoPath, request.repositoryPath) {
			if !s.Registry.Contains(job.RepoPath) {
				return roborev.JobMetadata{}, errPanelForeignRepository
			}
			return roborev.JobMetadata{}, errors.New("panel member repository does not match identity")
		}
		if job.PanelRunUUID != request.run {
			return roborev.JobMetadata{}, errors.New("panel member identity does not match requested panel run")
		}
		if job.PanelRole == "synthesis" {
			if job.JobType != "synthesis" {
				return roborev.JobMetadata{}, errors.New("panel synthesis job has an invalid synthesis type")
			}
			parentCount++
			if job.ID != request.parentJobID {
				return roborev.JobMetadata{}, errors.New("panel synthesis job does not match parent job")
			}
			parent = job
		}
	}
	if parentCount != 1 {
		return roborev.JobMetadata{}, errors.New("panel members response must contain exactly one synthesis job")
	}
	if err := validatePanelParent(parent, request); err != nil {
		return roborev.JobMetadata{}, err
	}
	for _, job := range jobs {
		if !panelIdentityMatches(job, parent, request.run) {
			return roborev.JobMetadata{}, errors.New("panel member identity does not match parent panel")
		}
	}
	return parent, nil
}

func panelIdentityMatches(job, parent roborev.JobMetadata, run string) bool {
	return job.PanelRunUUID == run && job.PanelName == parent.PanelName
}

func validatePanelMemberShow(raw json.RawMessage, request panelMembersRequest, member roborev.JobMetadata) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var response panelMemberShow
	if err := decoder.Decode(&response); err != nil {
		return fmt.Errorf("%w: %v", errPanelDetailMalformed, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON", errPanelDetailMalformed)
	}
	job := response.Job
	if response.JobID <= 0 || job.ID <= 0 || strings.TrimSpace(job.RepoPath) == "" || strings.TrimSpace(job.PanelRunUUID) == "" || strings.TrimSpace(job.PanelRole) == "" {
		return fmt.Errorf("%w: job identity fields are required", errPanelDetailMalformed)
	}
	if response.JobID != job.ID || job.ID != member.ID {
		return errors.New("panel member detail job identity does not match request")
	}
	if job.PanelRole != "member" || job.PanelRole != member.PanelRole || job.PanelRunUUID != request.run || job.PanelRunUUID != member.PanelRunUUID {
		return errors.New("panel member detail panel identity does not match request")
	}
	if !gitx.SameRepositoryPath(job.RepoPath, request.repositoryPath) || !gitx.SameRepositoryPath(job.RepoPath, member.RepoPath) {
		return errors.New("panel member detail repository does not match identity")
	}
	return nil
}

func (s *Server) authorizeJob(w http.ResponseWriter, r *http.Request, id int64) bool {
	job, err := s.RoboRev.JobMetadata(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found", "job")
		return false
	}
	if !s.Registry.Contains(job.RepoPath) {
		writeError(w, http.StatusForbidden, "job belongs to an unregistered repository", "repository")
		return false
	}
	return true
}

func mutationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

type repoBody struct {
	RepoPath string `json:"repoPath"`
}
type reviewBody struct {
	RepoPath string `json:"repoPath"`
	Panel    string `json:"panel"`
}
type panelSelectionBody struct {
	RepoPath string `json:"repoPath"`
	Panel    string `json:"panel"`
}

type panelRefineBody struct {
	RepoPath       string `json:"repoPath"`
	Panel          string `json:"panel"`
	FixAgent       string `json:"fixAgent"`
	MaxFixAttempts int    `json:"maxFixAttempts"`
	FinalPanel     string `json:"finalPanel"`
}

type fixBody struct {
	RepositoryIdentity string `json:"repositoryIdentity"`
	TargetJobID        int64  `json:"targetJobId"`
	Agent              string `json:"agent"`
	PreviewToken       string `json:"previewToken"`
}

func fixRequest(body fixBody) fixoperation.Request {
	return fixoperation.Request{RepositoryIdentity: body.RepositoryIdentity, TargetJobID: body.TargetJobID, Agent: body.Agent}
}

func fixErrorStatus(err error) int {
	var typed *fixoperation.Error
	if !errors.As(err, &typed) {
		return http.StatusInternalServerError
	}
	switch typed.Kind {
	case fixoperation.ErrorInvalid:
		return http.StatusBadRequest
	case fixoperation.ErrorForbidden:
		return http.StatusForbidden
	case fixoperation.ErrorNotFound:
		return http.StatusNotFound
	case fixoperation.ErrorConflict:
		return http.StatusConflict
	case fixoperation.ErrorUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func (s *Server) fixPreview(w http.ResponseWriter, r *http.Request) {
	if s.Fixes == nil {
		writeError(w, http.StatusServiceUnavailable, "Fix operation manager is unavailable", "fix")
		return
	}
	agent := r.URL.Query().Get("agent")
	if agent == "" {
		agent = "codex"
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("targetJobId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "targetJobId must be a positive integer", "targetJobId")
		return
	}
	preview, err := s.Fixes.Prepare(r.Context(), fixoperation.Request{RepositoryIdentity: r.URL.Query().Get("repositoryIdentity"), TargetJobID: id, Agent: agent})
	if err != nil {
		writeError(w, fixErrorStatus(err), err.Error(), "fix")
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

// taskExecutionDisabledMessage states the fail-closed reason without implying
// that any local config inspection can attest the daemon's effective state.
const taskExecutionDisabledMessage = "RoboRev v0.64 Custom Task execution is disabled: the daemon provides no machine-readable contract for its effective safety configuration, so the Dashboard cannot verify Codex read-only execution."

func (s *Server) taskPreview(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, taskExecutionDisabledMessage, "task-execution-disabled")
}

func (s *Server) startTask(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, taskExecutionDisabledMessage, "task-execution-disabled")
}

func analysisQuery(r *http.Request, allowed ...string) (map[string]string, error) {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	values := r.URL.Query()
	result := make(map[string]string, len(allowed))
	for name, entries := range values {
		if _, ok := allowedSet[name]; !ok || len(entries) != 1 || strings.TrimSpace(entries[0]) == "" {
			return nil, errors.New("unknown or duplicate analysis query field")
		}
		result[name] = entries[0]
	}
	return result, nil
}

func analysisErrorStatus(err error) int {
	var typed *analysis.Error
	if !errors.As(err, &typed) {
		return http.StatusInternalServerError
	}
	switch typed.Kind {
	case analysis.ErrorInvalid:
		return http.StatusBadRequest
	case analysis.ErrorUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func (s *Server) analysisTypes(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	if s.Analyses == nil {
		writeError(w, http.StatusServiceUnavailable, "Analyze capability is unavailable", "analysis")
		return
	}
	if len(r.URL.Query()) != 0 {
		writeError(w, http.StatusBadRequest, "unknown analysis query field", "request")
		return
	}
	types, err := s.Analyses.Types(r.Context())
	if err != nil {
		writeError(w, analysisErrorStatus(err), err.Error(), "analysis-types")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Types []roborev.AnalysisType `json:"types"`
	}{Types: types})
}

func (s *Server) analysisPrompt(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	if s.Analyses == nil {
		writeError(w, http.StatusServiceUnavailable, "Analyze capability is unavailable", "analysis")
		return
	}
	query, err := analysisQuery(r, "type")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "request")
		return
	}
	if query["type"] == "" {
		writeError(w, http.StatusBadRequest, "type is required", "analysis-type")
		return
	}
	prompt, err := s.Analyses.Prompt(r.Context(), query["type"])
	if err != nil {
		writeError(w, analysisErrorStatus(err), err.Error(), "analysis-prompt")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		AnalysisType string `json:"analysisType"`
		Prompt       string `json:"prompt"`
	}{AnalysisType: query["type"], Prompt: prompt})
}

func (s *Server) analysisPreview(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "RoboRev v0.64 Analyze execution is disabled because v0.64 analysis jobs run in agentic mode.", "analysis-execution-disabled")
}

func (s *Server) startAnalysis(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "RoboRev v0.64 Analyze execution is disabled because v0.64 analysis jobs run in agentic mode.", "analysis-execution-disabled")
}

func (s *Server) listFixes(w http.ResponseWriter, r *http.Request) {
	if s.Fixes == nil {
		writeJSON(w, http.StatusOK, fixoperation.ListResponse{Operations: []*fixoperation.Operation{}})
		return
	}
	identity := strings.TrimSpace(r.URL.Query().Get("repositoryIdentity"))
	if identity == "" {
		identity = strings.TrimSpace(r.URL.Query().Get("repo"))
	}
	if identity == "" {
		writeError(w, http.StatusBadRequest, "repository identity is required", "repository")
		return
	}
	if _, err := s.Registry.ResolveIdentity(identity); err != nil {
		writeError(w, http.StatusForbidden, err.Error(), "repository")
		return
	}
	writeJSON(w, http.StatusOK, s.Fixes.List(identity))
}

func (s *Server) getFix(w http.ResponseWriter, r *http.Request) {
	if s.Fixes == nil {
		writeError(w, http.StatusNotFound, "Fix operation not found", "fix")
		return
	}
	item, ok := s.Fixes.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Fix operation not found", "fix")
		return
	}
	if _, err := s.Registry.ResolveIdentity(item.RepositoryIdentity); err != nil {
		writeError(w, http.StatusForbidden, err.Error(), "repository")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) startFix(w http.ResponseWriter, r *http.Request) {
	if s.Fixes == nil {
		writeError(w, http.StatusServiceUnavailable, "Fix operation manager is unavailable", "fix")
		return
	}
	var body fixBody
	if !decodeBody(w, r, &body) {
		return
	}
	item, err := s.Fixes.Start(r.Context(), fixoperation.StartRequest{Request: fixRequest(body), PreviewToken: body.PreviewToken})
	if err != nil {
		writeError(w, fixErrorStatus(err), err.Error(), "fix")
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) cancelFix(w http.ResponseWriter, r *http.Request) {
	if s.Fixes == nil {
		writeError(w, http.StatusNotFound, "Fix operation not found", "fix")
		return
	}
	item, ok := s.Fixes.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Fix operation not found", "fix")
		return
	}
	if _, err := s.Registry.ResolveIdentity(item.RepositoryIdentity); err != nil {
		writeError(w, http.StatusForbidden, err.Error(), "repository")
		return
	}
	if err := s.Fixes.Cancel(item.ID); err != nil {
		writeError(w, fixErrorStatus(err), err.Error(), "fix")
		return
	}
	item, _ = s.Fixes.Get(item.ID)
	writeJSON(w, http.StatusAccepted, item)
}

type commentBody struct {
	Comment string `json:"comment"`
}

func (s *Server) createReview(w http.ResponseWriter, r *http.Request) {
	var body reviewBody
	if !decodeBody(w, r, &body) {
		return
	}
	path, ok := s.repoForRequest(w, body.RepoPath)
	if !ok {
		return
	}
	var releaseReviewLease func()
	if s.Refines != nil {
		var leaseErr error
		releaseReviewLease, leaseErr = s.Refines.AcquireReviewLease(path)
		if leaseErr != nil {
			writeError(w, http.StatusConflict, leaseErr.Error(), "repository")
			return
		}
		defer releaseReviewLease()
	}
	repo, err := s.Registry.Inspect(r.Context(), path)
	if err != nil {
		writeError(w, http.StatusConflict, "repository state is unavailable: "+err.Error(), "repository")
		return
	}
	if strings.TrimSpace(body.Panel) == "" {
		writeError(w, http.StatusBadRequest, "panel selection is required", "panel")
		return
	}
	catalog, err := s.Panels.Resolve(r.Context(), repo.Path)
	if err != nil {
		writeError(w, http.StatusConflict, "panel configuration is unavailable: "+err.Error(), "panel")
		return
	}
	if _, ok := catalog.Find(body.Panel); !ok {
		writeError(w, http.StatusBadRequest, "panel is not currently available", "panel")
		return
	}
	request := roborev.ReviewRequest{RepoPath: repo.Path, GitRef: repo.Head, Branch: repo.Branch, Panel: body.Panel}
	target, err := s.repositoryDTO(repo)
	if err != nil {
		writeError(w, http.StatusConflict, "repository identity is unavailable: "+err.Error(), "repository")
		return
	}
	ctx, cancel := mutationContext()
	defer cancel()
	receipt, err := s.RoboRev.Review(ctx, request)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "enqueue")
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Receipt json.RawMessage `json:"receipt"`
		Target  RepositoryDTO   `json:"target"`
	}{Receipt: receipt, Target: target})
}

func (s *Server) startPanelRefine(w http.ResponseWriter, r *http.Request) {
	if s.Refines == nil {
		writeError(w, http.StatusNotImplemented, "Panel Refine is unavailable", "panel-refine")
		return
	}
	var body panelRefineBody
	if !decodeBody(w, r, &body) {
		return
	}
	run, err := s.Refines.Start(r.Context(), refine.StartRequest{RepoPath: body.RepoPath, Panel: body.Panel, FixAgent: body.FixAgent, MaxFixAttempts: body.MaxFixAttempts, FinalPanel: body.FinalPanel})
	if err != nil {
		writeError(w, panelRefineStartStatus(err), err.Error(), "panel-refine")
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func panelRefineStartStatus(err error) int {
	var typed *refine.StartError
	if !errors.As(err, &typed) {
		return http.StatusInternalServerError
	}
	switch typed.Kind {
	case refine.StartInvalid:
		return http.StatusBadRequest
	case refine.StartForbidden:
		return http.StatusForbidden
	case refine.StartUnavailable:
		return http.StatusServiceUnavailable
	case refine.StartConflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func (s *Server) listPanelRefines(w http.ResponseWriter, r *http.Request) {
	if s.Refines == nil {
		writeJSON(w, http.StatusOK, []*refine.PanelRefineRun{})
		return
	}
	path := r.URL.Query().Get("repo")
	if path == "" {
		path = r.URL.Query().Get("repoPath")
	}
	if strings.TrimSpace(path) == "" {
		writeError(w, http.StatusBadRequest, "registered repository is required", "repository")
		return
	}
	resolved, err := s.Registry.Resolve(path)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error(), "repository")
		return
	}
	writeJSON(w, http.StatusOK, s.Refines.ListResponse(resolved))
}

func (s *Server) getPanelRefine(w http.ResponseWriter, r *http.Request) {
	if s.Refines == nil {
		writeError(w, http.StatusNotFound, "Panel Refine run not found", "panel-refine")
		return
	}
	run, ok := s.Refines.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Panel Refine run not found", "panel-refine")
		return
	}
	if !s.Registry.Contains(run.RepositoryPath) {
		writeError(w, http.StatusForbidden, "repository is not registered", "repository")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) cancelPanelRefine(w http.ResponseWriter, r *http.Request) {
	if s.Refines == nil {
		writeError(w, http.StatusNotFound, "Panel Refine run not found", "panel-refine")
		return
	}
	run, exists := s.Refines.Get(r.PathValue("id"))
	if !exists {
		writeError(w, http.StatusNotFound, "Panel Refine run not found", "panel-refine")
		return
	}
	if !s.Registry.Contains(run.RepositoryPath) {
		writeError(w, http.StatusForbidden, "repository is not registered", "repository")
		return
	}
	if err := s.Refines.Cancel(r.PathValue("id")); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error(), "panel-refine")
		} else {
			writeError(w, http.StatusConflict, err.Error(), "panel-refine")
		}
		return
	}
	run, _ = s.Refines.Get(r.PathValue("id"))
	writeJSON(w, http.StatusAccepted, run)
}

func resolvePanelState(identity string, catalog roborev.PanelCatalog, saved string, hasSaved bool) RepositoryPanels {
	state := RepositoryPanels{RepositoryIdentity: identity, Panels: catalog.Panels}
	if hasSaved {
		if _, ok := catalog.Find(saved); ok {
			state.SelectedPanel = saved
			return state
		}
	}
	named := ""
	namedCount := 0
	for _, panel := range catalog.Panels {
		if panel.Valid && panel.Kind == "panel" {
			named, namedCount = panel.Name, namedCount+1
		}
	}
	switch namedCount {
	case 0:
		state.SelectedPanel = roborev.PanelNone
	case 1:
		state.SelectedPanel = named
	default:
		state.SelectionRequired = true
	}
	return state
}

func (s *Server) panelState(ctx context.Context, path string) (RepositoryPanels, error) {
	repo, err := s.Registry.Inspect(ctx, path)
	if err != nil {
		return RepositoryPanels{}, err
	}
	identity, err := s.Registry.Identity(repo.Path)
	if err != nil {
		return RepositoryPanels{}, err
	}
	catalog, err := s.Panels.Resolve(ctx, repo.Path)
	if err != nil {
		return RepositoryPanels{RepositoryIdentity: identity, Panels: []roborev.PanelInfo{}, Error: err.Error()}, nil
	}
	saved, hasSaved, err := s.Registry.PanelSelection(repo.Path)
	if err != nil {
		return RepositoryPanels{}, err
	}
	return resolvePanelState(identity, catalog, saved, hasSaved), nil
}

func (s *Server) repositoryPanels(w http.ResponseWriter, r *http.Request) {
	path, ok := s.repoForRequest(w, r.URL.Query().Get("repo"))
	if !ok {
		return
	}
	state, err := s.panelState(r.Context(), path)
	if err != nil {
		writeError(w, http.StatusConflict, "repository panels are unavailable: "+err.Error(), "panel")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) selectRepositoryPanel(w http.ResponseWriter, r *http.Request) {
	var body panelSelectionBody
	if !decodeBody(w, r, &body) {
		return
	}
	path, ok := s.repoForRequest(w, body.RepoPath)
	if !ok {
		return
	}
	repo, err := s.Registry.Inspect(r.Context(), path)
	if err != nil {
		writeError(w, http.StatusConflict, "repository state is unavailable: "+err.Error(), "repository")
		return
	}
	catalog, err := s.Panels.Resolve(r.Context(), repo.Path)
	if err != nil {
		writeError(w, http.StatusConflict, "panel configuration is unavailable: "+err.Error(), "panel")
		return
	}
	if _, exists := catalog.Find(body.Panel); !exists {
		writeError(w, http.StatusBadRequest, "panel is not currently available", "panel")
		return
	}
	if err := s.Registry.SetPanelSelection(repo.Path, body.Panel); err != nil {
		writeError(w, http.StatusInternalServerError, "save panel selection: "+err.Error(), "panel")
		return
	}
	identity, _ := s.Registry.Identity(repo.Path)
	writeJSON(w, http.StatusOK, resolvePanelState(identity, catalog, body.Panel, true))
}

func (s *Server) comment(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var body commentBody
	if !decodeBody(w, r, &body) {
		return
	}
	if !s.authorizeJob(w, r, id) {
		return
	}
	ctx, cancel := mutationContext()
	defer cancel()
	if err := s.RoboRev.Comment(ctx, id, body.Comment); err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "comment")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) commentClose(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var body commentBody
	if !decodeBody(w, r, &body) {
		return
	}
	if !s.authorizeJob(w, r, id) {
		return
	}
	ctx, cancel := mutationContext()
	defer cancel()
	if err := s.RoboRev.Comment(ctx, id, body.Comment); err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "comment")
		return
	}
	if err := s.RoboRev.Close(ctx, id, false); err != nil {
		writeError(w, http.StatusBadGateway, "comment succeeded but close failed: "+err.Error(), "close")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) closeReview(w http.ResponseWriter, r *http.Request)  { s.closeOrReopen(w, r, false) }
func (s *Server) reopenReview(w http.ResponseWriter, r *http.Request) { s.closeOrReopen(w, r, true) }
func (s *Server) closeOrReopen(w http.ResponseWriter, r *http.Request, reopen bool) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if !s.authorizeJob(w, r, id) {
		return
	}
	ctx, cancel := mutationContext()
	defer cancel()
	if err := s.RoboRev.Close(ctx, id, reopen); err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "close")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
func (s *Server) cancelReview(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if !s.authorizeJob(w, r, id) {
		return
	}
	ctx, cancel := mutationContext()
	defer cancel()
	if err := s.RoboRev.Cancel(ctx, id); err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "cancel")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
func (s *Server) rerunReview(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	job, err := s.RoboRev.JobMetadata(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found", "job")
		return
	}
	// Repository ownership is authorized before any job-type policy so an
	// unregistered repository's job never reveals its type or rerun policy.
	if !s.Registry.Contains(job.RepoPath) {
		writeError(w, http.StatusForbidden, "job belongs to an unregistered repository", "repository")
		return
	}
	// A run job is a Custom Task and cannot be replayed through the generic
	// review rerun endpoint. Tasks require their own fresh safety preflight and
	// explicit confirmation; otherwise this route would bypass that boundary.
	if strings.TrimSpace(job.JobType) == "task" || strings.TrimSpace(job.GitRef) == "run" {
		writeError(w, http.StatusConflict, "Tasks v1 does not support rerun; generic frozen rerun is unavailable for task jobs", "task-rerun-unsupported")
		return
	}
	// Insights enqueue is an LLM/quota-bearing workflow with its own
	// preview/confirmation boundary. Generic frozen rerun must fail closed so
	// a stored Insights prompt cannot be replayed without a fresh preview.
	if strings.TrimSpace(job.JobType) == "insights" {
		writeError(w, http.StatusConflict, "Guideline Insights requires a fresh preview and explicit confirmation; generic rerun is unavailable", "insights-rerun-unsupported")
		return
	}
	var releaseReviewLease func()
	if s.Refines != nil {
		releaseReviewLease, err = s.Refines.AcquireReviewLease(job.RepoPath)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error(), "repository")
			return
		}
		defer releaseReviewLease()
	}
	ctx, cancel := mutationContext()
	defer cancel()
	if err := s.RoboRev.Rerun(ctx, id); err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "rerun")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"success": true})
}

func (s *Server) addRepository(w http.ResponseWriter, r *http.Request) {
	var body repoBody
	if !decodeBody(w, r, &body) {
		return
	}
	repo, err := s.Registry.Add(r.Context(), body.RepoPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "repository")
		return
	}
	dto, err := s.repositoryDTO(repo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "repository identity is unavailable: "+err.Error(), "repository")
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}
func (s *Server) selectRepository(w http.ResponseWriter, r *http.Request) {
	var body repoBody
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.Registry.SelectContext(r.Context(), body.RepoPath); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusRequestTimeout, err.Error(), "repository")
			return
		}
		writeError(w, http.StatusForbidden, err.Error(), "repository")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
func spaHandler() http.Handler {
	assets, err := fs.Sub(webui.Files, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(assets))
	serveIndex := func(w http.ResponseWriter) {
		data, readErr := fs.ReadFile(assets, "index.html")
		if readErr != nil {
			http.Error(w, "frontend assets are not built; run scripts/build.ps1", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "index.html" {
			serveIndex(w)
			return
		}
		if _, err := fs.Stat(assets, path); err == nil {
			clone := r.Clone(r.Context())
			clone.URL.Path = "/" + path
			files.ServeHTTP(w, clone)
			return
		}
		serveIndex(w)
	})
}
