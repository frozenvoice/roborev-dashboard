package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type reviewMetadataHTTPService struct {
	*fakeService
	result roborev.ReviewMetadataExportResult
	err    error
	calls  int
	paths  []string
}

func (s *reviewMetadataHTTPService) ReviewMetadataExport(_ context.Context, path string) (roborev.ReviewMetadataExportResult, error) {
	s.calls++
	s.paths = append(s.paths, path)
	return s.result, s.err
}

type reviewExportInspector struct {
	repository gitx.Repository
	err        error
	calls      int
}

func (i *reviewExportInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	i.calls++
	if i.err != nil {
		return gitx.Repository{}, i.err
	}
	repository := i.repository
	if repository.Path == "" {
		repository.Path = path
	}
	return repository, nil
}

func reviewMetadataExportServer(t *testing.T, service roborev.Service, inspector *reviewExportInspector, root string) *Server {
	t.Helper()
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func reviewMetadataExportRequest(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	return request
}

func TestReviewMetadataExportRouteScopesAndDownloadsValidatedBytes(t *testing.T) {
	root := `E:\selected root`
	inspector := &reviewExportInspector{repository: gitx.Repository{Name: "한글 / unsafe repository", Path: root}}
	service := &reviewMetadataHTTPService{fakeService: &fakeService{}, result: roborev.ReviewMetadataExportResult{Raw: []byte(`{"schema_version":1}`), Truncated: true}}
	server := reviewMetadataExportServer(t, service, inspector, root)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, reviewMetadataExportRequest("/api/exports/reviews?repositoryIdentity="+url.QueryEscape(gitx.RepositoryKey(root))))
	if recorder.Code != http.StatusOK || service.calls != 1 || string(recorder.Body.Bytes()) != `{"schema_version":1}` {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
	}
	if len(service.paths) != 1 || service.paths[0] != root {
		t.Fatalf("adapter received path=%v, want fresh root %q", service.paths, root)
	}
	if recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" || recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("X-RoboRev-Export-Truncated") != "true" {
		t.Fatalf("headers=%v", recorder.Header())
	}
	disposition := recorder.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disposition, `attachment; filename="roborev-reviews-`) || strings.ContainsAny(disposition, "\r\n한글") || strings.Contains(disposition, gitx.RepositoryKey(root)) {
		t.Fatalf("unsafe disposition=%q", disposition)
	}
}

func TestReviewMetadataExportRouteAcceptsNativeSameOriginAndRejectsForeignOrigin(t *testing.T) {
	root := `E:\selected root`
	inspector := &reviewExportInspector{repository: gitx.Repository{Name: "selected", Path: root}}
	service := &reviewMetadataHTTPService{fakeService: &fakeService{}, result: roborev.ReviewMetadataExportResult{Raw: []byte(`{}`)}}
	server := reviewMetadataExportServer(t, service, inspector, root)
	identity := url.QueryEscape(gitx.RepositoryKey(root))
	native := httptest.NewRequest(http.MethodGet, "/api/exports/reviews?repositoryIdentity="+identity, nil)
	native.Header.Set("Origin", "http://example.com")
	nativeRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(nativeRecorder, native)
	if nativeRecorder.Code != http.StatusOK || service.calls != 1 {
		t.Fatalf("native same-origin status=%d calls=%d", nativeRecorder.Code, service.calls)
	}
	foreign := httptest.NewRequest(http.MethodGet, "/api/exports/reviews?repositoryIdentity="+identity, nil)
	foreign.Header.Set("Origin", "http://evil.example")
	foreignRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(foreignRecorder, foreign)
	if foreignRecorder.Code != http.StatusForbidden || service.calls != 1 {
		t.Fatalf("foreign origin status=%d calls=%d", foreignRecorder.Code, service.calls)
	}
}

func TestReviewMetadataExportRouteRejectsBeforeAdapterSideEffects(t *testing.T) {
	root := `E:\selected root`
	inspector := &reviewExportInspector{repository: gitx.Repository{Name: "selected", Path: root}}
	service := &reviewMetadataHTTPService{fakeService: &fakeService{}, result: roborev.ReviewMetadataExportResult{Raw: []byte(`{}`)}}
	server := reviewMetadataExportServer(t, service, inspector, root)
	for _, test := range []struct {
		name   string
		target string
		want   int
	}{
		{name: "missing browser auth", target: "/api/exports/reviews?repositoryIdentity=" + url.QueryEscape(gitx.RepositoryKey(root)), want: http.StatusForbidden},
		{name: "extra query", target: "/api/exports/reviews?repositoryIdentity=" + url.QueryEscape(root) + "&extra=1", want: http.StatusBadRequest},
		{name: "duplicate query", target: "/api/exports/reviews?repositoryIdentity=" + url.QueryEscape(root) + "&repositoryIdentity=other", want: http.StatusBadRequest},
		{name: "foreign identity", target: "/api/exports/reviews?repositoryIdentity=foreign", want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			beforeCalls := service.calls
			beforeInspect := inspector.calls
			request := reviewMetadataExportRequest(test.target)
			if test.name == "missing browser auth" {
				request.Header = nil
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.want || service.calls != beforeCalls {
				t.Fatalf("status=%d want=%d service calls=%d before=%d", recorder.Code, test.want, service.calls, beforeCalls)
			}
			if test.name == "missing browser auth" && inspector.calls != beforeInspect {
				t.Fatalf("unauthorized request inspected repository: %d -> %d", beforeInspect, inspector.calls)
			}
		})
	}
}

func TestReviewMetadataExportRouteMapsFreshInspectionAndAdapterErrors(t *testing.T) {
	root := `E:\selected root`
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "root drift", err: errors.New("root drift"), want: http.StatusConflict},
		{name: "unsupported", err: roborev.ErrUnsupportedReviewMetadataExport, want: http.StatusServiceUnavailable},
		{name: "malformed", err: roborev.ErrMalformedReviewMetadataExport, want: http.StatusBadGateway},
		{name: "deadline", err: context.DeadlineExceeded, want: http.StatusGatewayTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			inspector := &reviewExportInspector{repository: gitx.Repository{Name: "selected", Path: root}}
			service := &reviewMetadataHTTPService{fakeService: &fakeService{}, err: test.err}
			server := reviewMetadataExportServer(t, service, inspector, root)
			if test.name == "root drift" {
				inspector.err = test.err
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, reviewMetadataExportRequest("/api/exports/reviews?repositoryIdentity="+url.QueryEscape(gitx.RepositoryKey(root))))
			if recorder.Code != test.want || service.calls != map[bool]int{true: 0, false: 1}[test.name == "root drift"] {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
			}
		})
	}
}
