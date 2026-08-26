package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/hookmanagement"
	"github.com/froze/roborev-dashboard/internal/hookstatus"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	hookManagementPreviewTTL = 3 * time.Minute
	hookManagementPreviewMax = 128

	// Browser previews describe the allowlisted command shape only. The actual
	// Dashboard-owned executable remains server-side in the binding/token and is
	// resolved again immediately before RunGitHook.
	hookManagementPreviewExecutable = "roborev"
	hookManagementPreviewBinary     = "<current-v0.64.0-binary>"
)

type hookManagementPreviewBody struct {
	RepositoryIdentity string `json:"repositoryIdentity"`
	Action             string `json:"action"`
}

type hookManagementExecuteBody struct {
	Token string `json:"token"`
}

type hookManagementBinding struct {
	Binary     string
	Version    string
	Generation uint64
	FileID     string
}

type hookManagementPreviewRecord struct {
	Identity  string
	Root      string
	Action    hookmanagement.Action
	Binding   hookManagementBinding
	Preflight hookmanagement.Preflight
	ExpiresAt time.Time
}

type hookManagementPreviewStore struct {
	mu      sync.Mutex
	pending map[string]hookManagementPreviewRecord
}

func newHookManagementPreviewStore() *hookManagementPreviewStore {
	return &hookManagementPreviewStore{pending: make(map[string]hookManagementPreviewRecord)}
}

func (s *hookManagementPreviewStore) put(record hookManagementPreviewRecord) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.expireLocked(now)
	for len(s.pending) >= hookManagementPreviewMax {
		var oldest string
		var oldestExpiry time.Time
		for candidate, pending := range s.pending {
			if oldest == "" || pending.ExpiresAt.Before(oldestExpiry) {
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

func (s *hookManagementPreviewStore) claim(token string) (hookManagementPreviewRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(time.Now())
	record, ok := s.pending[token]
	if ok {
		delete(s.pending, token)
	}
	return record, ok
}

func (s *hookManagementPreviewStore) expireLocked(now time.Time) {
	for token, record := range s.pending {
		if now.After(record.ExpiresAt) {
			delete(s.pending, token)
		}
	}
}

// hookManagementCommandDTO is a browser-facing semantic preview, not an
// execution command. Its values are restricted to the constants below.
type hookManagementCommandDTO struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type hookManagementPreviewDTO struct {
	Token        string                   `json:"token"`
	Action       hookmanagement.Action    `json:"action"`
	Repository   string                   `json:"repository"`
	Preflight    hookmanagement.Preflight `json:"preflight"`
	Summary      string                   `json:"summary"`
	WarningCodes []string                 `json:"warningCodes,omitempty"`
	Command      hookManagementCommandDTO `json:"command"`
	ExpiresAt    time.Time                `json:"expiresAt"`
}

type hookManagementResultDTO struct {
	Status    hookmanagement.Outcome   `json:"status"`
	Action    hookmanagement.Action    `json:"action"`
	Preflight hookmanagement.Preflight `json:"preflight"`
	ErrorCode string                   `json:"errorCode,omitempty"`
}

func (s *Server) repositoryHookManagement(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	identity, ok := strictManagementIdentity(w, r)
	if !ok {
		return
	}
	if !s.isSelectedIdentity(identity) {
		writeError(w, http.StatusForbidden, "repository is not selected", "repository")
		return
	}
	preflight, _, ok := s.hookManagementPreflight(w, r.Context(), identity)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sanitizedPreflight(preflight))
}

func (s *Server) repositoryHookManagementPreview(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeHookMutation(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body hookManagementPreviewBody
	if !decodeUniqueBody(w, r, &body, "repositoryIdentity", "action") {
		return
	}
	identity := strings.TrimSpace(body.RepositoryIdentity)
	if identity == "" {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "request")
		return
	}
	action := hookmanagement.Action(strings.TrimSpace(body.Action))
	if !action.Valid() {
		writeError(w, http.StatusBadRequest, "unsupported hook action", "request")
		return
	}
	if !s.isSelectedIdentity(identity) {
		writeError(w, http.StatusForbidden, "repository is not selected", "repository")
		return
	}
	preflight, binding, ok := s.hookManagementPreflight(w, r.Context(), identity)
	if !ok {
		return
	}
	available := availabilityForAction(preflight, action)
	if !available.Available {
		writeError(w, http.StatusConflict, "hook action is unavailable", available.Reason)
		return
	}
	repo, err := s.Registry.InspectIdentity(r.Context(), identity)
	if err != nil {
		writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
		return
	}
	record := hookManagementPreviewRecord{Identity: identity, Root: repo.Path, Action: action, Binding: binding, Preflight: preflight, ExpiresAt: time.Now().Add(hookManagementPreviewTTL)}
	token, err := s.hookPreviews.put(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "prepare hook action", "hook-management")
		return
	}
	writeJSON(w, http.StatusOK, hookManagementPreviewDTO{Token: token, Action: action, Repository: repo.Name, Preflight: sanitizedPreflight(preflight), Summary: actionSummary(action), WarningCodes: preflight.WarningCodes, Command: hookCommandPreview(action), ExpiresAt: record.ExpiresAt})
}

func (s *Server) repositoryHookManagementExecute(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeHookMutation(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "query parameters are not accepted", "request")
		return
	}
	var body hookManagementExecuteBody
	if !decodeUniqueBody(w, r, &body, "token") {
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		writeError(w, http.StatusBadRequest, "confirmation token is required", "request")
		return
	}
	record, ok := s.hookPreviews.claim(token)
	if !ok {
		writeError(w, http.StatusConflict, "confirmation token is missing or expired", "stale")
		return
	}
	// Re-authorize the opaque identity and perform the first fresh repository
	// inspection before touching the version probe or shared operation gate.
	resolved, err := s.Registry.ResolveIdentity(record.Identity)
	if err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return
	}
	if !s.isSelectedIdentity(record.Identity) {
		writeError(w, http.StatusForbidden, "repository is not selected", "repository")
		return
	}
	inspected, err := s.Registry.InspectIdentity(r.Context(), record.Identity)
	if err != nil || !gitx.SameRepositoryPath(resolved, inspected.Path) || !gitx.SameRepositoryPath(record.Root, inspected.Path) {
		writeError(w, http.StatusConflict, "repository changed after preview", "stale")
		return
	}
	transport, ok := s.RoboRev.(roborev.GitHookTransport)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Git hook management is unavailable", "hook-management")
		return
	}
	binding, err := transport.ProbeGitHookBinding(r.Context())
	if err != nil || !roborevcompat.IsSupportedVersion(binding.Version) {
		writeError(w, http.StatusServiceUnavailable, "RoboRev v0.64.0 binding is unavailable", "version")
		return
	}
	if !sameHookBinding(binding, record.Binding) {
		writeError(w, http.StatusConflict, "hook preview is stale", "stale")
		return
	}
	// A claimed token is consumed even when the shared gate is busy.
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
	releaseSelected, root, err := s.Registry.AcquireSelected(record.Identity)
	if err != nil {
		writeError(w, http.StatusConflict, "selected repository changed; refresh and retry", "repository")
		return
	}
	defer releaseSelected()
	if !gitx.SameRepositoryPath(root, record.Root) {
		writeError(w, http.StatusConflict, "repository changed after preview", "stale")
		return
	}
	binding, err = transport.ProbeGitHookBinding(r.Context())
	if err != nil || !roborevcompat.IsSupportedVersion(binding.Version) {
		writeError(w, http.StatusServiceUnavailable, "RoboRev v0.64.0 binding is unavailable", "version")
		return
	}
	if !sameHookBinding(binding, record.Binding) {
		writeError(w, http.StatusConflict, "hook preview is stale", "stale")
		return
	}
	if s.HookManagement == nil {
		writeError(w, http.StatusServiceUnavailable, "Git hook management is unavailable", "hook-management")
		return
	}
	current, err := s.HookManagement.Inspect(r.Context(), record.Identity, root, binding.Binary)
	if err != nil {
		writeError(w, http.StatusConflict, "hook state changed; refresh and retry", "stale")
		return
	}
	if hookmanagement.FingerprintsChanged(record.Preflight, current) {
		writeError(w, http.StatusConflict, "hook preview is stale", "stale")
		return
	}
	if available := availabilityForAction(current, record.Action); !available.Available {
		writeError(w, http.StatusConflict, "hook action is unavailable", available.Reason)
		return
	}
	execution, runErr := transport.RunGitHook(r.Context(), root, toRoboRevAction(record.Action), binding)
	// Always perform one fresh readback, including process start/exit errors.
	readCtx, cancelRead := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRead()
	readback, readErr := s.HookManagement.Inspect(readCtx, record.Identity, root, binding.Binary)
	if readErr != nil {
		writeJSON(w, http.StatusOK, hookManagementResultDTO{Status: hookmanagement.FailedUnverified, Action: record.Action, Preflight: sanitizedPreflight(syntheticUnavailableHookPreflight(record.Identity)), ErrorCode: "readback-unavailable"})
		return
	}
	status := hookmanagement.VerifyExecution(record.Action, current, readback, execution.Started)
	result := hookManagementResultDTO{Status: status, Action: record.Action, Preflight: sanitizedPreflight(readback)}
	if !execution.Started {
		result.ErrorCode = "cli-not-started"
	} else if runErr != nil {
		result.ErrorCode = "cli-failed"
	}
	writeJSON(w, http.StatusOK, result)
}

// syntheticUnavailableHookPreflight keeps the result wire shape structurally
// valid when the authoritative post-read fails before a layout snapshot can
// be constructed. It intentionally contains no stale pre-launch facts.
func syntheticUnavailableHookPreflight(identity string) hookmanagement.Preflight {
	return hookmanagement.Preflight{
		RepositoryIdentity: identity,
		Install:            hookmanagement.Availability{Reason: "unsafe-hooks-directory"},
		Repair:             hookmanagement.Availability{Reason: "unsafe-hooks-directory"},
		Remove:             hookmanagement.Availability{Reason: "unsafe-hooks-directory"},
		Safety:             hookmanagement.Safety{UnreadableHookPresent: true},
		PostCommit:         hookmanagement.Hook{Name: hookmanagement.PostCommit, Status: hookstatus.Unknown, Classification: hookmanagement.Unreadable},
		PostRewrite:        hookmanagement.Hook{Name: hookmanagement.PostRewrite, Status: hookstatus.Unknown, Classification: hookmanagement.Unreadable},
		SummaryCode:        "blocked",
		WarningCodes:       []string{"unsafe-hooks-directory"},
	}
}

func (s *Server) hookManagementPreflight(w http.ResponseWriter, ctx context.Context, identity string) (hookmanagement.Preflight, hookManagementBinding, bool) {
	if s.HookManagement == nil {
		writeError(w, http.StatusServiceUnavailable, "Git hook management is unavailable", "hook-management")
		return hookmanagement.Preflight{}, hookManagementBinding{}, false
	}
	transport, ok := s.RoboRev.(roborev.GitHookTransport)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Git hook management is unavailable", "hook-management")
		return hookmanagement.Preflight{}, hookManagementBinding{}, false
	}
	path, err := s.Registry.ResolveIdentity(identity)
	if err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return hookmanagement.Preflight{}, hookManagementBinding{}, false
	}
	repo, err := s.Registry.InspectIdentity(ctx, identity)
	if err != nil || !gitx.SameRepositoryPath(path, repo.Path) {
		writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
		return hookmanagement.Preflight{}, hookManagementBinding{}, false
	}
	binding, err := transport.ProbeGitHookBinding(ctx)
	if err != nil || !roborevcompat.IsSupportedVersion(binding.Version) {
		writeError(w, http.StatusServiceUnavailable, "Git hook management requires RoboRev v0.64.0", "version")
		return hookmanagement.Preflight{}, hookManagementBinding{}, false
	}
	preflight, err := s.HookManagement.Inspect(ctx, identity, repo.Path, binding.Binary)
	if err != nil {
		writeError(w, http.StatusConflict, "Git hook state is unavailable", "hook-management")
		return hookmanagement.Preflight{}, hookManagementBinding{}, false
	}
	return preflight, hookManagementBinding{Binary: binding.Binary, Version: binding.Version, Generation: binding.Generation, FileID: binding.FileID}, true
}

func sanitizedPreflight(preflight hookmanagement.Preflight) hookmanagement.Preflight {
	preflight.Root, preflight.HooksDir, preflight.LayoutDigest = "", "", ""
	preflight.PostCommit.Content, preflight.PostRewrite.Content = nil, nil
	return preflight
}

func (s *Server) isSelectedIdentity(identity string) bool {
	selected, err := s.Registry.Identity("")
	return err == nil && selected == strings.TrimSpace(identity)
}

func strictManagementIdentity(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !exactQuery(r, "repositoryIdentity") {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "repository")
		return "", false
	}
	identity := strings.TrimSpace(r.URL.Query().Get("repositoryIdentity"))
	if identity == "" {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "repository")
		return "", false
	}
	return identity, true
}

func (s *Server) authorizeHookMutation(w http.ResponseWriter, r *http.Request) bool {
	if !s.authorizeMutation(w, r) {
		return false
	}
	if len(r.Header.Values("Origin")) == 0 && (!exactHeaderValue(r.Header, "Sec-Fetch-Site", "same-origin") || !exactHeaderValue(r.Header, "Sec-Fetch-Mode", "cors") || !exactHeaderValue(r.Header, "Sec-Fetch-Dest", "empty")) {
		writeError(w, http.StatusForbidden, "same-origin browser request required", "origin")
		return false
	}
	return true
}

func availabilityForAction(p hookmanagement.Preflight, action hookmanagement.Action) hookmanagement.Availability {
	switch action {
	case hookmanagement.Install:
		return p.Install
	case hookmanagement.Repair:
		return p.Repair
	case hookmanagement.Remove:
		return p.Remove
	}
	return hookmanagement.Availability{Reason: "unsupported-action"}
}

func actionSummary(action hookmanagement.Action) string {
	switch action {
	case hookmanagement.Install:
		return "Install RoboRev Git automation hooks"
	case hookmanagement.Repair:
		return "Repair RoboRev-managed Git hook blocks"
	default:
		return "Remove RoboRev-managed Git hook blocks"
	}
}

func hookCommandPreview(action hookmanagement.Action) hookManagementCommandDTO {
	var args []string
	switch action {
	case hookmanagement.Install:
		args = []string{"install-hook", "--binary", hookManagementPreviewBinary}
	case hookmanagement.Repair:
		args = []string{"install-hook", "repair", "--binary", hookManagementPreviewBinary}
	case hookmanagement.Remove:
		args = []string{"uninstall-hook"}
	}
	return hookManagementCommandDTO{Executable: hookManagementPreviewExecutable, Args: args}
}

func sameHookBinding(binding roborev.GitHookBinding, expected hookManagementBinding) bool {
	return binding.Binary == expected.Binary && binding.Version == expected.Version && binding.Generation == expected.Generation && binding.FileID == expected.FileID
}

func toRoboRevAction(action hookmanagement.Action) roborev.GitHookAction {
	return roborev.GitHookAction(action)
}

// decodeUniqueBody rejects unknown, duplicate, trailing, and non-object JSON
// keys. decodeBody's unknown-field protection remains unchanged elsewhere.
func decodeUniqueBody(w http.ResponseWriter, r *http.Request, target any, allowed ...string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request", "request")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		writeError(w, http.StatusBadRequest, "invalid request", "request")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request: trailing JSON value", "request")
		return false
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key := range object {
		if _, ok := allowedSet[key]; !ok {
			writeError(w, http.StatusBadRequest, "invalid request field", "request")
			return false
		}
	}
	// Re-parse the object stream to detect duplicate keys (map decoding alone
	// intentionally keeps the last value).
	stream := json.NewDecoder(bytes.NewReader(data))
	token, err := stream.Token()
	if err != nil || token != json.Delim('{') {
		writeError(w, http.StatusBadRequest, "invalid request", "request")
		return false
	}
	seen := map[string]struct{}{}
	for stream.More() {
		keyToken, err := stream.Token()
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid request", "request")
			return false
		}
		key, ok := keyToken.(string)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid request", "request")
			return false
		}
		if _, exists := seen[key]; exists {
			writeError(w, http.StatusBadRequest, "duplicate request field", "request")
			return false
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := stream.Decode(&value); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request", "request")
			return false
		}
	}
	if _, err := stream.Token(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request", "request")
		return false
	}
	encoded, _ := json.Marshal(object)
	if err := json.Unmarshal(encoded, target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request", "request")
		return false
	}
	return true
}
