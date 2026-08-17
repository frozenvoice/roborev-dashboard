package fixprompt

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

const koreanFinding = "## High\n\n한글 경로에서 권한 검사가 누락됩니다.\n\n- 첫째 줄\n- 둘째 줄  \n"

func validPayload(t *testing.T, output string) []byte {
	t.Helper()
	payload := map[string]any{
		"job_id":       49,
		"output":       output,
		"verdict_bool": 0,
		"prompt":       "SENTINEL_ORIGINAL_PROMPT_SECRET",
		"diff_content": "SENTINEL_DIFF_SECRET",
		"log":          "SENTINEL_LOG_SECRET",
		"comments":     []string{"SENTINEL_COMMENT_SECRET"},
		"job": map[string]any{
			"id":             49,
			"repo_path":      `E:\Dev\한글 프로젝트`,
			"git_ref":        "0123456789abcdef",
			"branch":         "main",
			"commit_subject": "권한 경계 수정",
			"verdict":        "F",
			"status":         "done",
			"panel_role":     "synthesis",
			"output":         "SENTINEL_NESTED_JOB_OUTPUT",
		},
		"members": []any{
			map[string]any{"output": "SENTINEL_MEMBER_CODEX_OUTPUT", "prompt": "SENTINEL_MEMBER_PROMPT"},
			map[string]any{"output": "SENTINEL_MEMBER_CLAUDE_OUTPUT", "diff_content": "SENTINEL_MEMBER_DIFF"},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDecodeShowAndGenerateEqualTarget(t *testing.T) {
	review, err := DecodeShow(validPayload(t, koreanFinding))
	if err != nil {
		t.Fatal(err)
	}
	if review.JobID != 49 || review.RepositoryPath != `E:\Dev\한글 프로젝트` || review.PanelRole != "synthesis" {
		t.Fatalf("unexpected safe review metadata: %#v", review)
	}
	result, err := Generate(review, RepositoryState{Path: review.RepositoryPath, Head: review.TargetRef})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stale {
		t.Fatal("equal target was marked stale")
	}
	if strings.Contains(result.Text, "경고: 이 review는 현재 HEAD보다 오래된 target일 수 있으므로") {
		t.Fatal("equal target included stale warning")
	}
	if !strings.HasSuffix(result.Text, koreanFinding) {
		t.Fatalf("multiline Korean finding was rewritten:\n%s", result.Text)
	}
	for _, want := range []string{
		"Repository path: E:\\Dev\\한글 프로젝트",
		"RoboRev job ID: 49",
		"Review target ref/SHA: 0123456789abcdef",
		"Branch: main",
		"Commit subject: 권한 경계 수정",
		"Current HEAD: 0123456789abcdef",
		"AGENTS.md와 REVIEW.md를 먼저 읽고 준수한다.",
		"finding을 현재 코드 및 재현 가능한 증거와 직접 대조",
		"valid/applicable finding만 수정",
		"false positive 또는 not applicable이면 구체적인 근거를 보고",
		"unrelated user change를 보존",
		"amend하거나 rebase하지 않는다",
		"범위를 이 finding 수정으로 제한",
		"필요한 regression test를 추가",
		"저장소 문서에 정의된 validation 명령",
		"commit/push 규칙",
		"과거 RoboRev job을 rerun하지 않고",
		"새 HEAD review 대상으로 사용",
		"수정 내용, 검증 결과, commit SHA 및 남은 위험",
	} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestGenerateMarksDifferentTargetStale(t *testing.T) {
	review, err := DecodeShow(validPayload(t, "actionable finding"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Generate(review, RepositoryState{Path: review.RepositoryPath, Head: "fedcba9876543210"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Stale {
		t.Fatal("different HEAD was not marked stale")
	}
	warning := "경고: 이 review는 현재 HEAD보다 오래된 target일 수 있으므로 기존 finding이 현재 코드에도 여전히 적용되는지 먼저 검증하고, 후속 변경을 되돌리지 말 것."
	if strings.Count(result.Text, warning) != 1 {
		t.Fatalf("stale warning count = %d", strings.Count(result.Text, warning))
	}
}

func TestDecodeShowRejectsPassAndNonTerminalReviews(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"pass": func(payload map[string]any) {
			payload["verdict_bool"] = 1
			payload["job"].(map[string]any)["verdict"] = "P"
		},
		"running": func(payload map[string]any) {
			payload["job"].(map[string]any)["status"] = "running"
		},
	} {
		t.Run(name, func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal(validPayload(t, "finding"), &payload); err != nil {
				t.Fatal(err)
			}
			mutate(payload)
			raw, _ := json.Marshal(payload)
			if _, err := DecodeShow(raw); err == nil {
				t.Fatal("non-actionable review was accepted")
			}
		})
	}
}

func TestDecodeShowRejectsBlankAndOversizedFinding(t *testing.T) {
	for name, finding := range map[string]string{
		"blank":     " \n\t ",
		"oversized": strings.Repeat("가", MaxFindingBytes/len("가")+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeShow(validPayload(t, finding)); err == nil {
				t.Fatal("invalid finding was accepted")
			}
		})
	}
}

func TestGenerateRejectsOversizedMetadata(t *testing.T) {
	review, err := DecodeShow(validPayload(t, "finding"))
	if err != nil {
		t.Fatal(err)
	}
	review.CommitSubject = strings.Repeat("x", MaxMetadataBytes+1)
	if _, err := Generate(review, RepositoryState{Path: review.RepositoryPath, Head: review.TargetRef}); err == nil {
		t.Fatal("oversized review metadata was accepted")
	}
	review.CommitSubject = "subject"
	if _, err := Generate(review, RepositoryState{Path: review.RepositoryPath, Head: strings.Repeat("a", MaxMetadataBytes+1)}); err == nil {
		t.Fatal("oversized current metadata was accepted")
	}
}

func TestPanelSynthesisUsesOnlyTopLevelOutput(t *testing.T) {
	review, err := DecodeShow(validPayload(t, "ONLY_TOP_LEVEL_SYNTHESIS"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Generate(review, RepositoryState{Path: review.RepositoryPath, Head: review.TargetRef})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "ONLY_TOP_LEVEL_SYNTHESIS") {
		t.Fatal("top-level synthesis was omitted")
	}
	for _, forbidden := range []string{
		"SENTINEL_ORIGINAL_PROMPT_SECRET",
		"SENTINEL_DIFF_SECRET",
		"SENTINEL_LOG_SECRET",
		"SENTINEL_COMMENT_SECRET",
		"SENTINEL_NESTED_JOB_OUTPUT",
		"SENTINEL_MEMBER_CODEX_OUTPUT",
		"SENTINEL_MEMBER_CLAUDE_OUTPUT",
		"SENTINEL_MEMBER_PROMPT",
		"SENTINEL_MEMBER_DIFF",
	} {
		if strings.Contains(result.Text, forbidden) {
			t.Errorf("prompt leaked ignored field %q", forbidden)
		}
	}
}

func TestDecodeShowRejectsMalformedOrUnexpectedPayload(t *testing.T) {
	tests := map[string][]byte{
		"malformed":       []byte(`{"job_id":`),
		"unexpected type": []byte(`{"job_id":"49","output":"finding","job":{}}`),
		"trailing data":   append(validPayload(t, "finding"), []byte(` {}`)...),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeShow(raw); err == nil {
				t.Fatal("malformed payload was accepted")
			}
		})
	}
}

func TestDecodeShowRejectsMismatchedIdentityFields(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal(validPayload(t, "finding"), &payload); err != nil {
		t.Fatal(err)
	}
	payload["job"].(map[string]any)["id"] = 50
	raw, _ := json.Marshal(payload)
	if _, err := DecodeShow(raw); err == nil {
		t.Fatal("mismatched job identity was accepted")
	}
}

func TestGenerateRequiresRegisteredRepositoryIdentity(t *testing.T) {
	review, err := DecodeShow(validPayload(t, "finding"))
	if err != nil {
		t.Fatal(err)
	}
	caseVariant := strings.ToLower(review.RepositoryPath)
	_, err = Generate(review, RepositoryState{Path: caseVariant, Head: review.TargetRef})
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("Windows-equivalent path rejected: %v", err)
		}
	} else if err == nil {
		t.Fatal("case-sensitive platform accepted a different repository identity")
	}
	if _, err := Generate(review, RepositoryState{Path: "/different/repository", Head: review.TargetRef}); err == nil {
		t.Fatal("different registered repository was accepted")
	}
}

func TestDecodeShowSupportsBooleanVerdictCompatibility(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal(validPayload(t, "finding"), &payload); err != nil {
		t.Fatal(err)
	}
	payload["verdict_bool"] = false
	raw, _ := json.Marshal(payload)
	if _, err := DecodeShow(raw); err != nil {
		t.Fatalf("boolean verdict compatibility rejected: %v", err)
	}
}
