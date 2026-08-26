package roborev

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/froze/roborev-dashboard/internal/process"
)

func TestSkillsInstallArgsUsesExactSingleAgentCustomPathCommand(t *testing.T) {
	path, _ := filepath.Abs(filepath.Join("fake-home", ".codex", "skills"))
	for _, agent := range []string{"codex", "claude", "grok", "droid"} {
		args, err := SkillsInstallArgs(agent, path)
		if err != nil {
			t.Fatalf("agent=%s: %v", agent, err)
		}
		want := []string{"skills", "install", "--path", filepath.Clean(path), "--agent", agent}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("agent=%s args=%#v want=%#v", agent, args, want)
		}
	}
}

func TestSkillsInstallArgsRejectsBroadOrBrowserShapedTargets(t *testing.T) {
	for _, test := range []struct {
		name  string
		agent string
		path  string
	}{
		{name: "unsupported agent", agent: "all", path: filepath.Join("fake", "skills")},
		{name: "relative path", agent: "codex", path: filepath.Join("fake", "skills")},
		{name: "control character", agent: "codex", path: filepath.Join(string(filepath.Separator), "fake\nskills")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if args, err := SkillsInstallArgs(test.agent, test.path); err == nil || args != nil {
				t.Fatalf("args=%#v err=%v", args, err)
			}
		})
	}
}

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

func TestShowArgsUseTheV064Contract(t *testing.T) {
	got, err := ShowArgs(42)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"show", "--job", "42", "--json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch\n got: %#v\nwant: %#v", got, want)
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
	if capabilities("v0.64.0").PromptLazyLoad != true || !capabilities("v0.64.0").RawLog || capabilities("v0.63.9").PromptLazyLoad || capabilities("v0.63.9").RawLog {
		t.Fatal("lazy prompt/log capabilities must be gated to exact v0.64.0")
	}
	if !capabilities("v0.64.0").FrozenRerunAPI {
		t.Fatal("v0.64 must expose frozen-target rerun")
	}
	if !capabilities("v0.64.0").AnalyzeIntrospection || capabilities("v0.65.0").AnalyzeIntrospection {
		t.Fatal("analysis introspection must be gated to exact v0.64.0")
	}
	if capabilities("v0.64.9").FixCommand || capabilities("v0.65.0").FixCommand {
		t.Fatal("fix command must be gated to the exact verified v0.64.0 policy")
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
