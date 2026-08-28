package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/repomerge"
	"github.com/froze/roborev-dashboard/internal/repomove"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

const (
	repositoryMergePreviewTTL = 3 * time.Minute
	repositoryMergePreviewMax = 64
)

type repositoryMergePreviewBody struct {
	SourcePath               *string `json:"sourcePath"`
	TargetRepositoryIdentity *string `json:"targetRepositoryIdentity"`
}

type repositoryMergeConfirmBody struct {
	Token string `json:"token"`
}

type repositoryMergePreviewRecord struct {
	Request        repomerge.Request
	TargetIdentity string
	Preflight      repomerge.Preflight
	ExpiresAt      time.Time
}

// repositoryMergePreviewStore is deliberately independent from the move and
// rename stores. Its active map is only a same-feature claim guard; the shared
// operation Gate remains the authority for cross-feature repository leases.
type repositoryMergePreviewStore struct {
	mu      sync.Mutex
	pending map[string]repositoryMergePreviewRecord
	active  map[string]struct{}
}

func newRepositoryMergePreviewStore() *repositoryMergePreviewStore {
	return &repositoryMergePreviewStore{pending: make(map[string]repositoryMergePreviewRecord), active: make(map[string]struct{})}
}

func (s *repositoryMergePreviewStore) put(record repositoryMergePreviewRecord) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.expireLocked(now)
	for len(s.pending) >= repositoryMergePreviewMax {
		oldest := ""
		var oldestExpiry time.Time
		for candidate, pending := range s.pending {
			if oldest == "" || pending.ExpiresAt.Before(oldestExpiry) || (pending.ExpiresAt.Equal(oldestExpiry) && candidate < oldest) {
				oldest, oldestExpiry = candidate, pending.ExpiresAt
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

func (s *repositoryMergePreviewStore) peek(token string) (repositoryMergePreviewRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(time.Now())
	record, ok := s.pending[token]
	return record, ok
}

func (s *repositoryMergePreviewStore) keys(record repositoryMergePreviewRecord) []string {
	return []string{
		gitx.RepositoryKey(record.Preflight.Source.Path),
		gitx.RepositoryKey(record.Preflight.Target.Path),
	}
}

func (s *repositoryMergePreviewStore) claim(token string) (repositoryMergePreviewRecord, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(time.Now())
	record, ok := s.pending[token]
	if !ok {
		return repositoryMergePreviewRecord{}, false, false
	}
	keys := s.keys(record)
	for _, key := range keys {
		if _, busy := s.active[key]; busy {
			return repositoryMergePreviewRecord{}, false, true
		}
	}
	delete(s.pending, token)
	for _, key := range keys {
		s.active[key] = struct{}{}
	}
	return record, true, false
}

func (s *repositoryMergePreviewStore) release(record repositoryMergePreviewRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range s.keys(record) {
		delete(s.active, key)
	}
}

func (s *repositoryMergePreviewStore) expireLocked(now time.Time) {
	for token, record := range s.pending {
		if !now.Before(record.ExpiresAt) {
			delete(s.pending, token)
		}
	}
}

type repositoryMergeSnapshotDTO struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	JobCount int64  `json:"jobCount"`
}

type repositoryMergeCandidateDTO struct {
	repositoryMergeSnapshotDTO
	RepositoryIdentity string `json:"repositoryIdentity"`
}

type repositoryMergeEligibilityDTO struct {
	Eligible bool                          `json:"eligible"`
	Source   *repositoryMergeSnapshotDTO   `json:"source,omitempty"`
	Targets  []repositoryMergeCandidateDTO `json:"targets,omitempty"`
	Reason   string                        `json:"reason,omitempty"`
}

type repositoryMergeCommandDTO struct {
	Executable       string   `json:"executable"`
	Args             []string `json:"args"`
	WorkingDirectory string   `json:"workingDirectory"`
}

type repositoryMergePreviewDTO struct {
	Source    repositoryMergeSnapshotDTO  `json:"source"`
	Target    repositoryMergeCandidateDTO `json:"target"`
	Command   repositoryMergeCommandDTO   `json:"command"`
	Token     string                      `json:"token"`
	ExpiresAt time.Time                   `json:"expiresAt"`
}

func mergeSnapshotDTO(snapshot repomerge.RepositorySnapshot) repositoryMergeSnapshotDTO {
	return repositoryMergeSnapshotDTO{Path: snapshot.Path, Name: snapshot.Name, JobCount: snapshot.JobCount}
}

func mergeCandidateDTO(snapshot repomerge.RepositorySnapshot, identity string) repositoryMergeCandidateDTO {
	return repositoryMergeCandidateDTO{repositoryMergeSnapshotDTO: mergeSnapshotDTO(snapshot), RepositoryIdentity: identity}
}

// repositoryMergeEligibility is intentionally the only app-side discovery
// operation. The adapter owns version, health, inventory, stale filesystem,
// and active-job checks; the app only combines its fresh Registry view with
// the sanitized inventory to issue Dashboard identities for targets.
func (s *Server) repositoryMergeEligibility(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "repository merge eligibility does not accept query parameters", "request")
		return
	}
	var body repositoryMergeEligibilityBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.SourcePath == nil || strings.TrimSpace(*body.SourcePath) == "" {
		writeError(w, http.StatusBadRequest, "sourcePath is required", "request")
		return
	}
	sourcePath := *body.SourcePath
	if s.Registry.Contains(sourcePath) {
		writeJSON(w, http.StatusOK, repositoryMergeEligibilityDTO{Eligible: false, Reason: "dashboard-registered"})
		return
	}
	service, ok := s.RoboRev.(roborev.RepositoryMergeService)
	if !ok {
		writeRepositoryMergeError(w, roborev.ErrUnsupportedRepositoryMerge)
		return
	}
	eligibility, err := service.RepositoryMergeEligibility(r.Context(), sourcePath)
	if err != nil {
		if reason, ok := mergeEligibilityReason(err); ok {
			writeJSON(w, http.StatusOK, repositoryMergeEligibilityDTO{Eligible: false, Reason: reason})
			return
		}
		writeRepositoryMergeError(w, err)
		return
	}
	if s.Registry.Contains(eligibility.Source.Path) {
		writeJSON(w, http.StatusOK, repositoryMergeEligibilityDTO{Eligible: false, Reason: "dashboard-registered"})
		return
	}
	if strings.TrimSpace(eligibility.Source.Path) == "" || !gitx.SameRepositoryPath(eligibility.Source.Path, sourcePath) {
		writeError(w, http.StatusBadGateway, "RoboRev returned an invalid repository merge source", "repository-merge")
		return
	}
	if eligibility.Source.JobCount <= 0 {
		writeJSON(w, http.StatusOK, repositoryMergeEligibilityDTO{Eligible: false, Reason: "source-empty"})
		return
	}
	candidates, ambiguous := s.repositoryMergeCandidates(r.Context(), eligibility.Inventory.Repositories)
	if ambiguous {
		writeError(w, http.StatusConflict, "a registered target repository is ambiguous in RoboRev; refresh and try again", "repository-merge")
		return
	}
	if len(candidates) == 0 {
		writeJSON(w, http.StatusOK, repositoryMergeEligibilityDTO{Eligible: false, Reason: "no-targets"})
		return
	}
	source := mergeSnapshotDTO(eligibility.Source)
	writeJSON(w, http.StatusOK, repositoryMergeEligibilityDTO{Eligible: true, Source: &source, Targets: candidates})
}

// repositoryMergeCandidates never consumes the upstream inventory identity.
// A registered repository is a candidate only when exactly one fresh RoboRev
// row resolves to its platform-aware canonical path.
func (s *Server) repositoryMergeCandidates(ctx context.Context, inventory []repoinventory.Repository) ([]repositoryMergeCandidateDTO, bool) {
	registered := s.Registry.InspectAll(ctx)
	candidates := make([]repositoryMergeCandidateDTO, 0, len(registered))
	for _, repo := range registered {
		matches := make([]repoinventory.Repository, 0, 1)
		for _, row := range inventory {
			if gitx.SameRepositoryPath(row.RootPath, repo.Path) {
				matches = append(matches, row)
			}
		}
		if len(matches) != 1 {
			if len(matches) > 1 {
				return nil, true
			}
			continue
		}
		identity, err := s.Registry.Identity(repo.Path)
		if err != nil || strings.TrimSpace(identity) == "" {
			continue
		}
		snapshot := repomerge.RepositorySnapshot{Path: repo.Path, Name: matches[0].Name, JobCount: matches[0].Count}
		candidates = append(candidates, mergeCandidateDTO(snapshot, identity))
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].RepositoryIdentity < candidates[j].RepositoryIdentity })
	return candidates, false
}

type repositoryMergeEligibilityBody struct {
	SourcePath *string `json:"sourcePath"`
}

func (s *Server) repositoryMergePreview(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "repository merge preview does not accept query parameters", "request")
		return
	}
	var body repositoryMergePreviewBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.SourcePath == nil || body.TargetRepositoryIdentity == nil || strings.TrimSpace(*body.SourcePath) == "" || strings.TrimSpace(*body.TargetRepositoryIdentity) == "" {
		writeError(w, http.StatusBadRequest, "sourcePath and targetRepositoryIdentity are required", "request")
		return
	}
	sourcePath := *body.SourcePath
	targetIdentity := strings.TrimSpace(*body.TargetRepositoryIdentity)
	target, err := s.Registry.InspectIdentity(r.Context(), targetIdentity)
	if err != nil {
		writeError(w, http.StatusConflict, "target repository is unavailable", "repository-merge")
		return
	}
	if s.Registry.Contains(sourcePath) {
		writeError(w, http.StatusConflict, "source repository is registered in the Dashboard and cannot be merged", "repository-merge")
		return
	}
	service, ok := s.RoboRev.(roborev.RepositoryMergeService)
	if !ok {
		writeRepositoryMergeError(w, roborev.ErrUnsupportedRepositoryMerge)
		return
	}
	request := repomerge.Request{SourcePath: sourcePath, TargetPath: target.Path}
	preflight, err := service.RepositoryMergePreflight(r.Context(), request)
	if err != nil {
		writeRepositoryMergeError(w, err)
		return
	}
	if strings.TrimSpace(preflight.Source.Path) == "" || strings.TrimSpace(preflight.Target.Path) == "" || preflight.Source.JobCount <= 0 || preflight.Target.JobCount < 0 || s.Registry.Contains(preflight.Source.Path) || !gitx.SameRepositoryPath(preflight.Target.Path, target.Path) {
		writeError(w, http.StatusConflict, "repository merge preview is stale; refresh and try again", "stale")
		return
	}
	if !gitx.SameRepositoryPath(preflight.Source.Path, sourcePath) {
		writeError(w, http.StatusConflict, "repository merge source changed after preview", "stale")
		return
	}
	if s.mergePreviews == nil {
		s.mergePreviews = newRepositoryMergePreviewStore()
	}
	expires := time.Now().Add(repositoryMergePreviewTTL)
	record := repositoryMergePreviewRecord{Request: request, TargetIdentity: targetIdentity, Preflight: preflight, ExpiresAt: expires}
	token, err := s.mergePreviews.put(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue repository merge preview token", "repository-merge")
		return
	}
	targetSnapshot := mergeCandidateDTO(preflight.Target, targetIdentity)
	writeJSON(w, http.StatusOK, repositoryMergePreviewDTO{Source: mergeSnapshotDTO(preflight.Source), Target: targetSnapshot, Command: repositoryMergeCommandDTO{Executable: "roborev", Args: append([]string(nil), preflight.Command.Args...), WorkingDirectory: ""}, Token: token, ExpiresAt: expires})
}

func (s *Server) repositoryMergeConfirm(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "repository merge does not accept query parameters", "request")
		return
	}
	var body repositoryMergeConfirmBody
	if !decodeBody(w, r, &body) {
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		writeError(w, http.StatusBadRequest, "repository merge confirmation token is required", "request")
		return
	}
	if s.mergePreviews == nil {
		writeError(w, http.StatusConflict, "repository merge confirmation token is missing or expired", "stale")
		return
	}
	record, ok := s.mergePreviews.peek(token)
	if !ok {
		writeError(w, http.StatusConflict, "repository merge confirmation token is missing or expired", "stale")
		return
	}
	releaseGate, err := s.Gate.AcquireRepositories(gitx.RepositoryKey(record.Preflight.Source.Path), gitx.RepositoryKey(record.Preflight.Target.Path))
	if err != nil {
		writeError(w, http.StatusConflict, err.Error(), "operation")
		return
	}
	claimedRecord, claimed, busy := s.mergePreviews.claim(token)
	if busy {
		releaseGate()
		writeError(w, http.StatusConflict, "repository merge is already in progress", "operation")
		return
	}
	if !claimed {
		releaseGate()
		writeError(w, http.StatusConflict, "repository merge confirmation token is missing or expired", "stale")
		return
	}
	record = claimedRecord
	defer releaseGate()
	defer s.mergePreviews.release(record)
	target, err := s.Registry.InspectIdentity(r.Context(), record.TargetIdentity)
	if err != nil || !gitx.SameRepositoryPath(target.Path, record.Preflight.Target.Path) {
		writeError(w, http.StatusConflict, "target repository changed after preview", "stale")
		return
	}
	if s.Registry.Contains(record.Preflight.Source.Path) {
		writeError(w, http.StatusConflict, "source repository became registered after preview", "stale")
		return
	}
	service, ok := s.RoboRev.(roborev.RepositoryMergeService)
	if !ok {
		writeRepositoryMergeError(w, roborev.ErrUnsupportedRepositoryMerge)
		return
	}
	ctx, cancel := mutationContext()
	defer cancel()
	result, runErr := service.RepositoryMerge(ctx, record.Request, record.Preflight)
	if !result.Started {
		if runErr == nil {
			runErr = errors.New("RoboRev repository merge process did not start")
		}
		writeRepositoryMergeError(w, runErr)
		return
	}
	if !result.Verified {
		writeJSON(w, http.StatusOK, map[string]any{"status": "unverified", "message": "Merge process started but final RoboRev inventory could not prove the consolidated state. Refresh the RoboRev inventory before trying again."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "merged"})
}

func mergeEligibilityReason(err error) (string, bool) {
	switch {
	case errors.Is(err, repomove.ErrSourceExists):
		return "source-exists", true
	case errors.Is(err, roborev.ErrRepositoryMergeActiveJobs):
		return "active-source-jobs", true
	case errors.Is(err, repomerge.ErrSourceEmpty):
		return "source-empty", true
	case errors.Is(err, repomerge.ErrActiveSourceJobs):
		return "active-source-jobs", true
	default:
		return "", false
	}
}

func writeRepositoryMergeError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case errors.Is(err, roborev.ErrRepositoryMergeOffline), errors.Is(err, roborev.ErrRepositoryMergeUnhealthy), errors.Is(err, roborev.ErrUnsupportedRepositoryMerge):
		status = http.StatusServiceUnavailable
	case errors.Is(err, roborev.ErrBindingChanged), errors.Is(err, roborev.ErrRepositoryMergeActiveJobs), errors.Is(err, repomerge.ErrSourceEmpty), errors.Is(err, repomerge.ErrSourceStale), errors.Is(err, repomerge.ErrTargetStale), errors.Is(err, repomerge.ErrSourceTargetSame), errors.Is(err, repomerge.ErrSourceFallbackAmbiguous), errors.Is(err, repomerge.ErrTargetFallbackAmbiguous), errors.Is(err, repomerge.ErrActiveSourceJobs), errors.Is(err, repomerge.ErrSourceJobMismatch), errors.Is(err, repomove.ErrSourceExists):
		status = http.StatusConflict
	case errors.Is(err, repomerge.ErrInvalidRequest), errors.Is(err, repomerge.ErrSourcePathRequired), errors.Is(err, repomerge.ErrTargetPathRequired), errors.Is(err, repomerge.ErrSourcePathNotAbsolute), errors.Is(err, repomerge.ErrTargetPathNotAbsolute):
		status = http.StatusBadRequest
	}
	message := "Repository merge process failed."
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		message = "Repository merge timed out."
	case errors.Is(err, roborev.ErrRepositoryMergeOffline), errors.Is(err, roborev.ErrRepositoryMergeUnhealthy), errors.Is(err, roborev.ErrUnsupportedRepositoryMerge):
		message = "Repository merge is unavailable."
	case errors.Is(err, roborev.ErrBindingChanged):
		message = "Repository merge binding changed; refresh and try again."
	case errors.Is(err, roborev.ErrRepositoryMergeActiveJobs), errors.Is(err, repomerge.ErrSourceEmpty), errors.Is(err, repomerge.ErrSourceStale), errors.Is(err, repomerge.ErrTargetStale), errors.Is(err, repomerge.ErrSourceTargetSame), errors.Is(err, repomerge.ErrSourceFallbackAmbiguous), errors.Is(err, repomerge.ErrTargetFallbackAmbiguous), errors.Is(err, repomerge.ErrActiveSourceJobs), errors.Is(err, repomerge.ErrSourceJobMismatch), errors.Is(err, repomove.ErrSourceExists):
		message = "Repository merge preview is stale or conflicting; refresh and try again."
	case errors.Is(err, repomerge.ErrInvalidRequest), errors.Is(err, repomerge.ErrSourcePathRequired), errors.Is(err, repomerge.ErrTargetPathRequired), errors.Is(err, repomerge.ErrSourcePathNotAbsolute), errors.Is(err, repomerge.ErrTargetPathNotAbsolute):
		message = "Repository merge request is invalid."
	}
	writeError(w, status, message, "repository-merge")
}
