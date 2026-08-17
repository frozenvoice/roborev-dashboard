package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func fixPromptShow(t *testing.T, id int64, repo, target, role, verdict, finding string) json.RawMessage {
	t.Helper()
	payload := map[string]any{
		"job_id":       id,
		"output":       finding,
		"verdict_bool": verdict == "P",
		"prompt":       "SENTINEL_PROMPT_SECRET",
		"diff_content": "SENTINEL_DIFF_SECRET",
		"job": map[string]any{
			"id": id, "repo_path": repo, "git_ref": target, "branch": "main",
			"commit_subject": "subject", "verdict": verdict, "status": "done", "panel_role": role,
		},
		"members": []any{map[string]any{"output": "SENTINEL_MEMBER_OUTPUT"}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func fixPromptServer(t *testing.T, service *fakeService, repos ...string) *Server {
	t.Helper()
	registry := NewRegistry(fakeInspector{}, "")
	for _, repo := range repos {
		if _, err := registry.Add(context.Background(), repo); err != nil {
			t.Fatal(err)
		}
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func getFixPrompt(server *Server, id int, repo string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/reviews/"+strconv.Itoa(id)+"/fix-prompt?repo="+url.QueryEscape(repo), nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func TestFixPromptRequiresCurrentRepositoryOwnershipBeforeShow(t *testing.T) {
	repoA, repoB := `E:\repo-a`, `E:\repo-b`
	service := &fakeService{repoPath: repoA, showRaw: fixPromptShow(t, 42, repoA, "abcdef123456", "", "F", "finding")}
	server := fixPromptServer(t, service, repoA, repoB)
	if recorder := getFixPrompt(server, 42, repoB); recorder.Code != http.StatusForbidden {
		t.Fatalf("other registered repo status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := getFixPrompt(server, 42, `E:\unregistered`); recorder.Code != http.StatusForbidden {
		t.Fatalf("unregistered repo status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.showCalls != 0 {
		t.Fatalf("Show called before ownership validation: %d", service.showCalls)
	}
}

func TestFixPromptRequiresExplicitRepositoryQuery(t *testing.T) {
	server := fixPromptServer(t, &fakeService{}, `E:\repo`)
	recorder := getFixPrompt(server, 42, "")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFixPromptEqualAndStaleTargets(t *testing.T) {
	repo := `E:\repo`
	for _, test := range []struct {
		name, target string
		stale        bool
	}{
		{"equal", "abcdef123456", false},
		{"stale", "older-target", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeService{repoPath: repo, showRaw: fixPromptShow(t, 42, repo, test.target, "synthesis", "F", "actionable finding")}
			server := fixPromptServer(t, service, repo)
			recorder := getFixPrompt(server, 42, repo)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				Text  string `json:"text"`
				JobID int64  `json:"jobId"`
				Stale bool   `json:"stale"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.JobID != 42 || response.Stale != test.stale || !strings.Contains(response.Text, "actionable finding") {
				t.Fatalf("response=%#v", response)
			}
			for _, sentinel := range []string{"SENTINEL_PROMPT_SECRET", "SENTINEL_DIFF_SECRET", "SENTINEL_MEMBER_OUTPUT"} {
				if strings.Contains(response.Text, sentinel) {
					t.Fatalf("response leaked %q", sentinel)
				}
			}
		})
	}
}

func TestFixPromptAllowsPanelMemberOwnFinding(t *testing.T) {
	repo := `E:\repo`
	service := &fakeService{repoPath: repo, showRaw: fixPromptShow(t, 42, repo, "abcdef123456", "member", "F", "MEMBER_OWN_FINDING")}
	server := fixPromptServer(t, service, repo)
	recorder := getFixPrompt(server, 42, repo)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "MEMBER_OWN_FINDING") {
		t.Fatalf("member fix prompt status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFixPromptRejectsShowIdentityMismatchUnknownRoleAndPass(t *testing.T) {
	repo := `E:\repo`
	tests := []struct {
		name string
		raw  json.RawMessage
	}{
		{"job-id", fixPromptShow(t, 43, repo, "abcdef123456", "", "F", "finding")},
		{"repository", fixPromptShow(t, 42, `E:\other`, "abcdef123456", "", "F", "finding")},
		{"unknown-role", fixPromptShow(t, 42, repo, "abcdef123456", "coordinator", "F", "finding")},
		{"pass", fixPromptShow(t, 42, repo, "abcdef123456", "", "P", "not actionable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeService{repoPath: repo, showRaw: test.raw}
			server := fixPromptServer(t, service, repo)
			recorder := getFixPrompt(server, 42, repo)
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestFixPromptRejectsOversizedFindingSafely(t *testing.T) {
	repo := `E:\repo`
	service := &fakeService{repoPath: repo, showRaw: fixPromptShow(t, 42, repo, "abcdef123456", "", "F", strings.Repeat("x", (1<<20)+1))}
	server := fixPromptServer(t, service, repo)
	recorder := getFixPrompt(server, 42, repo)
	if recorder.Code != http.StatusConflict || strings.Contains(recorder.Body.String(), strings.Repeat("x", 1024)) {
		t.Fatalf("unsafe oversized response status=%d size=%d", recorder.Code, recorder.Body.Len())
	}
}
