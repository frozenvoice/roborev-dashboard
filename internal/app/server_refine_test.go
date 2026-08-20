package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/refine"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type blockingAppRefineService struct {
	*fakeService
	started chan struct{}
}

type offlineAppRefineService struct{ *fakeService }

func (offlineAppRefineService) Installation(context.Context) roborev.Installation {
	return roborev.Installation{Installed: true, Version: "v0.64.0", Status: json.RawMessage(`{"running":false}`), Capabilities: roborev.Capabilities{PanelReview: true, FixCommand: true, JobOutputStatusAPI: true}}
}

func (s *blockingAppRefineService) JobOutputStatus(ctx context.Context, _ int64) (roborev.JobOutputStatus, error) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return roborev.JobOutputStatus{}, ctx.Err()
}

func newEndpointRefineManager(t *testing.T, server *Server, service refine.ReviewService) *refine.Manager {
	t.Helper()
	manager, err := refine.NewManager(refine.Dependencies{
		Registry:     server.Registry,
		Panels:       server.Panels,
		RoboRev:      service,
		Binary:       "roborev",
		PollInterval: time.Millisecond,
		Wait: func(ctx context.Context, _ time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Refines = manager
	return manager
}

func waitPanelRefineTerminal(t *testing.T, manager *refine.Manager, id string) *refine.PanelRefineRun {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if run, ok := manager.Get(id); ok {
			switch run.Phase {
			case refine.PhasePassed, refine.PhaseNeedsAttention, refine.PhaseFailed, refine.PhaseCancelled:
				return run
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("Panel Refine run %s did not become terminal", id)
	return nil
}

func TestPanelRefineEndpointsRequireCSRFOriginAndHost(t *testing.T) {
	server := setupServer(t, &fakeService{})
	body := `{"repoPath":"E:\\repo","panel":"dashboard_cross"}`
	request := httptest.NewRequest(http.MethodPost, "/api/panel-refines", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", recorder.Code)
	}

	server.AllowedHost = "dashboard.local"
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/panel-refines", strings.NewReader(body))
	request.Host = "other.local"
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("invalid Host status = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/panel-refines", strings.NewReader(body))
	request.Host = "dashboard.local"
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	request.Header.Set("Origin", "http://evil.local")
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("invalid Origin status = %d", recorder.Code)
	}
}

func TestPanelRefineStartGetCancelAndCompetingReviewConflict(t *testing.T) {
	service := &fakeService{}
	server := setupServer(t, service)
	blocking := &blockingAppRefineService{fakeService: service, started: make(chan struct{}, 1)}
	manager := newEndpointRefineManager(t, server, blocking)
	defer manager.Shutdown(context.Background())

	body := `{"repoPath":"E:\\repo","panel":"dashboard_cross","maxFixAttempts":2}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/panel-refines", strings.NewReader(body))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("start status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var started refine.PanelRefineRun
	if err := json.Unmarshal(recorder.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("review polling did not start")
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/panel-refines/"+started.ID, nil)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/panel-refines", strings.NewReader(body))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("same-repository start status = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/reviews", strings.NewReader(`{"repoPath":"E:\\repo","panel":"dashboard_cross"}`))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("competing Review HEAD status = %d: %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/panel-refines/"+started.ID+"/cancel", nil)
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d: %s", recorder.Code, recorder.Body.String())
	}
	result := waitPanelRefineTerminal(t, manager, started.ID)
	if result.Phase != refine.PhaseCancelled || result.CancelState == nil {
		t.Fatalf("cancel result = %#v", result)
	}
}

func TestRerunDifferentRepositoryIsIndependentOfActiveRefine(t *testing.T) {
	service := &fakeService{}
	server := setupServer(t, service)
	if _, err := server.Registry.Add(context.Background(), `E:\other`); err != nil {
		t.Fatal(err)
	}
	blocking := &blockingAppRefineService{fakeService: service, started: make(chan struct{}, 1)}
	manager := newEndpointRefineManager(t, server, blocking)
	defer manager.Shutdown(context.Background())

	body := `{"repoPath":"E:\\repo","panel":"dashboard_cross"}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/panel-refines", strings.NewReader(body))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("start status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var run refine.PanelRefineRun
	if err := json.Unmarshal(recorder.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("refine did not become active")
	}

	service.repoPath = `E:\other`
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/reviews/42/rerun", nil)
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("independent rerun status = %d: %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/reviews/42/rerun", nil)
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	service.repoPath = `E:\repo`
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("same-repository rerun status = %d: %s", recorder.Code, recorder.Body.String())
	}
	_ = manager.Cancel(run.ID)
	_ = waitPanelRefineTerminal(t, manager, run.ID)
}

func TestPanelRefineStartRejectsAttemptsAndUnknownPanel(t *testing.T) {
	server := setupServer(t, &fakeService{})
	service := &fakeService{}
	manager := newEndpointRefineManager(t, server, service)
	defer manager.Shutdown(context.Background())
	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{name: "attempts", body: `{"repoPath":"E:\\repo","panel":"dashboard_cross","maxFixAttempts":11}`, want: http.StatusBadRequest},
		{name: "panel", body: `{"repoPath":"E:\\repo","panel":"missing"}`, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/panel-refines", strings.NewReader(test.body))
			request.Header.Set("X-CSRF-Token", server.csrfToken)
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestPanelRefineStartMapsOfflineDaemonToServiceUnavailable(t *testing.T) {
	server := setupServer(t, &fakeService{})
	service := &offlineAppRefineService{fakeService: &fakeService{}}
	manager := newEndpointRefineManager(t, server, service)
	defer manager.Shutdown(context.Background())
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/panel-refines", strings.NewReader(`{"repoPath":"E:\\repo","panel":"dashboard_cross"}`))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("offline start status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPanelRefineStartStatusUsesTypedKindNotMessage(t *testing.T) {
	for _, test := range []struct {
		kind refine.StartErrorKind
		want int
	}{
		{kind: refine.StartInvalid, want: http.StatusBadRequest},
		{kind: refine.StartForbidden, want: http.StatusForbidden},
		{kind: refine.StartConflict, want: http.StatusConflict},
		{kind: refine.StartUnavailable, want: http.StatusServiceUnavailable},
		{kind: refine.StartInternal, want: http.StatusInternalServerError},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			if got := panelRefineStartStatus(&refine.StartError{Kind: test.kind, Err: errors.New("same detail")}); got != test.want {
				t.Fatalf("status=%d, want %d", got, test.want)
			}
		})
	}
	if got := panelRefineStartStatus(errors.New("invalid request")); got != http.StatusInternalServerError {
		t.Fatalf("untyped status=%d, want 500", got)
	}
}
