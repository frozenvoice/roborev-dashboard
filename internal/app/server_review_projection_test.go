package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReviewBrowserProjectionStripsPrivateAuthorityRecursively(t *testing.T) {
	private := `C:\Users\private\secret-repository`
	list := json.RawMessage(`{"jobs":[{"id":42,"repo_path":"` + strings.ReplaceAll(private, `\`, `\\`) + `","git_ref":"abc","status":"done","prompt":"PRIVATE_PROMPT","diff_content":"PRIVATE_DIFF","error":"PRIVATE_ERROR","command_line":"PRIVATE_COMMAND","worker_id":"PRIVATE_WORKER","session_id":"PRIVATE_SESSION","token_usage":"PRIVATE_TOKEN","worktree_path":"PRIVATE_WORKTREE","commit_subject":"safe subject"}],"has_more":false,"stats":{"private":"PRIVATE_STATS"}}`)
	projectedList, err := projectReviewListForBrowser(list)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{private, "PRIVATE_", "repo_path", "prompt", "diff_content", "command_line", "worker_id", "session_id", "token_usage", "worktree_path", "stats"} {
		if strings.Contains(string(projectedList), marker) {
			t.Fatalf("list projection leaked %q: %s", marker, projectedList)
		}
	}
	if !strings.Contains(string(projectedList), `"commit_subject":"safe subject"`) {
		t.Fatalf("list projection lost safe display data: %s", projectedList)
	}

	detail := json.RawMessage(`{"id":7,"job_id":42,"output":"safe review","prompt":"PRIVATE_PROMPT","job":{"id":42,"repo_path":"` + strings.ReplaceAll(private, `\`, `\\`) + `","status":"done","error":"PRIVATE_ERROR"},"review":{"output":"nested safe review","raw_log":"PRIVATE_LOG"}}`)
	projectedDetail, err := projectReviewDetailForBrowser(detail)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{private, "PRIVATE_", "repo_path", "prompt", "error", "raw_log"} {
		if strings.Contains(string(projectedDetail), marker) {
			t.Fatalf("detail projection leaked %q: %s", marker, projectedDetail)
		}
	}
	if !strings.Contains(string(projectedDetail), "safe review") || !strings.Contains(string(projectedDetail), "nested safe review") {
		t.Fatalf("detail projection lost intended review output: %s", projectedDetail)
	}
}

func TestReviewReceiptProjectionDoesNotExposeDaemonAuthority(t *testing.T) {
	raw := json.RawMessage(`{"id":42,"repo_path":"C:\\private","agent":"PRIVATE_AGENT","model":"PRIVATE_MODEL","command_line":"PRIVATE_COMMAND","job_type":"review","status":"queued"}`)
	projected, err := projectReviewReceiptForBrowser(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"private", "PRIVATE_", "repo_path", "command_line", "agent", "model"} {
		if strings.Contains(string(projected), marker) {
			t.Fatalf("receipt projection leaked %q: %s", marker, projected)
		}
	}
	if !strings.Contains(string(projected), `"id":42`) || !strings.Contains(string(projected), `"job_type":"review"`) {
		t.Fatalf("receipt projection lost safe identity: %s", projected)
	}
}
