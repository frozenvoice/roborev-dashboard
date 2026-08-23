package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
)

type eligibilityCountingInspector struct {
	calls int
}

func (i *eligibilityCountingInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	i.calls++
	return gitx.Repository{Name: "repository", Path: path, Branch: "main", Head: "head"}, nil
}

type eligibilityCountingFS struct {
	err   error
	calls int
}

func (f *eligibilityCountingFS) Lstat(string) (os.FileInfo, error) {
	f.calls++
	return nil, f.err
}

func eligibilityRequest(server *Server, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Host = "example.com"
	request.Header.Set("Origin", "http://example.com")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func TestRepositoryMoveEligibilityReturnsStrictEligibleDTO(t *testing.T) {
	const source = `E:\old\project`
	service := &moveHTTPService{fakeService: &fakeService{}}
	server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}})
	server.repositoryMoveFS = moveAppAbsentFS{}
	response := eligibilityRequest(server, "/api/repository-moves/eligibility", `{"sourcePath":"E:\\old\\project"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var value map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if len(value) != 3 || value["eligible"] != true || value["sourcePath"] != source || value["sourceName"] != "old" {
		t.Fatalf("unexpected eligible DTO: %#v", value)
	}
	if _, ok := value["token"]; ok || service.preflightCalls != 0 || service.moveCalls != 0 {
		t.Fatalf("eligibility performed mutation/token work: value=%#v preflight=%d move=%d", value, service.preflightCalls, service.moveCalls)
	}
	server.movePreviews.mu.Lock()
	pending := len(server.movePreviews.pending)
	server.movePreviews.mu.Unlock()
	if pending != 0 {
		t.Fatalf("eligibility unexpectedly created %d preview tokens", pending)
	}
}

func TestRepositoryMoveEligibilityIncludesEmptySourceName(t *testing.T) {
	const source = `E:\old\unnamed`
	service := &moveHTTPService{fakeService: &fakeService{}}
	server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "", RootPath: source}}})
	server.repositoryMoveFS = moveAppAbsentFS{}
	response := eligibilityRequest(server, "/api/repository-moves/eligibility", `{"sourcePath":"E:\\old\\unnamed"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var value map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if len(value) != 3 || value["eligible"] != true || value["sourcePath"] != source || value["sourceName"] != "" {
		t.Fatalf("empty source name was not preserved: %#v", value)
	}
}

func TestRepositoryMoveEligibilityReturnsStrictIneligibleReasons(t *testing.T) {
	const source = `E:\old\project`
	for _, test := range []struct {
		name       string
		fs         repomoveFileSystemForTest
		registered bool
		wantReason string
	}{
		{name: "source exists", fs: repomoveFileSystemForTest{info: moveAppFileInfo{}}, wantReason: "source-exists"},
		{name: "dashboard registered", fs: repomoveFileSystemForTest{err: os.ErrNotExist}, registered: true, wantReason: "dashboard-registered"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &moveHTTPService{fakeService: &fakeService{}}
			server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}})
			server.repositoryMoveFS = test.fs
			if test.registered {
				if _, err := server.Registry.Add(context.Background(), source); err != nil {
					t.Fatal(err)
				}
			}
			response := eligibilityRequest(server, "/api/repository-moves/eligibility", `{"sourcePath":"E:\\old\\project"}`)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var value map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			if len(value) != 2 || value["eligible"] != false || value["reason"] != test.wantReason {
				t.Fatalf("unexpected ineligible DTO: %#v", value)
			}
			if service.preflightCalls != 0 || service.moveCalls != 0 {
				t.Fatalf("eligibility performed mutation work: preflight=%d move=%d", service.preflightCalls, service.moveCalls)
			}
			server.movePreviews.mu.Lock()
			pending := len(server.movePreviews.pending)
			server.movePreviews.mu.Unlock()
			if pending != 0 {
				t.Fatalf("eligibility unexpectedly created %d preview tokens", pending)
			}
		})
	}
}

// repomove.FileSystem is intentionally not embedded in this test table so
// each case can keep its deterministic Lstat result inline without touching
// the production filesystem adapter.
type repomoveFileSystemForTest struct {
	info os.FileInfo
	err  error
}

func (f repomoveFileSystemForTest) Lstat(string) (os.FileInfo, error) { return f.info, f.err }

func TestRepositoryMoveEligibilityRejectsMalformedRequestsBeforeSideEffects(t *testing.T) {
	service := &moveHTTPService{fakeService: &fakeService{}}
	server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\old\project`}}})
	for _, test := range []struct {
		name string
		path string
		body string
	}{
		{name: "query", path: "/api/repository-moves/eligibility?x=1", body: `{"sourcePath":"E:\\old\\project"}`},
		{name: "unknown field", path: "/api/repository-moves/eligibility", body: `{"sourcePath":"E:\\old\\project","destinationPath":"E:\\new"}`},
		{name: "missing source", path: "/api/repository-moves/eligibility", body: `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := eligibilityRequest(server, test.path, test.body)
			if response.Code != http.StatusBadRequest || service.installationCalls != 0 || service.inventoryCallCount != 0 {
				t.Fatalf("status=%d install=%d inventory=%d body=%s", response.Code, service.installationCalls, service.inventoryCallCount, response.Body.String())
			}
		})
	}
}

func TestRepositoryMoveEligibilityDoesNotProbeDuringInventoryLoad(t *testing.T) {
	const source = `E:\old\project`
	service := &moveHTTPService{fakeService: &fakeService{}, inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}}}
	server := moveTestServer(t, service, service.inventory)
	filesystem := &eligibilityCountingFS{err: os.ErrNotExist}
	server.repositoryMoveFS = filesystem
	request := httptest.NewRequest(http.MethodGet, "/api/repository-inventory", nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || filesystem.calls != 0 {
		t.Fatalf("inventory status=%d source probes=%d body=%s", response.Code, filesystem.calls, response.Body.String())
	}
}

func TestRepositoryMoveEligibilityDoesNotInspectDestination(t *testing.T) {
	const source = `E:\old\project`
	const destination = `E:\new\project`
	service := &moveHTTPService{fakeService: &fakeService{}, inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}}}
	inspector := &eligibilityCountingInspector{}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	inspector.calls = 0
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	server.repositoryMoveFS = moveAppAbsentFS{}
	response := eligibilityRequest(server, "/api/repository-moves/eligibility", `{"sourcePath":"E:\\old\\project"}`)
	if response.Code != http.StatusOK || inspector.calls != 0 {
		t.Fatalf("status=%d destination inspections=%d body=%s", response.Code, inspector.calls, response.Body.String())
	}
}

func TestRepositoryMoveEligibilityAuthUnknownAndSourceErrorsFailClosed(t *testing.T) {
	const source = `E:\old\project`
	t.Run("auth before installation", func(t *testing.T) {
		service := &moveHTTPService{fakeService: &fakeService{}}
		server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}})
		request := httptest.NewRequest(http.MethodPost, "/api/repository-moves/eligibility", strings.NewReader(`{"sourcePath":"E:\\old\\project"}`))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || service.installationCalls != 0 || service.inventoryCallCount != 0 {
			t.Fatalf("status=%d installation=%d inventory=%d body=%s", response.Code, service.installationCalls, service.inventoryCallCount, response.Body.String())
		}
	})

	for _, test := range []struct {
		name      string
		inventory repoinventory.Inventory
		fsErr     error
		want      int
	}{
		{name: "unknown source", inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "other", RootPath: `E:\other`}}}, want: http.StatusConflict},
		{name: "duplicate source", inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "one", RootPath: source}, {Name: "two", RootPath: source}}}, want: http.StatusConflict},
		{name: "permission source", inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}}, fsErr: os.ErrPermission, want: http.StatusConflict},
		{name: "unknown source error", inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}}, fsErr: errors.New("lstat failed"), want: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &moveHTTPService{fakeService: &fakeService{}}
			server := moveTestServer(t, service, test.inventory)
			if test.fsErr != nil {
				server.repositoryMoveFS = &moveAppStaticFS{err: test.fsErr}
			} else {
				server.repositoryMoveFS = moveAppAbsentFS{}
			}
			response := eligibilityRequest(server, "/api/repository-moves/eligibility", `{"sourcePath":"E:\\old\\project"}`)
			if response.Code != test.want || service.preflightCalls != 0 || service.moveCalls != 0 {
				t.Fatalf("status=%d want=%d preflight=%d move=%d body=%s", response.Code, test.want, service.preflightCalls, service.moveCalls, response.Body.String())
			}
			server.movePreviews.mu.Lock()
			pending := len(server.movePreviews.pending)
			server.movePreviews.mu.Unlock()
			if pending != 0 {
				t.Fatalf("eligibility unexpectedly created %d preview tokens", pending)
			}
		})
	}
}
