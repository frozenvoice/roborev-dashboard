package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

// fakeConfigService composes the ordinary fake RoboRev service with a bounded,
// deterministic configuration surface for handler tests.
type fakeConfigService struct {
	*fakeService
	mu           sync.Mutex
	listCalls    int
	listRows     []roborev.ConfigRow
	listErrors   []error
	getCalls     int
	getQueue     []string
	setCalls     int
	setKeys      []string
	setValues    []string
	setScopes    []roborev.ConfigScope
	version      string
	capabilities *roborev.Capabilities
}

func (f *fakeConfigService) Installation(context.Context) roborev.Installation {
	caps := roborev.Capabilities{ReviewEnqueueAPI: true, JobListAPI: true, ConfigIntrospection: true, ConfigMutation: true}
	if f.capabilities != nil {
		caps = *f.capabilities
	}
	return roborev.Installation{Installed: true, Version: f.version, Capabilities: caps, Status: json.RawMessage(`{"running":true,"known":true}`)}
}

func (f *fakeConfigService) ConfigList(_ context.Context, _ string) ([]roborev.ConfigRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if len(f.listErrors) > 0 {
		err := f.listErrors[0]
		f.listErrors = f.listErrors[1:]
		return append([]roborev.ConfigRow(nil), f.listRows...), err
	}
	return append([]roborev.ConfigRow(nil), f.listRows...), nil
}

func (f *fakeConfigService) ConfigGet(_ context.Context, _ string, key string, scope roborev.ConfigScope) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if len(f.getQueue) > 0 {
		value := f.getQueue[0]
		f.getQueue = f.getQueue[1:]
		return value, nil
	}
	for index := len(f.setKeys) - 1; index >= 0; index-- {
		if f.setKeys[index] == key && f.setScopes[index] == scope {
			return f.setValues[index], nil
		}
	}
	for _, row := range f.listRows {
		if row.Key != key {
			continue
		}
		if scope == roborev.ConfigScopeLocal && row.Origin == "local" {
			return row.Value, nil
		}
		if scope == roborev.ConfigScopeGlobal && row.Origin == "global" {
			return row.Value, nil
		}
	}
	return "", nil
}

func (f *fakeConfigService) ConfigSet(_ context.Context, _ string, key string, value string, scope roborev.ConfigScope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls++
	f.setKeys = append(f.setKeys, key)
	f.setValues = append(f.setValues, value)
	f.setScopes = append(f.setScopes, scope)
	return nil
}

func newConfigTestServer(t *testing.T) (*Server, *fakeConfigService, *mutableInspector, string) {
	t.Helper()
	inspector := &mutableInspector{repository: gitx.Repository{Name: "repo", Path: `E:\repo`, Branch: "main", Head: "abcdef123456", HeadShort: "abcdef12"}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	service := &fakeConfigService{fakeService: &fakeService{}, version: "v0.64.0"}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	return server, service, inspector, identity
}

func configGetRequest(t *testing.T, server *Server, identity string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/configuration?repositoryIdentity="+url.QueryEscape(identity), nil)
	request.Host = "example.com"
	if mutate != nil {
		mutate(request)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func configPostRequest(t *testing.T, server *Server, path string, body any, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	request.Host = "example.com"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://example.com")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	if mutate != nil {
		mutate(request)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func preparePreview(t *testing.T, server *Server, identity string) configurationPreviewDTO {
	t.Helper()
	recorder := configPostRequest(t, server, "/api/configuration/preview", map[string]any{
		"repositoryIdentity": identity,
		"key":                "review_reasoning",
		"scope":              "local",
		"value":              "thorough",
	}, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var preview configurationPreviewDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	return preview
}

func TestConfigurationSnapshotReturnsTypedRowsAndAllowlist(t *testing.T) {
	server, service, _, identity := newConfigTestServer(t)
	service.listRows = []roborev.ConfigRow{{Origin: "local", Key: "review_reasoning", Value: "standard"}, {Origin: "default", Key: "max_workers", Value: "4"}}
	recorder := configGetRequest(t, server, identity, func(request *http.Request) {
		request.Header.Set("Origin", "http://example.com")
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var snapshot struct {
		RepositoryIdentity string              `json:"repositoryIdentity"`
		Settings           []map[string]any    `json:"settings"`
		Rows               []roborev.ConfigRow `json:"rows"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.RepositoryIdentity != identity || len(snapshot.Rows) != 2 || snapshot.Rows[0].Key != "review_reasoning" {
		t.Fatalf("snapshot mismatch: %+v", snapshot)
	}
	if len(snapshot.Settings) != 8 {
		t.Fatalf("settings = %d entries, want the v1 allowlist", len(snapshot.Settings))
	}
	if service.listCalls != 1 {
		t.Fatalf("list calls = %d, want 1", service.listCalls)
	}
}

func TestConfigurationSnapshotAcceptsNativeSameOriginMetadata(t *testing.T) {
	server, service, _, identity := newConfigTestServer(t)
	recorder := configGetRequest(t, server, identity, func(request *http.Request) {
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		request.Header.Set("Sec-Fetch-Mode", "cors")
		request.Header.Set("Sec-Fetch-Dest", "empty")
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.listCalls != 1 {
		t.Fatalf("list calls = %d, want 1", service.listCalls)
	}
}

func TestConfigurationSnapshotRejectsForeignOriginBeforeAnyProcessCall(t *testing.T) {
	server, service, _, identity := newConfigTestServer(t)
	recorder := configGetRequest(t, server, identity, func(request *http.Request) {
		request.Header.Set("Origin", "https://evil.example")
	})
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if service.listCalls != 0 {
		t.Fatalf("list calls = %d, want 0 before authorization", service.listCalls)
	}
}

func TestConfigurationSnapshotRejectsMissingFetchMetadata(t *testing.T) {
	server, service, _, identity := newConfigTestServer(t)
	recorder := configGetRequest(t, server, identity, nil)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 without Origin or Fetch Metadata", recorder.Code)
	}
	if service.listCalls != 0 {
		t.Fatalf("list calls = %d, want 0", service.listCalls)
	}
}

func TestConfigurationSnapshotRejectsUnsupportedCapabilityBeforeList(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeConfigService)
	}{
		{name: "unsupported version", setup: func(service *fakeConfigService) {
			service.version = "v0.65.0"
		}},
		{name: "introspection unavailable", setup: func(service *fakeConfigService) {
			service.capabilities = &roborev.Capabilities{ReviewEnqueueAPI: true, JobListAPI: true, ConfigMutation: true}
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server, service, _, identity := newConfigTestServer(t)
			testCase.setup(service)
			recorder := configGetRequest(t, server, identity, func(request *http.Request) {
				request.Header.Set("Origin", "http://example.com")
			})
			if recorder.Code != http.StatusBadGateway {
				t.Fatalf("status = %d body=%s, want 502", recorder.Code, recorder.Body.String())
			}
			if service.listCalls != 0 {
				t.Fatalf("list calls = %d, want 0 before capability rejection", service.listCalls)
			}
		})
	}
}

func TestConfigurationPreviewBuildsExactCommandAndBindsToken(t *testing.T) {
	server, service, _, identity := newConfigTestServer(t)
	service.listRows = []roborev.ConfigRow{{Origin: "local", Key: "review_reasoning", Value: "standard"}}
	preview := preparePreview(t, server, identity)
	if preview.PreviewToken == "" {
		t.Fatal("preview token missing")
	}
	if preview.Key != "review_reasoning" || preview.Scope != "local" || preview.NewValue != "thorough" {
		t.Fatalf("preview fields mismatch: %+v", preview)
	}
	if preview.CurrentValue != "standard" || preview.CurrentOrigin != "local" {
		t.Fatalf("current state mismatch: %+v", preview.CurrentValue+"@"+preview.CurrentOrigin)
	}
	wantArgs := []string{"config", "set", "review_reasoning", "thorough", "--local"}
	if preview.Command.Executable != "roborev" || strings.Join(preview.Command.Args, " ") != strings.Join(wantArgs, " ") {
		t.Fatalf("command mismatch: %#v", preview.Command)
	}
	foundNormalize := false
	for _, warning := range preview.Warnings {
		if warning == "normalize" {
			foundNormalize = true
		}
	}
	if !foundNormalize {
		t.Fatalf("normalize warning missing: %#v", preview.Warnings)
	}
}

func TestConfigurationPreviewRejectsDirtyRepositoryForLocalScope(t *testing.T) {
	server, service, inspector, identity := newConfigTestServer(t)
	inspector.repository.Dirty = true
	service.listRows = []roborev.ConfigRow{}
	recorder := configPostRequest(t, server, "/api/configuration/preview", map[string]any{
		"repositoryIdentity": identity, "key": "review_reasoning", "scope": "local", "value": "thorough",
	}, nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for dirty repository", recorder.Code)
	}
}

func TestConfigurationPreviewFailsClosedOutsideTheAllowlist(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		scope string
		value any
	}{
		{name: "banned safety key", key: "allow_unsafe_agents", scope: "global", value: true},
		{name: "unknown key", key: "totally_made_up", scope: "global", value: 1},
		{name: "scope not allowed", key: "max_workers", scope: "local", value: 8},
		{name: "invalid enum", key: "review_reasoning", scope: "local", value: "extreme"},
		{name: "integer out of bounds", key: "max_workers", scope: "global", value: 64},
		{name: "wrong value type", key: "max_workers", scope: "global", value: "eight"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server, _, _, identity := newConfigTestServer(t)
			recorder := configPostRequest(t, server, "/api/configuration/preview", map[string]any{
				"repositoryIdentity": identity, "key": testCase.key, "scope": testCase.scope, "value": testCase.value,
			}, nil)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s, want 400", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestConfigurationMutationsRequireSameOriginAndCSRF(t *testing.T) {
	server, service, _, identity := newConfigTestServer(t)
	body := map[string]any{"repositoryIdentity": identity, "key": "review_reasoning", "scope": "local", "value": "thorough"}
	foreign := configPostRequest(t, server, "/api/configuration/preview", body, func(request *http.Request) {
		request.Header.Set("Origin", "https://evil.example")
	})
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("foreign origin status = %d, want 403", foreign.Code)
	}
	noCSRF := configPostRequest(t, server, "/api/configuration/preview", body, func(request *http.Request) {
		request.Header.Del("X-CSRF-Token")
	})
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing csrf status = %d, want 403", noCSRF.Code)
	}
	if service.listCalls != 0 && service.setCalls != 0 {
		t.Fatal("no RoboRev work may run before authorization")
	}
}

func TestConfigurationSetRunsOnceVerifiesScopedValueAndRefreshesSnapshot(t *testing.T) {
	server, service, _, identity := newConfigTestServer(t)
	service.listRows = []roborev.ConfigRow{{Origin: "default", Key: "review_reasoning", Value: "standard"}}
	preview := preparePreview(t, server, identity)

	recorder := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": preview.PreviewToken}, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var result configurationSetDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "succeeded" || result.VerifiedValue != "thorough" {
		t.Fatalf("result mismatch: %+v", result)
	}
	if len(result.Rows) == 0 {
		t.Fatal("fresh merged list missing from response")
	}
	if service.setCalls != 1 {
		t.Fatalf("set calls = %d, want exactly one", service.setCalls)
	}
	if service.setKeys[0] != "review_reasoning" || service.setValues[0] != "thorough" || service.setScopes[0] != roborev.ConfigScopeLocal {
		t.Fatalf("set arguments mismatch: %#v", service.setKeys)
	}
	if service.getCalls < 1 {
		t.Fatal("post-write verification did not read the scoped value back")
	}

	replay := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": preview.PreviewToken}, nil)
	if replay.Code != http.StatusConflict {
		t.Fatalf("replayed token status = %d, want 409", replay.Code)
	}
	if service.setCalls != 1 {
		t.Fatalf("replayed token ran another set: %d calls", service.setCalls)
	}
}

func TestConfigurationSetReportsUnverifiedInsteadOfClaimingUnchanged(t *testing.T) {
	server, service, _, identity := newConfigTestServer(t)
	service.getQueue = []string{"4"}
	preview := preparePreviewWithValues(t, server, identity, "max_workers", "global", 8)

	recorder := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": preview.PreviewToken}, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var result configurationSetDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "unverified" || result.Message == "" {
		t.Fatalf("expected unverified ambiguity, got %+v", result)
	}
	if service.setCalls != 1 {
		t.Fatalf("set calls = %d", service.setCalls)
	}
}

func TestConfigurationSetReportsUnverifiedWhenRefreshListFails(t *testing.T) {
	server, service, _, identity := newConfigTestServer(t)
	preview := preparePreview(t, server, identity)
	refreshErr := errors.New("refresh list failed")
	// The first list belongs to the fresh precondition check. The second is
	// the post-write authoritative refresh and must make the result ambiguous.
	service.listErrors = []error{nil, refreshErr}

	recorder := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": preview.PreviewToken}, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var result configurationSetDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "unverified" {
		t.Fatalf("status = %q, want unverified: %+v", result.Status, result)
	}
	if !strings.Contains(result.Message, refreshErr.Error()) {
		t.Fatalf("message = %q, want refresh error", result.Message)
	}
	if service.setCalls != 1 {
		t.Fatalf("set calls = %d, want exactly one", service.setCalls)
	}
}

func preparePreviewWithValues(t *testing.T, server *Server, identity string, key string, scope string, value any) configurationPreviewDTO {
	t.Helper()
	recorder := configPostRequest(t, server, "/api/configuration/preview", map[string]any{
		"repositoryIdentity": identity, "key": key, "scope": scope, "value": value,
	}, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var preview configurationPreviewDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	return preview
}

func TestConfigurationSetRejectsChangedStateAfterPreview(t *testing.T) {
	t.Run("configuration snapshot drifted", func(t *testing.T) {
		server, service, _, identity := newConfigTestServer(t)
		preview := preparePreview(t, server, identity)
		service.listRows = []roborev.ConfigRow{{Origin: "global", Key: "review_reasoning", Value: "fast"}}
		recorder := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": preview.PreviewToken}, nil)
		if recorder.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 stale", recorder.Code)
		}
		if service.setCalls != 0 {
			t.Fatalf("stale preview executed %d sets", service.setCalls)
		}
	})

	t.Run("HEAD moved", func(t *testing.T) {
		server, service, inspector, identity := newConfigTestServer(t)
		preview := preparePreview(t, server, identity)
		inspector.repository.Head = "bbbbbbbbbbbb"
		recorder := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": preview.PreviewToken}, nil)
		if recorder.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 stale", recorder.Code)
		}
		if service.setCalls != 0 {
			t.Fatal("stale HEAD must never execute a set")
		}
	})

	t.Run("dirty after preview", func(t *testing.T) {
		server, service, inspector, identity := newConfigTestServer(t)
		preview := preparePreview(t, server, identity)
		inspector.repository.Dirty = true
		recorder := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": preview.PreviewToken}, nil)
		if recorder.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 dirty", recorder.Code)
		}
		if service.setCalls != 0 {
			t.Fatal("dirty tree must never execute a local set")
		}
	})
}

func TestConfigurationSetRespectsExclusiveOperationGate(t *testing.T) {
	server, service, _, identity := newConfigTestServer(t)
	release, err := server.Gate.AcquireFix()
	if err != nil {
		t.Fatal(err)
	}
	preview := preparePreview(t, server, identity)
	recorder := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": preview.PreviewToken}, nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while another exclusive operation runs", recorder.Code)
	}
	release()

	// The busy-gate rejection must not have burned the prepared change.
	retry := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": preview.PreviewToken}, nil)
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status = %d body=%s, want the same token to succeed after the gate freed", retry.Code, retry.Body.String())
	}
	var result configurationSetDTO
	if err := json.Unmarshal(retry.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "succeeded" || service.setCalls != 1 {
		t.Fatalf("result=%+v setCalls=%d", result, service.setCalls)
	}

	// An invalid token still conflicts without touching RoboRev.
	bogus := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": "anything"}, nil)
	if bogus.Code != http.StatusConflict {
		t.Fatalf("bogus token status = %d, want 409", bogus.Code)
	}
	if service.setCalls != 1 {
		t.Fatalf("bogus token executed a set: %d calls", service.setCalls)
	}
}

func TestConfigurationSetRestoresTokenWhenLocalRepositoryLeaseBusy(t *testing.T) {
	server, service, _, identity := newConfigTestServer(t)
	preview := preparePreview(t, server, identity)

	releaseRepo, err := server.Gate.AcquireRepository(identity)
	if err != nil {
		t.Fatal(err)
	}
	busy := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": preview.PreviewToken}, nil)
	if busy.Code != http.StatusConflict {
		t.Fatalf("busy status = %d body=%s, want 409", busy.Code, busy.Body.String())
	}
	if service.setCalls != 0 {
		t.Fatalf("busy repository executed %d sets", service.setCalls)
	}
	releaseRepo()

	retry := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": preview.PreviewToken}, nil)
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status = %d body=%s, want the same token to succeed", retry.Code, retry.Body.String())
	}
	var result configurationSetDTO
	if err := json.Unmarshal(retry.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "succeeded" || service.setCalls != 1 {
		t.Fatalf("result=%+v setCalls=%d, want one successful set", result, service.setCalls)
	}
}

func TestConfigurationSetConcurrentDuplicateExecutesExactlyOnce(t *testing.T) {
	server, service, _, identity := newConfigTestServer(t)
	preview := preparePreview(t, server, identity)

	start := make(chan struct{})
	statuses := make(chan int, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	for range 2 {
		go func() {
			defer workers.Done()
			<-start
			recorder := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": preview.PreviewToken}, nil)
			statuses <- recorder.Code
		}()
	}
	close(start)
	workers.Wait()
	close(statuses)

	statusCounts := map[int]int{}
	for status := range statuses {
		statusCounts[status]++
	}
	if statusCounts[http.StatusOK] != 1 || statusCounts[http.StatusConflict] != 1 {
		t.Fatalf("statuses = %#v, want one 200 and one 409", statusCounts)
	}
	if service.setCalls != 1 {
		t.Fatalf("set calls = %d, want exactly one", service.setCalls)
	}
}

func TestConfigurationSetInvalidTokenNeverExecutesSet(t *testing.T) {
	server, service, _, _ := newConfigTestServer(t)
	recorder := configPostRequest(t, server, "/api/configuration/set", map[string]any{"previewToken": "not-a-token"}, nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409", recorder.Code, recorder.Body.String())
	}
	if service.setCalls != 0 {
		t.Fatalf("invalid token executed %d sets", service.setCalls)
	}
}

func TestConfigurationPreviewStoreRestoreDropsExpiredToken(t *testing.T) {
	store := newConfigPreviewStore()
	const token = "expired-token"
	store.restore(token, configPreviewRecord{ExpiresAt: time.Unix(0, 0)})
	if _, ok := store.take(token); ok {
		t.Fatal("expired restored token was usable")
	}
}

func TestConfigurationPreviewRejectsUnsupportedVersion(t *testing.T) {
	server, service, _, identity := newConfigTestServer(t)
	service.version = "v0.65.0"
	recorder := configPostRequest(t, server, "/api/configuration/preview", map[string]any{
		"repositoryIdentity": identity, "key": "review_reasoning", "scope": "local", "value": "thorough",
	}, nil)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 unsupported version", recorder.Code)
	}
}
