package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type fakeInspector struct{}

func (fakeInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	return gitx.Repository{Name: "repo", Path: path, Branch: "main", Head: "abcdef123456", HeadShort: "abcdef12"}, nil
}

type fakeService struct {
	commentErr error
	closeCalls int
}

func (f *fakeService) Installation(context.Context) roborev.Installation {
	return roborev.Installation{Installed: true, Version: "v0.64.0"}
}
func (f *fakeService) List(context.Context, string, int) (json.RawMessage, error) {
	return json.RawMessage(`[]`), nil
}
func (f *fakeService) Show(context.Context, int64) (json.RawMessage, error) {
	return json.RawMessage(`{"id":42}`), nil
}
func (f *fakeService) PanelMembers(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"jobs":[]}`), nil
}
func (f *fakeService) Prompt(context.Context, int64) (string, error) { return "prompt", nil }
func (f *fakeService) Log(context.Context, int64) (string, error)    { return "log", nil }
func (f *fakeService) Review(context.Context, roborev.ReviewRequest) (json.RawMessage, error) {
	return json.RawMessage(`{"id":42}`), nil
}
func (f *fakeService) Rerun(context.Context, int64) error           { return nil }
func (f *fakeService) Comment(context.Context, int64, string) error { return f.commentErr }
func (f *fakeService) Close(context.Context, int64, bool) error     { f.closeCalls++; return nil }
func (f *fakeService) Cancel(context.Context, int64) error          { return nil }
func (f *fakeService) StartDaemon(context.Context) error            { return nil }
func (f *fakeService) ReviewCommand(request roborev.ReviewRequest) (roborev.CommandPreview, error) {
	return roborev.ReviewPreview("roborev", request)
}

func setupServer(t *testing.T, service *fakeService) *Server {
	t.Helper()
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestCommentCloseDoesNotCloseWhenCommentFails(t *testing.T) {
	service := &fakeService{commentErr: errors.New("comment rejected")}
	server := setupServer(t, service)
	request := httptest.NewRequest(http.MethodPost, "/api/reviews/42/comment-close", strings.NewReader(`{"comment":"details"}`))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", recorder.Code)
	}
	if service.closeCalls != 0 {
		t.Fatalf("close called after failed comment: %d", service.closeCalls)
	}
}

func TestMutationRequiresCSRF(t *testing.T) {
	server := setupServer(t, &fakeService{})
	request := httptest.NewRequest(http.MethodPost, "/api/reviews/42/cancel", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", recorder.Code)
	}
}
