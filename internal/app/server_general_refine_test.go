package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeneralRefineMutationsAreLifecycleBlockedWithoutServiceCalls(t *testing.T) {
	service := &fakeService{}
	server := setupServer(t, service)
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "preview", path: "/api/general-refines/preview", body: `{"repositoryIdentity":"repo"}`},
		{name: "start", path: "/api/general-refines", body: `{"previewToken":"opaque-token"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("X-CSRF-Token", server.csrfToken)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNotImplemented || !strings.Contains(recorder.Body.String(), `"phase":"general-refine-fix-lifecycle-blocked"`) {
				t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	if service.reviewCalls != 0 || service.fixCalls != 0 || service.installationCalls != 0 || service.cancelCalls != 0 {
		t.Fatalf("blocked General Refine invoked service: review=%d fix=%d installation=%d cancel=%d", service.reviewCalls, service.fixCalls, service.installationCalls, service.cancelCalls)
	}
}

func TestGeneralRefineMutationsKeepAuthAndExactRequestBoundaries(t *testing.T) {
	server := setupServer(t, &fakeService{})
	request := httptest.NewRequest(http.MethodPost, "/api/general-refines?unexpected=1", strings.NewReader(`{"previewToken":"token"}`))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"phase":"request"`) {
		t.Fatalf("query status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/general-refines", strings.NewReader(`{"previewToken":"token","extra":true}`))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"phase":"request"`) {
		t.Fatalf("unknown field status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/general-refines", strings.NewReader(`{"previewToken":"token"}`))
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGeneralRefineHistoryRoutesExposeNoNewRuns(t *testing.T) {
	server := setupServer(t, &fakeService{})
	identity, err := server.Registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/general-refines?repositoryIdentity="+identity, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"runs":[]`) {
		t.Fatalf("history list status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	get := httptest.NewRequest(http.MethodGet, "/api/general-refines/general-refine-invalid", nil)
	get.Header.Set("Sec-Fetch-Site", "same-origin")
	get.Header.Set("Sec-Fetch-Mode", "cors")
	get.Header.Set("Sec-Fetch-Dest", "empty")
	getRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(getRecorder, get)
	if getRecorder.Code != http.StatusNotFound {
		t.Fatalf("history detail status = %d, body=%s", getRecorder.Code, getRecorder.Body.String())
	}
}
