package roborev

import (
	"context"
	"reflect"
	"testing"

	"github.com/froze/roborev-dashboard/internal/process"
)

func TestCommentArgsKeepUserTextInOneArgument(t *testing.T) {
	comment := "한글 복사 붙여넣기; $(danger) & whoami\nsecond line"
	got, err := CommentArgs(42, comment)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"comment", "--job", "42", "--message", comment, "--commenter", "roborev-dashboard"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestValidateReviewRequestAcceptsPinnedTarget(t *testing.T) {
	err := ValidateReviewRequest(ReviewRequest{RepoPath: `E:\한글 프로젝트\repo`, GitRef: "abc123", Branch: "main", Panel: "dashboard_cross"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateReviewRequestAcceptsDynamicPanelName(t *testing.T) {
	if err := ValidateReviewRequest(ReviewRequest{RepoPath: "repo", GitRef: "HEAD", Panel: "team-specific"}); err != nil {
		t.Fatal(err)
	}
}

func TestFixArgsUseTheV064Contract(t *testing.T) {
	got, err := FixArgs(49, "codex")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"fix", "--agent", "codex", "--quiet", "49"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch\n got: %#v\nwant: %#v", got, want)
	}
	if _, err := FixArgs(49, "$(arbitrary)"); err == nil {
		t.Fatal("arbitrary fix agent was accepted")
	}
}

func TestValidateReviewRequestRejectsEmptyAndControlPanel(t *testing.T) {
	for _, panel := range []string{"", " \t", "bad\nname"} {
		if err := ValidateReviewRequest(ReviewRequest{RepoPath: "repo", GitRef: "HEAD", Panel: panel}); err == nil {
			t.Fatalf("expected invalid panel %q", panel)
		}
	}
}

func TestCapabilitiesGateDaemonContractsByVersion(t *testing.T) {
	if capabilities("v0.63.9").ReviewEnqueueAPI {
		t.Fatal("pre-v0.64 must not use the version-gated daemon enqueue API")
	}
	if !capabilities("v0.64.0").FrozenRerunAPI {
		t.Fatal("v0.64 must expose frozen-target rerun")
	}
	if !capabilities("v0.64.9").FixCommand || capabilities("v0.65.0").FixCommand {
		t.Fatal("fix command must be gated to v0.64.x")
	}
}

func TestMalformedMachineOutputIsRejected(t *testing.T) {
	if _, err := decodeRaw("not-json"); err == nil {
		t.Fatal("expected malformed JSON error")
	}
}

func TestPanelMembersRejectsUntrustedRunIdentifier(t *testing.T) {
	client, err := NewClient("roborev", fakeRunner{}, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.PanelMembers(context.Background(), "../../api/secrets"); err == nil {
		t.Fatal("expected invalid panel run identifier error")
	}
}

type fakeRunner struct{}

func (fakeRunner) Run(context.Context, process.Command) (process.Result, error) {
	return process.Result{}, nil
}
