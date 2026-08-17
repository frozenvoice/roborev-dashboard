package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

type fakePanelGitMetadata struct {
	linked bool
	err    error
}

func (f fakePanelGitMetadata) IsLinkedWorktree(context.Context, string) (bool, error) {
	return f.linked, f.err
}

func writeRepoPanelConfig(t *testing.T, repo, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, ".roborev.toml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func panelTestServer(t *testing.T, configFile string, repos ...string) (*Server, *fakeService, *Registry) {
	t.Helper()
	registry := NewRegistry(fakeInspector{}, configFile)
	for _, repo := range repos {
		if _, err := registry.Add(context.Background(), repo); err != nil {
			t.Fatal(err)
		}
	}
	service := &fakeService{}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	server.Panels = &roborev.PanelResolver{GlobalConfigPath: filepath.Join(t.TempDir(), "missing-global.toml")}
	return server, service, registry
}

func requestPanels(t *testing.T, server *Server, repo string) RepositoryPanels {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/repos/panels?repo="+url.QueryEscape(repo), nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("panels status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response RepositoryPanels
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestRepositoryPanelCatalogsAreRepositoryScoped(t *testing.T) {
	repoA, repoB := t.TempDir(), t.TempDir()
	writeRepoPanelConfig(t, repoA, "[review.subagents.a]\nagent='codex'\n[review.panels.alpha]\nmembers=['a']\n")
	server, _, _ := panelTestServer(t, "", repoA, repoB)

	a := requestPanels(t, server, repoA)
	b := requestPanels(t, server, repoB)
	if a.SelectedPanel != "alpha" || len(a.Panels) != 2 {
		t.Fatalf("repo A panels = %#v", a)
	}
	if b.SelectedPanel != roborev.PanelNone || len(b.Panels) != 1 || b.Panels[0].Name != roborev.PanelNone {
		t.Fatalf("repo B panels = %#v", b)
	}
	if a.RepositoryIdentity == b.RepositoryIdentity {
		t.Fatal("repository identities collided")
	}
}

func TestMultiplePanelsRequireSelectionAndPersistAcrossRestart(t *testing.T) {
	repo := t.TempDir()
	writeRepoPanelConfig(t, repo, "[review.subagents.a]\nagent='codex'\n[review.panels.alpha]\nmembers=['a']\n[review.panels.beta]\nmembers=['a']\n")
	configFile := filepath.Join(t.TempDir(), "registry.json")
	server, _, _ := panelTestServer(t, configFile, repo)
	initial := requestPanels(t, server, repo)
	if !initial.SelectionRequired || initial.SelectedPanel != "" {
		t.Fatalf("initial selection state = %#v", initial)
	}

	body, _ := json.Marshal(panelSelectionBody{RepoPath: repo, Panel: "beta"})
	request := httptest.NewRequest(http.MethodPost, "/api/repos/panel-selection", strings.NewReader(string(body)))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("selection status %d: %s", recorder.Code, recorder.Body.String())
	}

	reloaded := NewRegistry(fakeInspector{}, configFile)
	reloaded.Load(context.Background())
	restarted, err := NewServer(&fakeService{}, reloaded, "")
	if err != nil {
		t.Fatal(err)
	}
	restarted.Panels = server.Panels
	state := requestPanels(t, restarted, repo)
	if state.SelectionRequired || state.SelectedPanel != "beta" {
		t.Fatalf("remembered state = %#v", state)
	}
}

func TestMalformedPanelConfigReturnsTypedUnavailableState(t *testing.T) {
	repo := t.TempDir()
	writeRepoPanelConfig(t, repo, "[review.panels.bad]\nmembers=['missing']\n")
	server, service, _ := panelTestServer(t, "", repo)
	state := requestPanels(t, server, repo)
	if state.Error == "" || len(state.Panels) != 0 || state.SelectedPanel != "" || state.SelectionRequired {
		t.Fatalf("unsafe malformed state = %#v", state)
	}

	body, _ := json.Marshal(reviewBody{RepoPath: repo, Panel: roborev.PanelNone})
	request := httptest.NewRequest(http.MethodPost, "/api/reviews", strings.NewReader(string(body)))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || service.reviewCalls != 0 {
		t.Fatalf("malformed config review status=%d calls=%d body=%s", recorder.Code, service.reviewCalls, recorder.Body.String())
	}
}

func TestReviewRejectsMalformedAllowFailureTypeBeforeEnqueue(t *testing.T) {
	repo := t.TempDir()
	writeRepoPanelConfig(t, repo, "[review.subagents.a]\nallow_failure='yes'\n[review.panels.dynamic]\nmembers=['a']\n")
	server, service, _ := panelTestServer(t, "", repo)
	body, _ := json.Marshal(reviewBody{RepoPath: repo, Panel: "dynamic"})
	request := httptest.NewRequest(http.MethodPost, "/api/reviews", strings.NewReader(string(body)))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || service.reviewCalls != 0 {
		t.Fatalf("malformed allow_failure status=%d calls=%d body=%s", recorder.Code, service.reviewCalls, recorder.Body.String())
	}
}

func TestReviewRevalidatesCurrentDynamicPanelAndBuiltinNone(t *testing.T) {
	repo := t.TempDir()
	configPath := filepath.Join(repo, ".roborev.toml")
	writeRepoPanelConfig(t, repo, "[review.subagents.a]\nagent='codex'\n[review.panels.dynamic]\nmembers=['a']\n")
	server, service, _ := panelTestServer(t, "", repo)

	postReview := func(panel string) int {
		body, _ := json.Marshal(reviewBody{RepoPath: repo, Panel: panel})
		request := httptest.NewRequest(http.MethodPost, "/api/reviews", strings.NewReader(string(body)))
		request.Header.Set("X-CSRF-Token", server.csrfToken)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		return recorder.Code
	}
	if code := postReview("dynamic"); code != http.StatusCreated || service.reviewRequest.Panel != "dynamic" {
		t.Fatalf("dynamic review code=%d request=%#v", code, service.reviewRequest)
	}
	if code := postReview(roborev.PanelNone); code != http.StatusCreated || service.reviewRequest.Panel != roborev.PanelNone {
		t.Fatalf("none review code=%d request=%#v", code, service.reviewRequest)
	}
	before := service.reviewCalls
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	for _, panel := range []string{"dynamic", "injected\nname", "unknown", ""} {
		if code := postReview(panel); code < 400 {
			t.Fatalf("stale/invalid panel %q accepted with %d", panel, code)
		}
	}
	if service.reviewCalls != before {
		t.Fatalf("invalid panels enqueued: before=%d after=%d", before, service.reviewCalls)
	}
}

func TestPanelSelectionRejectsRemovedCurrentPanel(t *testing.T) {
	repo := t.TempDir()
	writeRepoPanelConfig(t, repo, "[review.subagents.a]\nagent='codex'\n[review.panels.dynamic]\nmembers=['a']\n")
	server, _, registry := panelTestServer(t, "", repo)
	if err := os.Remove(filepath.Join(repo, ".roborev.toml")); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(panelSelectionBody{RepoPath: repo, Panel: "dynamic"})
	request := httptest.NewRequest(http.MethodPost, "/api/repos/panel-selection", strings.NewReader(string(body)))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("removed selection status %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, ok, err := registry.PanelSelection(repo); err != nil || ok {
		t.Fatalf("removed selection persisted: ok=%v err=%v", ok, err)
	}
}

func TestLinkedWorktreeWithoutDirectConfigAllowsOnlyBuiltinNoneReview(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: ../main/.git/worktrees/linked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(globalPath, []byte("[review.subagents.a]\nagent='codex'\n[review.panels.shared]\nmembers=['a']\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, service, _ := panelTestServer(t, "", repo)
	server.Panels = &roborev.PanelResolver{GlobalConfigPath: globalPath, Metadata: fakePanelGitMetadata{linked: true}}

	post := func(panel string) int {
		body, _ := json.Marshal(reviewBody{RepoPath: repo, Panel: panel})
		request := httptest.NewRequest(http.MethodPost, "/api/reviews", strings.NewReader(string(body)))
		request.Header.Set("X-CSRF-Token", server.csrfToken)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		return recorder.Code
	}
	if code := post("shared"); code != http.StatusBadRequest || service.reviewCalls != 0 {
		t.Fatalf("ambiguous global panel status=%d calls=%d", code, service.reviewCalls)
	}
	if code := post(roborev.PanelNone); code != http.StatusCreated || service.reviewCalls != 1 || service.reviewRequest.Panel != roborev.PanelNone {
		t.Fatalf("builtin none status=%d calls=%d request=%#v", code, service.reviewCalls, service.reviewRequest)
	}
}

func TestSubmoduleLikeGitFileKeepsGlobalPanelReviewAuthorized(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: ../super/.git/modules/lib\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(globalPath, []byte("[review.subagents.a]\nagent='codex'\n[review.panels.shared]\nmembers=['a']\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, service, _ := panelTestServer(t, "", repo)
	server.Panels = &roborev.PanelResolver{GlobalConfigPath: globalPath, Metadata: fakePanelGitMetadata{linked: false}}
	body, _ := json.Marshal(reviewBody{RepoPath: repo, Panel: "shared"})
	request := httptest.NewRequest(http.MethodPost, "/api/reviews", strings.NewReader(string(body)))
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || service.reviewCalls != 1 || service.reviewRequest.Panel != "shared" {
		t.Fatalf("submodule-like panel review was not enqueued: status=%d calls=%d request=%#v body=%s", recorder.Code, service.reviewCalls, service.reviewRequest, recorder.Body.String())
	}
}
