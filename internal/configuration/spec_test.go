package configuration

import (
	"encoding/json"
	"testing"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

func mustRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSettingsAllowlistCoversExactlyTheV1Keys(t *testing.T) {
	want := []string{
		"max_workers", "review_context_count", "auto_close_passing_reviews",
		"review_reasoning", "refine_reasoning",
		"review_min_severity", "fix_min_severity", "refine_min_severity",
	}
	settings := Settings()
	if len(settings) != len(want) {
		t.Fatalf("settings = %d entries, want %d", len(settings), len(want))
	}
	for index, spec := range settings {
		if spec.Key != want[index] {
			t.Fatalf("settings[%d] = %q, want %q", index, spec.Key, want[index])
		}
	}
}

func TestDangerousKeysAreNotEditable(t *testing.T) {
	banned := []string{
		"allow_unsafe_agents", "disable_codex_sandbox", "server_addr",
		"opencode_cmd", "grok_cmd", "codex_cmd", "claude_code_cmd",
		"anthropic_api_key", "database_url", "sync.password",
		"agent", "model", "review.panels", "review.subagents",
		"hooks", "acp", "auto_design", "exclude_patterns", "snapshot_dir",
	}
	for _, key := range banned {
		if _, ok := Lookup(key); ok {
			t.Fatalf("banned key %q is editable", key)
		}
		if _, ok := Lookup("review." + key); ok {
			t.Fatalf("banned namespaced key review.%q is editable", key)
		}
	}
}

func TestScopesMatchTheV1Policy(t *testing.T) {
	maxWorkers, ok := Lookup("max_workers")
	if !ok {
		t.Fatal("max_workers missing")
	}
	if maxWorkers.Allows(ScopeLocal) || !maxWorkers.Allows(ScopeGlobal) {
		t.Fatal("max_workers must be global-only")
	}
	for _, key := range []string{"review_context_count", "auto_close_passing_reviews", "review_reasoning", "refine_reasoning", "review_min_severity", "fix_min_severity", "refine_min_severity"} {
		spec, ok := Lookup(key)
		if !ok {
			t.Fatalf("%s missing", key)
		}
		if !spec.Allows(ScopeGlobal) || !spec.Allows(ScopeLocal) {
			t.Fatalf("%s must allow both scopes", key)
		}
	}
}

func TestCanonicalValueValidatesTypesBoundsAndEnums(t *testing.T) {
	maxWorkers, _ := Lookup("max_workers")
	cases := []struct {
		name      string
		spec      Spec
		scope     Scope
		value     any
		want      string
		wantError bool
	}{
		{name: "integer in bounds", spec: maxWorkers, scope: ScopeGlobal, value: 8, want: "8"},
		{name: "integer above max", spec: maxWorkers, scope: ScopeGlobal, value: 33, wantError: true},
		{name: "integer below min", spec: maxWorkers, scope: ScopeGlobal, value: 0, wantError: true},
		{name: "integer wrong type", spec: maxWorkers, scope: ScopeGlobal, value: "4", wantError: true},
		{name: "integer float rejected", spec: maxWorkers, scope: ScopeGlobal, value: 4.5, wantError: true},
		{name: "wrong scope", spec: maxWorkers, scope: ScopeLocal, value: 8, wantError: true},
		{
			name: "boolean true", spec: func() Spec { s, _ := Lookup("auto_close_passing_reviews"); return s }(),
			scope: ScopeGlobal, value: true, want: "true",
		},
		{
			name: "boolean wrong type", spec: func() Spec { s, _ := Lookup("auto_close_passing_reviews"); return s }(),
			scope: ScopeGlobal, value: "true", wantError: true,
		},
		{
			name: "enum valid", spec: func() Spec { s, _ := Lookup("review_reasoning"); return s }(),
			scope: ScopeLocal, value: "thorough", want: "thorough",
		},
		{
			name: "enum invalid", spec: func() Spec { s, _ := Lookup("review_reasoning"); return s }(),
			scope: ScopeLocal, value: "extreme", wantError: true,
		},
		{
			name: "severity empty allowed", spec: func() Spec { s, _ := Lookup("review_min_severity"); return s }(),
			scope: ScopeGlobal, value: "", want: "",
		},
		{
			name: "severity valid", spec: func() Spec { s, _ := Lookup("fix_min_severity"); return s }(),
			scope: ScopeLocal, value: "high", want: "high",
		},
		{
			name: "unknown key", spec: Spec{}, scope: ScopeGlobal, value: "x", wantError: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			canonical, err := CanonicalValue(testCase.spec, testCase.scope, mustRaw(t, testCase.value))
			if testCase.wantError {
				if err == nil {
					t.Fatalf("canonical = %q, want error", canonical)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if canonical != testCase.want {
				t.Fatalf("canonical = %q, want %q", canonical, testCase.want)
			}
		})
	}
}

func TestSnapshotDigestChangesWhenRowsChange(t *testing.T) {
	base := []roborev.ConfigRow{{Origin: "default", Key: "max_workers", Value: "4"}}
	same := []roborev.ConfigRow{{Origin: "default", Key: "max_workers", Value: "4"}}
	changedValue := []roborev.ConfigRow{{Origin: "default", Key: "max_workers", Value: "8"}}
	changedKey := []roborev.ConfigRow{{Origin: "default", Key: "review_context_count", Value: "4"}}
	changedOrigin := []roborev.ConfigRow{{Origin: "global", Key: "max_workers", Value: "4"}}

	baseDigest := SnapshotDigest(base)
	if SnapshotDigest(same) != baseDigest {
		t.Fatal("identical rows produced different digests")
	}
	for name, variant := range map[string][]roborev.ConfigRow{"value": changedValue, "key": changedKey, "origin": changedOrigin} {
		if SnapshotDigest(variant) == baseDigest {
			t.Fatalf("digest did not change when %s changed", name)
		}
	}
}
