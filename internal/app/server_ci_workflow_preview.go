package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/ciworkflowmanagement"
	"github.com/froze/roborev-dashboard/internal/ciworkflowpreview"
	"github.com/froze/roborev-dashboard/internal/gitx"
)

type ciWorkflowPreviewBody struct {
	RepositoryIdentity string `json:"repositoryIdentity"`
}

type ciWorkflowPreviewWorkflowDTO struct {
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

type ciWorkflowPreviewDTO struct {
	Version            string                       `json:"version"`
	RepositoryIdentity string                       `json:"repositoryIdentity"`
	Target             string                       `json:"target"`
	Workflow           ciWorkflowPreviewWorkflowDTO `json:"workflow"`
	Write              ciWorkflowWriteDTO           `json:"write"`
	Existing           *ciWorkflowExistingDTO       `json:"existing,omitempty"`
}

type ciWorkflowExistingDTO struct {
	Comparison string `json:"comparison"`
}

// ciWorkflowWriteDTO is an allowlisted wire shape. Available is the only
// status that carries authority; all unavailable states intentionally contain
// no token, expiry, path, or filesystem diagnostic.
type ciWorkflowWriteDTO struct {
	Status    string     `json:"status"`
	Token     string     `json:"token,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// ciWorkflowPreview is a strict, selected-repository-only read endpoint. It
// intentionally performs browser authorization before decoding the body or
// consulting the registry, and delegates all temporary filesystem/process
// safety to ciworkflowpreview.Service and the typed RoboRev adapter.
func (s *Server) ciWorkflowPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorizeCIWorkflowPreview(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body ciWorkflowPreviewBody
	if !decodeUniqueBody(w, r, &body, "repositoryIdentity") {
		return
	}
	identity := strings.TrimSpace(body.RepositoryIdentity)
	if identity == "" || identity != body.RepositoryIdentity {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "repository")
		return
	}
	if s.CIWorkflowPreview == nil {
		writeError(w, http.StatusServiceUnavailable, "CI workflow preview is unavailable", "ci-workflow-preview")
		return
	}
	release, selectedRoot, err := s.Registry.AcquireSelected(identity)
	if err != nil {
		writeError(w, http.StatusConflict, "repository selection changed", "repository")
		return
	}
	defer release()
	repo, err := s.Registry.InspectIdentity(r.Context(), identity)
	if err != nil || !gitx.SameRepositoryPath(repo.Path, selectedRoot) {
		writeError(w, http.StatusConflict, "repository is unavailable", "repository")
		return
	}
	workflow, err := s.CIWorkflowPreview.Generate(r.Context(), repo.Path)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusRequestTimeout, "CI workflow preview was canceled", "ci-workflow-preview")
			return
		}
		if errors.Is(err, ciworkflowpreview.ErrInvalidRepository) || errors.Is(err, ciworkflowpreview.ErrUnsafeTemporary) || errors.Is(err, ciworkflowpreview.ErrTemporaryOutput) {
			writeError(w, http.StatusConflict, "CI workflow preview is unavailable", "ci-workflow-preview")
			return
		}
		writeError(w, http.StatusServiceUnavailable, "CI workflow preview is unavailable", "ci-workflow-preview")
		return
	}
	// Recompute the digest at the HTTP boundary so the exact bytes sent in the
	// response are always self-consistent, even if a custom test service is
	// supplied. Production Service already computes this same digest.
	digest := sha256.Sum256([]byte(workflow.Content))
	workflow.SHA256 = hex.EncodeToString(digest[:])

	write := ciWorkflowWriteDTO{Status: "unsafe"}
	var existing *ciWorkflowExistingDTO
	if s.CIWorkflowManagement != nil && s.ciWorkflowWrites != nil {
		state, stateErr := s.Registry.InspectCIWorkflow(r.Context(), identity)
		plan, planErr := s.CIWorkflowManagement.Plan(r.Context(), repo.Path)
		status := ciWorkflowWriteStatus(state, stateErr, plan, planErr, repo.Path)
		write.Status = status
		if status == "exists" {
			comparison := s.CIWorkflowManagement.CompareExisting(r.Context(), plan, []byte(workflow.Content))
			existing = &ciWorkflowExistingDTO{Comparison: string(comparison)}
		}
		if status == "available" {
			expires := s.ciWorkflowWrites.expiresAt()
			record := ciWorkflowWriteRecord{
				Identity: identity,
				Root:     repo.Path,
				Git:      state,
				Plan:     plan,
				Content:  []byte(workflow.Content),
				Digest:   workflow.SHA256,
				Version:  ciworkflowpreview.Version,
				Expires:  expires,
			}
			token, tokenErr := s.ciWorkflowWrites.put(record)
			if tokenErr != nil {
				write.Status = "unsafe"
			} else {
				write.Token, write.ExpiresAt = token, &expires
			}
		}
	}
	writeJSON(w, http.StatusOK, ciWorkflowPreviewDTO{
		Version:            ciworkflowpreview.Version,
		RepositoryIdentity: identity,
		Target:             ciworkflowpreview.WorkflowTarget,
		Workflow:           ciWorkflowPreviewWorkflowDTO{Content: workflow.Content, SHA256: workflow.SHA256},
		Write:              write,
		Existing:           existing,
	})
}

func ciWorkflowWriteStatus(state gitx.CIWorkflowRepositoryState, stateErr error, plan ciworkflowmanagement.Plan, planErr error, root string) string {
	if stateErr != nil || planErr != nil || !gitx.SameRepositoryPath(state.Root, root) || state.Detached || strings.TrimSpace(state.Branch) == "" || !validFullCommitSHA(state.Head) || plan.Status() == ciworkflowmanagement.TargetUnsafe {
		return "unsafe"
	}
	// The precedence is deliberate: an existing target is reported as such
	// even when Git would also classify it as ignored or the checkout is dirty.
	if plan.Status() == ciworkflowmanagement.TargetExists {
		return "exists"
	}
	if !plan.Valid() {
		return "unsafe"
	}
	if state.Ignored {
		return "ignored"
	}
	if state.Dirty {
		return "dirty"
	}
	return "available"
}

func validFullCommitSHA(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F') {
			return false
		}
	}
	return true
}

// authorizeCIWorkflowPreview requires the complete same-origin browser Fetch
// Metadata contract in addition to one exact CSRF token. This endpoint can
// launch an upstream CLI process, so Origin-only compatibility used by older
// mutation routes is intentionally not sufficient here.
func (s *Server) authorizeCIWorkflowPreview(w http.ResponseWriter, r *http.Request) bool {
	if !s.authorizeSameOrigin(w, r) {
		return false
	}
	if !exactHeaderValue(r.Header, "X-CSRF-Token", s.csrfToken) {
		writeError(w, http.StatusForbidden, "invalid CSRF token", "csrf")
		return false
	}
	if !exactHeaderValue(r.Header, "Sec-Fetch-Site", "same-origin") ||
		!exactHeaderValue(r.Header, "Sec-Fetch-Mode", "cors") ||
		!exactHeaderValue(r.Header, "Sec-Fetch-Dest", "empty") {
		writeError(w, http.StatusForbidden, "same-origin browser request required", "origin")
		return false
	}
	return true
}
