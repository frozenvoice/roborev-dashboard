package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/analysis"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type analysisHTTPService struct {
	fakeService
	analysisCalls int
}

func (f *analysisHTTPService) AnalysisTypes(context.Context) ([]roborev.AnalysisType, error) {
	return []roborev.AnalysisType{{Name: "refactor", Description: "Suggest refactoring opportunities"}}, nil
}

func (f *analysisHTTPService) AnalysisPrompt(context.Context, string) (string, error) {
	return "prompt template", nil
}

func (f *analysisHTTPService) Analyze(_ context.Context, request roborev.AnalyzeRequest) (roborev.AnalysisReceipt, error) {
	f.analysisCalls++
	return roborev.AnalysisReceipt{Jobs: []roborev.AnalysisJob{{ID: 81, Agent: "codex"}}, AnalysisType: request.AnalysisType, Files: []string{"./..."}}, nil
}

func (f *analysisHTTPService) Installation(context.Context) roborev.Installation {
	return roborev.Installation{Installed: true, Version: "v0.64.0", Binary: `C:\tools\roborev.exe`, Status: json.RawMessage(`{"running":true,"known":true}`), Capabilities: roborev.Capabilities{AnalyzeCommand: true}}
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

func TestAnalysisRoutesRequireTypedInputsAndConfirmation(t *testing.T) {
	server, service := setupAnalysisServer(t)
	identity, err := server.Registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	typesRequest := httptest.NewRequest(http.MethodGet, "/api/analyses/types", nil)
	typesResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(typesResponse, typesRequest)
	if typesResponse.Code != http.StatusOK || !strings.Contains(typesResponse.Body.String(), "refactor") {
		t.Fatalf("types response = %d %s", typesResponse.Code, typesResponse.Body.String())
	}

	previewRequest := httptest.NewRequest(http.MethodGet, "/api/analyses/preview?repositoryIdentity="+url.QueryEscape(identity)+"&type=refactor&scope=repository", nil)
	previewResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview response = %d %s", previewResponse.Code, previewResponse.Body.String())
	}
	var preview struct {
		PreviewToken string `json:"previewToken"`
		Command      struct {
			Executable string   `json:"executable"`
			Args       []string `json:"args"`
		} `json:"command"`
	}
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &preview); err != nil || preview.PreviewToken == "" {
		t.Fatalf("preview = %#v, error = %v", preview, err)
	}
	if got := strings.Join(preview.Command.Args, " "); got != "analyze refactor ./... --json" {
		t.Fatalf("preview args = %q", got)
	}

	bodyBytes, err := json.Marshal(analysisBody{RepositoryIdentity: identity, AnalysisType: "refactor", Scope: "repository", PreviewToken: preview.PreviewToken})
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)
	withoutCSRF := httptest.NewRequest(http.MethodPost, "/api/analyses", strings.NewReader(body))
	withoutCSRFResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(withoutCSRFResponse, withoutCSRF)
	if withoutCSRFResponse.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF response = %d", withoutCSRFResponse.Code)
	}
	if service.analysisCalls != 0 {
		t.Fatal("confirmation was bypassed")
	}

	confirmed := httptest.NewRequest(http.MethodPost, "/api/analyses", strings.NewReader(body))
	confirmed.Header.Set("X-CSRF-Token", server.csrfToken)
	confirmedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(confirmedResponse, confirmed)
	if confirmedResponse.Code != http.StatusAccepted || service.analysisCalls != 1 || !strings.Contains(confirmedResponse.Body.String(), `"id":81`) {
		t.Fatalf("confirmed response = %d calls=%d %s", confirmedResponse.Code, service.analysisCalls, confirmedResponse.Body.String())
	}

	unknownBytes, err := json.Marshal(map[string]any{"repositoryIdentity": identity, "analysisType": "refactor", "scope": "repository", "previewToken": "x", "argv": []string{}})
	if err != nil {
		t.Fatal(err)
	}
	unknown := httptest.NewRequest(http.MethodPost, "/api/analyses", strings.NewReader(string(unknownBytes)))
	unknown.Header.Set("X-CSRF-Token", server.csrfToken)
	unknownResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown field response = %d", unknownResponse.Code)
	}
}

func TestAnalysisPreviewRejectsDuplicateAndUnknownQueryFields(t *testing.T) {
	server, _ := setupAnalysisServer(t)
	identity, err := server.Registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/analyses/preview?repositoryIdentity=" + url.QueryEscape(identity) + "&type=refactor&scope=repository"
	for _, raw := range []string{base + "&type=security", base + "&argv=analyze"} {
		request := httptest.NewRequest(http.MethodGet, raw, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("query %q returned %d, want 400", raw, response.Code)
		}
	}
}

func TestAnalysisErrorStatusMapsTypedKinds(t *testing.T) {
	for _, test := range []struct {
		kind analysis.ErrorKind
		want int
	}{
		{kind: analysis.ErrorInvalid, want: http.StatusBadRequest},
		{kind: analysis.ErrorForbidden, want: http.StatusForbidden},
		{kind: analysis.ErrorConflict, want: http.StatusConflict},
		{kind: analysis.ErrorUnavailable, want: http.StatusServiceUnavailable},
		{kind: analysis.ErrorAmbiguous, want: http.StatusBadGateway},
	} {
		if got := analysisErrorStatus(&analysis.Error{Kind: test.kind}); got != test.want {
			t.Fatalf("kind %s status = %d, want %d", test.kind, got, test.want)
		}
	}
}
