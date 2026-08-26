package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/ciworkflowmanagement"
	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
)

const ciWorkflowVerificationTimeout = 10 * time.Second

type ciWorkflowExecuteBody struct {
	Token string `json:"token"`
}

type ciWorkflowExecuteResultDTO struct {
	Status string `json:"status"`
}

// ciWorkflowManagementExecute is the only mutating CI workflow endpoint.
// The request carries one opaque token; all repository paths, plan witnesses,
// and exact preview bytes remain in the server-side single-use store.
func (s *Server) ciWorkflowManagementExecute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authorizeCIWorkflowPreview(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body ciWorkflowExecuteBody
	if !decodeUniqueBody(w, r, &body, "token") {
		return
	}
	if body.Token == "" || strings.TrimSpace(body.Token) != body.Token {
		writeError(w, http.StatusBadRequest, "confirmation token is required", "request")
		return
	}
	if s.ciWorkflowWrites == nil || s.CIWorkflowManagement == nil {
		writeError(w, http.StatusServiceUnavailable, "CI workflow management is unavailable", "ci-workflow-management")
		return
	}
	record, ok := s.ciWorkflowWrites.claim(body.Token)
	if !ok {
		writeError(w, http.StatusConflict, "confirmation token is missing or expired", "stale")
		return
	}

	// Resolve the opaque identity before entering operation serialization. This
	// is authority only; all mutable repository facts are inspected again while
	// the shared leases are held.
	root, err := s.Registry.ResolveIdentity(record.Identity)
	if err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return
	}
	if !s.isSelectedIdentity(record.Identity) {
		writeError(w, http.StatusConflict, "repository selection changed", "repository")
		return
	}
	if !gitx.SameRepositoryPath(root, record.Root) {
		writeError(w, http.StatusConflict, "CI workflow preview is stale", "stale")
		return
	}
	if s.Gate == nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}

	// CI workflow creation is a repository filesystem mutation and therefore
	// shares the global Fix slot and repository lease with other Dashboard
	// mutators. A claimed token is consumed even when either reservation is
	// busy.
	releaseFix, err := s.Gate.AcquireFix()
	if err != nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	defer releaseFix()
	releaseRepo, err := s.Gate.AcquireRepository(gitx.RepositoryKey(record.Root))
	if err != nil {
		writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
		return
	}
	defer releaseRepo()
	releaseSelected, selectedRoot, err := s.Registry.AcquireSelected(record.Identity)
	if err != nil {
		writeError(w, http.StatusConflict, "repository selection changed", "repository")
		return
	}
	defer releaseSelected()
	if !gitx.SameRepositoryPath(selectedRoot, record.Root) {
		writeError(w, http.StatusConflict, "CI workflow preview is stale", "stale")
		return
	}

	if err := r.Context().Err(); err != nil {
		writeCIWorkflowPreMutationContextError(w, err)
		return
	}
	if !s.ciWorkflowFreshState(r.Context(), record) {
		if err := r.Context().Err(); err != nil {
			writeCIWorkflowPreMutationContextError(w, err)
		} else {
			writeError(w, http.StatusConflict, "CI workflow preview is stale", "stale")
		}
		return
	}
	if !sameCIWorkflowContent(record.Content, record.Digest) || record.Version != "v0.64.0" {
		writeError(w, http.StatusConflict, "CI workflow preview is stale", "stale")
		return
	}
	if err := r.Context().Err(); err != nil {
		writeCIWorkflowPreMutationContextError(w, err)
		return
	}

	// Create owns the precise acceptance boundary. Before its first mkdir or
	// O_EXCL open it honors the request context; after mutation begins its
	// receipt remains available for detached verification.
	created := s.CIWorkflowManagement.Create(r.Context(), record.Plan, record.Content)
	switch created.Status {
	case ciworkflowmanagement.StatusPreMutationCanceled:
		writeCIWorkflowPreMutationContextError(w, context.Canceled)
		return
	case ciworkflowmanagement.StatusPreMutationStale:
		writeError(w, http.StatusConflict, "CI workflow preview is stale", "stale")
		return
	case ciworkflowmanagement.StatusVerifiedCreated, ciworkflowmanagement.StatusFailedUnverified:
		// Continue through the authoritative post-Git inspection and the
		// independent second filesystem read below.
	default:
		writeJSON(w, http.StatusOK, ciWorkflowExecuteResultDTO{Status: string(ciworkflowmanagement.StatusFailedUnverified)})
		return
	}

	// The browser request may be canceled at this point. Keep all leases held
	// and use a bounded server-owned context so disconnects cannot suppress
	// post-mutation verification.
	verifyCtx, cancel := context.WithTimeout(context.Background(), ciWorkflowVerificationTimeout)
	defer cancel()
	postGit, postGitErr := s.Registry.InspectCIWorkflow(verifyCtx, record.Identity)
	postGitOK := postGitErr == nil && sameCIWorkflowPostState(postGit, record.Git, record.Root)
	verified := false
	if created.Receipt != nil {
		readback := s.CIWorkflowManagement.VerifyCreated(verifyCtx, *created.Receipt)
		verified = postGitOK && readback.Status == ciworkflowmanagement.StatusVerifiedCreated
	}
	if verified {
		writeJSON(w, http.StatusOK, ciWorkflowExecuteResultDTO{Status: string(ciworkflowmanagement.StatusVerifiedCreated)})
		return
	}
	writeJSON(w, http.StatusOK, ciWorkflowExecuteResultDTO{Status: string(ciworkflowmanagement.StatusFailedUnverified)})
}

func writeCIWorkflowPreMutationContextError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeError(w, http.StatusRequestTimeout, "CI workflow creation was canceled", "ci-workflow-management")
		return
	}
	writeError(w, http.StatusConflict, "CI workflow preview is stale", "stale")
}

func (s *Server) ciWorkflowFreshState(ctx context.Context, record ciWorkflowWriteRecord) bool {
	state, err := s.Registry.InspectCIWorkflow(ctx, record.Identity)
	if err != nil || !sameCIWorkflowState(state, record.Git, record.Root) {
		return false
	}
	plan, err := s.CIWorkflowManagement.Plan(ctx, record.Root)
	if err != nil || plan.Status() != ciworkflowmanagement.TargetAbsentSafe || !plan.Valid() {
		return false
	}
	// Plan is an identity-bound value containing only comparable snapshots and
	// strings. DeepEqual intentionally includes the absence/ancestor witnesses
	// captured for both fixed directories and the target.
	return reflect.DeepEqual(plan, record.Plan)
}

func sameCIWorkflowState(got, want gitx.CIWorkflowRepositoryState, root string) bool {
	return gitx.SameRepositoryPath(got.Root, root) && gitx.SameRepositoryPath(want.Root, root) &&
		got.Branch == want.Branch && got.Detached == want.Detached && got.Head == want.Head &&
		got.Dirty == want.Dirty && got.Ignored == want.Ignored && !got.Dirty && !got.Ignored &&
		validFullCommitSHA(got.Head)
}

func sameCIWorkflowPostState(got, want gitx.CIWorkflowRepositoryState, root string) bool {
	return gitx.SameRepositoryPath(got.Root, root) && gitx.SameRepositoryPath(want.Root, root) &&
		got.Branch == want.Branch && got.Detached == want.Detached && got.Head == want.Head &&
		!got.Ignored && validFullCommitSHA(got.Head)
}

func sameCIWorkflowContent(content []byte, expected string) bool {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:]) == strings.ToLower(strings.TrimSpace(expected))
}
