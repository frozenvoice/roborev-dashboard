package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

type fixHTTPService struct {
	fakeService
	started int
	block   <-chan struct{}
}

func (f *fixHTTPService) Fix(ctx context.Context, _ roborev.FixRequest) error {
	f.started++
	if f.block != nil {
		select {
		case <-f.block:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (f *fixHTTPService) TerminalJobMetadata(_ context.Context, id int64) (roborev.JobMetadata, error) {
	closed := false
	return roborev.JobMetadata{ID: id, RepoPath: `E:\repo`, GitRef: "abcdef123456", Branch: "main", JobType: "review", Status: "done", Verdict: "F", Closed: &closed}, nil
}

func TestFixPreviewEndpointUsesBackendProjectionAndCSRFProtectsStart(t *testing.T) {
	service := &fixHTTPService{block: make(chan struct{})}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(`E:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	previewRequest := httptest.NewRequest(http.MethodGet, "/api/fixes/preview?repositoryIdentity="+url.QueryEscape(identity)+"&targetJobId=42&agent=codex", nil)
	previewRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(previewRecorder, previewRequest)
	if previewRecorder.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%s", previewRecorder.Code, previewRecorder.Body.String())
	}
	var preview struct {
		Target struct {
			JobID int64 `json:"jobId"`
		} `json:"target"`
		PreviewToken string `json:"previewToken"`
	}
	if err := json.Unmarshal(previewRecorder.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Target.JobID != 42 || strings.TrimSpace(preview.PreviewToken) == "" || service.started != 0 {
		t.Fatalf("preview projection = %#v", preview)
	}
	missingList := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingList, httptest.NewRequest(http.MethodGet, "/api/fixes", nil))
	if missingList.Code != http.StatusBadRequest {
		t.Fatalf("missing identity list status = %d", missingList.Code)
	}
	unknownGet := httptest.NewRecorder()
	server.Handler().ServeHTTP(unknownGet, httptest.NewRequest(http.MethodGet, "/api/fixes/unknown", nil))
	if unknownGet.Code != http.StatusNotFound {
		t.Fatalf("unknown get status = %d", unknownGet.Code)
	}
	body, _ := json.Marshal(map[string]any{"repositoryIdentity": identity, "targetJobId": 42, "agent": "codex", "previewToken": preview.PreviewToken})
	releaseRefine, err := server.Gate.AcquireRefine()
	if err != nil {
		t.Fatal(err)
	}
	blockedByRefine := httptest.NewRequest(http.MethodPost, "/api/fixes", strings.NewReader(string(body)))
	blockedByRefine.Header.Set("X-CSRF-Token", server.csrfToken)
	blockedRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(blockedRecorder, blockedByRefine)
	if blockedRecorder.Code != http.StatusConflict {
		t.Fatalf("fix during Panel Refine status = %d", blockedRecorder.Code)
	}
	releaseRefine()
	releaseReview, err := server.Gate.AcquireMutation()
	if err != nil {
		t.Fatal(err)
	}
	blockedByReview := httptest.NewRequest(http.MethodPost, "/api/fixes", strings.NewReader(string(body)))
	blockedByReview.Header.Set("X-CSRF-Token", server.csrfToken)
	blockedReviewRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(blockedReviewRecorder, blockedByReview)
	if blockedReviewRecorder.Code != http.StatusConflict {
		t.Fatalf("fix during review mutation status = %d", blockedReviewRecorder.Code)
	}
	releaseReview()
	post := httptest.NewRequest(http.MethodPost, "/api/fixes", strings.NewReader(string(body)))
	withoutCSRF := httptest.NewRecorder()
	server.Handler().ServeHTTP(withoutCSRF, post)
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", withoutCSRF.Code)
	}
	post = httptest.NewRequest(http.MethodPost, "/api/fixes", strings.NewReader(string(body)))
	post.Header.Set("X-CSRF-Token", server.csrfToken)
	withCSRF := httptest.NewRecorder()
	server.Handler().ServeHTTP(withCSRF, post)
	if withCSRF.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, body=%s", withCSRF.Code, withCSRF.Body.String())
	}
	var operation struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(withCSRF.Body.Bytes(), &operation); err != nil || operation.ID == "" {
		t.Fatalf("operation response = %s", withCSRF.Body.String())
	}
	list := httptest.NewRecorder()
	server.Handler().ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/fixes?repositoryIdentity="+url.QueryEscape(identity), nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), operation.ID) {
		t.Fatalf("list status/body = %d/%s", list.Code, list.Body.String())
	}
	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/fixes/"+url.PathEscape(operation.ID), nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status/body = %d/%s", get.Code, get.Body.String())
	}
	cancelRequest := httptest.NewRequest(http.MethodPost, "/api/fixes/"+url.PathEscape(operation.ID)+"/cancel", nil)
	withoutCancelCSRF := httptest.NewRecorder()
	server.Handler().ServeHTTP(withoutCancelCSRF, cancelRequest)
	if withoutCancelCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing cancel CSRF status = %d", withoutCancelCSRF.Code)
	}
	cancelRequest = httptest.NewRequest(http.MethodPost, "/api/fixes/"+url.PathEscape(operation.ID)+"/cancel", nil)
	cancelRequest.Header.Set("X-CSRF-Token", server.csrfToken)
	cancelRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(cancelRecorder, cancelRequest)
	if cancelRecorder.Code != http.StatusAccepted {
		t.Fatalf("cancel status/body = %d/%s", cancelRecorder.Code, cancelRecorder.Body.String())
	}
	shutdown, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdown)
}
