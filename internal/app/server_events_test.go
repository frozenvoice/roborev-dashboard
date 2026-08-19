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

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type eventService struct {
	*fakeService
	stream  *eventTestStream
	started chan struct{}
}

func (s *eventService) OpenStream(context.Context, string) (proc.Stream, error) {
	select {
	case <-s.started:
	default:
		close(s.started)
	}
	return s.stream, nil
}

type eventTestStream struct {
	lines chan []byte
	done  chan struct{}
	once  sync.Once
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
	request := httptest.NewRequest(http.MethodGet, "/api/events?repositoryIdentity="+url.QueryEscape(identity), nil)
	requestContext, cancel := context.WithCancel(request.Context())
	request = request.WithContext(requestContext)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { server.Handler().ServeHTTP(response, request); close(done) }()
	select {
	case <-service.started:
	case <-time.After(time.Second):
		t.Fatal("stream did not start")
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
		request := httptest.NewRequest(http.MethodGet, path, nil)
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
