package gitx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type recordingRunner struct {
	commands []proc.Command
	stdout   string
	err      error
}

type hooksPathRunner struct {
	commands  []proc.Command
	outputs   map[string]string
	errors    map[string]error
	exitCode  map[string]int
	showRoots []string
}

func (r *hooksPathRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	key := strings.Join(command.Args[2:], " ")
	stdout := r.outputs[key]
	if key == "rev-parse --show-toplevel" && len(r.showRoots) > 0 {
		stdout = r.showRoots[0]
		r.showRoots = r.showRoots[1:]
	}
	result := proc.Result{Stdout: stdout, ExitCode: r.exitCode[key]}
	if err := r.errors[key]; err != nil {
		return result, err
	}
	return result, nil
}

func TestHooksPathUsesGitPathAndSanitizedEnvironment(t *testing.T) {
	t.Setenv("HOME", `C:\Users\hook-home`)
	t.Setenv("USERPROFILE", `C:\Users\hook-user`)
	t.Setenv("HOMEDRIVE", `C:`)
	t.Setenv("HOMEPATH", `\Users\hook-user`)
	t.Setenv("GIT_DIR", `C:\attacker\git`)
	t.Setenv("GIT_CONFIG_GLOBAL", `C:\attacker\config`)
	t.Setenv("HOOK_STATUS_SECRET", "must-not-leak")
	root := filepath.Join(t.TempDir(), "repo")
	runner := &hooksPathRunner{outputs: map[string]string{
		"rev-parse --git-path hooks": ".githooks",
		"rev-parse --git-dir":        filepath.Join(root, ".git"),
		"rev-parse --git-common-dir": filepath.Join(root, ".git"),
		"rev-parse --show-toplevel":  root,
	}}
	service := New(runner)
	got, err := service.HooksPath(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".githooks"); got != want {
		t.Fatalf("hooks path=%q want=%q", got, want)
	}
	if len(runner.commands) != 6 {
		t.Fatalf("commands=%#v", runner.commands)
	}
	var command proc.Command
	for _, candidate := range runner.commands {
		if strings.Contains(strings.Join(candidate.Args, " "), "rev-parse --git-path hooks") {
			command = candidate
			break
		}
	}
	if command.Executable == "" {
		t.Fatalf("hooks path command missing: %#v", runner.commands)
	}
	if !reflect.DeepEqual(command.Args, []string{"-C", root, "rev-parse", "--git-path", "hooks"}) {
		t.Fatalf("unexpected args=%#v", command.Args)
	}
	for _, entry := range command.Env {
		if strings.HasPrefix(strings.ToUpper(entry), "GIT_DIR=") || strings.HasPrefix(strings.ToUpper(entry), "GIT_WORK_TREE=") {
			t.Fatalf("unsafe inherited Git environment: %q", entry)
		}
	}
	if !containsEnv(command.Env, "GIT_CONFIG_NOSYSTEM=1") || !containsEnv(command.Env, "GIT_CONFIG_GLOBAL=") || !containsEnv(command.Env, "GIT_CONFIG_COUNT=0") {
		t.Fatalf("missing config isolation env: %#v", command.Env)
	}
	for _, expected := range []string{
		`HOME=C:\Users\hook-home`,
		`USERPROFILE=C:\Users\hook-user`,
		`HOMEDRIVE=C:`,
		`HOMEPATH=\Users\hook-user`,
	} {
		if !containsEnv(command.Env, expected) {
			t.Fatalf("home-location variable %q missing from resolver env: %#v", expected, command.Env)
		}
	}
	for _, entry := range command.Env {
		upper := strings.ToUpper(entry)
		if strings.HasPrefix(upper, "GIT_DIR=") || strings.HasPrefix(upper, "GIT_CONFIG_GLOBAL=C:") || strings.HasPrefix(upper, "HOOK_STATUS_SECRET=") {
			t.Fatalf("unsafe resolver environment entry: %q", entry)
		}
	}
}

func TestHooksPathForwardsSafeDirectoryResetValuesAtCommandScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	runner := &hooksPathRunner{outputs: map[string]string{
		"config --system --includes -z --get-all safe.directory": "C:/trusted\x00\x00",
		"config --global --includes -z --get-all safe.directory": "D:/trusted\x00",
		"rev-parse --git-path hooks":                             filepath.Join(root, ".git", "hooks"),
	}}
	if _, err := New(runner).HooksPath(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	var resolverCommand *proc.Command
	for index := range runner.commands {
		if strings.Contains(strings.Join(runner.commands[index].Args, " "), "rev-parse --git-path hooks") {
			resolverCommand = &runner.commands[index]
			break
		}
	}
	if resolverCommand == nil {
		t.Fatalf("resolver command missing: %#v", runner.commands)
	}
	if !containsEnv(resolverCommand.Env, "GIT_CONFIG_COUNT=3") ||
		!containsEnv(resolverCommand.Env, "GIT_CONFIG_KEY_0=safe.directory") ||
		!containsEnv(resolverCommand.Env, "GIT_CONFIG_VALUE_0=C:/trusted") ||
		!containsEnv(resolverCommand.Env, "GIT_CONFIG_VALUE_1=") ||
		!containsEnv(resolverCommand.Env, "GIT_CONFIG_VALUE_2=D:/trusted") {
		t.Fatalf("safe.directory forwarding=%#v", resolverCommand.Env)
	}
}

func TestHooksPathResolvesLinkedWorktreeMainRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "main")
	common := filepath.Join(root, ".git")
	worktree := filepath.Join(common, "worktrees", "feature")
	runner := &hooksPathRunner{outputs: map[string]string{
		"rev-parse --git-path hooks": ".githooks",
		"rev-parse --git-dir":        worktree,
		"rev-parse --git-common-dir": common,
	}}
	got, err := New(runner).HooksPath(context.Background(), filepath.Join(root, "feature"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".githooks"); got != want {
		t.Fatalf("hooks path=%q want=%q", got, want)
	}
}

func TestHooksPathResolvesSeparateGitDirThroughCoreWorktree(t *testing.T) {
	base := t.TempDir()
	worktree := filepath.Join(base, "checkout")
	mainRoot := filepath.Join(base, "main")
	common := filepath.Join(base, "metadata")
	gitDir := filepath.Join(common, "worktrees", "checkout")
	configFile := filepath.Join(common, "config")
	runner := &hooksPathRunner{outputs: map[string]string{
		"rev-parse --git-path hooks":                        ".githooks",
		"rev-parse --git-dir":                               gitDir,
		"rev-parse --git-common-dir":                        common,
		"config --file " + configFile + " --bool core.bare": "false",
		"config --file " + configFile + " core.worktree":    filepath.Join("..", "main"),
	}}
	got, err := New(runner).HooksPath(context.Background(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(mainRoot, ".githooks"); got != want {
		t.Fatalf("hooks path=%q want=%q", got, want)
	}
}

func TestRegistrationRootUsesSelectedRootForOrdinaryCheckout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ordinary")
	runner := &hooksPathRunner{outputs: map[string]string{
		"rev-parse --show-toplevel":  root,
		"rev-parse --git-dir":        filepath.Join(root, ".git"),
		"rev-parse --git-common-dir": filepath.Join(root, ".git"),
	}}
	got, err := New(runner).RegistrationRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(root) {
		t.Fatalf("registration root=%q want=%q", got, filepath.Clean(root))
	}
}

func TestRegistrationRootPreservesLinkedMainRootSpelling(t *testing.T) {
	base := t.TempDir()
	selected := filepath.Join(base, "checkout")
	mainRoot := filepath.Join(base, "main")
	common := filepath.Join(base, "metadata")
	gitDir := filepath.Join(common, "worktrees", "checkout")
	configFile := filepath.Join(common, "config")
	runner := &hooksPathRunner{outputs: map[string]string{
		"rev-parse --show-toplevel":                         selected,
		"rev-parse --git-dir":                               gitDir,
		"rev-parse --git-common-dir":                        common,
		"config --file " + configFile + " --bool core.bare": "false",
		"config --file " + configFile + " core.worktree":    filepath.Join("..", "main"),
	}, showRoots: []string{selected, mainRoot}}
	got, err := New(runner).RegistrationRoot(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(mainRoot) {
		t.Fatalf("registration root=%q want=%q", got, filepath.Clean(mainRoot))
	}
}

func TestRegistrationRootUsesOrdinaryLinkedWorktreeMainCheckout(t *testing.T) {
	base := t.TempDir()
	selected := filepath.Join(base, "feature")
	mainRoot := filepath.Join(base, "main")
	common := filepath.Join(mainRoot, ".git")
	gitDir := filepath.Join(common, "worktrees", "feature")
	runner := &hooksPathRunner{outputs: map[string]string{
		"rev-parse --git-dir":        gitDir,
		"rev-parse --git-common-dir": common,
	}, showRoots: []string{selected, mainRoot}}
	got, err := New(runner).RegistrationRoot(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(mainRoot) {
		t.Fatalf("registration root=%q want main checkout=%q", got, filepath.Clean(mainRoot))
	}
}

func TestRegistrationRootKeepsBareBackedLinkedCheckout(t *testing.T) {
	base := t.TempDir()
	selected := filepath.Join(base, "feature")
	common := filepath.Join(base, "bare.git")
	gitDir := filepath.Join(common, "worktrees", "feature")
	configFile := filepath.Join(common, "config")
	runner := &hooksPathRunner{outputs: map[string]string{
		"rev-parse --git-dir":                               gitDir,
		"rev-parse --git-common-dir":                        common,
		"config --file " + configFile + " --bool core.bare": "true",
	}, showRoots: []string{selected, selected, selected}}
	got, err := New(runner).RegistrationRoot(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(selected) {
		t.Fatalf("registration root=%q want selected checkout=%q", got, filepath.Clean(selected))
	}
}

func TestResolveHookLayoutReadsEffectiveCoreHooksPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	gitDir := filepath.Join(root, ".git")
	hooks := filepath.Join(gitDir, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &hooksPathRunner{outputs: map[string]string{
		"rev-parse --show-toplevel":  root,
		"rev-parse --git-dir":        gitDir,
		"rev-parse --git-common-dir": gitDir,
		"rev-parse --git-path hooks": hooks,
		"config core.hooksPath":      "relative-hooks",
	}}
	layout, err := New(runner).ResolveHookLayout(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !layout.EffectiveHooksPathSet || !layout.EffectiveHooksPathIsRelative || layout.EffectiveHooksPath != "relative-hooks" || !layout.Safe {
		t.Fatalf("layout=%#v", layout)
	}
	for _, command := range runner.commands {
		if strings.Contains(strings.Join(command.Args, " "), "core.hooksPath") && !reflect.DeepEqual(command.Args, []string{"-C", root, "config", "core.hooksPath"}) {
			t.Fatalf("effective config args=%#v", command.Args)
		}
	}
}

func TestResolveHookLayoutTreatsUnsetConfigAsSafeDefault(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	gitDir := filepath.Join(root, ".git")
	hooks := filepath.Join(gitDir, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &hooksPathRunner{outputs: map[string]string{
		"rev-parse --show-toplevel":  root,
		"rev-parse --git-dir":        gitDir,
		"rev-parse --git-common-dir": gitDir,
		"rev-parse --git-path hooks": hooks,
	}, errors: map[string]error{"config core.hooksPath": errors.New("exit status 1")}, exitCode: map[string]int{"config core.hooksPath": 1}}
	layout, err := New(runner).ResolveHookLayout(context.Background(), root)
	if err != nil || layout.EffectiveHooksPathSet || !layout.Safe {
		t.Fatalf("unset config layout=%#v err=%v", layout, err)
	}
}

func TestResolveHookLayoutFailsClosedOnConfigReadError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	gitDir := filepath.Join(root, ".git")
	hooks := filepath.Join(gitDir, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &hooksPathRunner{outputs: map[string]string{
		"rev-parse --show-toplevel":  root,
		"rev-parse --git-dir":        gitDir,
		"rev-parse --git-common-dir": gitDir,
		"rev-parse --git-path hooks": hooks,
	}, errors: map[string]error{"config core.hooksPath": errors.New("permission denied")}, exitCode: map[string]int{"config core.hooksPath": 2}}
	if _, err := New(runner).ResolveHookLayout(context.Background(), root); err == nil {
		t.Fatal("non-unset config error was treated as an absent key")
	}
}

func TestIsTildePathMirrorsKitUserHomeForms(t *testing.T) {
	for _, value := range []string{"~", "~/hooks", `~\hooks`, "~alice/hooks", "~_shared/hooks"} {
		if !isTildePath(value) {
			t.Errorf("isTildePath(%q)=false", value)
		}
	}
	for _, value := range []string{"relative/hooks", "./hooks", "C:/hooks"} {
		if isTildePath(value) {
			t.Errorf("isTildePath(%q)=true", value)
		}
	}
}

func TestHooksPathNormalizesMSYSAbsoluteOutput(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("MSYS path conversion is Windows-specific")
	}
	runner := &hooksPathRunner{outputs: map[string]string{"rev-parse --git-path hooks": "/c/Workspace/hooks"}}
	got, err := New(runner).HooksPath(context.Background(), `C:\Workspace`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `C:\Workspace\hooks` {
		t.Fatalf("hooks path=%q", got)
	}
}

func containsEnv(env []string, expected string) bool {
	for _, entry := range env {
		if entry == expected {
			return true
		}
	}
	return false
}

func (r *recordingRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	return proc.Result{Stdout: r.stdout}, r.err
}

type inspectRunner struct {
	commands []proc.Command
	root     string
	branch   string
}

type ciWorkflowInspectRunner struct {
	root       string
	branch     string
	head       string
	dirty      bool
	ignored    bool
	detached   bool
	commands   []proc.Command
	malformed  string
	truncated  bool
	branchExit int
	configExit int
	failRoot   bool
	safeSystem string
	safeGlobal string
}

func (r *ciWorkflowInspectRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	args := command.Args
	if len(args) >= 3 && args[2] == "config" {
		if r.configExit != 0 {
			return proc.Result{ExitCode: r.configExit, Stderr: `C:\private\config-error`}, errors.New("private config failure")
		}
		if len(args) >= 4 && args[3] == "--system" {
			return proc.Result{Stdout: r.safeSystem, ExitCode: 0}, nil
		}
		return proc.Result{Stdout: r.safeGlobal, ExitCode: 0}, nil
	}
	if len(args) >= 4 && args[2] == "rev-parse" && args[3] == "--show-toplevel" {
		if r.failRoot {
			return proc.Result{ExitCode: 2, Stderr: `C:\private\repo-error`}, errors.New("private root failure")
		}
		return proc.Result{Stdout: r.root, ExitCode: 0}, nil
	}
	if len(args) >= 5 && args[2] == "rev-parse" && args[3] == "--verify" {
		result := proc.Result{Stdout: r.head, ExitCode: 0}
		if r.malformed == "head" {
			result.Stdout = "not-a-sha"
		} else if r.malformed == "utf8" {
			result.Stdout = string([]byte{0xff, 0xfe})
		}
		if r.truncated {
			result.StdoutTruncated = true
		}
		return result, nil
	}
	if len(args) >= 6 && args[2] == "symbolic-ref" && args[3] == "--quiet" {
		if r.detached {
			return proc.Result{ExitCode: 1}, errors.New("process exited with code 1")
		}
		return proc.Result{Stdout: r.branch, ExitCode: r.branchExit}, nil
	}
	if len(args) >= 5 && args[2] == "status" && args[3] == "--porcelain=v1" {
		status := ""
		if r.dirty {
			status = " M file.txt\n"
		}
		return proc.Result{Stdout: status, ExitCode: 0}, nil
	}
	if len(args) >= 7 && args[2] == "check-ignore" {
		if r.ignored {
			return proc.Result{ExitCode: 0}, nil
		}
		return proc.Result{ExitCode: 1}, errors.New("process exited with code 1")
	}
	return proc.Result{}, errors.New("unexpected Git command")
}

func TestInspectCIWorkflowUsesFixedTargetAndSanitizedTypedCommands(t *testing.T) {
	t.Setenv("GIT_DIR", `C:\attacker\git`)
	t.Setenv("GIT_WORK_TREE", `C:\attacker\worktree`)
	t.Setenv("GIT_CONFIG_GLOBAL", `C:\attacker\config`)
	t.Setenv("OPENAI_API_KEY", "secret")
	root := t.TempDir()
	runner := &ciWorkflowInspectRunner{root: root, branch: "main", head: strings.Repeat("a", 40)}
	state, err := New(runner).InspectCIWorkflow(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if state.Root != root || state.Branch != "main" || state.Detached || state.Head != strings.Repeat("a", 40) || state.Dirty || state.Ignored {
		t.Fatalf("state=%#v", state)
	}
	want := [][]string{
		{"-C", root, "rev-parse", "--show-toplevel"},
		{"-C", root, "rev-parse", "--verify", "HEAD^{commit}"},
		{"-C", root, "symbolic-ref", "--quiet", "--short", "HEAD"},
		{"-C", root, "status", "--porcelain=v1", "--untracked-files=normal"},
		{"-C", root, "check-ignore", "--quiet", "--no-index", "--", CIWorkflowTarget},
	}
	var actual [][]string
	for _, command := range runner.commands {
		if len(command.Args) > 2 && command.Args[2] == "config" {
			continue
		}
		actual = append(actual, command.Args)
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("Git args=%#v want=%#v", actual, want)
	}
	for _, command := range runner.commands {
		if len(command.Args) > 2 && command.Args[2] == "config" {
			continue
		}
		if containsEnv(command.Env, "GIT_DIR=") || containsEnv(command.Env, "GIT_WORK_TREE=") || containsEnv(command.Env, "GIT_CONFIG_GLOBAL=C:\\attacker\\config") || containsEnv(command.Env, "OPENAI_API_KEY=secret") {
			t.Fatalf("unsafe environment leaked: %#v", command.Env)
		}
		if !containsEnv(command.Env, "GIT_OPTIONAL_LOCKS=0") {
			t.Fatalf("missing lock-free Git environment: %#v", command.Env)
		}
	}
}

func TestInspectCIWorkflowReportsDirtyAndIgnored(t *testing.T) {
	root := t.TempDir()
	runner := &ciWorkflowInspectRunner{root: root, branch: "main", head: strings.Repeat("b", 40), dirty: true, ignored: true}
	state, err := New(runner).InspectCIWorkflow(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Dirty || !state.Ignored {
		t.Fatalf("state=%#v", state)
	}
}

func TestInspectCIWorkflowForwardsSafeDirectoryValuesToEveryProbe(t *testing.T) {
	root := t.TempDir()
	runner := &ciWorkflowInspectRunner{
		root:       root,
		branch:     "main",
		head:       strings.Repeat("e", 40),
		safeSystem: filepath.Join(root, "system-trusted") + "\x00",
		safeGlobal: filepath.Join(root, "global-trusted") + "\x00",
	}
	if _, err := New(runner).InspectCIWorkflow(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	for _, command := range runner.commands {
		if len(command.Args) < 3 || command.Args[2] == "config" {
			continue
		}
		if !containsEnv(command.Env, "GIT_CONFIG_COUNT=2") || !containsEnv(command.Env, "GIT_CONFIG_KEY_0=safe.directory") || !containsEnv(command.Env, "GIT_CONFIG_KEY_1=safe.directory") {
			t.Fatalf("safe.directory was not forwarded to command %#v env=%#v", command.Args, command.Env)
		}
	}
}

func TestInspectCIWorkflowDistinguishesDetachedFromLiteralBranch(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name       string
		branch     string
		detached   bool
		wantBranch string
	}{
		{name: "detached", detached: true, wantBranch: "detached"},
		{name: "literal detached branch", branch: "detached", wantBranch: "detached"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &ciWorkflowInspectRunner{root: root, branch: test.branch, detached: test.detached, head: strings.Repeat("c", 40)}
			state, err := New(runner).InspectCIWorkflow(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if state.Branch != test.wantBranch || state.Detached != test.detached {
				t.Fatalf("state=%#v", state)
			}
		})
	}
}

func TestInspectCIWorkflowRejectsMalformedHeadAndTruncatedOutput(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name      string
		malformed string
		truncated bool
	}{
		{name: "malformed", malformed: "head"},
		{name: "malformed UTF-8", malformed: "utf8"},
		{name: "truncated", truncated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &ciWorkflowInspectRunner{root: root, branch: "main", head: strings.Repeat("d", 40), malformed: test.malformed, truncated: test.truncated}
			if _, err := New(runner).InspectCIWorkflow(context.Background(), root); err == nil {
				t.Fatal("malformed Git response was accepted")
			}
		})
	}
}

func TestInspectCIWorkflowSanitizesGitAndConfigErrors(t *testing.T) {
	root := t.TempDir()
	for _, runner := range []*ciWorkflowInspectRunner{
		{root: root, configExit: 2},
		{root: root, failRoot: true},
	} {
		_, err := New(runner).InspectCIWorkflow(context.Background(), root)
		if err == nil || !strings.Contains(err.Error(), "inspection unavailable") {
			t.Fatalf("expected sanitized inspection error, got %v", err)
		}
		if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), root) {
			t.Fatalf("private Git diagnostic leaked: %v", err)
		}
	}
}

func (r *inspectRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	args := command.Args
	if len(args) >= 2 && args[len(args)-2] == "rev-parse" && args[len(args)-1] == "--show-toplevel" {
		return proc.Result{Stdout: r.root}, nil
	}
	if len(args) >= 2 && args[len(args)-2] == "rev-parse" && args[len(args)-1] == "HEAD" {
		return proc.Result{Stdout: "abcdef0123456789"}, nil
	}
	if len(args) >= 2 && args[len(args)-2] == "branch" && args[len(args)-1] == "--show-current" {
		return proc.Result{Stdout: r.branch}, nil
	}
	if len(args) >= 3 && args[len(args)-3] == "log" && args[len(args)-2] == "-1" {
		return proc.Result{Stdout: "subject"}, nil
	}
	if len(args) >= 3 && args[len(args)-3] == "status" && args[len(args)-2] == "--porcelain=v1" {
		return proc.Result{}, nil
	}
	return proc.Result{}, errors.New("unexpected Git command")
}

func TestIsLinkedWorktreeUsesTypedMetadataCommandAndPreservesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "한글 repository with spaces")
	common := filepath.Join(t.TempDir(), "common")
	gitDir := filepath.Join(t.TempDir(), "worktrees", "feature")
	runner := &recordingRunner{stdout: gitDir + "\n" + common}
	service := New(runner)
	linked, err := service.IsLinkedWorktree(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !linked {
		t.Fatal("different canonical git-dir/common-dir was not classified as linked")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	command := runner.commands[0]
	wantArgs := []string{"-C", abs, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir"}
	if command.Executable != service.Git || !reflect.DeepEqual(command.Args, wantArgs) {
		t.Fatalf("unexpected Git command: executable=%q args=%#v want=%#v", command.Executable, command.Args, wantArgs)
	}
}

func TestIsLinkedWorktreeKeepsOrdinarySubmoduleAndSeparateGitDirLayouts(t *testing.T) {
	for _, name := range []string{"ordinary", "submodule", "separate-git-dir"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			metadata := filepath.Join(root, "metadata", name)
			linked, err := New(&recordingRunner{stdout: metadata + "\n" + metadata}).IsLinkedWorktree(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if linked {
				t.Fatalf("layout %s was classified as linked", name)
			}
		})
	}
}

func TestIsLinkedWorktreeDoesNotSuppressOnMetadataFailureOrMalformedOutput(t *testing.T) {
	for _, test := range []struct {
		name   string
		stdout string
		err    error
	}{
		{name: "malformed", stdout: "/only/one/path"},
		{name: "relative", stdout: "relative/git\nrelative/common"},
		{name: "command-error", err: errors.New("git failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			linked, err := New(&recordingRunner{stdout: test.stdout, err: test.err}).IsLinkedWorktree(context.Background(), t.TempDir())
			if err == nil || linked {
				t.Fatalf("metadata failure did not fail closed: linked=%v err=%v", linked, err)
			}
		})
	}
}

func TestCanonicalRootRejectsBlankWithoutRunningGit(t *testing.T) {
	runner := &recordingRunner{}
	service := New(runner)
	if _, err := service.CanonicalRoot(context.Background(), " \t\r\n"); err == nil {
		t.Fatal("expected blank path rejection")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("git unexpectedly invoked: %#v", runner.commands)
	}
}

func TestCanonicalRootRejectsSubdirectory(t *testing.T) {
	root := t.TempDir()
	subdirectory := filepath.Join(root, "nested")
	if err := os.Mkdir(subdirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{stdout: root}
	if _, err := New(runner).CanonicalRoot(context.Background(), subdirectory); err == nil {
		t.Fatal("expected subdirectory rejection")
	}
}

func TestCanonicalRootUsesExactExecutableAndArgumentArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "한글 project with spaces; $(not-shell)")
	runner := &recordingRunner{}
	service := New(runner)
	service.Git = `C:\Program Files\Git\cmd\git.exe`
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	runner.stdout = abs
	got, err := service.CanonicalRoot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(abs) {
		t.Fatalf("canonical root = %q, want %q", got, filepath.Clean(abs))
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(runner.commands))
	}
	command := runner.commands[0]
	if command.Executable != service.Git {
		t.Fatalf("executable = %q", command.Executable)
	}
	wantArgs := []string{"-C", abs, "rev-parse", "--show-toplevel"}
	if !reflect.DeepEqual(command.Args, wantArgs) {
		t.Fatalf("argument boundaries changed\n got: %#v\nwant: %#v", command.Args, wantArgs)
	}
}

func TestCanonicalRootAcceptsWindowsStyleSpacesAndUTF8AsOneArgument(t *testing.T) {
	paths := []string{`C:\Workspace With Spaces\repository`, `C:\개발\로보 리뷰`}
	for _, input := range paths {
		t.Run(input, func(t *testing.T) {
			abs, err := filepath.Abs(input)
			if err != nil {
				t.Fatal(err)
			}
			runner := &recordingRunner{stdout: abs}
			if _, err := New(runner).CanonicalRoot(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			if got := runner.commands[0].Args; len(got) != 4 || got[1] != abs {
				t.Fatalf("path was not preserved as one argument: %#v", got)
			}
		})
	}
}

func TestCanonicalRootResolvesSymlinkWhenSupported(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(filepath.Dir(target), filepath.Base(target)+"-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	runner := &recordingRunner{stdout: target}
	got, err := New(runner).CanonicalRoot(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if !SameRepositoryPath(got, resolved) {
		t.Fatalf("root = %q, want resolved target %q", got, resolved)
	}
	if runner.commands[0].Args[1] != resolved {
		t.Fatalf("git received unresolved path: %#v", runner.commands[0].Args)
	}
}

func TestCanonicalRootCaseSemanticsFollowPlatform(t *testing.T) {
	input := t.TempDir()
	alternate := strings.ToUpper(input)
	if alternate == input {
		alternate = strings.ToLower(input)
	}
	runner := &recordingRunner{stdout: alternate}
	_, err := New(runner).CanonicalRoot(context.Background(), input)
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("Windows path casing should share identity: %v", err)
		}
	} else if err == nil {
		t.Fatal("case-sensitive platform accepted differently-cased canonical root")
	}
}

func TestInspectDistinguishesActualDetachedFromLiteralDetachedBranch(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name         string
		branchOutput string
		wantBranch   string
		wantDetached bool
	}{
		{name: "actual detached", branchOutput: "", wantBranch: "detached", wantDetached: true},
		{name: "literal detached branch", branchOutput: "detached", wantBranch: "detached"},
		{name: "main", branchOutput: "main", wantBranch: "main"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &inspectRunner{root: root, branch: test.branchOutput}
			repo, err := New(runner).Inspect(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if repo.Branch != test.wantBranch || repo.Detached != test.wantDetached {
				t.Fatalf("branch=%q detached=%v, want branch=%q detached=%v", repo.Branch, repo.Detached, test.wantBranch, test.wantDetached)
			}
			encoded, err := json.Marshal(repo)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if _, ok := fields["detached"]; ok {
				t.Fatalf("browser repository JSON leaked detached safety field: %s", encoded)
			}
			if len(runner.commands) != 5 {
				t.Fatalf("commands=%d, want 5", len(runner.commands))
			}
		})
	}
}

type snapshotProbeResponse struct {
	result proc.Result
	err    error
}

type snapshotProbeRunner struct {
	root      string
	responses map[string]snapshotProbeResponse
	commands  []proc.Command
}

func (r *snapshotProbeRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	stage := ""
	if len(command.Args) >= 3 {
		stage = command.Args[2]
	}
	if response, ok := r.responses[stage]; ok {
		return response.result, response.err
	}
	switch stage {
	case "rev-parse":
		return proc.Result{Stdout: r.root}, nil
	case "ls-files":
		return proc.Result{}, nil
	case "check-ignore":
		return proc.Result{}, nil
	default:
		return proc.Result{ExitCode: 2}, errors.New("unexpected Git snapshot command")
	}
}

func TestSnapshotProbeUsesExactTypedArgsAndSanitizedEnvironment(t *testing.T) {
	root := t.TempDir()
	relative := filepath.Join("cache", "snap shots")
	t.Setenv("HOME", `C:\Users\snapshot-home`)
	t.Setenv("GIT_DIR", `C:\Users\private\source\secret-project\.git`)
	t.Setenv("GIT_WORK_TREE", `C:\Users\private\source\secret-project`)
	t.Setenv("GIT_INDEX_FILE", `C:\Users\private\source\secret-project\index`)
	t.Setenv("GIT_CONFIG_GLOBAL", `C:\Users\private\.gitconfig`)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", `C:\Users\private\hooks`)
	runner := &snapshotProbeRunner{root: root, responses: map[string]snapshotProbeResponse{}}
	service := New(runner)
	got, err := service.SnapshotProbe(context.Background(), root, relative)
	if err != nil || got != (SnapshotProtection{Ignored: true}) {
		t.Fatalf("protection=%#v err=%v", got, err)
	}
	wantArgs := [][]string{
		{"-C", root, "rev-parse", "--show-toplevel"},
		{"-C", root, "ls-files", "--", filepath.ToSlash(relative)},
		{"-C", root, "check-ignore", "--quiet", "--no-index", "--", filepath.ToSlash(filepath.Join(relative, ".roborev-ignore-check"))},
	}
	if len(runner.commands) != len(wantArgs) {
		t.Fatalf("commands=%#v", runner.commands)
	}
	for index, command := range runner.commands {
		if command.Executable != service.Git || !reflect.DeepEqual(command.Args, wantArgs[index]) {
			t.Fatalf("command[%d]=%#v want args=%#v", index, command, wantArgs[index])
		}
		if command.Timeout != 10*time.Second || command.OutputLimit != 64<<10 {
			t.Fatalf("command bounds=%#v", command)
		}
		if !containsEnv(command.Env, "HOME=C:\\Users\\snapshot-home") ||
			!containsEnv(command.Env, "GIT_TERMINAL_PROMPT=0") ||
			!containsEnv(command.Env, "GIT_OPTIONAL_LOCKS=0") {
			t.Fatalf("required snapshot environment missing: %#v", command.Env)
		}
		for _, entry := range command.Env {
			name, _, _ := strings.Cut(entry, "=")
			upper := strings.ToUpper(name)
			if strings.HasPrefix(upper, "GIT_") && upper != "GIT_TERMINAL_PROMPT" && upper != "GIT_OPTIONAL_LOCKS" {
				t.Fatalf("inherited Git routing/config leaked: %q", entry)
			}
		}
	}
}

func TestSnapshotProbeTrackedFilesTakePrecedenceOverIgnore(t *testing.T) {
	root := t.TempDir()
	runner := &snapshotProbeRunner{root: root, responses: map[string]snapshotProbeResponse{
		"ls-files":     {result: proc.Result{Stdout: ".roborev/tracked.json\n"}},
		"check-ignore": {result: proc.Result{ExitCode: 2}, err: errors.New("must not run")},
	}}
	got, err := New(runner).SnapshotProbe(context.Background(), root, ".roborev")
	if err != nil || got != (SnapshotProtection{Tracked: true}) {
		t.Fatalf("protection=%#v err=%v", got, err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("tracked probe continued to ignore stage: %#v", runner.commands)
	}
}

func TestSnapshotProbeDistinguishesIgnoredAndNotIgnored(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name     string
		response snapshotProbeResponse
		want     SnapshotProtection
	}{
		{name: "ignored", response: snapshotProbeResponse{result: proc.Result{ExitCode: 0}}, want: SnapshotProtection{Ignored: true}},
		{name: "not ignored", response: snapshotProbeResponse{result: proc.Result{ExitCode: 1}, err: errors.New("process exited with code 1")}, want: SnapshotProtection{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &snapshotProbeRunner{root: root, responses: map[string]snapshotProbeResponse{"check-ignore": test.response}}
			got, err := New(runner).SnapshotProbe(context.Background(), root, filepath.Join("parent", ".roborev"))
			if err != nil || got != test.want {
				t.Fatalf("protection=%#v err=%v", got, err)
			}
		})
	}
}

func TestSnapshotProbeFailsClosedOnCommandAndExitAmbiguity(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name     string
		stage    string
		response snapshotProbeResponse
	}{
		{name: "root command error", stage: "rev-parse", response: snapshotProbeResponse{result: proc.Result{ExitCode: 2, Stderr: "PRIVATE_STDERR_MARKER"}, err: errors.New("private root failure")}},
		{name: "tracked command error", stage: "ls-files", response: snapshotProbeResponse{result: proc.Result{ExitCode: 2, Stderr: "PRIVATE_STDERR_MARKER"}, err: errors.New("private tracked failure")}},
		{name: "ignore unexpected exit", stage: "check-ignore", response: snapshotProbeResponse{result: proc.Result{ExitCode: 2, Stderr: "PRIVATE_STDERR_MARKER"}, err: errors.New("private ignore failure")}},
		{name: "ignore exit one without process error", stage: "check-ignore", response: snapshotProbeResponse{result: proc.Result{ExitCode: 1}}},
		{name: "ignore success with output", stage: "check-ignore", response: snapshotProbeResponse{result: proc.Result{Stdout: "PRIVATE_STDERR_MARKER"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &snapshotProbeRunner{root: root, responses: map[string]snapshotProbeResponse{test.stage: test.response}}
			if got, err := New(runner).SnapshotProbe(context.Background(), root, ".roborev"); err == nil || got != (SnapshotProtection{}) {
				t.Fatalf("protection=%#v err=%v", got, err)
			}
		})
	}
}

func TestSnapshotProbeRejectsTruncatedAndInvalidUTF8AtEveryStage(t *testing.T) {
	root := t.TempDir()
	for _, stage := range []string{"rev-parse", "ls-files", "check-ignore"} {
		for _, kind := range []string{"stdout truncated", "stderr truncated", "stdout utf8", "stderr utf8"} {
			t.Run(stage+"/"+kind, func(t *testing.T) {
				result := proc.Result{}
				if stage == "rev-parse" {
					result.Stdout = root
				}
				switch kind {
				case "stdout truncated":
					result.StdoutTruncated = true
				case "stderr truncated":
					result.StderrTruncated = true
				case "stdout utf8":
					result.Stdout = string([]byte{0xff})
				case "stderr utf8":
					result.Stderr = string([]byte{0xff})
				}
				runner := &snapshotProbeRunner{root: root, responses: map[string]snapshotProbeResponse{stage: {result: result}}}
				if got, err := New(runner).SnapshotProbe(context.Background(), root, ".roborev"); err == nil || got != (SnapshotProtection{}) {
					t.Fatalf("protection=%#v err=%v", got, err)
				}
			})
		}
	}
}

func TestSnapshotProbeRejectsUnsafeRelativeInputWithoutGit(t *testing.T) {
	for _, relative := range []string{"", "..", filepath.Join("..", "escape"), filepath.Join(t.TempDir(), "absolute")} {
		runner := &snapshotProbeRunner{root: t.TempDir(), responses: map[string]snapshotProbeResponse{}}
		if _, err := New(runner).SnapshotProbe(context.Background(), runner.root, relative); err == nil {
			t.Fatalf("relative %q was accepted", relative)
		}
		if len(runner.commands) != 0 {
			t.Fatalf("relative %q invoked Git: %#v", relative, runner.commands)
		}
	}
}
