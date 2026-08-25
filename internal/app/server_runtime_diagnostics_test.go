package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

type runtimeDiagnosticsTestService struct {
	*fakeService
	result roborev.RuntimeDiagnostics
	err    error
	calls  int
}

func (s *runtimeDiagnosticsTestService) RuntimeDiagnostics(context.Context) (roborev.RuntimeDiagnostics, error) {
	s.calls++
	return s.result, s.err
}

func runtimeDiagnosticsTestServer(t *testing.T, service roborev.Service) *Server {
	t.Helper()
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func runtimeDiagnosticsRequest(query string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/runtime/diagnostics"+query, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	return request
}

func TestRuntimeDiagnosticsEndpointReturnsTypedDTO(t *testing.T) {
	service := &runtimeDiagnosticsTestService{fakeService: &fakeService{}, result: roborev.RuntimeDiagnostics{
		Daemon:  roborev.RuntimeDiagnosticsDaemon{Running: true, Version: "v0.64.0", Uptime: "1h"},
		Workers: roborev.RuntimeDiagnosticsWorkers{Active: 1, Max: 4},
		Queue:   roborev.RuntimeDiagnosticsQueue{Queued: 2, Running: 1},
		Jobs:    roborev.RuntimeDiagnosticsJobs{Completed: 100, Failed: 3, Skipped: 4},
		Health:  roborev.RuntimeDiagnosticsHealth{Healthy: true, ErrorCount24h: 0, Components: []roborev.RuntimeDiagnosticsComponent{{Name: "database", Healthy: true}}},
	}}
	server := runtimeDiagnosticsTestServer(t, service)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, runtimeDiagnosticsRequest(""))
	if recorder.Code != http.StatusOK || service.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil || got["daemon"] == nil || got["health"] == nil {
		t.Fatalf("response=%s err=%v", recorder.Body.String(), err)
	}
}

func TestRuntimeDiagnosticsEndpointReturnsDegradedDiagnosticsAsSuccess(t *testing.T) {
	service := &runtimeDiagnosticsTestService{fakeService: &fakeService{}, result: roborev.RuntimeDiagnostics{
		Daemon:  roborev.RuntimeDiagnosticsDaemon{Running: true, Version: "v0.64.0", Uptime: "1h"},
		Workers: roborev.RuntimeDiagnosticsWorkers{Active: 2, Max: 4},
		Queue:   roborev.RuntimeDiagnosticsQueue{Paused: true, Queued: 3, Running: 2},
		Jobs:    roborev.RuntimeDiagnosticsJobs{Completed: 10, Failed: 2, Skipped: 1},
		Health: roborev.RuntimeDiagnosticsHealth{
			Healthy:       false,
			ErrorCount24h: 3,
			Components: []roborev.RuntimeDiagnosticsComponent{
				{Name: "database", Healthy: true},
				{Name: "workers", Healthy: false},
			},
		},
	}}
	server := runtimeDiagnosticsTestServer(t, service)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, runtimeDiagnosticsRequest(""))
	if recorder.Code != http.StatusOK || service.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
	}
	var got roborev.RuntimeDiagnostics
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Health.Healthy || got.Health.ErrorCount24h != 3 || len(got.Health.Components) != 2 {
		t.Fatalf("degraded response=%+v", got)
	}
}

func TestRuntimeDiagnosticsEndpointAuthorizesAndRejectsQueriesBeforeService(t *testing.T) {
	service := &runtimeDiagnosticsTestService{fakeService: &fakeService{}}
	server := runtimeDiagnosticsTestServer(t, service)
	unauthorized := httptest.NewRequest(http.MethodGet, "/api/runtime/diagnostics?x=1", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, unauthorized)
	if recorder.Code != http.StatusForbidden || service.calls != 0 {
		t.Fatalf("unauthorized status=%d calls=%d", recorder.Code, service.calls)
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, runtimeDiagnosticsRequest("?x=1"))
	if recorder.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("query status=%d calls=%d", recorder.Code, service.calls)
	}
}

func TestRuntimeDiagnosticsEndpointMapsTypedErrorsWithoutLeakage(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "offline", err: roborev.ErrRuntimeDiagnosticsOffline, want: http.StatusServiceUnavailable},
		{name: "unsupported", err: roborev.ErrUnsupportedRuntimeDiagnostics, want: http.StatusServiceUnavailable},
		{name: "malformed", err: roborev.ErrMalformedRuntimeDiagnostics, want: http.StatusBadGateway},
		{name: "deadline", err: context.DeadlineExceeded, want: http.StatusGatewayTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &runtimeDiagnosticsTestService{fakeService: &fakeService{}, err: errors.Join(test.err, errors.New("secret daemon body"))}
			server := runtimeDiagnosticsTestServer(t, service)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, runtimeDiagnosticsRequest(""))
			if recorder.Code != test.want || service.calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "secret daemon body") {
				t.Fatal("raw service error leaked into response")
			}
		})
	}
}
