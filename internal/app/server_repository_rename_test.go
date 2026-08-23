package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/reporename"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type renameHTTPService struct {
	*fakeService
	mu                 sync.Mutex
	installation       roborev.Installation
	inventories        []repoinventory.Inventory
	inventoryErrors    []error
	preflights         int
	preflightErr       error
	preflightBinding   string
	preflightCLI       string
	preflightDaemon    string
	preflightBindings  []string
	preflightCLIs      []string
	preflightDaemons   []string
	renameCalls        int
	renameResult       reporename.ExecutionResult
	renameResultSet    bool
	renameErr          error
	renameBlock        <-chan struct{}
	renameStarted      chan struct{}
	lastRenameRequest  reporename.Request
	installationCalls  int
	inventoryCallCount int
}

func (s *renameHTTPService) Installation(context.Context) roborev.Installation {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installationCalls++
	if s.installation.Installed || s.installation.Version != "" {
		return s.installation
	}
	return renameInstallation()
}

func renameInstallation() roborev.Installation {
	return roborev.Installation{Installed: true, Version: "v0.64.0", DaemonVersion: "v0.64.0", DaemonHealth: roborev.DaemonHealthOnline, DaemonCondition: roborev.DaemonConditionHealthy, Capabilities: roborev.Capabilities{RepositoryInventory: true, RepositoryRename: true}}
}

func (s *renameHTTPService) RepositoryInventory(context.Context) (repoinventory.Inventory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inventoryCallCount++
	if len(s.inventoryErrors) > 0 {
		err := s.inventoryErrors[0]
		s.inventoryErrors = s.inventoryErrors[1:]
		if err != nil {
			return repoinventory.Inventory{}, err
		}
	}
	if len(s.inventories) == 0 {
		return repoinventory.Inventory{}, nil
	}
	value := s.inventories[0]
	s.inventories = s.inventories[1:]
	return value, nil
}

func (s *renameHTTPService) RepositoryRenamePreflight(_ context.Context, request reporename.Request) (reporename.Preflight, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.preflights++
	if s.preflightErr != nil {
		return reporename.Preflight{}, s.preflightErr
	}
	binding, cli, daemon := s.preflightBinding, s.preflightCLI, s.preflightDaemon
	if len(s.preflightBindings) > 0 {
		binding = s.preflightBindings[0]
		s.preflightBindings = s.preflightBindings[1:]
	}
	if len(s.preflightCLIs) > 0 {
		cli = s.preflightCLIs[0]
		s.preflightCLIs = s.preflightCLIs[1:]
	}
	if len(s.preflightDaemons) > 0 {
		daemon = s.preflightDaemons[0]
		s.preflightDaemons = s.preflightDaemons[1:]
	}
	if binding == "" {
		binding = "1:roborev"
	}
	if cli == "" {
		cli = "v0.64.0"
	}
	if daemon == "" {
		daemon = "v0.64.0"
	}
	return reporename.Preflight{Command: reporename.Command{Executable: "roborev", Args: []string{"repo", "rename", request.Path, request.NewName}}, Binding: binding, CLIVersion: cli, DaemonVersion: daemon}, nil
}

func (s *renameHTTPService) RepositoryRename(_ context.Context, request reporename.Request, _ reporename.Preflight) (reporename.ExecutionResult, error) {
	if s.renameStarted != nil {
		select {
		case s.renameStarted <- struct{}{}:
		default:
		}
	}
	if s.renameBlock != nil {
		<-s.renameBlock
	}
	s.mu.Lock()
	s.renameCalls++
	s.lastRenameRequest = request
	result, err := s.renameResult, s.renameErr
	s.mu.Unlock()
	if !s.renameResultSet && result == (reporename.ExecutionResult{}) && err == nil {
		result = reporename.ExecutionResult{Started: true, Verified: true}
	}
	return result, err
}

func renameTestServer(t *testing.T, service *renameHTTPService, inventories ...repoinventory.Inventory) *Server {
	t.Helper()
	service.inventories = append([]repoinventory.Inventory(nil), inventories...)
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\tracked repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func renamePost(t *testing.T, server *Server, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	request.Host = "example.com"
	request.Header.Set("Origin", "http://example.com")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func TestRepositoryRenamePreviewDoesNotExecuteProcess(t *testing.T) {
	service := &renameHTTPService{fakeService: &fakeService{}}
	inventory := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`, Count: 4}}, TotalCount: 4}
	server := renameTestServer(t, service, inventory)
	response := renamePost(t, server, "/api/repository-renames/preview", map[string]string{"path": `E:\tracked repo`, "newName": "new name"})
	if response.Code != http.StatusOK || service.renameCalls != 0 || service.preflights != 1 {
		t.Fatalf("status=%d calls=%d preflights=%d body=%s", response.Code, service.renameCalls, service.preflights, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"repo","rename","E:\\tracked repo","new name"`) {
		t.Fatalf("exact command missing: %s", response.Body.String())
	}
}

func TestRepositoryRenameConfirmVerifiesFreshInventory(t *testing.T) {
	service := &renameHTTPService{fakeService: &fakeService{}}
	before := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`, Count: 4}}, TotalCount: 4}
	after := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "new name", RootPath: `E:\tracked repo`, Count: 4}}, TotalCount: 4}
	server := renameTestServer(t, service, before, before, after)
	preview := renamePost(t, server, "/api/repository-renames/preview", map[string]string{"path": `E:\tracked repo`, "newName": "new name"})
	var value struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &value); err != nil || value.Token == "" {
		t.Fatalf("preview=%s err=%v", preview.Body.String(), err)
	}
	response := renamePost(t, server, "/api/repository-renames", map[string]string{"token": value.Token})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"renamed"`) || service.renameCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, service.renameCalls, response.Body.String())
	}
	if service.installationCalls != 1 || service.preflights != 1 || service.inventoryCallCount != 1 {
		t.Fatalf("confirm duplicated handler work: installation=%d preflight=%d inventory=%d", service.installationCalls, service.preflights, service.inventoryCallCount)
	}
}

func TestRepositoryRenameConfirmErrorTaxonomyAndSingleServiceCall(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		result     reporename.ExecutionResult
		wantStatus int
	}{
		{name: "binding changed", err: roborev.ErrBindingChanged, wantStatus: http.StatusConflict},
		{name: "stale target", err: roborev.ErrRepositoryRenameStale, wantStatus: http.StatusConflict},
		{name: "ambiguous target", err: roborev.ErrRepositoryRenameAmbiguous, wantStatus: http.StatusConflict},
		{name: "unsupported", err: roborev.ErrUnsupportedRepositoryRename, wantStatus: http.StatusServiceUnavailable},
		{name: "offline", err: roborev.ErrRepositoryRenameOffline, wantStatus: http.StatusServiceUnavailable},
		{name: "unhealthy", err: roborev.ErrRepositoryRenameUnhealthy, wantStatus: http.StatusServiceUnavailable},
		{name: "deadline", err: context.DeadlineExceeded, wantStatus: http.StatusGatewayTimeout},
		{name: "unknown", err: errors.New("pre-process failure"), wantStatus: http.StatusBadGateway},
		{name: "started false nil", result: reporename.ExecutionResult{}, wantStatus: http.StatusBadGateway},
	}
	base := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &renameHTTPService{fakeService: &fakeService{}, renameResult: test.result, renameResultSet: true, renameErr: test.err}
			server := renameTestServer(t, service, base)
			token := prepareRenameToken(t, server, `E:\tracked repo`, "new")
			response := confirmRename(t, server, token)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if service.renameCalls != 1 || service.installationCalls != 1 || service.preflights != 1 || service.inventoryCallCount != 1 {
				t.Fatalf("duplicate/direct work: rename=%d installation=%d preflight=%d inventory=%d", service.renameCalls, service.installationCalls, service.preflights, service.inventoryCallCount)
			}
		})
	}
}

func TestRepositoryRenameConfirmUsesTypedStartedOutcome(t *testing.T) {
	base := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}}
	for _, test := range []struct {
		name       string
		result     reporename.ExecutionResult
		wantStatus string
	}{
		{name: "verified", result: reporename.ExecutionResult{Started: true, Verified: true}, wantStatus: "renamed"},
		{name: "unverified", result: reporename.ExecutionResult{Started: true}, wantStatus: "unverified"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &renameHTTPService{fakeService: &fakeService{}, renameResult: test.result, renameResultSet: true}
			server := renameTestServer(t, service, base)
			token := prepareRenameToken(t, server, `E:\tracked repo`, "new")
			response := confirmRename(t, server, token)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"`+test.wantStatus+`"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRepositoryRenameRejectsFallbackNameCollision(t *testing.T) {
	service := &renameHTTPService{fakeService: &fakeService{}}
	inventory := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}, {Name: `E:\tracked repo`, RootPath: `E:\other`}}, TotalCount: 0}
	server := renameTestServer(t, service, inventory)
	response := renamePost(t, server, "/api/repository-renames/preview", map[string]string{"path": `E:\tracked repo`, "newName": "new"})
	if response.Code != http.StatusConflict || service.preflights != 0 || service.renameCalls != 0 {
		t.Fatalf("status=%d preflights=%d calls=%d body=%s", response.Code, service.preflights, service.renameCalls, response.Body.String())
	}
}

func TestRepositoryRenamePreviewAuthAndShapeFailBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*http.Request)
		body   string
		query  string
	}{
		{name: "foreign origin", mutate: func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, body: `{"path":"E:\\tracked repo","newName":"new"}`},
		{name: "missing csrf", mutate: func(r *http.Request) { r.Header.Del("X-CSRF-Token") }, body: `{"path":"E:\\tracked repo","newName":"new"}`},
		{name: "query", body: `{"path":"E:\\tracked repo","newName":"new"}`, query: "unexpected=1"},
		{name: "unknown field", body: `{"path":"E:\\tracked repo","newName":"new","extra":true}`},
		{name: "trailing value", body: `{"path":"E:\\tracked repo","newName":"new"}{}`},
		{name: "missing path", body: `{"newName":"new"}`},
		{name: "missing name", body: `{"path":"E:\\tracked repo"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &renameHTTPService{fakeService: &fakeService{}}
			server := renameTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}})
			request := httptest.NewRequest(http.MethodPost, "/api/repository-renames/preview?"+test.query, strings.NewReader(test.body))
			request.Host = "example.com"
			request.Header.Set("Origin", "http://example.com")
			request.Header.Set("X-CSRF-Token", server.csrfToken)
			if test.mutate != nil {
				test.mutate(request)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code < http.StatusBadRequest || service.preflights != 0 || service.renameCalls != 0 {
				t.Fatalf("status=%d installation=%d inventory=%d preflight=%d rename=%d body=%s", response.Code, service.installationCalls, service.inventoryCallCount, service.preflights, service.renameCalls, response.Body.String())
			}
		})
	}
}

func TestRepositoryRenamePreviewValidatesTargetAndNameWithoutRegistryMutation(t *testing.T) {
	for _, test := range []struct {
		name       string
		path       string
		inventory  repoinventory.Inventory
		newName    string
		wantStatus int
	}{
		{name: "unknown path", path: `E:\unknown`, inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}}, newName: "new", wantStatus: http.StatusConflict},
		{name: "duplicate exact path", path: `E:\tracked repo`, inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "a", RootPath: `E:\tracked repo`}, {Name: "b", RootPath: `E:\tracked repo`}}}, newName: "new", wantStatus: http.StatusConflict},
		{name: "empty", path: `E:\tracked repo`, newName: "", wantStatus: http.StatusBadRequest},
		{name: "whitespace", path: `E:\tracked repo`, newName: " \t ", wantStatus: http.StatusBadRequest},
		{name: "control", path: `E:\tracked repo`, newName: "new\nname", wantStatus: http.StatusBadRequest},
		{name: "too long", path: `E:\tracked repo`, newName: strings.Repeat("x", reporename.MaxNameRunes+1), wantStatus: http.StatusBadRequest},
		{name: "leading dash", path: `E:\tracked repo`, newName: "-new", wantStatus: http.StatusBadRequest},
		{name: "same exact current", path: `E:\tracked repo`, inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}}, newName: "old", wantStatus: http.StatusBadRequest},
		{name: "raw current name is not trimmed", path: `E:\tracked repo`, inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: " old ", RootPath: `E:\tracked repo`}}}, newName: "old", wantStatus: http.StatusOK},
		{name: "unicode accepted", path: `E:\tracked repo`, inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}}, newName: " 새 이름 • ", wantStatus: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			inventory := test.inventory
			if inventory.Repositories == nil {
				inventory = repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}}
			}
			service := &renameHTTPService{fakeService: &fakeService{}}
			server := renameTestServer(t, service, inventory)
			response := renamePost(t, server, "/api/repository-renames/preview", map[string]string{"path": test.path, "newName": test.newName})
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantStatus != http.StatusOK && service.preflights != 0 {
				t.Fatalf("preflight=%d after rejected validation", service.preflights)
			}
		})
	}
}

func TestRepositoryRenamePreviewPreservesExactTrackedPathAndNoRegistryMutation(t *testing.T) {
	const exactPath = `  E:\tracked repo  `
	service := &renameHTTPService{fakeService: &fakeService{}}
	server := renameTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: exactPath, Count: 3}}})
	response := renamePost(t, server, "/api/repository-renames/preview", map[string]string{"path": exactPath, "newName": "new"})
	var preview repositoryRenamePreviewDTO
	if json.Unmarshal(response.Body.Bytes(), &preview) != nil || response.Code != http.StatusOK || preview.Path != exactPath {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if server.Registry.Contains(exactPath) {
		t.Fatal("preview unexpectedly registered tracked repository")
	}
}

func TestRepositoryRenameCapabilityIsExactAndOptional(t *testing.T) {
	service := &renameHTTPService{fakeService: &fakeService{}, installation: renameInstallation()}
	service.installation.Version = "v0.65.0"
	server := renameTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}})
	response := renamePost(t, server, "/api/repository-renames/preview", map[string]string{"path": `E:\tracked repo`, "newName": "new"})
	if response.Code != http.StatusServiceUnavailable || service.inventoryCallCount != 0 {
		t.Fatalf("status=%d inventory=%d body=%s", response.Code, service.inventoryCallCount, response.Body.String())
	}
	legacy := &fakeService{}
	legacyServer := setupServer(t, legacy)
	legacyResponse := renamePost(t, legacyServer, "/api/repository-renames/preview", map[string]string{"path": `E:\tracked repo`, "newName": "new"})
	if legacyResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("optional interface status=%d body=%s", legacyResponse.Code, legacyResponse.Body.String())
	}
}

func prepareRenameToken(t *testing.T, server *Server, path, newName string) string {
	t.Helper()
	response := renamePost(t, server, "/api/repository-renames/preview", map[string]string{"path": path, "newName": newName})
	if response.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", response.Code, response.Body.String())
	}
	var value struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil || value.Token == "" {
		t.Fatalf("preview token=%q err=%v body=%s", value.Token, err, response.Body.String())
	}
	return value.Token
}

func confirmRename(t *testing.T, server *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	return renamePost(t, server, "/api/repository-renames", map[string]string{"token": token})
}

func TestRepositoryRenameTokenExpiryReplayAndBusyPreserve(t *testing.T) {
	before := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}}
	after := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "new", RootPath: `E:\tracked repo`}}}
	service := &renameHTTPService{fakeService: &fakeService{}}
	server := renameTestServer(t, service, before, before, after)
	token := prepareRenameToken(t, server, `E:\tracked repo`, "new")
	server.renamePreviews.mu.Lock()
	record := server.renamePreviews.pending[token]
	record.ExpiresAt = time.Now().Add(-time.Second)
	server.renamePreviews.pending[token] = record
	server.renamePreviews.mu.Unlock()
	expired := confirmRename(t, server, token)
	if expired.Code != http.StatusConflict || service.renameCalls != 0 {
		t.Fatalf("expired status=%d calls=%d body=%s", expired.Code, service.renameCalls, expired.Body.String())
	}

	service = &renameHTTPService{fakeService: &fakeService{}}
	server = renameTestServer(t, service, before, before, after)
	token = prepareRenameToken(t, server, `E:\tracked repo`, "new")
	if response := confirmRename(t, server, token); response.Code != http.StatusOK || service.renameCalls != 1 {
		t.Fatalf("first confirm status=%d calls=%d body=%s", response.Code, service.renameCalls, response.Body.String())
	}
	replay := confirmRename(t, server, token)
	if replay.Code != http.StatusConflict || service.renameCalls != 1 {
		t.Fatalf("replay status=%d calls=%d body=%s", replay.Code, service.renameCalls, replay.Body.String())
	}

	service = &renameHTTPService{fakeService: &fakeService{}}
	server = renameTestServer(t, service, before, before, after)
	token = prepareRenameToken(t, server, `E:\tracked repo`, "new")
	releaseGate, err := server.Gate.AcquireFix()
	if err != nil {
		t.Fatal(err)
	}
	busy := confirmRename(t, server, token)
	releaseGate()
	if busy.Code != http.StatusConflict || service.renameCalls != 0 {
		t.Fatalf("busy status=%d calls=%d body=%s", busy.Code, service.renameCalls, busy.Body.String())
	}
	if response := confirmRename(t, server, token); response.Code != http.StatusOK || service.renameCalls != 1 {
		t.Fatalf("restored token status=%d calls=%d body=%s", response.Code, service.renameCalls, response.Body.String())
	}
}

func TestRepositoryRenameStartedFalseIsDefiniteFailure(t *testing.T) {
	before := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}}
	service := &renameHTTPService{fakeService: &fakeService{}, renameResult: reporename.ExecutionResult{Started: false}, renameResultSet: true}
	server := renameTestServer(t, service, before, before)
	token := prepareRenameToken(t, server, `E:\tracked repo`, "new")
	response := confirmRename(t, server, token)
	if response.Code != http.StatusBadGateway || service.renameCalls != 1 || service.inventoryCallCount != 1 {
		t.Fatalf("status=%d rename=%d inventory=%d body=%s", response.Code, service.renameCalls, service.inventoryCallCount, response.Body.String())
	}
}

func TestRepositoryRenameReadbackTaxonomyAndExitError(t *testing.T) {
	base := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}}
	cases := []struct {
		name        string
		readback    repoinventory.Inventory
		readbackErr error
		exitErr     error
		wantStatus  string
	}{
		{name: "nonzero but new name", readback: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "new", RootPath: `E:\tracked repo`}}}, exitErr: errors.New("exit 1"), wantStatus: "renamed"},
		{name: "old name", readback: base, wantStatus: "unverified"},
		{name: "third name", readback: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "third", RootPath: `E:\tracked repo`}}}, wantStatus: "unverified"},
		{name: "missing target", readback: repoinventory.Inventory{Repositories: []repoinventory.Repository{}}, wantStatus: "unverified"},
		{name: "readback unavailable", readbackErr: errors.New("daemon unavailable"), wantStatus: "unverified"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service := &renameHTTPService{fakeService: &fakeService{}, renameResult: reporename.ExecutionResult{Started: true, Verified: test.wantStatus == "renamed"}, renameResultSet: true, renameErr: test.exitErr}
			inventories := []repoinventory.Inventory{base, base}
			if test.readbackErr == nil {
				inventories = append(inventories, test.readback)
			}
			server := renameTestServer(t, service, inventories...)
			if test.readbackErr != nil {
				service.inventoryErrors = []error{nil, nil, test.readbackErr}
			}
			token := prepareRenameToken(t, server, `E:\tracked repo`, "new")
			response := confirmRename(t, server, token)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"`+test.wantStatus+`"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRepositoryRenameConfirmRejectsFreshDriftBeforeProcess(t *testing.T) {
	base := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}}
	cases := []struct {
		name          string
		inventories   []repoinventory.Inventory
		inventoryErrs []error
		mutate        func(*renameHTTPService)
	}{
		{name: "current name changed", inventories: []repoinventory.Inventory{base, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "external", RootPath: `E:\tracked repo`}}}}},
		{name: "target missing", inventories: []repoinventory.Inventory{base, repoinventory.Inventory{Repositories: []repoinventory.Repository{}}}},
		{name: "fallback collision", inventories: []repoinventory.Inventory{base, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}, {Name: `E:\tracked repo`, RootPath: `E:\other`}}}}},
		{name: "inventory unavailable", inventories: []repoinventory.Inventory{base}, inventoryErrs: []error{errors.New("offline")}},
		{name: "binding changed", inventories: []repoinventory.Inventory{base, base}, mutate: func(s *renameHTTPService) { s.preflightBindings = []string{"2:roborev"} }},
		{name: "cli version changed", inventories: []repoinventory.Inventory{base, base}, mutate: func(s *renameHTTPService) { s.preflightCLIs = []string{"v0.65.0"} }},
		{name: "daemon version changed", inventories: []repoinventory.Inventory{base, base}, mutate: func(s *renameHTTPService) { s.preflightDaemons = []string{"v0.65.0"} }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service := &renameHTTPService{fakeService: &fakeService{}}
			server := renameTestServer(t, service, test.inventories...)
			token := prepareRenameToken(t, server, `E:\tracked repo`, "new")
			if test.mutate != nil {
				test.mutate(service)
			}
			service.inventoryErrors = append([]error(nil), test.inventoryErrs...)
			service.renameResult = reporename.ExecutionResult{}
			service.renameResultSet = true
			service.renameErr = errors.New("adapter rejected stale target")
			response := confirmRename(t, server, token)
			if response.Code < http.StatusBadRequest || service.renameCalls != 1 || service.inventoryCallCount != 1 {
				t.Fatalf("status=%d rename=%d inventory=%d body=%s", response.Code, service.renameCalls, service.inventoryCallCount, response.Body.String())
			}
		})
	}

	service := &renameHTTPService{fakeService: &fakeService{}}
	server := renameTestServer(t, service, base, base)
	token := prepareRenameToken(t, server, `E:\tracked repo`, "new")
	service.installation = renameInstallation()
	service.installation.DaemonHealth = roborev.DaemonHealthOffline
	service.renameResult = reporename.ExecutionResult{}
	service.renameResultSet = true
	service.renameErr = roborev.ErrRepositoryRenameOffline
	response := confirmRename(t, server, token)
	if response.Code != http.StatusServiceUnavailable || service.renameCalls != 1 {
		t.Fatalf("health drift status=%d rename=%d body=%s", response.Code, service.renameCalls, response.Body.String())
	}
}

func TestRepositoryRenameDifferentTargetsRemainOrdinaryMutations(t *testing.T) {
	service := &renameHTTPService{fakeService: &fakeService{}}
	registry := NewRegistry(fakeInspector{}, "")
	for _, path := range []string{`E:\repo-a`, `E:\repo-b`} {
		if _, err := registry.Add(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	base := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "a", RootPath: `E:\repo-a`}, {Name: "b", RootPath: `E:\repo-b`}}}
	service.inventories = []repoinventory.Inventory{base, base, base, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "new-a", RootPath: `E:\repo-a`}, {Name: "b", RootPath: `E:\repo-b`}}}, base, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "new-a", RootPath: `E:\repo-a`}, {Name: "new-b", RootPath: `E:\repo-b`}}}}
	tokenA := prepareRenameToken(t, server, `E:\repo-a`, "new-a")
	tokenB := prepareRenameToken(t, server, `E:\repo-b`, "new-b")
	if response := confirmRename(t, server, tokenA); response.Code != http.StatusOK {
		t.Fatalf("target A status=%d body=%s", response.Code, response.Body.String())
	}
	if response := confirmRename(t, server, tokenB); response.Code != http.StatusOK {
		t.Fatalf("target B status=%d body=%s", response.Code, response.Body.String())
	}
	if service.renameCalls != 2 {
		t.Fatalf("rename calls=%d, want 2", service.renameCalls)
	}
}

func TestRepositoryRenameConcurrentSamePathClaimsAtMostOneProcess(t *testing.T) {
	base := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}}
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	service := &renameHTTPService{fakeService: &fakeService{}, renameBlock: release, renameStarted: started}
	server := renameTestServer(t, service, base, base, base, base, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "new", RootPath: `E:\tracked repo`}}})
	tokenA := prepareRenameToken(t, server, `E:\tracked repo`, "new")
	tokenB := prepareRenameToken(t, server, `E:\tracked repo`, "another")
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- confirmRename(t, server, tokenA) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first rename did not reach process")
	}
	second := confirmRename(t, server, tokenB)
	if second.Code != http.StatusConflict || service.renameCalls != 0 {
		t.Fatalf("second status=%d calls=%d body=%s", second.Code, service.renameCalls, second.Body.String())
	}
	replay := confirmRename(t, server, tokenA)
	if replay.Code != http.StatusConflict || service.renameCalls != 0 {
		t.Fatalf("concurrent replay status=%d calls=%d body=%s", replay.Code, service.renameCalls, replay.Body.String())
	}
	close(release)
	first := <-firstDone
	if first.Code != http.StatusOK || service.renameCalls != 1 {
		t.Fatalf("first status=%d calls=%d body=%s", first.Code, service.renameCalls, first.Body.String())
	}
}

func TestRepositoryRenameConfirmUsesBackgroundMutationContextAfterRequestCancel(t *testing.T) {
	base := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}}
	service := &renameHTTPService{fakeService: &fakeService{}}
	server := renameTestServer(t, service, base, base, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "new", RootPath: `E:\tracked repo`}}})
	token := prepareRenameToken(t, server, `E:\tracked repo`, "new")
	requestContext, cancel := context.WithCancel(context.Background())
	raw, _ := json.Marshal(map[string]string{"token": token})
	request := httptest.NewRequest(http.MethodPost, "/api/repository-renames", strings.NewReader(string(raw))).WithContext(requestContext)
	request.Host = "example.com"
	request.Header.Set("Origin", "http://example.com")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response := httptest.NewRecorder()
	cancel()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"renamed"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
