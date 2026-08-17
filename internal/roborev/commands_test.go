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

func TestReviewPreviewSeparatesRepoWithSpaces(t *testing.T) {
	got, err := ReviewPreview(`C:\Tools\roborev.exe`, ReviewRequest{RepoPath: `E:\한글 프로젝트\repo`, GitRef: "abc123", Panel: "dashboard_cross"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"review", "abc123", "--repo", `E:\한글 프로젝트\repo`, "--panel", "dashboard_cross"}
	if !reflect.DeepEqual(got.Args, want) {
		t.Fatalf("args mismatch: %#v", got.Args)
	}
}

func TestReviewPreviewRejectsUnknownPanel(t *testing.T) {
	_, err := ReviewPreview("roborev", ReviewRequest{RepoPath: "repo", GitRef: "HEAD", Panel: "$(calc)"})
	if err == nil {
		t.Fatal("expected panel allow-list error")
	}
}

func TestCapabilitiesGateDaemonContractsByVersion(t *testing.T) {
	if capabilities("v0.63.9").ReviewEnqueueAPI {
		t.Fatal("pre-v0.64 must not use the version-gated daemon enqueue API")
	}
	if !capabilities("v0.64.0").FrozenRerunAPI {
		t.Fatal("v0.64 must expose frozen-target rerun")
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
