package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/repomove"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type moveHTTPService struct {
	*fakeService
	mu                 sync.Mutex
	installation       roborev.Installation
	inventory          repoinventory.Inventory
	inventoryCallCount int
	preflightCalls     int
	moveCalls          int
	moveResult         repomove.ExecutionResult
	moveResultSet      bool
	moveErr            error
}

func (s *moveHTTPService) Installation(context.Context) roborev.Installation {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installationCalls++
	if s.installation.Installed || s.installation.Version != "" {
		return s.installation
	}
	return roborev.Installation{Installed: true, Version: "v0.64.0", DaemonVersion: "v0.64.0", DaemonHealth: roborev.DaemonHealthOnline, DaemonCondition: roborev.DaemonConditionHealthy, Capabilities: roborev.Capabilities{RepositoryInventory: true, RepositoryRename: true, RepositoryMove: true}}
}

func (s *moveHTTPService) RepositoryInventory(context.Context) (repoinventory.Inventory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inventoryCallCount++
	return s.inventory, nil
}

func (s *moveHTTPService) RepositoryMovePreflight(_ context.Context, request repomove.Request) (repomove.Preflight, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.preflightCalls++
	return repomove.Preflight{Command: repomove.Command{Executable: "roborev", Args: []string{"repo", "move", request.SourcePath, "."}, Dir: request.DestinationPath}, Binding: "1:roborev", CLIVersion: "v0.64.0", DaemonVersion: "v0.64.0"}, nil
}

func (s *moveHTTPService) RepositoryMove(context.Context, repomove.Request, repomove.Preflight) (repomove.ExecutionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.moveCalls++
	if !s.moveResultSet && s.moveResult == (repomove.ExecutionResult{}) && s.moveErr == nil {
		return repomove.ExecutionResult{Started: true, Verified: true}, nil
	}
	return s.moveResult, s.moveErr
}

type moveAppAbsentFS struct{}

func (moveAppAbsentFS) Lstat(string) (os.FileInfo, error) { return nil, os.ErrNotExist }

type moveAppStaticFS struct {
	info os.FileInfo
	err  error
}

func (f *moveAppStaticFS) Lstat(string) (os.FileInfo, error) { return f.info, f.err }

type moveAppFileInfo struct{ mode os.FileMode }

func (f moveAppFileInfo) Name() string       { return "source" }
func (f moveAppFileInfo) Size() int64        { return 0 }
func (f moveAppFileInfo) Mode() os.FileMode  { return f.mode }
func (f moveAppFileInfo) ModTime() time.Time { return time.Time{} }
func (f moveAppFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f moveAppFileInfo) Sys() any           { return nil }

type moveFailingInspector struct {
	path string
	fail bool
}

func (i *moveFailingInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	if i.fail {
		return gitx.Repository{}, errors.New("destination inspection failed")
	}
	return gitx.Repository{Name: "destination", Path: i.pathOr(path), Branch: "main", Head: "abcdef123456", HeadShort: "abcdef12"}, nil
}

func (i *moveFailingInspector) pathOr(path string) string {
	if i.path != "" {
		return i.path
	}
	return path
}

func moveTestServer(t *testing.T, service *moveHTTPService, inventory repoinventory.Inventory) *Server {
	t.Helper()
	service.inventory = inventory
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\new\project`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	server.repositoryMoveFS = moveAppAbsentFS{}
	return server
}

func movePost(t *testing.T, server *Server, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Host = "example.com"
	request.Header.Set("Origin", "http://example.com")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func movePreviewBody(source, destinationIdentity string) string {
	raw, _ := json.Marshal(map[string]string{"sourcePath": source, "destinationRepositoryIdentity": destinationIdentity})
	return string(raw)
}

func prepareMoveToken(t *testing.T, server *Server, source, destination string) string {
	t.Helper()
	response := movePost(t, server, "/api/repository-moves/preview", movePreviewBody(source, gitx.RepositoryKey(destination)))
	if response.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", response.Code, response.Body.String())
	}
	var dto repositoryMovePreviewDTO
	if err := json.Unmarshal(response.Body.Bytes(), &dto); err != nil || dto.Token == "" {
		t.Fatalf("preview dto=%#v err=%v", dto, err)
	}
	return dto.Token
}

func TestRepositoryMovePreviewAndConfirmKeepRegistryUnchanged(t *testing.T) {
	service := &moveHTTPService{fakeService: &fakeService{}}
	const source = `E:\old\project`
	const destination = `E:\new\project`
	service.inventory = repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source, Count: 4}}, TotalCount: 4}
	server := moveTestServer(t, service, service.inventory)
	selectedBefore := server.Registry.Selected()
	preview := movePost(t, server, "/api/repository-moves/preview", movePreviewBody(source, gitx.RepositoryKey(destination)))
	if preview.Code != http.StatusOK || service.preflightCalls != 1 || service.moveCalls != 0 {
		t.Fatalf("preview status=%d preflight=%d move=%d body=%s", preview.Code, service.preflightCalls, service.moveCalls, preview.Body.String())
	}
	var dto repositoryMovePreviewDTO
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil || dto.SourcePath != source || dto.DestinationPath != destination || dto.Command.WorkingDirectory != destination || len(dto.Command.Args) != 4 || dto.Command.Args[3] != "." {
		t.Fatalf("dto=%#v err=%v body=%s", dto, err, preview.Body.String())
	}
	confirm := movePost(t, server, "/api/repository-moves", `{"token":"`+dto.Token+`"}`)
	if confirm.Code != http.StatusOK || !strings.Contains(confirm.Body.String(), `"status":"moved"`) || service.moveCalls != 1 {
		t.Fatalf("confirm status=%d move=%d body=%s", confirm.Code, service.moveCalls, confirm.Body.String())
	}
	if service.installationCalls != 1 || service.inventoryCallCount != 1 || service.preflightCalls != 1 {
		t.Fatalf("confirm repeated app work installation=%d inventory=%d preflight=%d", service.installationCalls, service.inventoryCallCount, service.preflightCalls)
	}
	if server.Registry.Selected() != selectedBefore || server.Registry.Contains(source) || !server.Registry.Contains(destination) {
		t.Fatalf("registry mutated selected=%q before=%q source=%v destination=%v", server.Registry.Selected(), selectedBefore, server.Registry.Contains(source), server.Registry.Contains(destination))
	}
}

func TestRepositoryMovePreviewRejectsRegisteredSourceAndDestinationConflict(t *testing.T) {
	const source = `E:\old\project`
	const destination = `E:\new\project`
	for _, test := range []struct {
		name       string
		registered bool
		inventory  repoinventory.Inventory
		want       int
	}{
		{name: "registered source", registered: true, inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}}, want: http.StatusConflict},
		{name: "destination tracked", inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}, {Name: "new", RootPath: destination}}}, want: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &moveHTTPService{fakeService: &fakeService{}}
			server := moveTestServer(t, service, test.inventory)
			if test.registered {
				if _, err := server.Registry.Add(context.Background(), source); err != nil {
					t.Fatal(err)
				}
			}
			response := movePost(t, server, "/api/repository-moves/preview", movePreviewBody(source, gitx.RepositoryKey(destination)))
			if response.Code != test.want || service.preflightCalls != 0 || service.moveCalls != 0 {
				t.Fatalf("status=%d want=%d preflight=%d move=%d body=%s", response.Code, test.want, service.preflightCalls, service.moveCalls, response.Body.String())
			}
		})
	}
}

func TestRepositoryMovePreviewResolvesSourceSemanticallyAndPreservesFreshSpelling(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows semantic path identity contract")
	}
	const sourceStored = `E:\old\project`
	const destination = `E:\new\project`
	service := &moveHTTPService{fakeService: &fakeService{}}
	server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: sourceStored}}})
	response := movePost(t, server, "/api/repository-moves/preview", movePreviewBody(`e:/OLD/project`, gitx.RepositoryKey(destination)))
	if response.Code != http.StatusOK || service.preflightCalls != 1 || !strings.Contains(response.Body.String(), `E:\\old\\project`) {
		t.Fatalf("status=%d preflight=%d body=%s", response.Code, service.preflightCalls, response.Body.String())
	}

	duplicate := &moveHTTPService{fakeService: &fakeService{}}
	duplicateServer := moveTestServer(t, duplicate, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: sourceStored}, {Name: "old-copy", RootPath: `e:/OLD/project`}}})
	duplicateResponse := movePost(t, duplicateServer, "/api/repository-moves/preview", movePreviewBody(`E:/old/project`, gitx.RepositoryKey(destination)))
	if duplicateResponse.Code != http.StatusConflict || duplicate.preflightCalls != 0 {
		t.Fatalf("duplicate status=%d preflight=%d body=%s", duplicateResponse.Code, duplicate.preflightCalls, duplicateResponse.Body.String())
	}
}

func TestRepositoryMovePreviewRejectsSemanticDestinationConflict(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows semantic path identity contract")
	}
	const source = `E:\old\project`
	service := &moveHTTPService{fakeService: &fakeService{}}
	server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}, {Name: "tracked-destination", RootPath: `e:/NEW/project`}}})
	response := movePost(t, server, "/api/repository-moves/preview", movePreviewBody(source, gitx.RepositoryKey(`E:\new\project`)))
	if response.Code != http.StatusConflict || service.preflightCalls != 0 {
		t.Fatalf("status=%d preflight=%d body=%s", response.Code, service.preflightCalls, response.Body.String())
	}
}

func TestRepositoryMovePreviewFailsClosedForEveryExistingSourceEntry(t *testing.T) {
	const source = `E:\old\project`
	const destination = `E:\new\project`
	for _, test := range []struct {
		name string
		mode os.FileMode
		err  error
	}{
		{name: "file"},
		{name: "directory", mode: os.ModeDir},
		{name: "symlink", mode: os.ModeSymlink},
		{name: "permission", err: os.ErrPermission},
		{name: "unknown", err: errors.New("Lstat failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &moveHTTPService{fakeService: &fakeService{}}
			server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}})
			server.repositoryMoveFS = &moveAppStaticFS{info: moveAppFileInfo{mode: test.mode}, err: test.err}
			response := movePost(t, server, "/api/repository-moves/preview", movePreviewBody(source, gitx.RepositoryKey(destination)))
			if response.Code != http.StatusConflict || service.preflightCalls != 0 || service.moveCalls != 0 {
				t.Fatalf("status=%d preflight=%d move=%d body=%s", response.Code, service.preflightCalls, service.moveCalls, response.Body.String())
			}
		})
	}
}

func TestRepositoryMoveAllowsEmptySourceNameAsReadbackBinding(t *testing.T) {
	const source = `E:\old\unnamed`
	const destination = `E:\new\project`
	service := &moveHTTPService{fakeService: &fakeService{}}
	server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "", RootPath: source}}})
	preview := movePost(t, server, "/api/repository-moves/preview", movePreviewBody(source, gitx.RepositoryKey(destination)))
	if preview.Code != http.StatusOK {
		t.Fatalf("empty source name preview status=%d body=%s", preview.Code, preview.Body.String())
	}
}

func TestRepositoryMoveConfirmSourceReappearanceStopsBeforeService(t *testing.T) {
	const source = `E:\old\reappeared`
	const destination = `E:\new\project`
	service := &moveHTTPService{fakeService: &fakeService{}}
	server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}})
	fs := &moveAppStaticFS{err: os.ErrNotExist}
	server.repositoryMoveFS = fs
	preview := movePost(t, server, "/api/repository-moves/preview", movePreviewBody(source, gitx.RepositoryKey(destination)))
	if preview.Code != http.StatusOK {
		t.Fatal(preview.Body.String())
	}
	var dto repositoryMovePreviewDTO
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	fs.err = nil
	fs.info = moveAppFileInfo{}
	confirm := movePost(t, server, "/api/repository-moves", `{"token":"`+dto.Token+`"}`)
	if confirm.Code != http.StatusConflict || service.moveCalls != 0 {
		t.Fatalf("status=%d move=%d body=%s", confirm.Code, service.moveCalls, confirm.Body.String())
	}
}

func TestRepositoryMoveConfirmRejectsDestinationIdentityDriftBeforeService(t *testing.T) {
	const source = `E:\old\drift`
	const destination = `E:\new\project`
	inspector := &mutableInspector{repository: gitx.Repository{Name: "destination", Path: destination}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	service := &moveHTTPService{fakeService: &fakeService{}, inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}}}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	server.repositoryMoveFS = moveAppAbsentFS{}
	token := prepareMoveToken(t, server, source, destination)
	inspector.repository.Path = `E:\somewhere\else`
	response := movePost(t, server, "/api/repository-moves", `{"token":"`+token+`"}`)
	if response.Code != http.StatusConflict || service.moveCalls != 0 {
		t.Fatalf("status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
	}
}

func TestRepositoryMoveConfirmRejectsDestinationInspectionFailureWhileStillRegistered(t *testing.T) {
	const source = `E:\old\inspection-failure`
	const destination = `E:\new\project`
	inspector := &moveFailingInspector{path: destination}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	service := &moveHTTPService{fakeService: &fakeService{}, inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}}}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	server.repositoryMoveFS = moveAppAbsentFS{}
	token := prepareMoveToken(t, server, source, destination)
	inspector.fail = true
	if !server.Registry.Contains(destination) {
		t.Fatal("destination unexpectedly removed from Registry")
	}
	response := movePost(t, server, "/api/repository-moves", `{"token":"`+token+`"}`)
	if response.Code != http.StatusConflict || service.moveCalls != 0 {
		t.Fatalf("status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
	}
}

func TestRepositoryMoveConfirmRejectsRemovedDestinationIdentityBeforeService(t *testing.T) {
	const source = `E:\old\removed`
	const destination = `E:\new\project`
	service := &moveHTTPService{fakeService: &fakeService{}}
	server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}})
	token := prepareMoveToken(t, server, source, destination)
	identity := gitx.RepositoryKey(destination)
	server.Registry.mu.Lock()
	delete(server.Registry.paths, identity)
	server.Registry.mu.Unlock()
	response := movePost(t, server, "/api/repository-moves", `{"token":"`+token+`"}`)
	if response.Code != http.StatusConflict || service.moveCalls != 0 {
		t.Fatalf("status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
	}
}

func TestRepositoryMoveConfirmRejectsSourceRegistrationDriftBeforeService(t *testing.T) {
	const source = `E:\old\registered-late`
	const destination = `E:\new\project`
	service := &moveHTTPService{fakeService: &fakeService{}}
	server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}})
	token := prepareMoveToken(t, server, source, destination)
	if _, err := server.Registry.Add(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	response := movePost(t, server, "/api/repository-moves", `{"token":"`+token+`"}`)
	if response.Code != http.StatusConflict || service.moveCalls != 0 {
		t.Fatalf("status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
	}
}

func TestRepositoryMovePreviewRejectsBrowserDestinationPathAndUnknownIdentity(t *testing.T) {
	const source = `E:\old\project`
	service := &moveHTTPService{fakeService: &fakeService{}}
	server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}})
	unknown := movePost(t, server, "/api/repository-moves/preview", movePreviewBody(source, "unknown-identity"))
	if unknown.Code != http.StatusConflict || service.preflightCalls != 0 {
		t.Fatalf("unknown identity status=%d preflight=%d", unknown.Code, service.preflightCalls)
	}
	pathBody := `{"sourcePath":"E:\\old\\project","destinationRepositoryIdentity":"` + gitx.RepositoryKey(`E:\new\project`) + `","destinationPath":"E:\\arbitrary"}`
	pathResponse := movePost(t, server, "/api/repository-moves/preview", pathBody)
	if pathResponse.Code != http.StatusBadRequest || service.preflightCalls != 0 {
		t.Fatalf("browser path status=%d preflight=%d body=%s", pathResponse.Code, service.preflightCalls, pathResponse.Body.String())
	}
}

func TestRepositoryMovePreviewRejectsFallbackCollision(t *testing.T) {
	const source = `E:\old\project`
	const destination = `E:\new\project`
	key, err := repomove.SourceFallbackKey(source)
	if err != nil {
		t.Fatal(err)
	}
	service := &moveHTTPService{fakeService: &fakeService{}}
	server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}, {Name: key, RootPath: `E:\other\repo`}}})
	response := movePost(t, server, "/api/repository-moves/preview", movePreviewBody(source, gitx.RepositoryKey(destination)))
	if response.Code != http.StatusConflict || service.preflightCalls != 0 {
		t.Fatalf("status=%d preflight=%d body=%s", response.Code, service.preflightCalls, response.Body.String())
	}
}

func TestRepositoryMoveTokenExpiryReplayAndDoubleConfirm(t *testing.T) {
	const source = `E:\old\token`
	const destination = `E:\new\project`
	base := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}}
	service := &moveHTTPService{fakeService: &fakeService{}}
	server := moveTestServer(t, service, base)
	expired := prepareMoveToken(t, server, source, destination)
	server.movePreviews.mu.Lock()
	record := server.movePreviews.pending[expired]
	record.ExpiresAt = time.Now().Add(-time.Second)
	server.movePreviews.pending[expired] = record
	server.movePreviews.mu.Unlock()
	if response := movePost(t, server, "/api/repository-moves", `{"token":"`+expired+`"}`); response.Code != http.StatusConflict || service.moveCalls != 0 {
		t.Fatalf("expired status=%d move=%d", response.Code, service.moveCalls)
	}

	token := prepareMoveToken(t, server, source, destination)
	first := movePost(t, server, "/api/repository-moves", `{"token":"`+token+`"}`)
	second := movePost(t, server, "/api/repository-moves", `{"token":"`+token+`"}`)
	if first.Code != http.StatusOK || second.Code != http.StatusConflict || service.moveCalls != 1 {
		t.Fatalf("first=%d second=%d move=%d firstBody=%s secondBody=%s", first.Code, second.Code, service.moveCalls, first.Body.String(), second.Body.String())
	}
}

func TestRepositoryMoveConfirmStartedOutcomeTaxonomy(t *testing.T) {
	const source = `E:\old\outcome`
	const destination = `E:\new\project`
	for _, test := range []struct {
		name       string
		result     repomove.ExecutionResult
		err        error
		wantStatus int
		wantBody   string
	}{
		{name: "started false", wantStatus: http.StatusBadGateway},
		{name: "started false typed error", err: errors.New("private stderr marker C:\\private\\roborev.exe"), wantStatus: http.StatusBadGateway},
		{name: "started unverified", result: repomove.ExecutionResult{Started: true}, wantStatus: http.StatusOK, wantBody: `"status":"unverified"`},
		{name: "offline", err: roborev.ErrRepositoryMoveOffline, wantStatus: http.StatusServiceUnavailable},
		{name: "unhealthy", err: roborev.ErrRepositoryMoveUnhealthy, wantStatus: http.StatusServiceUnavailable},
		{name: "unsupported", err: roborev.ErrUnsupportedRepositoryMove, wantStatus: http.StatusServiceUnavailable},
		{name: "deadline", err: context.DeadlineExceeded, wantStatus: http.StatusGatewayTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &moveHTTPService{fakeService: &fakeService{}, moveResult: test.result, moveResultSet: test.name == "started false" || test.name == "started unverified", moveErr: test.err}
			server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}})
			token := prepareMoveToken(t, server, source, destination)
			response := movePost(t, server, "/api/repository-moves", `{"token":"`+token+`"}`)
			if response.Code != test.wantStatus || service.moveCalls != 1 || test.wantBody != "" && !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("status=%d want=%d move=%d body=%s", response.Code, test.wantStatus, service.moveCalls, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "private stderr marker") || strings.Contains(response.Body.String(), `C:\private\roborev.exe`) {
				t.Fatalf("private process detail leaked in response: %s", response.Body.String())
			}
		})
	}
}

func TestRepositoryMovePreviewAuthAndShapeBeforeSideEffects(t *testing.T) {
	service := &moveHTTPService{fakeService: &fakeService{}}
	server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\old\project`}}})
	for _, test := range []struct {
		name string
		path string
		body string
	}{
		{name: "foreign", path: "/api/repository-moves/preview", body: movePreviewBody(`E:\old\project`, "x")},
		{name: "query", path: "/api/repository-moves/preview?x=1", body: movePreviewBody(`E:\old\project`, "x")},
		{name: "unknown body", path: "/api/repository-moves/preview", body: `{"sourcePath":"E:\\old\\project","destinationRepositoryIdentity":"x","extra":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Host = "example.com"
			request.Header.Set("Origin", "http://example.com")
			request.Header.Set("X-CSRF-Token", server.csrfToken)
			if test.name == "foreign" {
				request.Header.Set("Origin", "http://evil.example")
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code < http.StatusBadRequest || service.installationCalls != 0 || service.inventoryCallCount != 0 || service.preflightCalls != 0 {
				t.Fatalf("status=%d side effects install=%d inventory=%d preflight=%d body=%s", response.Code, service.installationCalls, service.inventoryCallCount, service.preflightCalls, response.Body.String())
			}
		})
	}
}

func TestRepositoryMoveAvailabilityUsesProductionTypedTruthTable(t *testing.T) {
	const source = `E:\old\project`
	const destination = `E:\new\project`
	baseInventory := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}}
	allCapabilities := roborev.Capabilities{RepositoryInventory: true, RepositoryRename: true, RepositoryMove: true}
	tests := []struct {
		name         string
		installation roborev.Installation
		wantStatus   int
	}{
		{name: "no installation", installation: roborev.Installation{Version: "v0.64.0"}, wantStatus: http.StatusServiceUnavailable},
		{name: "unsupported version", installation: roborev.Installation{Installed: true, Version: "v0.63.0", Capabilities: roborev.Capabilities{}}, wantStatus: http.StatusServiceUnavailable},
		{name: "v064 offline", installation: roborev.Installation{Installed: true, Version: "v0.64.0", DaemonVersion: "v0.64.0", DaemonHealth: roborev.DaemonHealthOffline, DaemonCondition: roborev.DaemonConditionUnknown, Capabilities: allCapabilities}, wantStatus: http.StatusServiceUnavailable},
		{name: "v064 unhealthy", installation: roborev.Installation{Installed: true, Version: "v0.64.0", DaemonVersion: "v0.64.0", DaemonHealth: roborev.DaemonHealthOnline, DaemonCondition: roborev.DaemonConditionUnhealthy, Capabilities: allCapabilities}, wantStatus: http.StatusServiceUnavailable},
		{name: "health invalid", installation: roborev.Installation{Installed: true, Version: "v0.64.0", DaemonVersion: "v0.64.0", DaemonHealth: roborev.DaemonHealthInvalid, DaemonCondition: roborev.DaemonConditionUnknown, Capabilities: allCapabilities}, wantStatus: http.StatusBadGateway},
		{name: "health unknown", installation: roborev.Installation{Installed: true, Version: "v0.64.0", DaemonVersion: "v0.64.0", DaemonHealth: roborev.DaemonHealthUnknown, DaemonCondition: roborev.DaemonConditionUnknown, Capabilities: allCapabilities}, wantStatus: http.StatusBadGateway},
		{name: "daemon version mismatch", installation: roborev.Installation{Installed: true, Version: "v0.64.0", DaemonVersion: "v0.63.0", DaemonHealth: roborev.DaemonHealthOnline, DaemonCondition: roborev.DaemonConditionHealthy, Capabilities: allCapabilities}, wantStatus: http.StatusBadGateway},
		{name: "available", installation: roborev.Installation{Installed: true, Version: "v0.64.0", DaemonVersion: "v0.64.0", DaemonHealth: roborev.DaemonHealthOnline, DaemonCondition: roborev.DaemonConditionHealthy, Capabilities: allCapabilities}, wantStatus: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &moveHTTPService{fakeService: &fakeService{}, installation: test.installation}
			server := moveTestServer(t, service, baseInventory)
			response := movePost(t, server, "/api/repository-moves/preview", movePreviewBody(source, gitx.RepositoryKey(destination)))
			if response.Code != test.wantStatus || service.inventoryCallCount != boolInt(test.wantStatus == http.StatusOK) || service.preflightCalls != boolInt(test.wantStatus == http.StatusOK) {
				t.Fatalf("status=%d want=%d inventory=%d preflight=%d body=%s", response.Code, test.wantStatus, service.inventoryCallCount, service.preflightCalls, response.Body.String())
			}
		})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestRepositoryMovePreviewStoreReservesBothPaths(t *testing.T) {
	store := newRepositoryMovePreviewStore()
	now := time.Now().Add(time.Minute)
	recordA := repositoryMovePreviewRecord{Request: repomove.Request{SourcePath: `E:\a`, DestinationPath: `E:\b`}, ExpiresAt: now}
	recordC := repositoryMovePreviewRecord{Request: repomove.Request{SourcePath: `E:\c`, DestinationPath: `E:\b`}, ExpiresAt: now}
	recordD := repositoryMovePreviewRecord{Request: repomove.Request{SourcePath: `E:\a`, DestinationPath: `E:\d`}, ExpiresAt: now}
	tokenA, err := store.put(recordA)
	if err != nil {
		t.Fatal(err)
	}
	tokenC, err := store.put(recordC)
	if err != nil {
		t.Fatal(err)
	}
	tokenD, err := store.put(recordD)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, busy := store.claim(tokenA)
	if !ok || busy || claimed.Request.SourcePath != recordA.Request.SourcePath {
		t.Fatalf("claim A=%#v ok=%v busy=%v", claimed, ok, busy)
	}
	if _, ok, busy := store.claim(tokenC); ok || !busy {
		t.Fatalf("claim C ok=%v busy=%v", ok, busy)
	}
	if _, ok, busy := store.claim(tokenD); ok || !busy {
		t.Fatalf("claim D ok=%v busy=%v", ok, busy)
	}
	store.release(claimed)
	if _, ok, busy := store.claim(tokenC); !ok || busy {
		t.Fatalf("claim C after release ok=%v busy=%v", ok, busy)
	}
}

func TestRepositoryMovePreviewStoreExpiresBeforeCapacityAndEvictsDeterministically(t *testing.T) {
	store := newRepositoryMovePreviewStore()
	now := time.Now()
	store.pending["expired"] = repositoryMovePreviewRecord{ExpiresAt: now.Add(-time.Second)}
	for index := 0; index < repositoryMovePreviewMax-1; index++ {
		store.pending[fmt.Sprintf("live-%02d", index)] = repositoryMovePreviewRecord{ExpiresAt: now.Add(time.Minute)}
	}
	if _, err := store.put(repositoryMovePreviewRecord{ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if len(store.pending) != repositoryMovePreviewMax {
		t.Fatalf("expiry was not processed first: pending=%d", len(store.pending))
	}
	if _, ok := store.pending["expired"]; ok {
		t.Fatal("expired preview was not removed before capacity eviction")
	}
	store = newRepositoryMovePreviewStore()
	for index := 0; index < repositoryMovePreviewMax; index++ {
		store.pending[fmt.Sprintf("token-%02d", index)] = repositoryMovePreviewRecord{ExpiresAt: now.Add(time.Minute)}
	}
	if _, err := store.put(repositoryMovePreviewRecord{ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.pending["token-00"]; ok {
		t.Fatal("lexicographically oldest token was not evicted on expiry tie")
	}
	if len(store.pending) != repositoryMovePreviewMax {
		t.Fatalf("pending=%d want=%d", len(store.pending), repositoryMovePreviewMax)
	}
}

var _ roborev.RepositoryMoveService = (*moveHTTPService)(nil)
