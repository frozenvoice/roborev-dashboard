package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/agentstatus"
	"github.com/froze/roborev-dashboard/internal/fixprompt"
	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/refine"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/runtimecontrol"
	"github.com/froze/roborev-dashboard/internal/setup"
	"github.com/froze/roborev-dashboard/internal/webui"
)

type Server struct {
	RoboRev     roborev.Service
	Registry    *Registry
	Panels      roborev.PanelCatalogResolver
	Refines     *refine.Manager
	Setup       *setup.Manager
	Agents      *agentstatus.Service
	Runtime     *runtimecontrol.Manager
	Gate        *operation.Gate
	AllowedHost string
	csrfToken   string
	mux         *http.ServeMux
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
	panels := roborev.NewPanelResolver(registry)
	// Ensure the long-lived Panel Refine manager and HTTP mutations share the
	// same lifecycle gate.
	refines, err := refine.NewManager(refine.Dependencies{Registry: registry, Panels: panels, RoboRev: service, Binary: serviceBinary(service), Gate: gate})
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
	s := &Server{RoboRev: service, Registry: registry, Panels: panels, Refines: refines, Setup: setupManager, Agents: agentstatus.NewService(panels, checker), Runtime: runtimeManager, Gate: gate, AllowedHost: allowedHost, csrfToken: base64.RawURLEncoding.EncodeToString(tokenBytes), mux: http.NewServeMux()}
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

// Shutdown cancels every process-scoped operation and waits for its gate
// lease and child work to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	var shutdownErrors []error
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
	if s.Setup != nil {
		if err := s.Setup.Shutdown(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	}
	return errors.Join(shutdownErrors...)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/bootstrap", s.bootstrap)
	s.mux.HandleFunc("GET /api/runtime", s.runtime)
	s.mux.HandleFunc("GET /api/agents", s.agentStatuses)
	s.mux.HandleFunc("GET /api/runtime-control", s.runtimeControlStatus)
	s.mux.HandleFunc("GET /api/setup/roborev", s.setupStatus)
	s.mux.HandleFunc("GET /api/reviews", s.listReviews)
	s.mux.HandleFunc("GET /api/reviews/{id}", s.showReview)
	s.mux.HandleFunc("GET /api/reviews/{id}/prompt", s.showPrompt)
	s.mux.HandleFunc("GET /api/reviews/{id}/log", s.showLog)
	s.mux.HandleFunc("GET /api/reviews/{id}/fix-prompt", s.showFixPrompt)
	s.mux.HandleFunc("GET /api/panels/{run}/members", s.panelMembers)
	s.mux.HandleFunc("GET /api/repos/panels", s.repositoryPanels)
	s.mux.HandleFunc("GET /api/panel-refines", s.listPanelRefines)
	s.mux.HandleFunc("GET /api/panel-refines/{id}", s.getPanelRefine)
	s.mux.HandleFunc("POST /api/reviews", s.mutation(s.createReview))
	s.mux.HandleFunc("POST /api/panel-refines", s.mutation(s.startPanelRefine))
	s.mux.HandleFunc("POST /api/panel-refines/{id}/cancel", s.mutation(s.cancelPanelRefine))
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

func (s *Server) setupMutation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeMutation(w, r) {
			return
		}
		next(w, r)
	}
}

func (s *Server) authorizeMutation(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-CSRF-Token") != s.csrfToken {
		writeError(w, http.StatusForbidden, "invalid CSRF token", "csrf")
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(parsed.Host, r.Host) {
			writeError(w, http.StatusForbidden, "cross-origin mutation rejected", "origin")
			return false
		}
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
	raw, err := s.RoboRev.List(r.Context(), repo, 100)
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
	if !s.authorizeJob(w, r, id) {
		return
	}
	raw, err := s.RoboRev.Show(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "detail")
		return
	}
	writeRawJSON(w, http.StatusOK, raw)
}
func (s *Server) showPrompt(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if !s.authorizeJob(w, r, id) {
		return
	}
	value, err := s.RoboRev.Prompt(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "prompt")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": value})
}
func (s *Server) showLog(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if !s.authorizeJob(w, r, id) {
		return
	}
	value, err := s.RoboRev.Log(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "log")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": value})
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

func (s *Server) panelMembers(w http.ResponseWriter, r *http.Request) {
	raw, err := s.RoboRev.PanelMembers(r.Context(), r.PathValue("run"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "panel-members")
		return
	}
	var response struct {
		Jobs []roborev.JobMetadata `json:"jobs"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Jobs) == 0 {
		writeError(w, http.StatusNotFound, "panel run not found", "panel-members")
		return
	}
	for _, job := range response.Jobs {
		if !s.Registry.Contains(job.RepoPath) {
			writeError(w, http.StatusForbidden, "panel belongs to an unregistered repository", "repository")
			return
		}
	}
	writeRawJSON(w, http.StatusOK, raw)
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
	if !s.Registry.Contains(job.RepoPath) {
		writeError(w, http.StatusForbidden, "job belongs to an unregistered repository", "repository")
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
