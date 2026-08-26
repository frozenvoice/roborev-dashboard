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

type readinessHTTPService struct {
	*fakeService
	readiness roborev.AgentHookReadiness
	err       error
	calls     int
}

func (s *readinessHTTPService) AgentHookReadiness(context.Context) (roborev.AgentHookReadiness, error) {
	s.calls++
	return s.readiness, s.err
}

func strictReadinessRequest(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	return request
}

func readinessHTTPTestServer(t *testing.T, service *readinessHTTPService) *Server {
	t.Helper()
	server := setupServer(t, service.fakeService)
	server.RoboRev = service
	return server
}

func TestAgentHookReadinessEndpointReturnsSanitizedDTO(t *testing.T) {
	service := &readinessHTTPService{fakeService: &fakeService{}, readiness: roborev.AgentHookReadiness{
		Version: "v0.64.0",
		Agents: []roborev.AgentHookReadinessAgent{
			{Agent: "codex", Status: "current"},
			{Agent: "claude", Status: "needs-update"},
			{Agent: "grok", Status: "unavailable"},
			{Agent: "droid", Status: "current"},
		},
	}}
	server := readinessHTTPTestServer(t, service)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, strictReadinessRequest("/api/agent-hooks/readiness"))
	if recorder.Code != http.StatusOK || service.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["binary"]; ok || strings.Contains(recorder.Body.String(), "C:\\private") || strings.Contains(recorder.Body.String(), "SECRET") {
		t.Fatalf("readiness response leaked private data: %s", recorder.Body.String())
	}
	if got["version"] != "v0.64.0" || len(got["agents"].([]any)) != 4 {
		t.Fatalf("response=%#v", got)
	}
}

func TestAgentHookReadinessRejectsAuthAndQueryBeforeAdapter(t *testing.T) {
	service := &readinessHTTPService{fakeService: &fakeService{}, readiness: roborev.AgentHookReadiness{Version: "v0.64.0"}}
	server := readinessHTTPTestServer(t, service)

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/agent-hooks/readiness", nil))
	if recorder.Code != http.StatusForbidden || service.calls != 0 {
		t.Fatalf("missing auth status=%d calls=%d", recorder.Code, service.calls)
	}

	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, strictReadinessRequest("/api/agent-hooks/readiness?scope=evil"))
	if recorder.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("query status=%d calls=%d", recorder.Code, service.calls)
	}
}

func TestAgentHookReadinessEndpointOptionalAdapterAndGlobalErrors(t *testing.T) {
	server := setupServer(t, &fakeService{})
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, strictReadinessRequest("/api/agent-hooks/readiness"))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing adapter status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "binding", err: roborev.ErrBindingChanged, status: http.StatusServiceUnavailable},
		{name: "timeout", err: context.DeadlineExceeded, status: http.StatusGatewayTimeout},
		{name: "unexpected", err: errors.New("private failure marker"), status: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &readinessHTTPService{fakeService: &fakeService{}, err: test.err}
			server := readinessHTTPTestServer(t, service)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, strictReadinessRequest("/api/agent-hooks/readiness"))
			if recorder.Code != test.status || strings.Contains(recorder.Body.String(), "private failure marker") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
