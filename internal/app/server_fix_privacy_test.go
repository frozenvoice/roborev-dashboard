package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/fixoperation"
	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

func TestFixBrowserDTOsRedactPrivatePathsExecutablesAndOutput(t *testing.T) {
	privateRoot := `C:\Users\private\source\secret-project`
	privateBinary := `C:\fake\very-secret\roborev.exe`
	privateStderr := "PRIVATE_FIX_STDERR"
	privateOutput := "PRIVATE_FIX_OUTPUT"
	now := time.Now().UTC()
	preview := &fixoperation.Preview{
		RepositoryIdentity: "opaque-repository-id",
		RepositoryPath:     privateRoot,
		StartingHead:       "head-before",
		StartingBranch:     "main",
		Target:             fixoperation.Target{JobID: 42, GitRef: "head-before", Branch: "main", JobType: "review"},
		Agent:              "codex",
		Command:            fixoperation.Command{Executable: privateBinary, Args: []string{"fix", "--agent", "codex", "--quiet", "42"}},
		PreviewToken:       "opaque-preview-token",
	}
	operation := &fixoperation.Operation{
		ID:                 "fix-42",
		RepositoryIdentity: preview.RepositoryIdentity,
		RepositoryPath:     privateRoot,
		TargetJobID:        42,
		Target:             preview.Target,
		Agent:              "codex",
		Phase:              fixoperation.StatusFailed,
		Status:             fixoperation.StatusFailed,
		Command:            preview.Command,
		StartingHead:       preview.StartingHead,
		StartingBranch:     preview.StartingBranch,
		StartedAt:          now,
		UpdatedAt:          now,
		ErrorCode:          fixoperation.ErrorCodeFixCommandFailed,
		Error:              privateStderr + " " + privateOutput + " " + privateRoot,
	}

	previewRaw, err := json.Marshal(toFixPreviewDTO(preview))
	if err != nil {
		t.Fatal(err)
	}
	operationRaw, err := json.Marshal(toFixOperationDTO(operation))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{previewRaw, operationRaw} {
		assertNoPrivateFixMarkers(t, raw, privateRoot, privateBinary, privateStderr, privateOutput)
	}
	if !strings.Contains(string(previewRaw), `"executable":"roborev"`) || !strings.Contains(string(operationRaw), `"executable":"roborev"`) {
		t.Fatalf("display command was not fixed to roborev: preview=%s operation=%s", previewRaw, operationRaw)
	}
	for _, raw := range [][]byte{previewRaw, operationRaw} {
		if strings.Contains(string(raw), `"repositoryPath"`) || strings.Contains(string(raw), `"error"`) {
			t.Fatalf("private Fix field crossed HTTP DTO boundary: %s", raw)
		}
	}

	// The projection must not mutate or erase the manager's private authority.
	if preview.RepositoryPath != privateRoot || preview.Command.Executable != privateBinary {
		t.Fatalf("preview manager authority changed by projection: %#v", preview)
	}
	if operation.RepositoryPath != privateRoot || operation.Command.Executable != privateBinary || operation.Error != privateStderr+" "+privateOutput+" "+privateRoot {
		t.Fatalf("operation manager authority changed by projection: %#v", operation)
	}
	if got := safeFixError(&fixoperation.Error{Kind: fixoperation.ErrorUnavailable, Err: errors.New(privateStderr + " " + privateOutput)}); strings.Contains(got, privateStderr) || strings.Contains(got, privateOutput) {
		t.Fatalf("raw Fix diagnostic crossed error boundary: %q", got)
	}
}

func assertNoPrivateFixMarkers(t *testing.T, raw []byte, markers ...string) {
	t.Helper()
	value := string(raw)
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			t.Fatalf("private Fix marker leaked in raw JSON: %q in %s", marker, raw)
		}
		encoded, err := json.Marshal(marker)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(value, string(encoded)) {
			t.Fatalf("private Fix marker leaked in JSON-escaped form: %q in %s", marker, raw)
		}
	}
}

type privacyFixService struct {
	fixHTTPService
	privateRoot   string
	privateBinary string
	fixStarted    chan struct{}
	startOnce     sync.Once
	block         <-chan struct{}
}

func (s *privacyFixService) BinaryPath() string { return s.privateBinary }

func (s *privacyFixService) TerminalJobMetadata(_ context.Context, id int64) (roborev.JobMetadata, error) {
	closed := false
	return roborev.JobMetadata{ID: id, RepoPath: s.privateRoot, GitRef: "abcdef123456", Branch: "main", JobType: "review", Status: "done", Verdict: "F", Closed: &closed}, nil
}

func (s *privacyFixService) Fix(ctx context.Context, _ roborev.FixRequest) error {
	s.started++
	if s.fixStarted != nil {
		s.startOnce.Do(func() { close(s.fixStarted) })
	}
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return errors.New("PRIVATE_FIX_STDERR PRIVATE_FIX_OUTPUT " + s.privateRoot + " " + s.privateBinary)
}

type privacyFixInspector struct {
	privateRoot string
}

func (i privacyFixInspector) Inspect(_ context.Context, _ string) (gitx.Repository, error) {
	return gitx.Repository{Name: "secret-project", Path: i.privateRoot, Branch: "main", Head: "abcdef123456", HeadShort: "abcdef12"}, nil
}

func TestFixEndpointsProjectPrivateManagerOperationThroughSanitizedDTO(t *testing.T) {
	privateRoot := `C:\Users\private\source\secret-project`
	privateBinary := `C:\fake\very-secret\roborev.exe`
	releaseFix := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFix) }) }
	service := &privacyFixService{privateRoot: privateRoot, privateBinary: privateBinary, fixStarted: make(chan struct{})}
	registry := NewRegistry(privacyFixInspector{privateRoot: privateRoot}, "")
	if _, err := registry.Add(context.Background(), privateRoot); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(&fakeService{}, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		release()
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	manager, err := fixoperation.NewManager(fixoperation.Dependencies{Registry: server.Registry, RoboRev: service, Binary: privateBinary, Gate: operation.NewGate()})
	if err != nil {
		t.Fatal(err)
	}
	server.Fixes = manager
	identity, err := server.Registry.Identity(privateRoot)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := manager.Prepare(context.Background(), fixoperation.Request{RepositoryIdentity: identity, TargetJobID: 42, Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := manager.Start(context.Background(), fixoperation.StartRequest{Request: fixoperation.Request{RepositoryIdentity: identity, TargetJobID: 42, Agent: "codex"}, PreviewToken: preview.PreviewToken})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-service.fixStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fake Fix to finish")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, ok := manager.Get(item.ID)
		if ok && current.CompletedAt != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	internal, ok := manager.Get(item.ID)
	if !ok || internal.CompletedAt == nil || internal.RepositoryPath != privateRoot || internal.Command.Executable != privateBinary {
		t.Fatalf("manager did not retain actual private authority: ok=%v operation=%#v", ok, internal)
	}
	if !strings.Contains(internal.Error, "PRIVATE_FIX_STDERR") || !strings.Contains(internal.Error, "PRIVATE_FIX_OUTPUT") {
		t.Fatalf("completed manager operation did not retain private diagnostics for projection test: %#v", internal)
	}

	previewRequest := httptest.NewRequest(http.MethodGet, "/api/fixes/preview?repositoryIdentity="+url.QueryEscape(identity)+"&targetJobId=42&agent=codex", nil)
	previewRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(previewRecorder, previewRequest)
	if previewRecorder.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%s", previewRecorder.Code, previewRecorder.Body.String())
	}
	listRequest := httptest.NewRequest(http.MethodGet, "/api/fixes?repositoryIdentity="+url.QueryEscape(identity), nil)
	listRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	detailRequest := httptest.NewRequest(http.MethodGet, "/api/fixes/"+url.PathEscape(item.ID), nil)
	detailRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(detailRecorder, detailRequest)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body=%s", detailRecorder.Code, detailRecorder.Body.String())
	}
	for name, raw := range map[string][]byte{"preview": previewRecorder.Body.Bytes(), "list": listRecorder.Body.Bytes(), "detail": detailRecorder.Body.Bytes()} {
		assertNoPrivateFixMarkers(t, raw, privateRoot, privateBinary, "PRIVATE_FIX_STDERR", "PRIVATE_FIX_OUTPUT")
		if strings.Contains(string(raw), `"repositoryPath"`) || strings.Contains(string(raw), `"error"`) {
			t.Fatalf("%s response exposes private Fix fields: %s", name, raw)
		}
	}
	if !strings.Contains(previewRecorder.Body.String(), `"executable":"roborev"`) || !strings.Contains(detailRecorder.Body.String(), `"executable":"roborev"`) {
		t.Fatalf("sanitized command executable missing: preview=%s detail=%s", previewRecorder.Body, detailRecorder.Body)
	}
	terminalCancelRequest := httptest.NewRequest(http.MethodPost, "/api/fixes/"+url.PathEscape(item.ID)+"/cancel", nil)
	terminalCancelRequest.Header.Set("X-CSRF-Token", server.csrfToken)
	terminalCancelRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(terminalCancelRecorder, terminalCancelRequest)
	if terminalCancelRecorder.Code != http.StatusConflict {
		t.Fatalf("completed cancel status = %d, body=%s", terminalCancelRecorder.Code, terminalCancelRecorder.Body.String())
	}

	// Use a fresh manager for a second operation, this time keeping Fix active
	// so the historical cancel contract is exercised without relying on the
	// timestamp-based operation IDs being distinct.
	service.block = releaseFix
	service.fixStarted = make(chan struct{})
	service.startOnce = sync.Once{}
	_ = manager.Shutdown(context.Background())
	activeManager, err := fixoperation.NewManager(fixoperation.Dependencies{Registry: server.Registry, RoboRev: service, Binary: privateBinary, Gate: operation.NewGate()})
	if err != nil {
		t.Fatal(err)
	}
	server.Fixes = activeManager
	preview, err = activeManager.Prepare(context.Background(), fixoperation.Request{RepositoryIdentity: identity, TargetJobID: 42, Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	active, err := activeManager.Start(context.Background(), fixoperation.StartRequest{Request: fixoperation.Request{RepositoryIdentity: identity, TargetJobID: 42, Agent: "codex"}, PreviewToken: preview.PreviewToken})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-service.fixStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fake Fix to become active")
	}
	activeInternal, ok := activeManager.Get(active.ID)
	if !ok || activeInternal.RepositoryPath != privateRoot || activeInternal.Command.Executable != privateBinary {
		t.Fatalf("active manager operation did not retain actual private authority: ok=%v operation=%#v", ok, activeInternal)
	}
	cancelRequest := httptest.NewRequest(http.MethodPost, "/api/fixes/"+url.PathEscape(active.ID)+"/cancel", nil)
	cancelRequest.Header.Set("X-CSRF-Token", server.csrfToken)
	cancelRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(cancelRecorder, cancelRequest)
	if cancelRecorder.Code != http.StatusAccepted {
		t.Fatalf("active cancel status = %d, body=%s", cancelRecorder.Code, cancelRecorder.Body.String())
	}
	assertNoPrivateFixMarkers(t, cancelRecorder.Body.Bytes(), privateRoot, privateBinary, "PRIVATE_FIX_STDERR", "PRIVATE_FIX_OUTPUT")
	if strings.Contains(cancelRecorder.Body.String(), `"repositoryPath"`) || strings.Contains(cancelRecorder.Body.String(), `"error"`) || !strings.Contains(cancelRecorder.Body.String(), `"executable":"roborev"`) {
		t.Fatalf("cancel response was not sanitized: %s", cancelRecorder.Body.String())
	}
	release()
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, ok := activeManager.Get(active.ID)
		if ok && current.CompletedAt != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	current, ok := activeManager.Get(active.ID)
	if !ok || current.CompletedAt == nil || current.Status != fixoperation.StatusCanceled {
		t.Fatalf("active cancel did not reach terminal canceled state: ok=%v operation=%#v", ok, current)
	}
	if service.started != 2 {
		t.Fatalf("internal manager Fix did not retain execution proof, calls=%d", service.started)
	}
}
