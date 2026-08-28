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
	"github.com/froze/roborev-dashboard/internal/reporename"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	repositoryRenamePreviewTTL = 3 * time.Minute
	repositoryRenamePreviewMax = 64
)

type repositoryRenamePreviewBody struct {
	Path    *string `json:"path"`
	NewName *string `json:"newName"`
}

type repositoryRenameConfirmBody struct {
	Token string `json:"token"`
}

type repositoryRenamePreviewRecord struct {
	Path        string
	CurrentName string
	NewName     string
	Preflight   reporename.Preflight
	ExpiresAt   time.Time
}

type repositoryRenamePreviewStore struct {
	mu      sync.Mutex
	pending map[string]repositoryRenamePreviewRecord
	active  map[string]struct{}
}

func newRepositoryRenamePreviewStore() *repositoryRenamePreviewStore {
	return &repositoryRenamePreviewStore{pending: make(map[string]repositoryRenamePreviewRecord), active: make(map[string]struct{})}
}

func (s *repositoryRenamePreviewStore) put(record repositoryRenamePreviewRecord) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.expireLocked(now)
	for len(s.pending) >= repositoryRenamePreviewMax {
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

// peek reads a still-valid preview without consuming it or reserving the
// target. The caller releases the store lock before taking the shared
// repository gate so lock ordering remains one-way across mutators.
func (s *repositoryRenamePreviewStore) peek(token string) (repositoryRenamePreviewRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(time.Now())
	record, ok := s.pending[token]
	return record, ok
}

// claim atomically consumes a token and reserves its exact target path. A
// busy target leaves the token intact so a later confirm can try again.
func (s *repositoryRenamePreviewStore) claim(token string) (repositoryRenamePreviewRecord, bool, bool) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(now)
	record, ok := s.pending[token]
	if !ok {
		return repositoryRenamePreviewRecord{}, false, false
	}
	key := gitx.RepositoryKey(record.Path)
	if _, busy := s.active[key]; busy {
		return repositoryRenamePreviewRecord{}, false, true
	}
	delete(s.pending, token)
	s.active[key] = struct{}{}
	return record, true, false
}

func (s *repositoryRenamePreviewStore) release(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, gitx.RepositoryKey(path))
}

func (s *repositoryRenamePreviewStore) expireLocked(now time.Time) {
	for token, record := range s.pending {
		if !now.Before(record.ExpiresAt) {
			delete(s.pending, token)
		}
	}
}

type repositoryRenamePreviewDTO struct {
	Path                string                     `json:"path"`
	CurrentName         string                     `json:"currentName"`
	NewName             string                     `json:"newName"`
	JobCount            int64                      `json:"jobCount"`
	DashboardRegistered bool                       `json:"dashboardRegistered"`
	Command             repositoryRenameCommandDTO `json:"command"`
	Token               string                     `json:"token"`
	ExpiresAt           time.Time                  `json:"expiresAt"`
}

type repositoryRenameCommandDTO struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

func (s *Server) repositoryRenamePreview(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "repository rename does not accept query parameters", "request")
		return
	}
	var body repositoryRenamePreviewBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Path == nil || body.NewName == nil {
		writeError(w, http.StatusBadRequest, "path and newName are required", "request")
		return
	}
	requestPath := *body.Path
	if requestPath == "" {
		writeError(w, http.StatusBadRequest, "repository path is required", "request")
		return
	}
	service, ok := s.repositoryRenameService(w, r.Context())
	if !ok {
		return
	}
	inventory, inventoryErr := s.repositoryRenameInventory(r.Context())
	if inventoryErr != nil {
		writeRepositoryInventoryError(w, inventoryErr)
		return
	}
	target, ok := resolveRenameTarget(w, inventory, requestPath)
	if !ok {
		return
	}
	newName, err := reporename.NormalizeName(*body.NewName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "request")
		return
	}
	if newName == target.Name {
		writeError(w, http.StatusBadRequest, reporename.ErrSameName.Error(), "request")
		return
	}
	preflight, err := service.RepositoryRenamePreflight(r.Context(), reporename.Request{Path: target.RootPath, CurrentName: target.Name, NewName: newName})
	if err != nil {
		writeRepositoryRenameError(w, err)
		return
	}
	expires := time.Now().Add(repositoryRenamePreviewTTL)
	record := repositoryRenamePreviewRecord{Path: target.RootPath, CurrentName: target.Name, NewName: newName, Preflight: preflight, ExpiresAt: expires}
	token, err := s.renamePreviews.put(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "prepare repository rename", "rename")
		return
	}
	writeJSON(w, http.StatusOK, repositoryRenamePreviewDTO{Path: target.RootPath, CurrentName: target.Name, NewName: newName, JobCount: target.Count, DashboardRegistered: s.Registry.Contains(target.RootPath), Command: repositoryRenameCommandDTO{Executable: "roborev", Args: append([]string(nil), preflight.Command.Args...)}, Token: token, ExpiresAt: expires})
}

func (s *Server) repositoryRenameConfirm(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "repository rename does not accept query parameters", "request")
		return
	}
	var body repositoryRenameConfirmBody
	if !decodeBody(w, r, &body) {
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		writeError(w, http.StatusBadRequest, "repository rename confirmation token is required", "request")
		return
	}
	record, ok := s.renamePreviews.peek(token)
	if !ok {
		writeError(w, http.StatusConflict, "repository rename confirmation token is missing or expired", "stale")
		return
	}
	releaseGate, gateErr := s.Gate.AcquireRepository(gitx.RepositoryKey(record.Path))
	if gateErr != nil {
		writeError(w, http.StatusConflict, gateErr.Error(), "operation")
		return
	}
	claimedRecord, claimed, busy := s.renamePreviews.claim(token)
	if busy {
		releaseGate()
		writeError(w, http.StatusConflict, "repository rename is already in progress", "operation")
		return
	}
	if !claimed {
		releaseGate()
		writeError(w, http.StatusConflict, "repository rename confirmation token is missing or expired", "stale")
		return
	}
	record = claimedRecord
	defer releaseGate()
	defer s.renamePreviews.release(record.Path)
	ctx, cancel := mutationContext()
	defer cancel()
	service, ok := s.RoboRev.(roborev.RepositoryRenameService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Repository rename is unavailable", "repository-rename")
		return
	}
	result, runErr := service.RepositoryRename(ctx, reporename.Request{Path: record.Path, CurrentName: record.CurrentName, NewName: record.NewName}, record.Preflight)
	if !result.Started {
		if runErr == nil {
			runErr = errors.New("RoboRev rename process did not start")
		}
		writeRepositoryRenameError(w, runErr)
		return
	}
	if result.Verified {
		writeJSON(w, http.StatusOK, map[string]any{"status": "renamed", "path": record.Path, "previousName": record.CurrentName, "name": record.NewName})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "unverified", "path": record.Path, "message": "Rename outcome could not be verified. Refresh the RoboRev inventory before trying again."})
}

func (s *Server) repositoryRenameService(w http.ResponseWriter, ctx context.Context) (roborev.RepositoryRenameService, bool) {
	installation := s.RoboRev.Installation(ctx)
	if !installation.Installed || !roborevcompat.IsSupportedVersion(installation.Version) || !installation.Capabilities.RepositoryRename {
		writeError(w, http.StatusServiceUnavailable, "Repository rename requires RoboRev v0.64.0", "repository-rename")
		return nil, false
	}
	if installation.DaemonHealth == roborev.DaemonHealthOffline || installation.DaemonCondition == roborev.DaemonConditionUnhealthy {
		writeError(w, http.StatusServiceUnavailable, "Repository rename requires the RoboRev daemon to be online and healthy.", "repository-rename")
		return nil, false
	}
	if installation.DaemonHealth != roborev.DaemonHealthOnline || installation.DaemonCondition != roborev.DaemonConditionHealthy || !roborevcompat.IsMatchingSupportedVersion(installation.Version, installation.DaemonVersion) {
		writeError(w, http.StatusBadGateway, "RoboRev daemon health is unavailable", "repository-rename")
		return nil, false
	}
	service, ok := s.RoboRev.(roborev.RepositoryRenameService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Repository rename is unavailable", "repository-rename")
		return nil, false
	}
	return service, true
}

func (s *Server) repositoryRenameInventory(ctx context.Context) (repoinventory.Inventory, error) {
	inventoryService, ok := s.RoboRev.(roborev.RepositoryInventoryService)
	if !ok {
		return repoinventory.Inventory{}, roborev.ErrUnsupportedRepositoryInventory
	}
	inventory, err := inventoryService.RepositoryInventory(ctx)
	if err != nil {
		return repoinventory.Inventory{}, err
	}
	return inventory, nil
}

func resolveRenameTarget(w http.ResponseWriter, inventory repoinventory.Inventory, path string) (repoinventory.Repository, bool) {
	var target repoinventory.Repository
	count := 0
	for _, row := range inventory.Repositories {
		if row.RootPath == path {
			target = row
			count++
		}
	}
	if count != 1 {
		writeError(w, http.StatusConflict, "repository path is not uniquely tracked by RoboRev; refresh and try again", "repository")
		return repoinventory.Repository{}, false
	}
	for _, row := range inventory.Repositories {
		if row.RootPath != path && row.Name == path {
			writeError(w, http.StatusConflict, "repository path is ambiguous in RoboRev; refresh and try again", "repository")
			return repoinventory.Repository{}, false
		}
	}
	return target, true
}

func writeRepositoryRenameError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case errors.Is(err, roborev.ErrRepositoryRenameOffline), errors.Is(err, roborev.ErrRepositoryRenameUnhealthy), errors.Is(err, roborev.ErrUnsupportedRepositoryRename):
		status = http.StatusServiceUnavailable
	case errors.Is(err, roborev.ErrBindingChanged):
		status = http.StatusConflict
	case errors.Is(err, roborev.ErrRepositoryRenameStale), errors.Is(err, roborev.ErrRepositoryRenameAmbiguous):
		status = http.StatusConflict
	case errors.Is(err, reporename.ErrInvalidName), errors.Is(err, reporename.ErrSameName):
		status = http.StatusBadRequest
	}
	message := "Repository rename process failed."
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		message = "Repository rename timed out."
	case errors.Is(err, roborev.ErrRepositoryRenameOffline), errors.Is(err, roborev.ErrRepositoryRenameUnhealthy), errors.Is(err, roborev.ErrUnsupportedRepositoryRename):
		message = "Repository rename is unavailable."
	case errors.Is(err, roborev.ErrBindingChanged):
		message = "Repository rename binding changed; refresh and try again."
	case errors.Is(err, roborev.ErrRepositoryRenameStale), errors.Is(err, roborev.ErrRepositoryRenameAmbiguous):
		message = "Repository rename preview is stale; refresh and try again."
	case errors.Is(err, reporename.ErrInvalidName):
		message = "Repository name is invalid."
	case errors.Is(err, reporename.ErrSameName):
		message = "Repository already has that name."
	}
	writeError(w, status, message, "repository-rename")
}
