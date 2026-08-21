package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

type analysisHTTPService struct {
	fakeService
	typesCalls  int
	promptCalls int
}

func (f *analysisHTTPService) Installation(context.Context) roborev.Installation {
	installation := f.fakeService.Installation(context.Background())
	installation.Capabilities.AnalyzeIntrospection = true
	return installation
}

func (f *analysisHTTPService) AnalysisTypes(context.Context) ([]roborev.AnalysisType, error) {
	f.typesCalls++
	return []roborev.AnalysisType{{Name: "refactor", Description: "Suggest refactoring opportunities"}}, nil
}

func (f *analysisHTTPService) AnalysisPrompt(context.Context, string) (string, error) {
	f.promptCalls++
	return "prompt template", nil
}

func setupAnalysisServer(t *testing.T) (*Server, *analysisHTTPService) {
	t.Helper()
	service := &analysisHTTPService{}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	return server, service
}

func TestAnalysisReadRoutesRequireStrictBrowserAuthorizationBeforeService(t *testing.T) {
	routes := []struct {
		name    string
		target  string
		calls   func(*analysisHTTPService) int
		content string
	}{
		{name: "types", target: "/api/analyses/types", calls: func(service *analysisHTTPService) int { return service.typesCalls }, content: "refactor"},
		{name: "prompt", target: "/api/analyses/prompt?type=refactor", calls: func(service *analysisHTTPService) int { return service.promptCalls }, content: "prompt template"},
	}
	cases := []struct {
		name   string
		origin string
		native bool
		query  bool
	}{
		{name: "explicit exact origin", origin: "same"},
		{name: "native fetch metadata", native: true},
		{name: "foreign origin", origin: "http://evil.test"},
		{name: "null origin", origin: "null"},
		{name: "duplicate origin", origin: "duplicate"},
		{name: "missing fetch metadata"},
		{name: "cross site fetch metadata", native: true, origin: "cross-site"},
		{name: "unauthorized invalid query", query: true},
	}
	for _, route := range routes {
		for _, test := range cases {
			t.Run(route.name+"/"+test.name, func(t *testing.T) {
				server, service := setupAnalysisServer(t)
				target := route.target
				if test.query {
					separator := "?"
					if strings.Contains(target, "?") {
						separator = "&"
					}
					target += separator + "unexpected=x"
				}
				request := httptest.NewRequest(http.MethodGet, target, nil)
				switch {
				case test.origin == "same":
					request.Header.Set("Origin", "http://"+request.Host)
				case test.origin == "duplicate":
					request.Header.Add("Origin", "http://"+request.Host)
					request.Header.Add("Origin", "http://"+request.Host)
				case test.origin == "cross-site":
					request.Header.Set("Sec-Fetch-Site", "same-origin")
					request.Header.Set("Sec-Fetch-Mode", "cors")
					request.Header.Set("Sec-Fetch-Dest", "empty")
				case test.origin != "":
					request.Header.Set("Origin", test.origin)
				case test.native:
					request.Header.Set("Sec-Fetch-Site", "same-origin")
					request.Header.Set("Sec-Fetch-Mode", "cors")
					request.Header.Set("Sec-Fetch-Dest", "empty")
				}
				if test.origin == "cross-site" {
					request.Header.Set("Sec-Fetch-Site", "cross-site")
				}
				before := route.calls(service)
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, request)
				allowed := test.name == "explicit exact origin" || test.name == "native fetch metadata"
				if allowed {
					if response.Code != http.StatusOK || route.calls(service)-before != 1 || !strings.Contains(response.Body.String(), route.content) {
						t.Fatalf("status=%d calls delta=%d body=%s", response.Code, route.calls(service)-before, response.Body.String())
					}
					return
				}
				if response.Code != http.StatusForbidden || route.calls(service) != before {
					t.Fatalf("status=%d calls before=%d after=%d body=%s", response.Code, before, route.calls(service), response.Body.String())
				}
			})
		}
	}
}

func TestAnalysisExecutionRoutesAreExplicitDisabledTombstones(t *testing.T) {
	server, service := setupAnalysisServer(t)
	preview := httptest.NewRequest(http.MethodGet, "/api/analyses/preview?repositoryIdentity=foreign&type=refactor&scope=repository", nil)
	previewResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(previewResponse, preview)
	if previewResponse.Code != http.StatusNotImplemented || !strings.Contains(previewResponse.Body.String(), "analysis-execution-disabled") {
		t.Fatalf("preview status=%d body=%s", previewResponse.Code, previewResponse.Body.String())
	}

	post := httptest.NewRequest(http.MethodPost, "/api/analyses", strings.NewReader("not-json"))
	post.Header.Set("Origin", "http://"+post.Host)
	post.Header.Set("X-CSRF-Token", server.csrfToken)
	postResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(postResponse, post)
	if postResponse.Code != http.StatusNotImplemented || !strings.Contains(postResponse.Body.String(), "analysis-execution-disabled") {
		t.Fatalf("post status=%d body=%s", postResponse.Code, postResponse.Body.String())
	}
	if service.typesCalls != 0 || service.promptCalls != 0 {
		t.Fatalf("execution tombstones touched service: types=%d prompt=%d", service.typesCalls, service.promptCalls)
	}

	withoutCSRF := httptest.NewRequest(http.MethodPost, "/api/analyses", strings.NewReader("not-json"))
	withoutCSRF.Header.Set("Origin", "http://"+withoutCSRF.Host)
	withoutCSRFResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(withoutCSRFResponse, withoutCSRF)
	if withoutCSRFResponse.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", withoutCSRFResponse.Code)
	}
}
