package gitx

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type snapshotIgnoreInspectResponse struct {
	result proc.Result
	err    error
}

type snapshotIgnoreInspectRunner struct {
	root      string
	head      string
	branch    string
	detached  bool
	dirty     bool
	tracked   bool
	ignored   bool
	commands  []proc.Command
	overrides map[string]snapshotIgnoreInspectResponse
}

func (r *snapshotIgnoreInspectRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	role := snapshotIgnoreCommandRole(command.Args)
	if response, ok := r.overrides[role]; ok {
		return response.result, response.err
	}
	switch role {
	case "root":
		return proc.Result{Stdout: r.root, ExitCode: 0}, nil
	case "head":
		return proc.Result{Stdout: r.head, ExitCode: 0}, nil
	case "branch":
		if r.detached {
			return proc.Result{ExitCode: 1}, errors.New("process exited with code 1")
		}
		return proc.Result{Stdout: r.branch, ExitCode: 0}, nil
	case "status":
		if r.dirty {
			return proc.Result{Stdout: " M unrelated.txt\n", ExitCode: 0}, nil
		}
		return proc.Result{ExitCode: 0}, nil
	case "tracked":
		if r.tracked {
			return proc.Result{Stdout: "snapshot/file.json\n", ExitCode: 0}, nil
		}
		return proc.Result{ExitCode: 0}, nil
	case "ignore":
		if r.ignored {
			return proc.Result{ExitCode: 0}, nil
		}
		return proc.Result{ExitCode: 1}, errors.New("process exited with code 1")
	default:
		return proc.Result{ExitCode: 2, Stderr: "unexpected command"}, errors.New("unexpected Git command")
	}
}

func snapshotIgnoreCommandRole(args []string) string {
	if len(args) < 4 {
		return ""
	}
	switch {
	case args[2] == "rev-parse" && args[3] == "--show-toplevel":
		return "root"
	case args[2] == "rev-parse" && args[3] == "--verify":
		return "head"
	case args[2] == "symbolic-ref" && args[3] == "--quiet":
		return "branch"
	case args[2] == "status" && args[3] == "--porcelain=v1":
		return "status"
	case args[2] == "ls-files":
		return "tracked"
	case args[2] == "check-ignore":
		return "ignore"
	default:
		return ""
	}
}

func newSnapshotIgnoreInspectRunner(root string) *snapshotIgnoreInspectRunner {
	return &snapshotIgnoreInspectRunner{
		root:      root,
		head:      strings.Repeat("a", 40),
		branch:    "main",
		overrides: make(map[string]snapshotIgnoreInspectResponse),
	}
}

func inspectSnapshotIgnore(t *testing.T, service *Service, root, relative string) (SnapshotIgnoreRepositoryState, error) {
	t.Helper()
	state, continuation, err := service.InspectSnapshotIgnoreThroughTracked(context.Background(), root, relative)
	if err != nil || state.Tracked {
		return state, err
	}
	return service.FinishSnapshotIgnoreInspection(context.Background(), continuation)
}

func TestInspectSnapshotIgnoreUsesExactReadOnlyInventoryAndSanitizedEnvironment(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo with spaces", "한글")
	relative := filepath.Join("cache", "snap shots", "한글")
	t.Setenv("PATH", `C:\safe\bin`)
	t.Setenv("HOME", `C:\Users\safe-home`)
	t.Setenv("GIT_DIR", `C:\private\attacker\.git`)
	t.Setenv("GIT_WORK_TREE", `C:\private\attacker`)
	t.Setenv("GIT_INDEX_FILE", `C:\private\attacker\index`)
	t.Setenv("GIT_CONFIG_GLOBAL", `C:\private\attacker\.gitconfig`)
	t.Setenv("GIT_CONFIG_SYSTEM", `C:\private\attacker\system`)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", `C:\private\hooks`)
	runner := newSnapshotIgnoreInspectRunner(root)
	service := New(runner)
	service.Git = `C:\Program Files\Git\cmd\git.exe`

	state, err := inspectSnapshotIgnore(t, service, root, relative)
	if err != nil {
		t.Fatal(err)
	}
	if state.Root != filepath.Clean(root) || state.Head != strings.Repeat("a", 40) || state.Branch != "main" || state.Detached || state.Dirty || state.Tracked || state.Ignored {
		t.Fatalf("state=%#v", state)
	}
	wantArgs := [][]string{
		{"-C", root, "rev-parse", "--show-toplevel"},
		{"-C", root, "rev-parse", "--verify", "HEAD^{commit}"},
		{"-C", root, "symbolic-ref", "--quiet", "--short", "HEAD"},
		{"-C", root, "status", "--porcelain=v1", "--untracked-files=normal"},
		{"-C", root, "ls-files", "--", filepath.ToSlash(relative)},
		{"-C", root, "check-ignore", "--quiet", "--no-index", "--", filepath.ToSlash(filepath.Join(relative, ".roborev-ignore-check"))},
	}
	if len(runner.commands) != len(wantArgs) {
		t.Fatalf("commands=%#v want %d commands", runner.commands, len(wantArgs))
	}
	for index, command := range runner.commands {
		if command.Executable != service.Git || !reflect.DeepEqual(command.Args, wantArgs[index]) {
			t.Fatalf("command[%d]=%#v want executable=%q args=%#v", index, command, service.Git, wantArgs[index])
		}
		if command.Timeout != 10*time.Second || command.OutputLimit != 64<<10 {
			t.Fatalf("command[%d] bounds=%#v", index, command)
		}
		if !containsEnv(command.Env, `PATH=C:\safe\bin`) || !containsEnv(command.Env, `HOME=C:\Users\safe-home`) || !containsEnv(command.Env, "GIT_TERMINAL_PROMPT=0") || !containsEnv(command.Env, "GIT_OPTIONAL_LOCKS=0") {
			t.Fatalf("command[%d] safe environment=%#v", index, command.Env)
		}
		for _, entry := range command.Env {
			name, _, _ := strings.Cut(entry, "=")
			if strings.HasPrefix(strings.ToUpper(name), "GIT_") && name != "GIT_TERMINAL_PROMPT" && name != "GIT_OPTIONAL_LOCKS" {
				t.Fatalf("command[%d] inherited Git routing/configuration leaked: %q", index, entry)
			}
		}
	}
	for _, command := range runner.commands {
		lowerArgs := make([]string, len(command.Args))
		for index, arg := range command.Args {
			lowerArgs[index] = strings.ToLower(arg)
		}
		for _, forbidden := range []string{"init", "config", "add", "commit", "push", "review", "wait", "daemon"} {
			for _, arg := range lowerArgs {
				if arg == forbidden {
					t.Fatalf("mutation/lifecycle command appeared in inventory: %#v", command.Args)
				}
			}
		}
	}
}

func TestInspectSnapshotIgnoreCanonicalizesRootAndPreservesWindowsRelativeSeparators(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	input := filepath.Join(root, ".", "nested", "..")
	relative := filepath.Join("cache", "nested folder", "한글")
	runner := newSnapshotIgnoreInspectRunner(root + string(filepath.Separator))
	state, err := inspectSnapshotIgnore(t, New(runner), input, relative)
	if err != nil {
		t.Fatal(err)
	}
	if state.Root != filepath.Clean(root) {
		t.Fatalf("canonical root=%q want=%q", state.Root, filepath.Clean(root))
	}
	if len(runner.commands) != 6 {
		t.Fatalf("commands=%#v", runner.commands)
	}
	for _, command := range runner.commands {
		if command.Args[1] != filepath.Clean(input) {
			t.Fatalf("Git -C root=%#v", command.Args)
		}
	}
	tracked := runner.commands[4].Args
	ignore := runner.commands[5].Args
	if len(tracked) != 5 || tracked[4] != filepath.ToSlash(relative) {
		t.Fatalf("tracked path argument=%#v want=%q", tracked, filepath.ToSlash(relative))
	}
	wantProbe := filepath.ToSlash(filepath.Join(relative, ".roborev-ignore-check"))
	if len(ignore) != 7 || ignore[6] != wantProbe {
		t.Fatalf("ignore probe argument=%#v want=%q", ignore, wantProbe)
	}
}

func TestInspectSnapshotIgnoreAccepts40And64CharacterFullCommitSHA(t *testing.T) {
	for _, length := range []int{40, 64} {
		t.Run(string(rune('0'+length/40)), func(t *testing.T) {
			root := t.TempDir()
			runner := newSnapshotIgnoreInspectRunner(root)
			runner.head = strings.Repeat("a", length)
			state, err := inspectSnapshotIgnore(t, New(runner), root, ".roborev")
			if err != nil || state.Head != runner.head {
				t.Fatalf("state=%#v err=%v", state, err)
			}
		})
	}
}

func TestInspectSnapshotIgnoreRejectsInvalidCommitSHA(t *testing.T) {
	for _, head := range []string{
		strings.Repeat("a", 39),
		strings.Repeat("a", 41),
		strings.Repeat("a", 63),
		strings.Repeat("a", 65),
		strings.Repeat("g", 40),
		string([]byte{0xff}),
	} {
		t.Run("invalid", func(t *testing.T) {
			root := t.TempDir()
			runner := newSnapshotIgnoreInspectRunner(root)
			runner.head = head
			if _, err := inspectSnapshotIgnore(t, New(runner), root, ".roborev"); err == nil {
				t.Fatalf("invalid head %q was accepted", head)
			}
		})
	}
}

func TestInspectSnapshotIgnoreDistinguishesKnownBranchDetachedAndDirtyState(t *testing.T) {
	tests := []struct {
		name       string
		branch     string
		detached   bool
		dirty      bool
		wantBranch string
	}{
		{name: "known branch and clean", branch: "feature/topic\n", wantBranch: "feature/topic"},
		{name: "known branch and dirty", branch: "main", dirty: true, wantBranch: "main"},
		{name: "detached", detached: true, wantBranch: "detached"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			runner := newSnapshotIgnoreInspectRunner(root)
			runner.branch, runner.detached, runner.dirty = test.branch, test.detached, test.dirty
			state, err := inspectSnapshotIgnore(t, New(runner), root, ".roborev")
			if err != nil {
				t.Fatal(err)
			}
			if state.Branch != test.wantBranch || state.Detached != test.detached || state.Dirty != test.dirty {
				t.Fatalf("state=%#v", state)
			}
		})
	}
}

func TestInspectSnapshotIgnoreRejectsBlankAndMultilineBranch(t *testing.T) {
	for _, branch := range []string{"", " \t\n", "feature\nother", "feature\r\nother"} {
		t.Run("invalid", func(t *testing.T) {
			root := t.TempDir()
			runner := newSnapshotIgnoreInspectRunner(root)
			runner.branch = branch
			if _, err := inspectSnapshotIgnore(t, New(runner), root, ".roborev"); err == nil {
				t.Fatalf("branch %q was accepted", branch)
			}
		})
	}
}

func TestInspectSnapshotIgnoreTrackedFilesShortCircuitBeforeCheckIgnore(t *testing.T) {
	root := t.TempDir()
	runner := newSnapshotIgnoreInspectRunner(root)
	runner.tracked = true
	runner.overrides["ignore"] = snapshotIgnoreInspectResponse{result: proc.Result{ExitCode: 2, Stderr: "must not run"}, err: errors.New("must not run")}
	state, err := inspectSnapshotIgnore(t, New(runner), root, filepath.Join(".roborev", "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	if !state.Tracked || state.Ignored {
		t.Fatalf("state=%#v", state)
	}
	for _, command := range runner.commands {
		if snapshotIgnoreCommandRole(command.Args) == "ignore" {
			t.Fatalf("check-ignore ran after tracked file was found: %#v", runner.commands)
		}
	}
}

func TestSnapshotIgnoreContinuationStagesAreSingleUseAndBound(t *testing.T) {
	root := t.TempDir()

	t.Run("tracked stage stops at five commands", func(t *testing.T) {
		runner := newSnapshotIgnoreInspectRunner(root)
		runner.tracked = true
		service := New(runner)
		state, continuation, err := service.InspectSnapshotIgnoreThroughTracked(context.Background(), root, ".roborev")
		if err != nil || !state.Tracked {
			t.Fatalf("stage1 state=%#v err=%v", state, err)
		}
		if len(runner.commands) != 5 {
			t.Fatalf("stage1 commands=%d want 5", len(runner.commands))
		}
		if _, err := service.FinishSnapshotIgnoreInspection(context.Background(), continuation); err == nil {
			t.Fatal("tracked continuation unexpectedly ran check-ignore")
		}
		if len(runner.commands) != 5 {
			t.Fatalf("tracked finish commands=%d want 5", len(runner.commands))
		}
	})

	t.Run("zero continuation is rejected without a command", func(t *testing.T) {
		runner := newSnapshotIgnoreInspectRunner(root)
		service := New(runner)
		if _, err := service.FinishSnapshotIgnoreInspection(context.Background(), SnapshotIgnoreContinuation{}); err == nil {
			t.Fatal("zero continuation unexpectedly accepted")
		}
		if len(runner.commands) != 0 {
			t.Fatalf("zero continuation commands=%d want 0", len(runner.commands))
		}
	})

	t.Run("wrong service cannot consume continuation", func(t *testing.T) {
		runner := newSnapshotIgnoreInspectRunner(root)
		service := New(runner)
		_, continuation, err := service.InspectSnapshotIgnoreThroughTracked(context.Background(), root, ".roborev")
		if err != nil {
			t.Fatal(err)
		}
		otherRunner := newSnapshotIgnoreInspectRunner(root)
		other := New(otherRunner)
		if _, err := other.FinishSnapshotIgnoreInspection(context.Background(), continuation); err == nil {
			t.Fatal("wrong service unexpectedly consumed continuation")
		}
		if len(otherRunner.commands) != 0 {
			t.Fatalf("wrong service commands=%d want 0", len(otherRunner.commands))
		}
		if _, err := service.FinishSnapshotIgnoreInspection(context.Background(), continuation); err != nil {
			t.Fatalf("original service could not finish continuation: %v", err)
		}
		if len(runner.commands) != 6 {
			t.Fatalf("original service commands=%d want 6", len(runner.commands))
		}
	})

	t.Run("normal finish runs exact sixth check-ignore once", func(t *testing.T) {
		runner := newSnapshotIgnoreInspectRunner(root)
		service := New(runner)
		_, continuation, err := service.InspectSnapshotIgnoreThroughTracked(context.Background(), root, filepath.FromSlash("cache/snapshots"))
		if err != nil {
			t.Fatal(err)
		}
		if len(runner.commands) != 5 {
			t.Fatalf("stage1 commands=%d want 5", len(runner.commands))
		}
		state, err := service.FinishSnapshotIgnoreInspection(context.Background(), continuation)
		if err != nil || state.Ignored {
			t.Fatalf("finish state=%#v err=%v", state, err)
		}
		if len(runner.commands) != 6 || snapshotIgnoreCommandRole(runner.commands[5].Args) != "ignore" {
			t.Fatalf("finish commands=%#v", runner.commands)
		}
		if _, err := service.FinishSnapshotIgnoreInspection(context.Background(), continuation); err == nil {
			t.Fatal("reused continuation unexpectedly accepted")
		}
		if len(runner.commands) != 6 {
			t.Fatalf("reused continuation commands=%d want 6", len(runner.commands))
		}
	})
}

func TestInspectSnapshotIgnoreHandlesCheckIgnoreExitStates(t *testing.T) {
	tests := []struct {
		name        string
		result      proc.Result
		err         error
		wantIgnored bool
		wantError   bool
	}{
		{name: "ignored", result: proc.Result{ExitCode: 0}, wantIgnored: true},
		{name: "not ignored", result: proc.Result{ExitCode: 1}, err: errors.New("process exited with code 1")},
		{name: "unexpected exit", result: proc.Result{ExitCode: 2}, err: errors.New("process exited with code 2"), wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			runner := newSnapshotIgnoreInspectRunner(root)
			runner.overrides["ignore"] = snapshotIgnoreInspectResponse{result: test.result, err: test.err}
			state, err := inspectSnapshotIgnore(t, New(runner), root, ".roborev")
			if test.wantError {
				if err == nil {
					t.Fatalf("unexpected check-ignore exit was accepted: state=%#v", state)
				}
				return
			}
			if err != nil || state.Ignored != test.wantIgnored {
				t.Fatalf("state=%#v err=%v", state, err)
			}
		})
	}
}

func TestInspectSnapshotIgnoreFailsClosedOnTruncationInvalidUTF8AndAmbiguousOutput(t *testing.T) {
	roles := []string{"root", "head", "branch", "status", "tracked", "ignore"}
	for _, role := range roles {
		for _, kind := range []string{"stdout truncated", "stderr truncated", "stdout invalid UTF-8", "stderr invalid UTF-8", "raw stderr", "command error"} {
			t.Run(role+"/"+kind, func(t *testing.T) {
				root := t.TempDir()
				runner := newSnapshotIgnoreInspectRunner(root)
				response := snapshotIgnoreInspectResponse{result: proc.Result{ExitCode: 0}}
				switch kind {
				case "stdout truncated":
					response.result.StdoutTruncated = true
				case "stderr truncated":
					response.result.StderrTruncated = true
				case "stdout invalid UTF-8":
					response.result.Stdout = string([]byte{0xff, 0xfe})
				case "stderr invalid UTF-8":
					response.result.Stderr = string([]byte{0xff, 0xfe})
				case "raw stderr":
					response.result.Stderr = "PRIVATE_STDERR_MARKER"
				case "command error":
					response.err = errors.New("private command ambiguity")
				}
				if role == "branch" && kind != "stdout invalid UTF-8" {
					response.result.Stdout = "main"
				}
				if role == "root" && kind != "stdout invalid UTF-8" {
					response.result.Stdout = root
				}
				if role == "head" && kind != "stdout invalid UTF-8" {
					response.result.Stdout = strings.Repeat("a", 40)
				}
				if role == "ignore" {
					response.result.ExitCode = 0
				}
				runner.overrides[role] = response
				state, err := inspectSnapshotIgnore(t, New(runner), root, ".roborev")
				if err == nil || state != (SnapshotIgnoreRepositoryState{}) {
					t.Fatalf("ambiguous Git evidence accepted: state=%#v err=%v", state, err)
				}
				if strings.Contains(err.Error(), "PRIVATE_STDERR_MARKER") || strings.Contains(err.Error(), "private command") {
					t.Fatalf("raw Git diagnostic leaked: %v", err)
				}
			})
		}
	}
}
