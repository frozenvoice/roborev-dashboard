package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type inventoryHTTPService struct {
	fakeService
	installation roborev.Installation
	inventory    repoinventory.Inventory
	inventoryErr error
	installCalls int
	listCalls    int
}

func (s *inventoryHTTPService) Installation(context.Context) roborev.Installation {
	s.installCalls++
	return s.installation
}

func (s *inventoryHTTPService) RepositoryInventory(context.Context) (repoinventory.Inventory, error) {
	s.listCalls++
	return s.inventory, s.inventoryErr
}

func inventoryInstallation() roborev.Installation {
	return roborev.Installation{
		Installed: true,
		Version:   "v0.64.0",
		Capabilities: roborev.Capabilities{
			RepositoryInventory: true,
		},
		DaemonHealth:    roborev.DaemonHealthOnline,
		DaemonCondition: roborev.DaemonConditionHealthy,
		DaemonVersion:   "v0.64.0",
	}
}

func setupInventoryServer(t *testing.T, service *inventoryHTTPService, paths ...string) *Server {
	t.Helper()
	registry := NewRegistry(fakeInspector{}, "")
	for _, path := range paths {
		if _, err := registry.Add(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func inventoryRequest(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	return request
}

func TestRepositoryInventoryReturnsDashboardDTOInUpstreamOrder(t *testing.T) {
	service := &inventoryHTTPService{
		installation: inventoryInstallation(),
		inventory: repoinventory.Inventory{
			Repositories: []repoinventory.Repository{
				{Name: "project", RootPath: `E:\tracked`, Identity: "upstream-only-a", Count: 4},
				{Name: "project", RootPath: `E:\foreign`, Identity: "upstream-only-b", Count: 0},
				{Name: "other", RootPath: `E:\missing`, Count: 3},
			},
			TotalCount: 7,
		},
	}
	server := setupInventoryServer(t, service, `E:\tracked`)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, inventoryRequest("/api/repository-inventory"))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body RepositoryInventoryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RepositoryCount != 3 || body.TotalJobCount != 7 || len(body.Repositories) != 3 {
		t.Fatalf("unexpected totals: %#v", body)
	}
	if body.Repositories[0].Name != "project" || body.Repositories[0].Path != `E:\tracked` || body.Repositories[0].JobCount != 4 || !body.Repositories[0].DashboardRegistered {
		t.Fatalf("first row=%#v", body.Repositories[0])
	}
	if body.Repositories[1].Name != "project" || body.Repositories[1].Path != `E:\foreign` || body.Repositories[1].DashboardRegistered {
		t.Fatalf("second row=%#v", body.Repositories[1])
	}
	if body.Repositories[2].Name != "other" || body.Repositories[2].Path != `E:\missing` || body.Repositories[2].DashboardRegistered {
		t.Fatalf("third row=%#v", body.Repositories[2])
	}
	if strings.Contains(response.Body.String(), "upstream-only") || strings.Contains(response.Body.String(), "identity") {
		t.Fatalf("upstream identity leaked into DTO: %s", response.Body.String())
	}
	if service.listCalls != 1 || service.installCalls != 1 {
		t.Fatalf("service calls list=%d install=%d", service.listCalls, service.installCalls)
	}
}

func TestRepositoryInventoryAllowsEmptyInventory(t *testing.T) {
	service := &inventoryHTTPService{installation: inventoryInstallation(), inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{}, TotalCount: 0}}
	server := setupInventoryServer(t, service)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, inventoryRequest("/api/repository-inventory"))
	if response.Code != http.StatusOK || response.Body.String() != "{\"repositories\":[],\"repositoryCount\":0,\"totalJobCount\":0}\n" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRepositoryInventoryUsesPlatformAwareRegistryCorrelation(t *testing.T) {
	service := &inventoryHTTPService{
		installation: inventoryInstallation(),
		inventory:    repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "repo", RootPath: `E:/tracked`, Identity: "wrong", Count: 1}}, TotalCount: 1},
	}
	server := setupInventoryServer(t, service, `E:\tracked`)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, inventoryRequest("/api/repository-inventory"))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"dashboardRegistered":true`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRepositoryInventoryAllowsExactOrigin(t *testing.T) {
	service := &inventoryHTTPService{
		installation: inventoryInstallation(),
		inventory:    repoinventory.Inventory{Repositories: []repoinventory.Repository{}, TotalCount: 0},
	}
	server := setupInventoryServer(t, service)
	request := httptest.NewRequest(http.MethodGet, "/api/repository-inventory", nil)
	request.Header.Set("Origin", "http://example.com")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.installCalls != 1 || service.listCalls != 1 {
		t.Fatalf("status=%d installCalls=%d listCalls=%d body=%s", response.Code, service.installCalls, service.listCalls, response.Body.String())
	}
}

func TestRepositoryInventoryAllowsNativeFetchMetadataTriple(t *testing.T) {
	service := &inventoryHTTPService{
		installation: inventoryInstallation(),
		inventory:    repoinventory.Inventory{Repositories: []repoinventory.Repository{}, TotalCount: 0},
	}
	server := setupInventoryServer(t, service)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, inventoryRequest("/api/repository-inventory"))
	if response.Code != http.StatusOK || service.installCalls != 1 || service.listCalls != 1 {
		t.Fatalf("status=%d installCalls=%d listCalls=%d body=%s", response.Code, service.installCalls, service.listCalls, response.Body.String())
	}
}

func TestRepositoryInventoryAuthRejectionPrecedesSideEffects(t *testing.T) {
	tests := []struct {
		name    string
		request func() *http.Request
		status  int
	}{
		{name: "missing fetch site", request: func() *http.Request {
			r := inventoryRequest("/api/repository-inventory")
			r.Header.Del("Sec-Fetch-Site")
			return r
		}, status: http.StatusForbidden},
		{name: "wrong fetch site", request: func() *http.Request {
			r := inventoryRequest("/api/repository-inventory")
			r.Header.Set("Sec-Fetch-Site", "same-site")
			return r
		}, status: http.StatusForbidden},
		{name: "duplicate fetch site", request: func() *http.Request {
			r := inventoryRequest("/api/repository-inventory")
			r.Header.Add("Sec-Fetch-Site", "same-origin")
			return r
		}, status: http.StatusForbidden},
		{name: "foreign origin", request: func() *http.Request {
			r := inventoryRequest("/api/repository-inventory")
			r.Header.Set("Origin", "http://evil.example")
			return r
		}, status: http.StatusForbidden},
		{name: "null origin", request: func() *http.Request {
			r := inventoryRequest("/api/repository-inventory")
			r.Header.Set("Origin", "null")
			return r
		}, status: http.StatusForbidden},
		{name: "duplicate origin", request: func() *http.Request {
			r := inventoryRequest("/api/repository-inventory")
			r.Header.Set("Origin", "http://example.com")
			r.Header.Add("Origin", "http://example.com")
			return r
		}, status: http.StatusForbidden},
		{name: "missing fetch mode", request: func() *http.Request {
			r := inventoryRequest("/api/repository-inventory")
			r.Header.Del("Sec-Fetch-Mode")
			return r
		}, status: http.StatusForbidden},
		{name: "wrong fetch mode", request: func() *http.Request {
			r := inventoryRequest("/api/repository-inventory")
			r.Header.Set("Sec-Fetch-Mode", "navigate")
			return r
		}, status: http.StatusForbidden},
		{name: "duplicate fetch mode", request: func() *http.Request {
			r := inventoryRequest("/api/repository-inventory")
			r.Header.Add("Sec-Fetch-Mode", "cors")
			return r
		}, status: http.StatusForbidden},
		{name: "missing fetch destination", request: func() *http.Request {
			r := inventoryRequest("/api/repository-inventory")
			r.Header.Del("Sec-Fetch-Dest")
			return r
		}, status: http.StatusForbidden},
		{name: "wrong fetch destination", request: func() *http.Request {
			r := inventoryRequest("/api/repository-inventory")
			r.Header.Set("Sec-Fetch-Dest", "document")
			return r
		}, status: http.StatusForbidden},
		{name: "duplicate fetch destination", request: func() *http.Request {
			r := inventoryRequest("/api/repository-inventory")
			r.Header.Add("Sec-Fetch-Dest", "empty")
			return r
		}, status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &inventoryHTTPService{installation: inventoryInstallation()}
			server := setupInventoryServer(t, service)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, test.request())
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
			if service.installCalls != 0 || service.listCalls != 0 {
				t.Fatalf("side effects after rejected request: install=%d list=%d", service.installCalls, service.listCalls)
			}
		})
	}
}

func TestRepositoryInventoryQueryRejectionPrecedesSideEffects(t *testing.T) {
	for _, query := range []string{"branch=main", "prefix=E%3A%5C%5Ctracked", "foo=bar", "repositoryIdentity=opaque"} {
		t.Run(query, func(t *testing.T) {
			service := &inventoryHTTPService{installation: inventoryInstallation()}
			server := setupInventoryServer(t, service)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, inventoryRequest("/api/repository-inventory?"+query))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if service.installCalls != 0 || service.listCalls != 0 {
				t.Fatalf("side effects after query rejection: install=%d list=%d", service.installCalls, service.listCalls)
			}
		})
	}
}

func TestRepositoryInventoryAvailabilityGate(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*roborev.Installation)
		want      int
		listCalls int
	}{
		{name: "not installed", mutate: func(i *roborev.Installation) { i.Installed = false }, want: http.StatusServiceUnavailable},
		{name: "unsupported cli", mutate: func(i *roborev.Installation) { i.Version = "v0.65.0" }, want: http.StatusServiceUnavailable},
		{name: "missing capability", mutate: func(i *roborev.Installation) { i.Capabilities.RepositoryInventory = false }, want: http.StatusServiceUnavailable},
		{name: "offline", mutate: func(i *roborev.Installation) { i.DaemonHealth = roborev.DaemonHealthOffline }, want: http.StatusServiceUnavailable},
		{name: "invalid health", mutate: func(i *roborev.Installation) { i.DaemonHealth = roborev.DaemonHealthInvalid }, want: http.StatusBadGateway},
		{name: "unknown condition", mutate: func(i *roborev.Installation) { i.DaemonCondition = roborev.DaemonConditionUnknown }, want: http.StatusBadGateway},
		{name: "unhealthy", mutate: func(i *roborev.Installation) { i.DaemonCondition = roborev.DaemonConditionUnhealthy }, want: http.StatusServiceUnavailable},
		{name: "version mismatch", mutate: func(i *roborev.Installation) { i.DaemonVersion = "v0.63.0" }, want: http.StatusServiceUnavailable},
		{name: "available", mutate: func(i *roborev.Installation) {}, want: http.StatusOK, listCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			installation := inventoryInstallation()
			test.mutate(&installation)
			service := &inventoryHTTPService{installation: installation, inventory: repoinventory.Inventory{Repositories: []repoinventory.Repository{}, TotalCount: 0}}
			server := setupInventoryServer(t, service)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, inventoryRequest("/api/repository-inventory"))
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
			}
			if service.listCalls != test.listCalls {
				t.Fatalf("inventory calls=%d want=%d", service.listCalls, test.listCalls)
			}
		})
	}
}

func TestRepositoryInventoryMapsServiceFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "malformed", err: errors.New("malformed"), want: http.StatusBadGateway},
		{name: "offline", err: roborev.ErrRepositoryInventoryOffline, want: http.StatusServiceUnavailable},
		{name: "unhealthy", err: roborev.ErrRepositoryInventoryUnhealthy, want: http.StatusServiceUnavailable},
		{name: "unsupported", err: roborev.ErrUnsupportedRepositoryInventory, want: http.StatusServiceUnavailable},
		{name: "timeout", err: context.DeadlineExceeded, want: http.StatusGatewayTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &inventoryHTTPService{installation: inventoryInstallation(), inventoryErr: test.err}
			server := setupInventoryServer(t, service)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, inventoryRequest("/api/repository-inventory"))
			if response.Code != test.want || service.listCalls != 1 {
				t.Fatalf("status=%d want=%d listCalls=%d body=%s", response.Code, test.want, service.listCalls, response.Body.String())
			}
		})
	}
}
