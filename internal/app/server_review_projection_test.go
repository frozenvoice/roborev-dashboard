package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestReviewProjectionRejectsWrongShapesBeforeTheyReachTheBrowser(t *testing.T) {
	const privateMarkers = `PRIVATE_NESTED_REPO_PATH PRIVATE_NESTED_PROMPT PRIVATE_NESTED_BINARY PRIVATE_NESTED_STDERR`
	cases := []struct {
		name  string
		field string
		value string
	}{
		{name: "object output", field: "output", value: `{"repo_path":"` + privateMarkers + `"}`},
		{name: "object agent", field: "agent", value: `{"binary":"` + privateMarkers + `"}`},
		{name: "array status", field: "status", value: `["done"]`},
		{name: "object member IDs", field: "member_job_ids", value: `{"id":42}`},
		{name: "unsafe ID", field: "id", value: `9007199254740992`},
		{name: "unsafe member ID", field: "member_job_ids", value: `[9007199254740992]`},
		{name: "quoted duration", field: "duration_ms", value: `"42"`},
		{name: "null agent", field: "agent", value: `null`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			list := json.RawMessage(`{"jobs":[{"id":42,"` + test.field + `":` + test.value + `}]}`)
			if projected, err := projectReviewListForBrowser(list); err == nil || projected != nil || strings.Contains(string(projected), privateMarkers) {
				t.Fatalf("invalid list shape was projected: err=%v body=%s", err, projected)
			}
			detail := json.RawMessage(`{"id":7,"job_id":42,"` + test.field + `":` + test.value + `}`)
			if projected, err := projectReviewDetailForBrowser(detail); err == nil || projected != nil || strings.Contains(string(projected), privateMarkers) {
				t.Fatalf("invalid detail shape was projected: err=%v body=%s", err, projected)
			}
			if test.field == "status" || test.field == "member_job_ids" {
				receipt := json.RawMessage(`{"job_id":42,"` + test.field + `":` + test.value + `}`)
				if projected, err := projectReviewReceiptForBrowser(receipt); err == nil || projected != nil || strings.Contains(string(projected), privateMarkers) {
					t.Fatalf("invalid receipt shape was projected: err=%v body=%s", err, projected)
				}
			}
		})
	}
	for _, field := range []string{"branch", "model", "started_at", "closed", "review", "member_job_ids"} {
		t.Run("explicit null "+field, func(t *testing.T) {
			list := json.RawMessage(`{"jobs":[{"id":42,"` + field + `":null}]}`)
			if projected, err := projectReviewListForBrowser(list); err == nil || projected != nil {
				t.Fatalf("explicit null list field was accepted: err=%v body=%s", err, projected)
			}
			detail := json.RawMessage(`{"id":7,"job_id":42,"` + field + `":null}`)
			if projected, err := projectReviewDetailForBrowser(detail); err == nil || projected != nil {
				t.Fatalf("explicit null detail field was accepted: err=%v body=%s", err, projected)
			}
			if field == "branch" || field == "member_job_ids" {
				receipt := json.RawMessage(`{"job_id":42,"` + field + `":null}`)
				if projected, err := projectReviewReceiptForBrowser(receipt); err == nil || projected != nil {
					t.Fatalf("explicit null receipt field was accepted: err=%v body=%s", err, projected)
				}
			}
		})
	}
	if projected, err := projectReviewDetailForBrowser(json.RawMessage(`{"id":7,"job_id":42,"job":null}`)); err == nil || projected != nil {
		t.Fatalf("explicit null nested job was accepted: err=%v body=%s", err, projected)
	}
}

func TestReviewProjectionNormalizesV064NumericVerdictBool(t *testing.T) {
	list, err := projectReviewListForBrowser(json.RawMessage(`{"jobs":[{"id":42,"verdict_bool":0},{"id":43,"verdict_bool":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(list), `"verdict_bool":false`) || !strings.Contains(string(list), `"verdict_bool":true`) {
		t.Fatalf("numeric verdict_bool was not normalized: %s", list)
	}
	detail, err := projectReviewDetailForBrowser(json.RawMessage(`{"id":7,"job_id":42,"verdict_bool":1}`))
	if err != nil || !strings.Contains(string(detail), `"verdict_bool":true`) {
		t.Fatalf("numeric detail verdict_bool was not normalized: err=%v body=%s", err, detail)
	}

	for _, value := range []string{"2", "-1", `"1"`, `{}`, `[]`, `null`, `true`} {
		t.Run("invalid "+value, func(t *testing.T) {
			list := json.RawMessage(`{"jobs":[{"id":42,"verdict_bool":` + value + `}]}`)
			if projected, err := projectReviewListForBrowser(list); err == nil || projected != nil {
				t.Fatalf("invalid numeric verdict_bool was accepted by list: err=%v body=%s", err, projected)
			}
			detail := json.RawMessage(`{"id":7,"job_id":42,"verdict_bool":` + value + `}`)
			if projected, err := projectReviewDetailForBrowser(detail); err == nil || projected != nil {
				t.Fatalf("invalid numeric verdict_bool was accepted by detail: err=%v body=%s", err, projected)
			}
		})
	}
}

func TestReviewProjectionRejectsNullCollections(t *testing.T) {
	for _, raw := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`{"jobs":null}`), json.RawMessage(`{"reviews":null}`), json.RawMessage(`{"items":null}`)} {
		if projected, err := projectReviewListForBrowser(raw); err == nil || projected != nil {
			t.Fatalf("null review collection was normalized: input=%s err=%v body=%s", raw, err, projected)
		}
	}
	if projected, err := projectReviewArray(json.RawMessage(`null`)); err == nil || projected != nil {
		t.Fatalf("bare null array was normalized: err=%v body=%s", err, projected)
	}
}

func TestReviewProjectionPreservesValidScalarArrayAndDedicatedNestedReview(t *testing.T) {
	list := json.RawMessage(`{"jobs":[{"id":42,"agent":"codex","status":"done","output":"Finding references C:\\src\\foo.go","member_job_ids":[42,43],"review":{"output":"nested review","verdict":"P","repo_path":"PRIVATE_NESTED_REPO_PATH","prompt":"PRIVATE_NESTED_PROMPT","binary":"PRIVATE_NESTED_BINARY","stderr":"PRIVATE_NESTED_STDERR"}}],"has_more":false}`)
	projected, err := projectReviewListForBrowser(list)
	if err != nil {
		t.Fatal(err)
	}
	body := string(projected)
	if !strings.Contains(body, `Finding references C:\\src\\foo.go`) || !strings.Contains(body, `"member_job_ids":[42,43]`) || !strings.Contains(body, `"review":{"output":"nested review","verdict":"P"}`) {
		t.Fatalf("valid list values were not preserved: %s", body)
	}
	for _, marker := range []string{"PRIVATE_NESTED_REPO_PATH", "PRIVATE_NESTED_PROMPT", "PRIVATE_NESTED_BINARY", "PRIVATE_NESTED_STDERR"} {
		if strings.Contains(body, marker) {
			t.Fatalf("nested private marker leaked: %q in %s", marker, body)
		}
	}

	detail := json.RawMessage(`{"id":7,"job_id":42,"output":"Finding references C:\\src\\foo.go","job":{"id":42,"status":"done","closed":false,"repo_path":"PRIVATE_NESTED_REPO_PATH"},"review":{"output":"nested review","verdict":"P","stderr":"PRIVATE_NESTED_STDERR"}}`)
	projected, err = projectReviewDetailForBrowser(detail)
	if err != nil {
		t.Fatal(err)
	}
	body = string(projected)
	if !strings.Contains(body, `Finding references C:\\src\\foo.go`) || !strings.Contains(body, `"job":`) || !strings.Contains(body, `"id":42`) || !strings.Contains(body, `"status":"done"`) || !strings.Contains(body, `"closed":false`) || !strings.Contains(body, `"review":{"output":"nested review","verdict":"P"}`) {
		t.Fatalf("valid detail values were not preserved: %s", body)
	}
	if strings.Contains(body, "PRIVATE_NESTED_") {
		t.Fatalf("nested detail private marker leaked: %s", body)
	}

	receipt := json.RawMessage(`{"job_id":42,"status":"queued","member_job_ids":[42,43]}`)
	projected, err = projectReviewReceiptForBrowser(receipt)
	if err != nil || !strings.Contains(string(projected), `"member_job_ids":[42,43]`) {
		t.Fatalf("valid receipt values were not preserved: err=%v body=%s", err, projected)
	}
}

type reviewProjectionListService struct {
	*fakeService
	raw json.RawMessage
}

func (s *reviewProjectionListService) List(context.Context, string, int) (json.RawMessage, error) {
	return s.raw, nil
}

func TestReviewProjectionHandlerUsesSafeErrorAtMalformedShapeBoundary(t *testing.T) {
	privatePath := `C:\PRIVATE_NESTED_REPO_PATH`
	privatePrompt := "PRIVATE_NESTED_PROMPT"
	privateBinary := `C:\PRIVATE_NESTED_BINARY`
	privateStderr := "PRIVATE_NESTED_STDERR"
	malformed, err := json.Marshal(map[string]any{
		"jobs": []map[string]any{{
			"id":     42,
			"output": map[string]string{"repo_path": privatePath, "prompt": privatePrompt, "binary": privateBinary, "stderr": privateStderr},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), `E:\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(&reviewProjectionListService{fakeService: &fakeService{}, raw: malformed}, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/reviews?repo=E%3A%5Crepo", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("malformed projection status = %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, marker := range []string{privatePath, privatePrompt, privateBinary, privateStderr} {
		if strings.Contains(body, marker) {
			t.Fatalf("raw private marker escaped malformed handler: %q in %s", marker, body)
		}
	}
	for _, marker := range []string{`C:\\PRIVATE_NESTED_REPO_PATH`, `PRIVATE_NESTED_PROMPT`, `C:\\PRIVATE_NESTED_BINARY`, `PRIVATE_NESTED_STDERR`} {
		if strings.Contains(body, marker) {
			t.Fatalf("JSON-escaped private marker escaped malformed handler: %q in %s", marker, body)
		}
	}
}
