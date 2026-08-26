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

type skillsReadinessHTTPService struct {
	*fakeService
	readiness roborev.SkillsReadiness
	err       error
	calls     int
}

func (s *skillsReadinessHTTPService) SkillsReadiness(context.Context) (roborev.SkillsReadiness, error) {
	s.calls++
	return s.readiness, s.err
}

func strictSkillsReadinessRequest(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	return request
}

func validSkillsReadinessDTO() roborev.SkillsReadiness {
	return roborev.SkillsReadiness{Version: "v0.64.0", Agents: []roborev.SkillsReadinessAgent{
		{Agent: "codex", Status: "current", Current: 10, Total: 10},
		{Agent: "claude", Status: "needs-install-update", Current: 8, Outdated: 1, Missing: 1, Total: 10},
		{Agent: "grok", Status: "config-unavailable", Total: 10},
		{Agent: "droid", Status: "current", Current: 10, Total: 10},
	}}
}

func skillsReadinessHTTPTestServer(t *testing.T, service *skillsReadinessHTTPService) *Server {
	t.Helper()
	server := setupServer(t, service.fakeService)
	server.RoboRev = service
	return server
}

func TestSkillsReadinessEndpointReturnsOnlyTypedAggregateDTO(t *testing.T) {
	service := &skillsReadinessHTTPService{fakeService: &fakeService{}, readiness: validSkillsReadinessDTO()}
	server := skillsReadinessHTTPTestServer(t, service)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, strictSkillsReadinessRequest("/api/skills/readiness"))
	if recorder.Code != http.StatusOK || service.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	for _, private := range []string{`C:\Users\private`, `C:\fake-home`, `C:\fake\very-secret\roborev.exe`, "PRIVATE_DESCRIPTION_MARKER", "raw stdout", "raw stderr", "SKILL.md"} {
		if strings.Contains(body, private) {
			t.Fatalf("response leaked %q: %s", private, body)
		}
	}
	if got["version"] != "v0.64.0" || len(got["agents"].([]any)) != 4 {
		t.Fatalf("response=%#v", got)
	}
	if _, ok := got["configPath"]; ok {
		t.Fatalf("private field leaked: %#v", got)
	}
}

func TestSkillsReadinessEndpointRejectsAuthAndQueryBeforeAdapter(t *testing.T) {
	service := &skillsReadinessHTTPService{fakeService: &fakeService{}, readiness: validSkillsReadinessDTO()}
	server := skillsReadinessHTTPTestServer(t, service)

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/skills/readiness", nil))
	if recorder.Code != http.StatusForbidden || service.calls != 0 {
		t.Fatalf("missing auth status=%d calls=%d", recorder.Code, service.calls)
	}

	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, strictSkillsReadinessRequest("/api/skills/readiness?scope=evil"))
	if recorder.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("query status=%d calls=%d", recorder.Code, service.calls)
	}
}

func TestSkillsReadinessEndpointOptionalAdapterAndSanitizedErrors(t *testing.T) {
	server := setupServer(t, &fakeService{})
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, strictSkillsReadinessRequest("/api/skills/readiness"))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing adapter status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	for _, test := range []struct {
		name   string
		err    error
		status int
		leak   string
	}{
		{name: "binding", err: roborev.ErrBindingChanged, status: http.StatusServiceUnavailable},
		{name: "timeout", err: context.DeadlineExceeded, status: http.StatusGatewayTimeout},
		{name: "malformed", err: roborev.ErrMalformedSkillsReadinessOutput, status: http.StatusBadGateway},
		{name: "unexpected", err: errors.New("private failure marker"), status: http.StatusBadGateway, leak: "private failure marker"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &skillsReadinessHTTPService{fakeService: &fakeService{}, err: test.err}
			server := skillsReadinessHTTPTestServer(t, service)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, strictSkillsReadinessRequest("/api/skills/readiness"))
			if recorder.Code != test.status || (test.leak != "" && strings.Contains(recorder.Body.String(), test.leak)) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestSkillsReadinessEndpointValidatesDTOInvariantsBeforeSerialization(t *testing.T) {
	invalid := validSkillsReadinessDTO()
	invalid.Agents[0].Current = 9
	service := &skillsReadinessHTTPService{fakeService: &fakeService{}, readiness: invalid}
	server := skillsReadinessHTTPTestServer(t, service)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, strictSkillsReadinessRequest("/api/skills/readiness"))
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "skills readiness response is invalid") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
