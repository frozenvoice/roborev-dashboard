package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/repomove"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const repositoryMovePreviewTTL = 3 * time.Minute

type repositoryMovePreviewBody struct {
	SourcePath                    *string `json:"sourcePath"`
	DestinationRepositoryIdentity *string `json:"destinationRepositoryIdentity"`
}

type repositoryMoveConfirmBody struct {
	Token string `json:"token"`
}

type repositoryMovePreviewRecord struct {
	Request             repomove.Request
	DestinationIdentity string
	Preflight           repomove.Preflight
	ExpiresAt           time.Time
}

type repositoryMovePreviewStore struct {
	mu      sync.Mutex
	pending map[string]repositoryMovePreviewRecord
	active  map[string]struct{}
}

func newRepositoryMovePreviewStore() *repositoryMovePreviewStore {
	return &repositoryMovePreviewStore{pending: make(map[string]repositoryMovePreviewRecord), active: make(map[string]struct{})}
}

func (s *repositoryMovePreviewStore) put(record repositoryMovePreviewRecord) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(time.Now())
	s.pending[token] = record
	return token, nil
}

// peek reads a still-valid preview without consuming it or reserving either
// path.  The caller must release the store lock before acquiring any other
// lease; keeping this operation separate from claim avoids a lock-order
// inversion with the process-wide repository gate.
func (s *repositoryMovePreviewStore) peek(token string) (repositoryMovePreviewRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(time.Now())
	record, ok := s.pending[token]
	return record, ok
}

// claim atomically consumes a token and reserves both source and destination
// for this move store. Cross-feature ownership is provided by Gate; the local
// active map retains move-specific same-feature semantics. A conflicting
// operation leaves the token available for a later confirmation.
func (s *repositoryMovePreviewStore) claim(token string) (repositoryMovePreviewRecord, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(time.Now())
	record, ok := s.pending[token]
	if !ok {
		return repositoryMovePreviewRecord{}, false, false
	}
	keys := []string{gitx.RepositoryKey(record.Request.SourcePath), gitx.RepositoryKey(record.Request.DestinationPath)}
	for _, key := range keys {
		if _, busy := s.active[key]; busy {
			return repositoryMovePreviewRecord{}, false, true
		}
	}
	delete(s.pending, token)
	for _, key := range keys {
		s.active[key] = struct{}{}
	}
	return record, true, false
}

func (s *repositoryMovePreviewStore) release(record repositoryMovePreviewRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, gitx.RepositoryKey(record.Request.SourcePath))
	delete(s.active, gitx.RepositoryKey(record.Request.DestinationPath))
}

func (s *repositoryMovePreviewStore) expireLocked(now time.Time) {
	for token, record := range s.pending {
		if !now.Before(record.ExpiresAt) {
			delete(s.pending, token)
		}
	}
}

type repositoryMovePreviewDTO struct {
	SourcePath                    string                   `json:"sourcePath"`
	SourceName                    string                   `json:"sourceName"`
	JobCount                      int64                    `json:"jobCount"`
	DestinationRepositoryIdentity string                   `json:"destinationRepositoryIdentity"`
	DestinationPath               string                   `json:"destinationPath"`
	DestinationName               string                   `json:"destinationName"`
	Command                       repositoryMoveCommandDTO `json:"command"`
	Token                         string                   `json:"token"`
	ExpiresAt                     time.Time                `json:"expiresAt"`
}

type repositoryMoveCommandDTO struct {
	Executable       string   `json:"executable"`
	Args             []string `json:"args"`
	WorkingDirectory string   `json:"workingDirectory"`
}

func (s *Server) repositoryMovePreview(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "repository move does not accept query parameters", "request")
		return
	}
	var body repositoryMovePreviewBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.SourcePath == nil || body.DestinationRepositoryIdentity == nil || strings.TrimSpace(*body.SourcePath) == "" || strings.TrimSpace(*body.DestinationRepositoryIdentity) == "" {
		writeError(w, http.StatusBadRequest, "sourcePath and destinationRepositoryIdentity are required", "request")
		return
	}
	service, ok := s.repositoryMoveAvailable(w, r.Context())
	if !ok {
		return
	}
	inventory, err := s.repositoryMoveInventory(r.Context())
	if err != nil {
		writeRepositoryMoveError(w, err)
		return
	}
	source, ok := resolveMoveSource(w, inventory, *body.SourcePath)
	if !ok {
		return
	}
	if s.Registry.Contains(source.RootPath) {
		writeError(w, http.StatusConflict, "source repository is registered in the Dashboard and cannot be repaired", "repository-move")
		return
	}
	if err := repomove.SourceAbsent(s.repositoryMoveFileSystem(), source.RootPath); err != nil {
		writeRepositoryMoveError(w, err)
		return
	}
	destination, err := s.Registry.InspectIdentity(r.Context(), strings.TrimSpace(*body.DestinationRepositoryIdentity))
	if err != nil {
		writeError(w, http.StatusConflict, "destination repository is unavailable: "+err.Error(), "repository-move")
		return
	}
	if gitx.SameRepositoryPath(source.RootPath, destination.Path) {
		writeError(w, http.StatusConflict, repomove.ErrSourceDestinationSame.Error(), "repository-move")
		return
	}
	if err := validateMoveDestinationConflict(inventory, destination.Path); err != nil {
		writeRepositoryMoveError(w, err)
		return
	}
	if err := validateMoveFallback(inventory, source.RootPath); err != nil {
		writeRepositoryMoveError(w, err)
		return
	}
	request := repomove.Request{SourcePath: source.RootPath, SourceName: source.Name, DestinationPath: destination.Path}
	preflight, err := service.RepositoryMovePreflight(r.Context(), request)
	if err != nil {
		writeRepositoryMoveError(w, err)
		return
	}
	expires := time.Now().Add(repositoryMovePreviewTTL)
	record := repositoryMovePreviewRecord{Request: request, DestinationIdentity: strings.TrimSpace(*body.DestinationRepositoryIdentity), Preflight: preflight, ExpiresAt: expires}
	if s.movePreviews == nil {
		s.movePreviews = newRepositoryMovePreviewStore()
	}
	token, err := s.movePreviews.put(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue repository move preview token", "repository-move")
		return
	}
	writeJSON(w, http.StatusOK, repositoryMovePreviewDTO{SourcePath: source.RootPath, SourceName: source.Name, JobCount: source.Count, DestinationRepositoryIdentity: record.DestinationIdentity, DestinationPath: destination.Path, DestinationName: destination.Name, Command: repositoryMoveCommandDTO{Executable: preflight.Command.Executable, Args: append([]string(nil), preflight.Command.Args...), WorkingDirectory: preflight.Command.Dir}, Token: token, ExpiresAt: expires})
}

func (s *Server) repositoryMoveConfirm(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "repository move does not accept query parameters", "request")
		return
	}
	var body repositoryMoveConfirmBody
	if !decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Token) == "" {
		writeError(w, http.StatusBadRequest, "token is required", "request")
		return
	}
	service, ok := s.RoboRev.(roborev.RepositoryMoveService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Repository move is unavailable", "repository-move")
		return
	}
	if s.movePreviews == nil {
		writeError(w, http.StatusConflict, "repository move preview is unavailable or expired", "repository-move")
		return
	}
	record, ok := s.movePreviews.peek(body.Token)
	if !ok {
		writeError(w, http.StatusConflict, "repository move preview is unavailable or expired", "repository-move")
		return
	}
	// The destination binding is part of the opaque token. Reject a malformed
	// or stale record before reserving either repository lease.
	if record.DestinationIdentity != gitx.RepositoryKey(record.Request.DestinationPath) {
		writeError(w, http.StatusConflict, "repository move preview binding is invalid", "repository-move")
		return
	}
	releaseGate, gateErr := s.Gate.AcquireRepositories(
		gitx.RepositoryKey(record.Request.SourcePath),
		gitx.RepositoryKey(record.Request.DestinationPath),
	)
	if gateErr != nil {
		writeError(w, http.StatusConflict, gateErr.Error(), "operation")
		return
	}
	claimedRecord, claimed, busy := s.movePreviews.claim(body.Token)
	if busy {
		releaseGate()
		writeError(w, http.StatusConflict, "repository move is already in progress", "repository-move")
		return
	}
	if !claimed {
		releaseGate()
		writeError(w, http.StatusConflict, "repository move preview is unavailable or expired", "repository-move")
		return
	}
	record = claimedRecord
	defer releaseGate()
	defer s.movePreviews.release(record)
	destination, err := s.Registry.InspectIdentity(r.Context(), record.DestinationIdentity)
	if err != nil || !gitx.SameRepositoryPath(destination.Path, record.Request.DestinationPath) {
		writeError(w, http.StatusConflict, "destination repository changed after preview", "repository-move")
		return
	}
	if s.Registry.Contains(record.Request.SourcePath) {
		writeError(w, http.StatusConflict, repomove.ErrSourceTracked.Error(), "repository-move")
		return
	}
	if err := repomove.SourceAbsent(s.repositoryMoveFileSystem(), record.Request.SourcePath); err != nil {
		writeRepositoryMoveError(w, err)
		return
	}
	ctx, cancel := mutationContext()
	defer cancel()
	result, runErr := service.RepositoryMove(ctx, record.Request, record.Preflight)
	if !result.Started {
		if runErr == nil {
			runErr = errors.New("RoboRev repository move process did not start")
		}
		writeRepositoryMoveError(w, runErr)
		return
	}
	if !result.Verified {
		writeJSON(w, http.StatusOK, map[string]any{"status": "unverified", "sourcePath": record.Request.SourcePath, "destinationPath": record.Request.DestinationPath, "message": "Move outcome could not be verified. Refresh the RoboRev inventory before trying again."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "moved", "sourcePath": record.Request.SourcePath, "destinationPath": record.Request.DestinationPath})
}

func (s *Server) repositoryMoveAvailable(w http.ResponseWriter, ctx context.Context) (roborev.RepositoryMoveService, bool) {
	installation := s.RoboRev.Installation(ctx)
	if !installation.Installed || !roborevcompat.IsSupportedVersion(installation.Version) || !installation.Capabilities.RepositoryInventory || !installation.Capabilities.RepositoryMove {
		writeError(w, http.StatusServiceUnavailable, "Repository move requires RoboRev v0.64.0", "repository-move")
		return nil, false
	}
	if installation.DaemonHealth == roborev.DaemonHealthOffline || installation.DaemonCondition == roborev.DaemonConditionUnhealthy {
		writeError(w, http.StatusServiceUnavailable, "Repository move requires the RoboRev daemon to be online.", "repository-move")
		return nil, false
	}
	if installation.DaemonHealth != roborev.DaemonHealthOnline || installation.DaemonCondition != roborev.DaemonConditionHealthy || !roborevcompat.IsMatchingSupportedVersion(installation.Version, installation.DaemonVersion) {
		writeError(w, http.StatusBadGateway, "RoboRev daemon health is unavailable", "repository-move")
		return nil, false
	}
	service, ok := s.RoboRev.(roborev.RepositoryMoveService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Repository move is unavailable", "repository-move")
		return nil, false
	}
	return service, true
}

func (s *Server) repositoryMoveInventory(ctx context.Context) (repoinventory.Inventory, error) {
	service, ok := s.RoboRev.(roborev.RepositoryInventoryService)
	if !ok {
		return repoinventory.Inventory{}, roborev.ErrUnsupportedRepositoryInventory
	}
	return service.RepositoryInventory(ctx)
}

func (s *Server) repositoryMoveFileSystem() repomove.FileSystem {
	if s.repositoryMoveFS != nil {
		return s.repositoryMoveFS
	}
	return repomove.OSFileSystem{}
}

func resolveMoveSource(w http.ResponseWriter, inventory repoinventory.Inventory, sourcePath string) (repoinventory.Repository, bool) {
	var source repoinventory.Repository
	count := 0
	for _, row := range inventory.Repositories {
		if gitx.SameRepositoryPath(row.RootPath, sourcePath) {
			source = row
			count++
		}
	}
	if count != 1 {
		writeError(w, http.StatusConflict, "source path is not uniquely tracked by RoboRev; refresh and try again", "repository-move")
		return repoinventory.Repository{}, false
	}
	return source, true
}

func validateMoveDestinationConflict(inventory repoinventory.Inventory, destinationPath string) error {
	for _, row := range inventory.Repositories {
		if gitx.SameRepositoryPath(row.RootPath, destinationPath) {
			return repomove.ErrDestinationConflict
		}
	}
	return nil
}

func validateMoveFallback(inventory repoinventory.Inventory, sourcePath string) error {
	key, err := repomove.SourceFallbackKey(sourcePath)
	if err != nil {
		return err
	}
	for _, row := range inventory.Repositories {
		if !gitx.SameRepositoryPath(row.RootPath, sourcePath) && row.Name == key {
			return repomove.ErrFallbackAmbiguous
		}
	}
	return nil
}

func writeRepositoryMoveError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case errors.Is(err, roborev.ErrRepositoryMoveOffline), errors.Is(err, roborev.ErrRepositoryMoveUnhealthy), errors.Is(err, roborev.ErrUnsupportedRepositoryMove), errors.Is(err, roborev.ErrUnsupportedRepositoryInventory):
		status = http.StatusServiceUnavailable
	case errors.Is(err, roborev.ErrBindingChanged), errors.Is(err, roborev.ErrRepositoryMoveStale), errors.Is(err, roborev.ErrRepositoryMoveConflict), errors.Is(err, roborev.ErrRepositoryMoveAmbiguous), errors.Is(err, repomove.ErrSourceTracked), errors.Is(err, repomove.ErrSourceExists), errors.Is(err, repomove.ErrSourceStale), errors.Is(err, repomove.ErrDestinationConflict), errors.Is(err, repomove.ErrFallbackAmbiguous), errors.Is(err, repomove.ErrSourceDestinationSame), errors.Is(err, repomove.ErrReadSource):
		status = http.StatusConflict
	case errors.Is(err, repomove.ErrInvalidRequest), errors.Is(err, repomove.ErrSourcePathRequired), errors.Is(err, repomove.ErrDestinationPathRequired), errors.Is(err, repomove.ErrSourcePathNotAbsolute), errors.Is(err, repomove.ErrDestinationNotAbsolute):
		status = http.StatusBadRequest
	}
	writeError(w, status, err.Error(), "repository-move")
}
