package insights

import (
	"strings"
	"testing"
)

func TestBuildCommandExactArgv(t *testing.T) {
	all, err := BuildCommand(`C:\RoboRev\roborev.exe`, Request{RepoPath: `E:\repo`, Window: Window30d, BranchScope: BranchAll})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(all.Args, "\x00"), strings.Join([]string{"insights", "--repo", `E:\repo`, "--since", "30d", "--json"}, "\x00"); got != want {
		t.Fatalf("all argv = %q, want %q", got, want)
	}
	current, err := BuildCommand(`C:\RoboRev\roborev.exe`, Request{RepoPath: `E:\repo`, Window: Window90d, BranchScope: BranchCurrent, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(current.Args, "\x00"), strings.Join([]string{"insights", "--repo", `E:\repo`, "--branch", "main", "--since", "90d", "--json"}, "\x00"); got != want {
		t.Fatalf("current argv = %q, want %q", got, want)
	}
	for _, forbidden := range []string{"--wait", "--agent", "--model", "--reasoning", "--agentic", "--yolo"} {
		if strings.Contains(strings.Join(current.Args, "\x00"), forbidden) {
			t.Fatalf("argv contains forbidden %q", forbidden)
		}
	}
}

func TestParseReceiptStrictShapes(t *testing.T) {
	valid, err := ParseReceipt([]byte(`{"job_id":42,"agent":"codex","since":"2026-08-23T12:00:00Z"}`))
	if err != nil || valid.JobID != 42 || valid.Agent != "codex" {
		t.Fatalf("valid receipt = %#v, err=%v", valid, err)
	}
	skip, err := ParseReceipt([]byte(`{"skipped":true,"reason":"no failing reviews","since":"2026-08-23T12:00:00Z"}`))
	if err != nil || !skip.Skipped || skip.Reason == "" {
		t.Fatalf("valid skip = %#v, err=%v", skip, err)
	}
	for _, raw := range []string{
		`{"job_id":-1,"agent":"codex","since":"2026-08-23T12:00:00Z"}`,
		`{"job_id":0,"agent":"codex","since":"2026-08-23T12:00:00Z"}`,
		`{"job_id":9007199254740992,"agent":"codex","since":"2026-08-23T12:00:00Z"}`,
		`{"job_id":42,"agent":"","since":"2026-08-23T12:00:00Z"}`,
		`{"job_id":42,"agent":"codex","since":"not-time"}`,
		`{"skipped":false,"reason":"none","since":"2026-08-23T12:00:00Z"}`,
		`{"skipped":true,"reason":"","since":"2026-08-23T12:00:00Z"}`,
		`{"job_id":42,"agent":"codex","since":"2026-08-23T12:00:00Z","extra":1}`,
		`{"job_id":42,"agent":"codex","since":"2026-08-23T12:00:00Z"}{}`,
		`{"job_id":42,"job_id":43,"agent":"codex","since":"2026-08-23T12:00:00Z"}`,
	} {
		if _, err := ParseReceipt([]byte(raw)); err == nil {
			t.Errorf("accepted malformed receipt %s", raw)
		}
	}
	if _, err := ParseReceipt([]byte(`{"job_id":42,"agent":"` + strings.Repeat("a", MaxReceiptString+1) + `","since":"2026-08-23T12:00:00Z"}`)); err == nil {
		t.Fatal("accepted oversized receipt string")
	}
	if _, err := ParseReceipt([]byte(`{"skipped":true,"reason":"` + strings.Repeat("a", MaxReceiptBytes) + `","since":"2026-08-23T12:00:00Z"}`)); err == nil {
		t.Fatal("accepted oversized receipt output")
	}
}
