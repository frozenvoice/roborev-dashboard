package stats

import (
	"strings"
	"testing"
)

const validSummaryJSON = `{
  "since":"2026-08-22T00:00:00Z",
  "repo_path":"E:\\한글 프로젝트\\repo",
  "branch":"main",
  "overview":{"total":4,"queued":1,"running":1,"done":1,"failed":1,"canceled":0,"applied":0,"rebased":0},
  "verdicts":{"total":2,"passed":1,"failed":1,"addressed":1,"pass_rate":0.5,"resolution_rate":1},
  "agents":[{"agent":"codex","total":2,"passed":1,"failed":1,"errors":0,"pass_rate":0.5,"median_duration_secs":12.5}],
  "duration":{"review_p50_secs":1,"review_p90_secs":2,"review_p99_secs":3,"queue_p50_secs":4,"queue_p90_secs":5,"queue_p99_secs":6},
  "job_types":[{"type":"review","count":2,"applied":1,"rebased":0}],
  "failures":{"total":1,"retries":0,"errors":{"timeout":1}},
  "repos":[{"name":"repo","path":"E:\\한글 프로젝트\\repo","total":2,"passed":1,"failed":1,"addressed":1}],
  "cost":{"total_usd":0,"jobs_with_cost":2,"jobs_total":2,"complete":true}
}`

const validCostJSON = `{"total_usd":0,"jobs_with_cost":1,"jobs_total":2,"complete":false}`

const validZeroSummaryJSON = `{"since":"2026-08-22T00:00:00Z","overview":{"total":0,"queued":0,"running":0,"done":0,"failed":0,"canceled":0,"applied":0,"rebased":0},"verdicts":{"total":0,"passed":0,"failed":0,"addressed":0,"pass_rate":0,"resolution_rate":0},"agents":[{"agent":"codex","total":0,"passed":0,"failed":0,"errors":0,"pass_rate":0,"median_duration_secs":0}],"duration":{"review_p50_secs":0,"review_p90_secs":0,"review_p99_secs":0,"queue_p50_secs":0,"queue_p90_secs":0,"queue_p99_secs":0},"job_types":null,"failures":{"total":0,"retries":0,"errors":{}},"cost":{"total_usd":0,"jobs_with_cost":0,"jobs_total":0,"complete":false}}`

func TestParseSummaryValidAndNormalizesZeroDataArrays(t *testing.T) {
	parsed, err := ParseSummary(validSummaryJSON)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.RepoPath == "" || len(parsed.Agents) != 1 || parsed.Cost.JobsTotal != 2 {
		t.Fatalf("unexpected summary: %+v", parsed)
	}
	zero := strings.ReplaceAll(validSummaryJSON, `"agents":[{"agent":"codex","total":2,"passed":1,"failed":1,"errors":0,"pass_rate":0.5,"median_duration_secs":12.5}]`, `"agents":null`)
	zero = strings.ReplaceAll(zero, `"job_types":[{"type":"review","count":2,"applied":1,"rebased":0}]`, `"job_types":null`)
	zero = strings.ReplaceAll(zero, `,"repos":[{"name":"repo","path":"E:\\한글 프로젝트\\repo","total":2,"passed":1,"failed":1,"addressed":1}]`, "")
	parsed, err = ParseSummary(zero)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Agents == nil || parsed.JobTypes == nil || parsed.Repos == nil {
		t.Fatal("zero-data arrays were not normalized")
	}
	withSchema := strings.Replace(validSummaryJSON, "{\n", "{\n  \"$schema\":\"https://roborev.dev/schema/summary-v0.64\",\n", 1)
	if parsed, err := ParseSummary(withSchema); err != nil || parsed.Schema == "" {
		t.Fatalf("optional summary schema rejected: %+v %v", parsed, err)
	}
}

func TestParseSummaryAcceptsZeroDenominatorRates(t *testing.T) {
	parsed, err := ParseSummary(validZeroSummaryJSON)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Verdicts.Total != 0 || parsed.Verdicts.PassRate != 0 || parsed.Verdicts.ResolutionRate != 0 || len(parsed.Agents) != 1 || parsed.Agents[0].PassRate != 0 {
		t.Fatalf("unexpected zero-denominator summary: %+v", parsed)
	}
}

func TestParseSummaryAllowsMutuallyExclusiveCompletedStatuses(t *testing.T) {
	completed := `"overview":{"total":3,"queued":0,"running":0,"done":1,"failed":0,"canceled":0,"applied":1,"rebased":1}`
	accepted := strings.Replace(validSummaryJSON, `"overview":{"total":4,"queued":1,"running":1,"done":1,"failed":1,"canceled":0,"applied":0,"rebased":0}`, completed, 1)
	if parsed, err := ParseSummary(accepted); err != nil || parsed.Overview.Total != 3 || parsed.Overview.Done != 1 || parsed.Overview.Applied != 1 || parsed.Overview.Rebased != 1 {
		t.Fatalf("mutually exclusive completed statuses rejected: %+v %v", parsed, err)
	}
	// done/applied/rebased are mutually exclusive status buckets, so their
	// individual counts may add up to total without violating the invariant.
	overflow := `"overview":{"total":1,"queued":0,"running":0,"done":1,"failed":0,"canceled":0,"applied":1,"rebased":0}`
	rejected := strings.Replace(validSummaryJSON, `"overview":{"total":4,"queued":1,"running":1,"done":1,"failed":1,"canceled":0,"applied":0,"rebased":0}`, overflow, 1)
	if _, err := ParseSummary(rejected); err == nil {
		t.Fatal("overlapping overview status counts were accepted")
	}
}

func TestParseSummaryRejectsMalformedAndUnsafeShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"unknown top-level", strings.Replace(validSummaryJSON, `"cost":`, `"unknown":true,"cost":`, 1)},
		{"unknown nested", strings.Replace(validSummaryJSON, `"total":4`, `"total":4,"unexpected":1`, 1)},
		{"trailing", validSummaryJSON + `{}`},
		{"duplicate", strings.Replace(validSummaryJSON, `"branch":"main"`, `"branch":"main","branch":"other"`, 1)},
		{"negative count", strings.Replace(validSummaryJSON, `"failed":1`, `"failed":-1`, 1)},
		{"invalid rate", strings.Replace(validSummaryJSON, `"pass_rate":0.5`, `"pass_rate":1.1`, 1)},
		{"addressed exceeds failed", strings.Replace(validSummaryJSON, `"addressed":1`, `"addressed":2`, 1)},
		{"agent invariant", strings.Replace(validSummaryJSON, `"passed":1,"failed":1,"errors":0`, `"passed":2,"failed":1,"errors":0`, 1)},
		{"percentile order", strings.Replace(validSummaryJSON, `"review_p90_secs":2`, `"review_p90_secs":0`, 1)},
		{"oversized string", strings.Replace(validSummaryJSON, `"branch":"main"`, `"branch":"`+strings.Repeat("x", maxShortString+1)+`"`, 1)},
		{"schema null", strings.Replace(validSummaryJSON, `"since"`, `"$schema":null,"since"`, 1)},
		{"schema non-string", strings.Replace(validSummaryJSON, `"since"`, `"$schema":true,"since"`, 1)},
		{"schema oversized", strings.Replace(validSummaryJSON, `"since"`, `"$schema":"`+strings.Repeat("x", maxSchemaBytes+1)+`","since"`, 1)},
		{"schema control", strings.Replace(validSummaryJSON, `"since"`, `"$schema":"bad\u0001schema","since"`, 1)},
		{"verdict pass rate mismatch", strings.Replace(validSummaryJSON, `"pass_rate":0.5`, `"pass_rate":0.6`, 1)},
		{"verdict resolution rate mismatch", strings.Replace(validSummaryJSON, `"resolution_rate":1`, `"resolution_rate":0.8`, 1)},
		{"agent pass rate mismatch", strings.Replace(validSummaryJSON, `"errors":0,"pass_rate":0.5`, `"errors":0,"pass_rate":0.6`, 1)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseSummary(test.body); err == nil {
				t.Fatal("malformed summary was accepted")
			}
		})
	}
	invalidUTF8 := string([]byte{'{', '"', 's', 'i', 'n', 'c', 'e', '"', ':', '"', 0xff, '"', '}'})
	if _, err := ParseSummary(invalidUTF8); err == nil {
		t.Fatal("invalid UTF-8 was accepted")
	}
}

func TestParseCostValidAndRejectsCoverageViolations(t *testing.T) {
	for _, body := range []string{validCostJSON, `{"total_usd":0,"jobs_with_cost":0,"jobs_total":0,"complete":false}`, `{"total_usd":0,"jobs_with_cost":1,"jobs_total":1,"complete":true}`, `{"$schema":"https://roborev.dev/schema/cost-v0.64","total_usd":0,"jobs_with_cost":0,"jobs_total":0,"complete":false}`} {
		if _, err := ParseCost(body); err != nil {
			t.Fatalf("valid cost rejected: %v", err)
		}
	}
	for _, body := range []string{
		`{"total_usd":-1,"jobs_with_cost":0,"jobs_total":0,"complete":false}`,
		`{"total_usd":0,"jobs_with_cost":2,"jobs_total":1,"complete":false}`,
		`{"total_usd":0,"jobs_with_cost":0,"jobs_total":0,"complete":true}`,
		`{"total_usd":0,"jobs_with_cost":1,"jobs_total":2,"complete":true}`,
		`{"total_usd":0,"jobs_with_cost":1,"jobs_total":1,"complete":false}`,
		`{"total_usd":0,"jobs_with_cost":1,"jobs_total":1,"complete":true}{}`,
		`{"total_usd":0,"jobs_with_cost":1,"jobs_total":1,"complete":true,"extra":1}`,
		`{"$schema":null,"total_usd":0,"jobs_with_cost":0,"jobs_total":0,"complete":false}`,
		`{"$schema":7,"total_usd":0,"jobs_with_cost":0,"jobs_total":0,"complete":false}`,
		`{"$schema":"` + strings.Repeat("x", maxSchemaBytes+1) + `","total_usd":0,"jobs_with_cost":0,"jobs_total":0,"complete":false}`,
		`{"$schema":"bad\u0001schema","total_usd":0,"jobs_with_cost":0,"jobs_total":0,"complete":false}`,
	} {
		if _, err := ParseCost(body); err == nil {
			t.Fatalf("invalid cost accepted: %s", body)
		}
	}
	if _, err := ParseCost(string([]byte{'{', '"', 't', 'o', 't', 'a', 'l', '_', 'u', 's', 'd', '"', ':', 0xff, '}'})); err == nil {
		t.Fatal("invalid UTF-8 cost was accepted")
	}
	if _, err := ParseCost(strings.Repeat("x", CostOutputLimit+1)); err == nil {
		t.Fatal("oversized cost was accepted")
	}
	if _, err := ParseSummary(strings.Repeat("x", SummaryOutputLimit+1)); err == nil {
		t.Fatal("oversized summary was accepted")
	}
}
