package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/snapshotignoremanagement"
)

type snapshotIgnoreTestInspector struct {
	root        string
	state       gitx.SnapshotIgnoreRepositoryState
	sequence    []gitx.SnapshotIgnoreRepositoryState
	ignoreAfter int
	calls       int
	beginCalls  int
	finishCalls int
	events      []string
	lastState   gitx.SnapshotIgnoreRepositoryState
	inspectErr  error
}

func (i *snapshotIgnoreTestInspector) Inspect(_ context.Context, path string) (gitx.Repository, error) {
	if i.inspectErr != nil {
		return gitx.Repository{}, i.inspectErr
	}
	return gitx.Repository{Path: path, Branch: "main", Head: i.state.Head}, nil
}
func (i *snapshotIgnoreTestInspector) InspectSnapshotIgnoreThroughTracked(_ context.Context, path, _ string) (gitx.SnapshotIgnoreRepositoryState, gitx.SnapshotIgnoreContinuation, error) {
	i.calls++
	i.beginCalls++
	i.events = append(i.events, "begin")
	state := i.state
	if len(i.sequence) > 0 {
		index := i.calls - 1
		if index >= len(i.sequence) {
			index = len(i.sequence) - 1
		}
		state = i.sequence[index]
	}
	if state.Root == "" {
		state.Root = path
	}
	if state.Branch == "" && !state.Detached {
		state.Branch = "main"
	}
	ignoreAfter := i.ignoreAfter
	if ignoreAfter == 0 {
		ignoreAfter = 3
	}
	if ignoreAfter > 0 && i.calls >= ignoreAfter {
		state.Ignored = true
	}
	i.lastState = state
	return state, gitx.SnapshotIgnoreContinuation{}, nil
}
func (i *snapshotIgnoreTestInspector) FinishSnapshotIgnoreInspection(_ context.Context, _ gitx.SnapshotIgnoreContinuation) (gitx.SnapshotIgnoreRepositoryState, error) {
	i.finishCalls++
	i.events = append(i.events, "finish")
	return i.lastState, nil
}

type snapshotIgnoreTestService struct {
	fakeService
	snapshotDir string
	snapshotErr error
}

func (s snapshotIgnoreTestService) SnapshotDirectory(context.Context, string) (string, error) {
	if s.snapshotErr != nil {
		return "", s.snapshotErr
	}
	if s.snapshotDir != "" {
		return s.snapshotDir, nil
	}
	return ".roborev", nil
}

var snapshotIgnoreAliasPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func snapshotIgnoreHTTPServer(t *testing.T, inspector *snapshotIgnoreTestInspector, service *snapshotIgnoreTestService) (*Server, string, string) {
	t.Helper()
	root := inspector.root
	if root == "" {
		root = t.TempDir()
		inspector.root = root
	}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithSetup(service, registry, "", operation.NewGate(), nil)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(root)
	if err != nil {
		t.Fatal(err)
	}
	return server, identity, server.snapshotIgnoreAlias(root)
}

func snapshotIgnoreRequest(server *Server, method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Sec-Fetch-Mode", "cors")
	r.Header.Set("Sec-Fetch-Dest", "empty")
	r.Header.Set("X-CSRF-Token", server.csrfToken)
	return r
}

func snapshotIgnoreResponseToken(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("response JSON: %v (%s)", err, response.Body.String())
	}
	token, _ := body["token"].(string)
	return token
}

func snapshotIgnorePreviewToken(t *testing.T, server *Server, alias string) string {
	t.Helper()
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", `{"repositoryIdentity":"`+alias+`"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", response.Code, response.Body.String())
	}
	token := snapshotIgnoreResponseToken(t, response)
	if token == "" {
		t.Fatal("preview returned empty token")
	}
	return token
}

func snapshotIgnoreGitignore(t *testing.T, root string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSnapshotIgnoreHTTPPreviewAndExecuteHaveExactWireShapes(t *testing.T) {
	root := t.TempDir()
	inspector := &snapshotIgnoreTestInspector{root: root, state: gitx.SnapshotIgnoreRepositoryState{Root: root, Branch: "main", Head: strings.Repeat("a", 40)}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	testService := &snapshotIgnoreTestService{}
	service, err := NewServer(testService, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	alias := service.snapshotIgnoreAlias(root)
	request := func(path, body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("Sec-Fetch-Mode", "cors")
		r.Header.Set("Sec-Fetch-Dest", "empty")
		r.Header.Set("X-CSRF-Token", service.csrfToken)
		return r
	}
	preview := httptest.NewRecorder()
	service.Handler().ServeHTTP(preview, request("/api/snapshot-ignore/preview", `{"repositoryIdentity":"`+alias+`"}`))
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var dto map[string]any
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto) != 7 {
		t.Fatalf("preview key count=%d dto=%#v", len(dto), dto)
	}
	if value, _ := dto["target"].(string); value != ".gitignore" {
		t.Fatalf("target=%q", value)
	}
	if value, _ := dto["action"].(string); value != "append-snapshot-ignore" {
		t.Fatalf("action=%q", value)
	}
	if value, _ := dto["repositoryIdentity"].(string); value != alias {
		t.Fatalf("identity=%q alias=%q", value, alias)
	}
	token, ok := dto["token"].(string)
	if !ok || token == "" {
		t.Fatalf("token=%#v", dto["token"])
	}
	execute := httptest.NewRecorder()
	service.Handler().ServeHTTP(execute, request("/api/snapshot-ignore/execute", `{"token":"`+token+`"}`))
	if execute.Code != http.StatusOK || execute.Body.String() != `{"status":"verified-success"}`+"\n" {
		t.Fatalf("execute status=%d body=%q", execute.Code, execute.Body.String())
	}
	second := httptest.NewRecorder()
	service.Handler().ServeHTTP(second, request("/api/snapshot-ignore/execute", `{"token":"`+token+`"}`))
	if second.Code != http.StatusConflict {
		t.Fatalf("replay status=%d body=%s", second.Code, second.Body.String())
	}
}

func TestSnapshotIgnorePreviewSeparatesTrackedShortCircuitAndPatternFinish(t *testing.T) {
	t.Run("tracked preview does not finish", func(t *testing.T) {
		root := t.TempDir()
		inspector := &snapshotIgnoreTestInspector{
			root: root,
			state: gitx.SnapshotIgnoreRepositoryState{
				Root: root, Branch: "main", Head: strings.Repeat("a", 40), Tracked: true,
			},
			ignoreAfter: -1,
		}
		service := &snapshotIgnoreTestService{}
		server, _, alias := snapshotIgnoreHTTPServer(t, inspector, service)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", `{"repositoryIdentity":"`+alias+`"}`))
		if response.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if inspector.beginCalls != 1 || inspector.finishCalls != 0 {
			t.Fatalf("tracked stage calls begin=%d finish=%d", inspector.beginCalls, inspector.finishCalls)
		}
		if strings.Join(inspector.events, ",") != "begin" {
			t.Fatalf("tracked stage order=%v", inspector.events)
		}
	})

	t.Run("normal preview finishes after begin", func(t *testing.T) {
		root := t.TempDir()
		inspector := &snapshotIgnoreTestInspector{
			root: root,
			state: gitx.SnapshotIgnoreRepositoryState{
				Root: root, Branch: "main", Head: strings.Repeat("b", 40),
			},
			ignoreAfter: -1,
		}
		service := &snapshotIgnoreTestService{}
		server, _, alias := snapshotIgnoreHTTPServer(t, inspector, service)
		response := httptest.NewRecorder()
		body := `{"repositoryIdentity":"` + alias + `"}`
		server.Handler().ServeHTTP(response, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", body))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if inspector.beginCalls != 1 || inspector.finishCalls != 1 {
			t.Fatalf("normal stage calls begin=%d finish=%d", inspector.beginCalls, inspector.finishCalls)
		}
		if strings.Join(inspector.events, ",") != "begin,finish" {
			t.Fatalf("normal stage order=%v", inspector.events)
		}
	})
}

func TestSnapshotIgnoreHTTPRejectsUnauthorizedMalformedAndUnknownRequestsBeforeProbes(t *testing.T) {
	root := t.TempDir()
	inspector := &snapshotIgnoreTestInspector{root: root, state: gitx.SnapshotIgnoreRepositoryState{Head: strings.Repeat("a", 40)}, ignoreAfter: -1}
	service := &snapshotIgnoreTestService{}
	server, _, alias := snapshotIgnoreHTTPServer(t, inspector, service)
	inspector.calls = 0
	requestBody := `{"repositoryIdentity":"` + alias + `"}`
	tests := []struct {
		name   string
		build  func() *http.Request
		status int
	}{
		{name: "missing csrf", build: func() *http.Request {
			r := snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", requestBody)
			r.Header.Del("X-CSRF-Token")
			return r
		}, status: http.StatusForbidden},
		{name: "duplicate csrf", build: func() *http.Request {
			r := snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", requestBody)
			r.Header.Add("X-CSRF-Token", server.csrfToken)
			return r
		}, status: http.StatusForbidden},
		{name: "foreign origin", build: func() *http.Request {
			r := snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", requestBody)
			r.Header.Set("Origin", "https://evil.example")
			return r
		}, status: http.StatusForbidden},
		{name: "query", build: func() *http.Request {
			return snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview?unexpected=1", requestBody)
		}, status: http.StatusBadRequest},
		{name: "missing body field", build: func() *http.Request {
			return snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", `{}`)
		}, status: http.StatusBadRequest},
		{name: "unknown body field", build: func() *http.Request {
			return snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", `{"repositoryIdentity":"`+alias+`","path":"PRIVATE_PATH_MARKER"}`)
		}, status: http.StatusBadRequest},
		{name: "duplicate body field", build: func() *http.Request {
			return snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", `{"repositoryIdentity":"`+alias+`","repositoryIdentity":"other"}`)
		}, status: http.StatusBadRequest},
		{name: "trailing body", build: func() *http.Request {
			return snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", requestBody+` {}`)
		}, status: http.StatusBadRequest},
		{name: "unknown alias", build: func() *http.Request {
			return snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", `{"repositoryIdentity":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`)
		}, status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, test.build())
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "PRIVATE_PATH_MARKER") || strings.Contains(response.Body.String(), root) {
				t.Fatalf("private marker leaked: %s", response.Body.String())
			}
		})
	}
	if inspector.calls != 0 {
		t.Fatalf("snapshot Git probes=%d want zero", inspector.calls)
	}
	if got := snapshotIgnoreGitignore(t, root); got != nil {
		t.Fatalf("malformed requests created .gitignore: %q", got)
	}
}

func TestSnapshotIgnoreHTTPPreviewBlocksUnsafeStatesWithoutTokenOrMutation(t *testing.T) {
	privateMarkers := []string{"PRIVATE_CONFIG_MARKER", "PRIVATE_STDERR_MARKER", `C:\private\secret-repo`}
	tests := []struct {
		name      string
		state     gitx.SnapshotIgnoreRepositoryState
		service   snapshotIgnoreTestService
		setup     func(t *testing.T, root string)
		wantError string
	}{
		{name: "tracked", state: gitx.SnapshotIgnoreRepositoryState{Tracked: true}, wantError: "Tracked files exist under the snapshot directory."},
		{name: "already ignored", state: gitx.SnapshotIgnoreRepositoryState{Ignored: true}, wantError: "snapshot directory is already ignored"},
		{name: "dirty", state: gitx.SnapshotIgnoreRepositoryState{Dirty: true}},
		{name: "detached", state: gitx.SnapshotIgnoreRepositoryState{Detached: true, Branch: "detached"}},
		{name: "configuration error", service: snapshotIgnoreTestService{snapshotErr: errors.New(strings.Join(privateMarkers, " "))}},
		{name: "unsafe config path", service: snapshotIgnoreTestService{snapshotDir: "../escape"}},
		{name: "unsafe snapshot directory", setup: func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, ".roborev"), []byte("not-a-directory"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "oversize gitignore", setup: func(t *testing.T, root string) {
			data := bytes.Repeat([]byte("x"), int(snapshotignoremanagement.MaxBytes+1))
			if err := os.WriteFile(filepath.Join(root, ".gitignore"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			state := test.state
			state.Head = strings.Repeat("b", 40)
			inspector := &snapshotIgnoreTestInspector{root: root, state: state, ignoreAfter: -1}
			service := test.service
			server, _, alias := snapshotIgnoreHTTPServer(t, inspector, &service)
			if test.setup != nil {
				test.setup(t, root)
			}
			before := snapshotIgnoreGitignore(t, root)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", `{"repositoryIdentity":"`+alias+`"}`))
			if response.Code != http.StatusConflict {
				t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusConflict, response.Body.String())
			}
			if token := snapshotIgnoreResponseToken(t, response); token != "" {
				t.Fatalf("blocked preview returned token %q", token)
			}
			if test.wantError != "" && !strings.Contains(response.Body.String(), test.wantError) {
				t.Fatalf("body=%s missing exact sanitized error %q", response.Body.String(), test.wantError)
			}
			for _, marker := range privateMarkers {
				if strings.Contains(response.Body.String(), marker) {
					t.Fatalf("private marker %q leaked: %s", marker, response.Body.String())
				}
			}
			if after := snapshotIgnoreGitignore(t, root); !bytes.Equal(after, before) {
				t.Fatalf("blocked preview mutated .gitignore")
			}
		})
	}
}

func TestSnapshotIgnoreHTTPPreviewRequiresCurrentlySelectedRepository(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	inspector := &snapshotIgnoreTestInspector{root: rootA, state: gitx.SnapshotIgnoreRepositoryState{Head: strings.Repeat("c", 40)}, ignoreAfter: -1}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), rootA); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(context.Background(), rootB); err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithSetup(&snapshotIgnoreTestService{}, registry, "", operation.NewGate(), nil)
	if err != nil {
		t.Fatal(err)
	}
	aliasB := server.snapshotIgnoreAlias(rootB)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", `{"repositoryIdentity":"`+aliasB+`"}`))
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-selected repository status=%d body=%s", response.Code, response.Body.String())
	}
	if inspector.calls != 0 {
		t.Fatalf("non-selected repository triggered Git probes=%d", inspector.calls)
	}
}

func TestSnapshotIgnoreHTTPPreviewReturnsPathFreeAliasAndExactDTO(t *testing.T) {
	root := t.TempDir()
	privateConfig := `C:\Users\private\snapshot-config`
	inspector := &snapshotIgnoreTestInspector{root: root, state: gitx.SnapshotIgnoreRepositoryState{Head: strings.Repeat("d", 40)}, ignoreAfter: -1}
	service := &snapshotIgnoreTestService{snapshotDir: ".roborev"}
	server, legacy, alias := snapshotIgnoreHTTPServer(t, inspector, service)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/preview", `{"repositoryIdentity":"`+alias+`"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var dto snapshotIgnorePreviewDTO
	if err := json.Unmarshal(response.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Version != snapshotignoremanagement.Version || dto.RepositoryIdentity != alias || dto.Action != "append-snapshot-ignore" || dto.Target != ".gitignore" || dto.Summary != "Add the RoboRev snapshot directory to Git ignore rules." || dto.Token == "" || dto.ExpiresAt.Before(time.Now()) {
		t.Fatalf("unexpected preview DTO: %+v", dto)
	}
	if !snapshotIgnoreAliasPattern.MatchString(dto.RepositoryIdentity) || dto.RepositoryIdentity == legacy || strings.Contains(dto.RepositoryIdentity, root) {
		t.Fatalf("identity is not path-free alias: %q legacy=%q root=%q", dto.RepositoryIdentity, legacy, root)
	}
	if strings.Contains(response.Body.String(), privateConfig) || strings.Contains(response.Body.String(), root) || strings.Contains(response.Body.String(), ".roborev") || strings.Contains(response.Body.String(), "pattern") || strings.Contains(response.Body.String(), "content") {
		t.Fatalf("preview leaked private plan data: %s", response.Body.String())
	}
}

func TestSnapshotIgnoreHTTPExecuteClaimsBeforeBusyAndRejectsReplay(t *testing.T) {
	root := t.TempDir()
	inspector := &snapshotIgnoreTestInspector{root: root, state: gitx.SnapshotIgnoreRepositoryState{Head: strings.Repeat("e", 40)}, ignoreAfter: -1}
	service := &snapshotIgnoreTestService{}
	server, _, alias := snapshotIgnoreHTTPServer(t, inspector, service)
	token := snapshotIgnorePreviewToken(t, server, alias)
	release, err := server.Gate.AcquireFix()
	if err != nil {
		t.Fatal(err)
	}
	busy := httptest.NewRecorder()
	server.Handler().ServeHTTP(busy, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/execute", `{"token":"`+token+`"}`))
	release()
	if busy.Code != http.StatusConflict {
		t.Fatalf("busy execute status=%d body=%s", busy.Code, busy.Body.String())
	}
	if got := snapshotIgnoreGitignore(t, root); got != nil {
		t.Fatalf("busy execute mutated .gitignore: %q", got)
	}
	replay := httptest.NewRecorder()
	server.Handler().ServeHTTP(replay, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/execute", `{"token":"`+token+`"}`))
	if replay.Code != http.StatusConflict {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	if !strings.Contains(replay.Body.String(), "confirmation token is missing or expired") {
		t.Fatalf("replay did not expose stale token phase: %s", replay.Body.String())
	}
}

func TestSnapshotIgnoreHTTPExecuteRequiresTokenOnlyBodyAndConsumesInvalidAttempt(t *testing.T) {
	root := t.TempDir()
	inspector := &snapshotIgnoreTestInspector{root: root, state: gitx.SnapshotIgnoreRepositoryState{Head: strings.Repeat("f", 40)}, ignoreAfter: -1}
	service := &snapshotIgnoreTestService{}
	server, _, alias := snapshotIgnoreHTTPServer(t, inspector, service)
	token := snapshotIgnorePreviewToken(t, server, alias)
	for _, body := range []string{
		`{}`,
		`{"token":""}`,
		`{"token":"` + token + `","extra":"PRIVATE_MARKER"}`,
		`{"token":"` + token + `","token":"other"}`,
	} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/execute", body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d want=%d response=%s", body, response.Code, http.StatusBadRequest, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "PRIVATE_MARKER") {
			t.Fatalf("private marker leaked: %s", response.Body.String())
		}
	}
	// The token-only valid body is the sole shape that may reach the claim and
	// append path. The malformed attempts above must not consume it.
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/execute", `{"token":"`+token+`"}`))
	if response.Code != http.StatusOK || response.Body.String() != `{"status":"failed-unverified"}`+"\n" {
		// The test inspector intentionally keeps the post-Git state unignored;
		// this still proves the token reached exactly one execution attempt.
		t.Fatalf("valid token-only execute status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSnapshotIgnoreHTTPExecuteRejectsConfigGitAndFilesystemDriftBeforeWrite(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string, inspector *snapshotIgnoreTestInspector, service *snapshotIgnoreTestService)
	}{
		{name: "configuration drift", mutate: func(_ *testing.T, _ string, _ *snapshotIgnoreTestInspector, service *snapshotIgnoreTestService) {
			service.snapshotDir = "other-snapshot"
		}},
		{name: "HEAD drift", mutate: func(_ *testing.T, _ string, inspector *snapshotIgnoreTestInspector, _ *snapshotIgnoreTestService) {
			inspector.state.Head = strings.Repeat("1", 40)
		}},
		{name: "branch drift", mutate: func(_ *testing.T, _ string, inspector *snapshotIgnoreTestInspector, _ *snapshotIgnoreTestService) {
			inspector.state.Branch = "feature/private"
		}},
		{name: "dirty drift", mutate: func(_ *testing.T, _ string, inspector *snapshotIgnoreTestInspector, _ *snapshotIgnoreTestService) {
			inspector.state.Dirty = true
		}},
		{name: "tracked drift", mutate: func(_ *testing.T, _ string, inspector *snapshotIgnoreTestInspector, _ *snapshotIgnoreTestService) {
			inspector.state.Tracked = true
		}},
		{name: "ignored drift", mutate: func(_ *testing.T, _ string, inspector *snapshotIgnoreTestInspector, _ *snapshotIgnoreTestService) {
			inspector.state.Ignored = true
		}},
		{name: "snapshot path drift", mutate: func(_ *testing.T, _ string, _ *snapshotIgnoreTestInspector, service *snapshotIgnoreTestService) {
			service.snapshotDir = "../escape"
		}},
		{name: "target appeared", mutate: func(t *testing.T, root string, _ *snapshotIgnoreTestInspector, _ *snapshotIgnoreTestService) {
			if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("user content\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			inspector := &snapshotIgnoreTestInspector{root: root, state: gitx.SnapshotIgnoreRepositoryState{Head: strings.Repeat("0", 40)}, ignoreAfter: -1}
			service := &snapshotIgnoreTestService{}
			server, _, alias := snapshotIgnoreHTTPServer(t, inspector, service)
			token := snapshotIgnorePreviewToken(t, server, alias)
			test.mutate(t, root, inspector, service)
			before := snapshotIgnoreGitignore(t, root)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/execute", `{"token":"`+token+`"}`))
			if response.Code != http.StatusConflict {
				t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusConflict, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), "stale") && !strings.Contains(response.Body.String(), "snapshot ignore preview") {
				t.Fatalf("drift response was not sanitized stale response: %s", response.Body.String())
			}
			if after := snapshotIgnoreGitignore(t, root); !bytes.Equal(after, before) {
				t.Fatalf("%s drift mutated .gitignore", test.name)
			}
			// Claim-before-fresh-check means a drifted token cannot be retried.
			replay := httptest.NewRecorder()
			server.Handler().ServeHTTP(replay, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/execute", `{"token":"`+token+`"}`))
			if replay.Code != http.StatusConflict || !strings.Contains(replay.Body.String(), "missing or expired") {
				t.Fatalf("drift replay status=%d body=%s", replay.Code, replay.Body.String())
			}
		})
	}
}

func TestSnapshotIgnoreHTTPExecutePostGitVerificationFailureIsHonest(t *testing.T) {
	root := t.TempDir()
	good := gitx.SnapshotIgnoreRepositoryState{Root: root, Branch: "main", Head: strings.Repeat("a", 40)}
	postFailure := good
	postFailure.Dirty = true
	inspector := &snapshotIgnoreTestInspector{root: root, sequence: []gitx.SnapshotIgnoreRepositoryState{good, good, postFailure}, ignoreAfter: -1}
	server, _, alias := snapshotIgnoreHTTPServer(t, inspector, &snapshotIgnoreTestService{})
	token := snapshotIgnorePreviewToken(t, server, alias)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/execute", `{"token":"`+token+`"}`))
	if response.Code != http.StatusOK || response.Body.String() != `{"status":"failed-unverified"}`+"\n" {
		t.Fatalf("post verification status=%d body=%s", response.Code, response.Body.String())
	}
	if got := snapshotIgnoreGitignore(t, root); !bytes.Contains(got, []byte("# roborev snapshots\n/.roborev/\n")) {
		t.Fatalf("expected append despite failed verification, got %q", got)
	}
}

func TestSnapshotIgnoreHTTPExecuteCanceledRequestDoesNotWriteAndTokenCannotReplay(t *testing.T) {
	root := t.TempDir()
	inspector := &snapshotIgnoreTestInspector{root: root, state: gitx.SnapshotIgnoreRepositoryState{Head: strings.Repeat("b", 40)}, ignoreAfter: -1}
	server, _, alias := snapshotIgnoreHTTPServer(t, inspector, &snapshotIgnoreTestService{})
	token := snapshotIgnorePreviewToken(t, server, alias)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/execute", `{"token":"`+token+`"}`).WithContext(ctx)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusRequestTimeout {
		t.Fatalf("canceled execute status=%d body=%s", response.Code, response.Body.String())
	}
	if got := snapshotIgnoreGitignore(t, root); got != nil {
		t.Fatalf("canceled execute mutated .gitignore: %q", got)
	}
	replay := httptest.NewRecorder()
	server.Handler().ServeHTTP(replay, snapshotIgnoreRequest(server, http.MethodPost, "/api/snapshot-ignore/execute", `{"token":"`+token+`"}`))
	if replay.Code != http.StatusConflict {
		t.Fatalf("canceled replay status=%d body=%s", replay.Code, replay.Body.String())
	}
}
