package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/streamrelay"
)

type eventService struct {
	*fakeService
	stream    *eventTestStream
	started   chan struct{}
	mu        sync.Mutex
	openCalls int
}

func (s *eventService) OpenStream(context.Context, string) (proc.Stream, error) {
	s.mu.Lock()
	s.openCalls++
	s.mu.Unlock()
	select {
	case <-s.started:
	default:
		close(s.started)
	}
	return s.stream, nil
}

func (s *eventService) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openCalls
}

type eventTestStream struct {
	lines chan []byte
	done  chan struct{}
	once  sync.Once
}

type flushNotifyingRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
	once    sync.Once
}

func (r *flushNotifyingRecorder) Flush() {
	r.ResponseRecorder.Flush()
	r.once.Do(func() { close(r.flushed) })
}

func newEventTestStream() *eventTestStream {
	return &eventTestStream{lines: make(chan []byte), done: make(chan struct{})}
}
func (s *eventTestStream) Lines() <-chan []byte { return s.lines }
func (s *eventTestStream) Wait() error          { <-s.done; return nil }
func (s *eventTestStream) Close() error {
	s.once.Do(func() { close(s.lines); close(s.done) })
	return nil
}

func nativeEventSourceRequest(method, target string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	return request
}

func newEventTestServer(t *testing.T) (*Server, *eventService) {
	t.Helper()
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	service := &eventService{fakeService: &fakeService{}, stream: newEventTestStream(), started: make(chan struct{})}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	return server, service
}

func TestEventsShutdownClosesActiveSSEPromptly(t *testing.T) {
	server, service := newEventTestServer(t)
	identity, err := server.Registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	request := nativeEventSourceRequest(http.MethodGet, "/api/events?repositoryIdentity="+url.QueryEscape(identity))
	requestContext, cancel := context.WithCancel(request.Context())
	request = request.WithContext(requestContext)
	response := &flushNotifyingRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
	done := make(chan struct{})
	go func() { server.Handler().ServeHTTP(response, request); close(done) }()
	select {
	case <-service.started:
	case <-time.After(time.Second):
		t.Fatal("stream did not start")
	}
	select {
	case <-response.flushed:
	case <-time.After(time.Second):
		t.Fatal("SSE response did not become active")
	}
	shutdownDone := make(chan struct{})
	go func() {
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = server.BeginShutdown(ctx)
		close(shutdownDone)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("active SSE did not close during shutdown")
	}
	if got := response.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("native EventSource content type=%q, want text/event-stream", got)
	}
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("stream shutdown did not complete")
	}
	cancel()
}

func TestEventsRejectsUnexpectedQueryShape(t *testing.T) {
	server, _ := newEventTestServer(t)
	for _, path := range []string{
		"/api/events?repositoryIdentity=repo&repositoryIdentity=repo",
		"/api/events?repositoryIdentity=repo&unexpected=x",
	} {
		request := nativeEventSourceRequest(http.MethodGet, path)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if err := server.Shutdown(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type countingEventInspector struct {
	mu    sync.Mutex
	calls int
}

func (i *countingEventInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	i.mu.Lock()
	i.calls++
	i.mu.Unlock()
	return gitx.Repository{Name: "repo", Path: path, Branch: "main", Head: "abcdef123456", HeadShort: "abcdef12"}, nil
}

func (i *countingEventInspector) Calls() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.calls
}

type countingEventRelay struct {
	delegate EventRelay
	mu       sync.Mutex
	calls    int
}

func (r *countingEventRelay) Subscribe(ctx context.Context, identity string) (<-chan streamrelay.Event, func(), error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return r.delegate.Subscribe(ctx, identity)
}

func (r *countingEventRelay) Shutdown(ctx context.Context) error {
	return r.delegate.Shutdown(ctx)
}

func (r *countingEventRelay) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestEventsRejectsForeignOrMalformedOriginBeforeRegistryOrSubscribe(t *testing.T) {
	inspector := &countingEventInspector{}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	baseline := inspector.Calls()
	service := &eventService{fakeService: &fakeService{}, stream: newEventTestStream(), started: make(chan struct{})}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	relay := &countingEventRelay{delegate: server.Events}
	server.Events = relay
	identity, err := registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"http://evil.test", "null", "not-an-origin", ""} {
		request := httptest.NewRequest(http.MethodGet, "/api/events?unexpected=x&repositoryIdentity="+url.QueryEscape(identity), nil)
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("origin=%q status=%d body=%s", origin, response.Code, response.Body.String())
		}
	}
	for _, headers := range []map[string]string{
		{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "empty"},
		{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors"},
		{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "empty"},
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/events?repositoryIdentity="+url.QueryEscape(identity), nil)
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("fetch metadata=%v status=%d body=%s", headers, response.Code, response.Body.String())
		}
	}
	if inspector.Calls() != baseline {
		t.Fatalf("registry inspection calls=%d, baseline=%d", inspector.Calls(), baseline)
	}
	if service.calls() != 0 {
		t.Fatalf("stream open calls=%d, want zero", service.calls())
	}
	if relay.Calls() != 0 {
		t.Fatalf("stream subscribe calls=%d, want zero", relay.Calls())
	}
	duplicate := httptest.NewRequest(http.MethodGet, "/api/events?repositoryIdentity="+url.QueryEscape(identity), nil)
	duplicate.Header.Add("Origin", "http://evil.test")
	duplicate.Header.Add("Origin", "http://evil.test")
	duplicateResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(duplicateResponse, duplicate)
	if duplicateResponse.Code != http.StatusForbidden {
		t.Fatalf("duplicate origin status=%d body=%s", duplicateResponse.Code, duplicateResponse.Body.String())
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEventsAllowsSameOriginWithoutCSRFAndRejectsUnauthorizedIdentity(t *testing.T) {
	server, service := newEventTestServer(t)
	identity, err := server.Registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/events?repositoryIdentity="+url.QueryEscape(identity), nil)
	request.Header.Set("Origin", "http://"+request.Host)
	requestContext, cancel := context.WithCancel(request.Context())
	request = request.WithContext(requestContext)
	response := &flushNotifyingRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
	done := make(chan struct{})
	go func() { server.Handler().ServeHTTP(response, request); close(done) }()
	select {
	case <-response.flushed:
	case <-time.After(time.Second):
		t.Fatal("same-origin stream did not flush connected response")
	}
	if response.Code != http.StatusOK {
		t.Fatalf("same-origin status=%d", response.Code)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("same-origin stream did not disconnect")
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	unauthorized := nativeEventSourceRequest(http.MethodGet, "/api/events?repositoryIdentity=not-registered")
	unauthorizedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status=%d", unauthorizedResponse.Code)
	}
	if service.calls() != 1 {
		t.Fatalf("unauthorized request opened stream; calls=%d", service.calls())
	}
}

func TestEventsUnavailableWhenServiceHasNoStreamSource(t *testing.T) {
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(&fakeService{}, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	request := nativeEventSourceRequest(http.MethodGet, "/api/events?repositoryIdentity="+url.QueryEscape(identity))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil event manager status=%d body=%s", response.Code, response.Body.String())
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
