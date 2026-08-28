package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/configuration"
	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

// configPreviewTTL bounds how long a prepared configuration change stays
// valid. Every bound field is reverified fresh at set time regardless.
const configPreviewTTL = 10 * time.Minute
const configPreviewMax = 64
const configVerificationTimeout = 10 * time.Second

// configPreviewRecord binds one prepared change to the exact repository,
// checkout state, binary, and configuration snapshot it was validated against.
type configPreviewRecord struct {
	Identity     string
	Path         string
	Head         string
	Branch       string
	RootSnapshot pathsafety.Snapshot
	Binding      roborev.ConfigurationBinding
	Key          string
	Scope        configuration.Scope
	Value        string
	Snapshot     string
	ExpiresAt    time.Time
}

// configPreviewStore is a single-use token store. Tokens are 256-bit random
// values; consuming a record deletes it so a preview can never run twice.
type configPreviewStore struct {
	mu      sync.Mutex
	pending map[string]configPreviewRecord
}

func newConfigPreviewStore() *configPreviewStore {
	return &configPreviewStore{pending: make(map[string]configPreviewRecord)}
}

func (s *configPreviewStore) put(record configPreviewRecord) (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tokenBytes)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.expireLocked(now)
	for len(s.pending) >= configPreviewMax {
		oldest := ""
		var oldestExpiry time.Time
		for token, pending := range s.pending {
			if oldest == "" || pending.ExpiresAt.Before(oldestExpiry) || (pending.ExpiresAt.Equal(oldestExpiry) && token < oldest) {
				oldest, oldestExpiry = token, pending.ExpiresAt
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

func (s *configPreviewStore) take(token string) (configPreviewRecord, bool) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.pending[token]
	if !ok {
		return configPreviewRecord{}, false
	}
	delete(s.pending, token)
	if !now.Before(record.ExpiresAt) {
		return configPreviewRecord{}, false
	}
	return record, true
}

func (s *configPreviewStore) expireLocked(now time.Time) {
	for token, record := range s.pending {
		if !now.Before(record.ExpiresAt) {
			delete(s.pending, token)
		}
	}
}

func configService(service roborev.Service) (roborev.ConfigService, bool) {
	configSvc, ok := service.(roborev.ConfigService)
	return configSvc, ok
}

func configurationMutationTransport(service roborev.Service) (roborev.ConfigurationMutationTransport, bool) {
	transport, ok := service.(roborev.ConfigurationMutationTransport)
	return transport, ok
}

func requireConfigCapability(installation roborev.Installation, mutation bool) error {
	if !installation.Installed {
		return errors.New("roborev is not installed")
	}
	if !roborevcompat.IsSupportedVersion(installation.Version) {
		return errUnsupportedConfigVersion
	}
	if mutation && !installation.Capabilities.ConfigMutation {
		return errUnsupportedConfigVersion
	}
	if !installation.Capabilities.ConfigIntrospection {
		return errUnsupportedConfigVersion
	}
	return nil
}

var errUnsupportedConfigVersion = errors.New("this RoboRev version does not expose the bounded configuration contract")

// configurationSnapshot returns the merged effective configuration for one
// registered repository plus the editable allowlist schema. The list is a CLI
// filesystem operation and intentionally works while the daemon is offline.
func (s *Server) configurationSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	identity := strings.TrimSpace(r.URL.Query().Get("repositoryIdentity"))
	if identity == "" {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "repository")
		return
	}
	repo, ok := s.inspectAuthorizedRepository(w, r.Context(), identity)
	if !ok {
		return
	}
	if err := requireConfigCapability(s.RoboRev.Installation(r.Context()), false); err != nil {
		writeError(w, http.StatusBadGateway, safeExternalError(err, "configuration introspection is unavailable"), "configuration")
		return
	}
	configSvc, ok := configService(s.RoboRev)
	if !ok {
		writeError(w, http.StatusNotImplemented, "configuration introspection is unavailable", "configuration")
		return
	}
	rows, err := configSvc.ConfigList(r.Context(), repo.Path)
	if err != nil {
		writeError(w, http.StatusBadGateway, safeExternalError(err, "configuration is unavailable"), "configuration")
		return
	}
	writeJSON(w, http.StatusOK, configurationSnapshotDTO{
		RepositoryIdentity: identity,
		Settings:           configuration.Settings(),
		Rows:               rows,
	})
}

type configurationSnapshotDTO struct {
	RepositoryIdentity string               `json:"repositoryIdentity"`
	Settings           []configuration.Spec `json:"settings"`
	Rows               []roborev.ConfigRow  `json:"rows"`
}

type configurationPreviewRequest struct {
	RepositoryIdentity string          `json:"repositoryIdentity"`
	Key                string          `json:"key"`
	Scope              string          `json:"scope"`
	Value              json.RawMessage `json:"value"`
}

// configurationPreview validates one allowlisted change against fresh state
// and returns an exact command preview. It never mutates anything.
func (s *Server) configurationPreview(w http.ResponseWriter, r *http.Request) {
	var body configurationPreviewRequest
	if !decodeBody(w, r, &body) {
		return
	}
	scope := configuration.Scope(strings.TrimSpace(body.Scope))
	spec, ok := configuration.Lookup(strings.TrimSpace(body.Key))
	if !ok {
		writeError(w, http.StatusBadRequest, "setting is not editable", "request")
		return
	}
	canonical, err := configuration.CanonicalValue(spec, scope, body.Value)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "request")
		return
	}
	identity := strings.TrimSpace(body.RepositoryIdentity)
	transport, ok := configurationMutationTransport(s.RoboRev)
	if !ok {
		writeError(w, http.StatusNotImplemented, "configuration mutation is unavailable", "configuration")
		return
	}
	releaseSelected, root, err := s.Registry.AcquireSelected(identity)
	if err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return
	}
	defer releaseSelected()
	repo, err := s.Registry.InspectIdentity(r.Context(), identity)
	if err != nil || !gitx.SameRepositoryPath(repo.Path, root) {
		writeError(w, http.StatusConflict, "repository selection changed", "repository")
		return
	}
	if scope == configuration.ScopeLocal && repo.Dirty {
		writeError(w, http.StatusConflict, "repository must be clean before local configuration changes", "dirty")
		return
	}
	rootSnapshot, err := s.captureConfigurationRoot(root)
	if err != nil {
		writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
		return
	}
	preflight, err := transport.ConfigurationPreflight(r.Context(), root)
	if err != nil || !preflight.Binding.Valid() {
		writeError(w, http.StatusBadGateway, "configuration mutation is unavailable", "configuration")
		return
	}
	rows := preflight.Rows
	args, err := roborev.ConfigSetArgs(spec.Key, canonical, toRoboRevScope(scope))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "request")
		return
	}
	currentValue := ""
	currentOrigin := "default"
	for _, row := range rows {
		if row.Key == spec.Key {
			currentValue = row.Value
			currentOrigin = row.Origin
			break
		}
	}
	record := configPreviewRecord{
		Identity:     identity,
		Path:         root,
		Head:         repo.Head,
		Branch:       repo.Branch,
		RootSnapshot: rootSnapshot,
		Binding:      preflight.Binding,
		Key:          spec.Key,
		Scope:        scope,
		Value:        canonical,
		Snapshot:     configuration.SnapshotDigest(rows),
		ExpiresAt:    time.Now().Add(configPreviewTTL),
	}
	token, err := s.configPreviews.put(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "prepare configuration change", "configuration")
		return
	}
	writeJSON(w, http.StatusOK, configurationPreviewDTO{
		PreviewToken:  token,
		Key:           spec.Key,
		Scope:         string(scope),
		CurrentValue:  currentValue,
		CurrentOrigin: currentOrigin,
		NewValue:      canonical,
		Command:       commandPreviewDTO{Executable: "roborev", Args: args},
		Warnings:      configurationWarnings(scope),
	})
}

type commandPreviewDTO struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type configurationPreviewDTO struct {
	PreviewToken  string            `json:"previewToken"`
	Key           string            `json:"key"`
	Scope         string            `json:"scope"`
	CurrentValue  string            `json:"currentValue"`
	CurrentOrigin string            `json:"currentOrigin"`
	NewValue      string            `json:"newValue"`
	Command       commandPreviewDTO `json:"command"`
	Warnings      []string          `json:"warnings"`
}

func configurationWarnings(scope configuration.Scope) []string {
	if scope == configuration.ScopeLocal {
		return []string{"local_toml_modified", "tracked_dirty", "normalize"}
	}
	return []string{"global_normalize"}
}

func toRoboRevScope(scope configuration.Scope) roborev.ConfigScope {
	if scope == configuration.ScopeGlobal {
		return roborev.ConfigScopeGlobal
	}
	return roborev.ConfigScopeLocal
}

func bindingGeneration(service roborev.Service) string {
	if provider, ok := service.(interface{ BindingIdentity() string }); ok {
		return provider.BindingIdentity()
	}
	return ""
}

type configurationSetRequest struct {
	PreviewToken string `json:"previewToken"`
}

type configurationSetDTO struct {
	Status          string              `json:"status"`
	ErrorCode       string              `json:"errorCode,omitempty"`
	Message         string              `json:"message,omitempty"`
	Key             string              `json:"key,omitempty"`
	Scope           string              `json:"scope,omitempty"`
	Value           string              `json:"value,omitempty"`
	VerifiedValue   string              `json:"verifiedValue,omitempty"`
	RepositoryDirty bool                `json:"repositoryDirty,omitempty"`
	Rows            []roborev.ConfigRow `json:"rows,omitempty"`
}

// configurationSet executes one previously prepared allowlisted change. The
// preview token is single-use; repository identity, HEAD/branch, clean tree
// (local), binary generation, and the configuration snapshot digest are all
// revalidated fresh before RoboRev runs. Exit 0 alone is never success: the
// scoped value is read back through `config get` and the merged list is
// refreshed afterwards. Any post-start failure reports an unverified result
// instead of claiming the value did not change.
func (s *Server) configurationSet(w http.ResponseWriter, r *http.Request) {
	var body configurationSetRequest
	if !decodeBody(w, r, &body) {
		return
	}
	token := strings.TrimSpace(body.PreviewToken)
	record, ok := s.configPreviews.take(token)
	if !ok {
		writeError(w, http.StatusConflict, "prepared configuration change is missing or expired; refresh and prepare again", "stale")
		return
	}
	transport, ok := configurationMutationTransport(s.RoboRev)
	if !ok {
		writeError(w, http.StatusNotImplemented, "configuration mutation is unavailable", "configuration")
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
	if record.Scope == configuration.ScopeLocal {
		releaseRepo, repoErr := s.Gate.AcquireRepository(gitx.RepositoryKey(record.Path))
		if repoErr != nil {
			writeError(w, http.StatusConflict, operation.ErrBusy.Error(), "operation")
			return
		}
		defer releaseRepo()
	}
	releaseSelected, root, err := s.Registry.AcquireSelected(record.Identity)
	if err != nil || !gitx.SameRepositoryPath(root, record.Path) {
		if releaseSelected != nil {
			releaseSelected()
		}
		writeError(w, http.StatusConflict, "repository selection changed", "repository")
		return
	}
	defer releaseSelected()
	inspect, err := s.Registry.InspectIdentity(r.Context(), record.Identity)
	if err != nil || !gitx.SameRepositoryPath(inspect.Path, record.Path) || inspect.Head != record.Head || inspect.Branch != record.Branch {
		writeError(w, http.StatusConflict, "repository changed after this change was prepared; refresh and prepare again", "stale")
		return
	}
	if record.Scope == configuration.ScopeLocal && inspect.Dirty {
		writeError(w, http.StatusConflict, "repository must be clean before local configuration changes", "dirty")
		return
	}
	rootSnapshot, err := s.captureConfigurationRoot(record.Path)
	if err != nil || !rootSnapshot.Equal(record.RootSnapshot) {
		writeError(w, http.StatusConflict, "repository changed after this change was prepared; refresh and prepare again", "stale")
		return
	}
	preflight, err := transport.ConfigurationPreflight(r.Context(), record.Path)
	if err != nil || !preflight.Binding.Equal(record.Binding) || configuration.SnapshotDigest(preflight.Rows) != record.Snapshot {
		writeError(w, http.StatusConflict, "configuration changed after this change was prepared; refresh and prepare again", "stale")
		return
	}
	finalGuard := func(ctx context.Context) error {
		snapshot, guardErr := s.captureConfigurationRoot(record.Path)
		if guardErr != nil || !snapshot.Equal(record.RootSnapshot) {
			return errors.New("configuration root changed")
		}
		return nil
	}
	result, setErr := transport.ConfigurationSet(r.Context(), record.Path, record.Key, record.Value, toRoboRevScope(record.Scope), record.Binding, finalGuard)
	if !result.Started {
		writeJSON(w, http.StatusOK, configurationSetDTO{Status: "unverified", ErrorCode: "process-not-started", Message: "configuration result could not be verified"})
		return
	}
	verifyCtx, cancel := context.WithTimeout(context.Background(), configVerificationTimeout)
	defer cancel()
	readback, readErr := transport.ConfigurationReadback(verifyCtx, record.Path, record.Key, toRoboRevScope(record.Scope), record.Binding)
	verified := readErr == nil && readback.Binding.Equal(record.Binding) && readback.Value == record.Value && configurationRowsVerify(readback.Rows, record.Key, record.Scope, record.Value)
	postSnapshot, postRootErr := s.captureConfigurationRoot(record.Path)
	verified = verified && postRootErr == nil && postSnapshot.Equal(record.RootSnapshot)
	if !verified {
		_ = setErr
		writeJSON(w, http.StatusOK, configurationSetDTO{Status: "unverified", ErrorCode: configurationReadbackCode(readErr), Message: "configuration result could not be verified"})
		return
	}
	writeJSON(w, http.StatusOK, configurationSetDTO{Status: "succeeded", Key: record.Key, Scope: string(record.Scope), Value: record.Value, VerifiedValue: readback.Value, Rows: readback.Rows})
}

func configurationRowsVerify(rows []roborev.ConfigRow, key string, scope configuration.Scope, value string) bool {
	expectedOrigin := "global"
	if scope == configuration.ScopeLocal {
		expectedOrigin = "local"
	}
	matched := 0
	for _, row := range rows {
		if row.Key != key {
			continue
		}
		matched++
		if row.Origin != expectedOrigin || row.Value != value {
			return false
		}
	}
	return matched == 1
}

func configurationReadbackCode(err error) string {
	if errors.Is(err, roborev.ErrBindingChanged) {
		return "binding-changed"
	}
	return "readback-unverified"
}

func (s *Server) captureConfigurationRoot(root string) (pathsafety.Snapshot, error) {
	fs := s.ConfigurationFS
	if fs == nil {
		fs = pathsafety.OSFileSystem{}
	}
	return pathsafety.CaptureDirectory(fs, root)
}

// inspectAuthorizedRepository resolves an opaque identity into a freshly
// inspected registered canonical repository.
func (s *Server) inspectAuthorizedRepository(w http.ResponseWriter, ctx context.Context, identity string) (gitx.Repository, bool) {
	repo, err := s.Registry.InspectIdentity(ctx, identity)
	if err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return gitx.Repository{}, false
	}
	return repo, true
}
