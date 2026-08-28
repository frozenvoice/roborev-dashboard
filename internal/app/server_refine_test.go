package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPanelRefineStartRequiresMutationAuthorization(t *testing.T) {
	server := setupServer(t, &fakeService{})
	body := `{"repoPath":"E:\\repo","panel":"dashboard_cross"}`
	request := httptest.NewRequest(http.MethodPost, "/api/panel-refines", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	server.AllowedHost = "dashboard.local"
	request = httptest.NewRequest(http.MethodPost, "/api/panel-refines", strings.NewReader(body))
	request.Host = "other.local"
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("invalid Host status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestPanelRefineStartIsLifecycleBlockedBeforeReviewOrFix(t *testing.T) {
	service := &fakeService{}
	server := setupServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/api/panel-refines", strings.NewReader(`{"repoPath":"E:\\repo","panel":"dashboard_cross","maxFixAttempts":2}`))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotImplemented || !strings.Contains(recorder.Body.String(), `"phase":"panel-refine-fix-lifecycle-blocked"`) {
		t.Fatalf("blocked start status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if service.reviewCalls != 0 || service.fixCalls != 0 || service.installationCalls != 0 || service.cancelCalls != 0 {
		t.Fatalf("blocked Panel Refine invoked service: review=%d fix=%d installation=%d cancel=%d", service.reviewCalls, service.fixCalls, service.installationCalls, service.cancelCalls)
	}
}

func TestPanelRefineStartRejectsQueryAndMalformedBodyBeforeBlockedTombstone(t *testing.T) {
	server := setupServer(t, &fakeService{})
	for _, test := range []struct {
		name string
		path string
		body string
		want string
	}{
		{name: "query", path: "/api/panel-refines?unexpected=1", body: `{"repoPath":"E:\\repo","panel":"dashboard_cross"}`, want: `"phase":"request"`},
		{name: "unknown-field", path: "/api/panel-refines", body: `{"repoPath":"E:\\repo","panel":"dashboard_cross","unexpected":true}`, want: `"phase":"request"`},
		{name: "missing-repository", path: "/api/panel-refines", body: `{"panel":"dashboard_cross"}`, want: `"phase":"request"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("X-CSRF-Token", server.csrfToken)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), test.want) {
				t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestPanelRefineHistoryAndCancelRoutesRemainReadOnly(t *testing.T) {
	server := setupServer(t, &fakeService{})
	list := httptest.NewRecorder()
	server.Handler().ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/panel-refines?repo=E%3A%5Crepo", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("history list status = %d, body=%s", list.Code, list.Body.String())
	}
	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/panel-refines/unknown", nil))
	if get.Code != http.StatusNotFound {
		t.Fatalf("history detail status = %d, body=%s", get.Code, get.Body.String())
	}
	cancel := httptest.NewRequest(http.MethodPost, "/api/panel-refines/unknown/cancel", nil)
	cancel.Header.Set("X-CSRF-Token", server.csrfToken)
	cancelRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(cancelRecorder, cancel)
	if cancelRecorder.Code != http.StatusNotFound {
		t.Fatalf("cancel status = %d, body=%s", cancelRecorder.Code, cancelRecorder.Body.String())
	}
	_ = server.Shutdown(context.Background())
}
