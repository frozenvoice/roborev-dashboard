package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type blockingLazyInspector struct {
	blocked bool
	started chan struct{}
}

func (i *blockingLazyInspector) Inspect(ctx context.Context, path string) (gitx.Repository, error) {
	if !i.blocked {
		return gitx.Repository{Path: path}, nil
	}
	select {
	case i.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return gitx.Repository{}, ctx.Err()
}

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
			if service.metadataCalls != 0 || service.authorizedPromptCalls != 0 || service.authorizedLogCalls != 0 || service.promptCalls != 0 || service.logCalls != 0 {
				t.Fatalf("strict auth side effects metadata=%d ownerPrompt=%d ownerLog=%d prompt=%d log=%d", service.metadataCalls, service.authorizedPromptCalls, service.authorizedLogCalls, service.promptCalls, service.logCalls)
			}
		})
	}
}

func TestLazyContentHandlersUseNarrowOwnerAndSortedRegistrySnapshot(t *testing.T) {
	service := &fakeService{}
	server := setupServer(t, service)
	if _, err := server.Registry.Add(context.Background(), `E:\repo-b`); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/reviews/42/prompt", nil)
	request.Header.Set("Origin", "http://"+request.Host)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.authorizedPromptCalls != 1 || service.authorizedLogCalls != 0 {
		t.Fatalf("narrow calls prompt=%d log=%d", service.authorizedPromptCalls, service.authorizedLogCalls)
	}
	want := []string{`E:\repo`, `E:\repo-b`}
	if len(service.authorizedPaths) != len(want) || service.authorizedPaths[0] != want[0] || service.authorizedPaths[1] != want[1] {
		t.Fatalf("authorized paths=%v, want sorted snapshot %v", service.authorizedPaths, want)
	}
}

func TestLazyContentHandlersNeverPassDriftedRegistryRoot(t *testing.T) {
	inspector := mappingInspector{repositories: map[string]gitx.Repository{}}
	registry := NewRegistry(inspector, "")
	for _, path := range []string{`E:\repo`, `E:\stale-repo`} {
		if _, err := registry.Add(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	inspector.repositories[`E:\stale-repo`] = gitx.Repository{Path: `E:\replacement-repo`}
	service := &fakeService{}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/reviews/42/prompt", nil)
	request.Header.Set("Origin", "http://"+request.Host)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(service.authorizedPaths) != 1 || service.authorizedPaths[0] != `E:\repo` {
		t.Fatalf("authorized paths=%v, want only fresh canonical root", service.authorizedPaths)
	}
}

func TestLazyContentHandlersDeadlineCoversFreshInspectionBeforeService(t *testing.T) {
	for _, path := range []string{"/api/reviews/42/prompt", "/api/reviews/42/log"} {
		t.Run(path, func(t *testing.T) {
			inspector := &blockingLazyInspector{started: make(chan struct{}, 1)}
			registry := NewRegistry(inspector, "")
			if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
				t.Fatal(err)
			}
			inspector.blocked = true
			service := &fakeService{}
			server, err := NewServer(service, registry, "")
			if err != nil {
				t.Fatal(err)
			}
			requestContext, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(requestContext)
			request.Header.Set("Origin", "http://"+request.Host)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			select {
			case <-inspector.started:
			default:
				t.Fatal("fresh registry inspection did not run")
			}
			if recorder.Code != http.StatusGatewayTimeout {
				t.Fatalf("status=%d body=%s, want gateway timeout", recorder.Code, recorder.Body.String())
			}
			if service.authorizedPromptCalls != 0 || service.authorizedLogCalls != 0 || service.promptCalls != 0 || service.logCalls != 0 {
				t.Fatalf("service calls authorized prompt/log=%d/%d content=%d/%d, want zero", service.authorizedPromptCalls, service.authorizedLogCalls, service.promptCalls, service.logCalls)
			}
		})
	}
}

func TestLazyContentHandlersParentCancellationWithNonCooperativeProbeSkipsService(t *testing.T) {
	for _, path := range []string{"/api/reviews/42/prompt", "/api/reviews/42/log"} {
		t.Run(path, func(t *testing.T) {
			inspector := &blockingRegistryInspector{started: make(chan string, 2), release: make(chan struct{})}
			t.Cleanup(inspector.Release)
			registry := NewRegistry(inspector, "")
			registry.paths[registryKey("blocked")] = "blocked"
			service := &fakeService{}
			server, err := NewServer(service, registry, "")
			if err != nil {
				t.Fatal(err)
			}
			requestContext, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(requestContext)
			request.Header.Set("Origin", "http://"+request.Host)
			recorder := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				server.Handler().ServeHTTP(recorder, request)
				close(done)
			}()
			select {
			case <-inspector.started:
			case <-time.After(time.Second):
				t.Fatal("non-cooperative probe did not start")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(500 * time.Millisecond):
				t.Fatal("handler did not return after parent cancellation")
			}
			if recorder.Code != http.StatusGatewayTimeout {
				t.Fatalf("status=%d body=%s, want gateway timeout", recorder.Code, recorder.Body.String())
			}
			if service.authorizedPromptCalls != 0 || service.authorizedLogCalls != 0 || service.promptCalls != 0 || service.logCalls != 0 {
				t.Fatalf("service calls prompt=%d log=%d authorizedPrompt=%d authorizedLog=%d, want zero", service.promptCalls, service.logCalls, service.authorizedPromptCalls, service.authorizedLogCalls)
			}
		})
	}
}

func TestLazyContentHandlersAllowCompleteWitnessWithIncompleteRegistryProbe(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
	}{
		{name: "prompt", path: "/api/reviews/42/prompt"},
		{name: "log", path: "/api/reviews/42/log"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inspector := mappingInspector{repositories: map[string]gitx.Repository{}, errors: map[string]error{}}
			registry := NewRegistry(inspector, "")
			for _, path := range []string{`E:\repo`, `E:\slow`} {
				if _, err := registry.Add(context.Background(), path); err != nil {
					t.Fatal(err)
				}
			}
			inspector.errors[`E:\slow`] = context.DeadlineExceeded
			service := &fakeService{}
			server, err := NewServer(service, registry, "")
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Origin", "http://"+request.Host)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s, want successful complete witness", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestLazyContentHandlersMapIncompleteNoMatchToUnavailable(t *testing.T) {
	inspector := mappingInspector{repositories: map[string]gitx.Repository{}, errors: map[string]error{}}
	registry := NewRegistry(inspector, "")
	for _, path := range []string{`E:\registered`, `E:\slow`} {
		if _, err := registry.Add(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	inspector.errors[`E:\slow`] = context.DeadlineExceeded
	service := &fakeService{repoPath: `E:\foreign`}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/reviews/42/prompt", nil)
	request.Header.Set("Origin", "http://"+request.Host)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s, want unavailable witness result", recorder.Code, recorder.Body.String())
	}
}

func TestLazyContentHandlersRequireNarrowAuthorizationService(t *testing.T) {
	base := &fakeService{}
	service := struct{ roborev.Service }{Service: base}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/reviews/42/prompt", nil)
	request.Header.Set("Origin", "http://"+request.Host)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503: %s", recorder.Code, recorder.Body.String())
	}
	if base.promptCalls != 0 || base.logCalls != 0 || base.metadataCalls != 0 {
		t.Fatalf("unsafe service calls prompt=%d log=%d metadata=%d", base.promptCalls, base.logCalls, base.metadataCalls)
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
