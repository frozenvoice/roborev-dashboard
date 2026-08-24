package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

func TestLazyContentHandlersAuthorizeAndRejectQueriesBeforeServiceRead(t *testing.T) {
	service := &fakeService{promptValue: "exact prompt", logValue: "exact log"}
	server := setupServer(t, service)
	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/api/reviews/42/prompt", want: http.StatusOK},
		{path: "/api/reviews/42/log", want: http.StatusOK},
		{path: "/api/reviews/42/prompt?repositoryIdentity=ignored", want: http.StatusBadRequest},
		{path: "/api/reviews/+42/log", want: http.StatusBadRequest},
		{path: "/api/reviews/0/prompt", want: http.StatusBadRequest},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Header.Set("Origin", "http://"+request.Host)
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != test.want {
			t.Fatalf("%s status = %d, want %d: %s", test.path, recorder.Code, test.want, recorder.Body.String())
		}
	}
	if service.promptCalls != 1 || service.logCalls != 1 {
		t.Fatalf("content calls prompt=%d log=%d, want one each", service.promptCalls, service.logCalls)
	}
	metadataRequest := httptest.NewRequest(http.MethodGet, "/api/reviews/42/prompt", nil)
	metadataRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	metadataRequest.Header.Set("Sec-Fetch-Mode", "cors")
	metadataRequest.Header.Set("Sec-Fetch-Dest", "empty")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, metadataRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("Fetch Metadata request status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
}

func TestLazyContentHandlersRejectForeignJobsBeforeContentService(t *testing.T) {
	service := &fakeService{repoPath: `E:\foreign`}
	server := setupServer(t, service)
	for _, path := range []string{"/api/reviews/42/prompt", "/api/reviews/42/log"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Origin", "http://"+request.Host)
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403", path, recorder.Code)
		}
	}
	if service.promptCalls != 0 || service.logCalls != 0 {
		t.Fatalf("foreign content calls prompt=%d log=%d, want zero", service.promptCalls, service.logCalls)
	}
}

func TestLazyContentHandlersRequireStrictBrowserReadBeforeAuthorization(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*http.Request)
	}{
		{name: "missing metadata", setup: func(_ *http.Request) {}},
		{name: "foreign origin", setup: func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }},
		{name: "duplicate origin", setup: func(r *http.Request) {
			r.Header.Set("Origin", "http://"+r.Host)
			r.Header.Add("Origin", "http://"+r.Host)
		}},
		{name: "malformed fetch metadata", setup: func(r *http.Request) {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			r.Header.Set("Sec-Fetch-Mode", "cors")
			r.Header.Set("Sec-Fetch-Dest", "empty")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeService{}
			server := setupServer(t, service)
			for _, path := range []string{"/api/reviews/42/prompt?unexpected=true", "/api/reviews/42/log?unexpected=true"} {
				request := httptest.NewRequest(http.MethodGet, path, nil)
				test.setup(request)
				recorder := httptest.NewRecorder()
				server.Handler().ServeHTTP(recorder, request)
				if recorder.Code != http.StatusForbidden {
					t.Fatalf("%s status = %d, want 403: %s", path, recorder.Code, recorder.Body.String())
				}
			}
			if service.metadataCalls != 0 || service.promptCalls != 0 || service.logCalls != 0 {
				t.Fatalf("strict auth side effects metadata=%d prompt=%d log=%d", service.metadataCalls, service.promptCalls, service.logCalls)
			}
		})
	}
}

func TestLazyContentAuthorizationRejectsMetadataIdentityMismatch(t *testing.T) {
	service := &fakeService{metadataID: 43}
	server := setupServer(t, service)
	request := httptest.NewRequest(http.MethodGet, "/api/reviews/42/log", nil)
	request.Header.Set("Origin", "http://"+request.Host)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway || service.logCalls != 0 {
		t.Fatalf("mismatch status=%d logCalls=%d body=%s", recorder.Code, service.logCalls, recorder.Body.String())
	}
}

func TestLazyContentAuthorizationMapsMetadataErrorsWithoutContentCall(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "absent", err: roborev.ErrJobMetadataNotFound, want: http.StatusNotFound},
		{name: "unsupported", err: roborev.ErrUnsupportedJobMetadata, want: http.StatusServiceUnavailable},
		{name: "offline", err: roborev.ErrDaemonOffline, want: http.StatusServiceUnavailable},
		{name: "binding", err: roborev.ErrBindingChanged, want: http.StatusServiceUnavailable},
		{name: "malformed", err: roborev.ErrMalformedJobMetadata, want: http.StatusBadGateway},
		{name: "deadline", err: context.DeadlineExceeded, want: http.StatusGatewayTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeService{metadataErr: test.err}
			server := setupServer(t, service)
			request := httptest.NewRequest(http.MethodGet, "/api/reviews/42/prompt", nil)
			request.Header.Set("Origin", "http://"+request.Host)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.want || service.promptCalls != 0 {
				t.Fatalf("status=%d want=%d promptCalls=%d body=%s", recorder.Code, test.want, service.promptCalls, recorder.Body.String())
			}
		})
	}
}

func TestLazyContentHandlersMapTypedFailuresWithoutDetails(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		err  error
		want int
		leak string
	}{
		{name: "unsupported prompt", path: "/api/reviews/42/prompt", err: roborev.ErrLazyContentUnsupported, want: http.StatusServiceUnavailable, leak: "upstream secret"},
		{name: "offline log", path: "/api/reviews/42/log", err: roborev.ErrLazyContentOffline, want: http.StatusServiceUnavailable, leak: "filesystem path"},
		{name: "daemon offline prompt", path: "/api/reviews/42/prompt", err: roborev.ErrDaemonOffline, want: http.StatusServiceUnavailable, leak: "daemon transport"},
		{name: "not found prompt", path: "/api/reviews/42/prompt", err: roborev.ErrLazyContentNotFound, want: http.StatusNotFound, leak: "prompt body"},
		{name: "malformed log", path: "/api/reviews/42/log", err: roborev.ErrLazyContentMalformed, want: http.StatusBadGateway, leak: "raw daemon"},
		{name: "deadline prompt", path: "/api/reviews/42/prompt", err: context.DeadlineExceeded, want: http.StatusGatewayTimeout, leak: "deadline"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeService{}
			if test.path[len("/api/reviews/42/"):] == "prompt" {
				service.promptErr = errors.Join(test.err, errors.New(test.leak))
			} else {
				service.logErr = errors.Join(test.err, errors.New(test.leak))
			}
			server := setupServer(t, service)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Origin", "http://"+request.Host)
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
			if body := recorder.Body.String(); body == "" || containsString(body, test.leak) {
				t.Fatalf("response leaked detail: %s", body)
			}
		})
	}
}

func containsString(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
