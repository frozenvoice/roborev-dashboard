package roborev

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type ciWorkflowRecordingRunner struct {
	mu           sync.Mutex
	commands     []proc.Command
	content      []byte
	config       string
	versions     []string
	versionCalls int
	events       *[]string
}

func (r *ciWorkflowRecordingRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.mu.Lock()
	r.commands = append(r.commands, proc.Command{Executable: command.Executable, Args: append([]string(nil), command.Args...), Dir: command.Dir, Env: append([]string(nil), command.Env...), Timeout: command.Timeout, OutputLimit: command.OutputLimit})
	r.mu.Unlock()
	switch {
	case reflect.DeepEqual(command.Args, []string{"version", "--json"}):
		if r.events != nil {
			*r.events = append(*r.events, "VERSION_PROBE")
		}
		r.mu.Lock()
		index := r.versionCalls
		r.versionCalls++
		version := "v0.64.0"
		if index < len(r.versions) {
			version = r.versions[index]
		}
		r.mu.Unlock()
		return proc.Result{Started: true, ExitCode: 0, Stdout: `{"name":"roborev","version":"` + version + `"}`}, nil
	case reflect.DeepEqual(command.Args, ConfigListArgs()):
		if r.events != nil {
			*r.events = append(*r.events, "CONFIG_PREFLIGHT")
		}
		config := r.config
		if config == "" {
			config = "default default_agent codex\n"
		}
		return proc.Result{Started: true, ExitCode: 0, Stdout: config}, nil
	case len(command.Args) == 6 && command.Args[0] == "init" && command.Args[1] == "gh-action":
		if r.events != nil {
			*r.events = append(*r.events, "GENERATE")
		}
		if err := os.WriteFile(command.Args[3], r.content, 0o600); err != nil {
			return proc.Result{Started: true, ExitCode: 1}, err
		}
		return proc.Result{Started: true, ExitCode: 0, Stdout: "Created workflow at private path\n"}, nil
	default:
		return proc.Result{Started: true, ExitCode: 1}, errors.New("unexpected fake command")
	}
}

func (r *ciWorkflowRecordingRunner) snapshot() []proc.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]proc.Command(nil), r.commands...)
}

func TestGenerateCIWorkflowUsesExactBoundedSequenceAndPinnedCLIArg(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "roborev.exe")
	if err := os.WriteFile(binary, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "한글 repository with spaces")
	outputDir := filepath.Join(t.TempDir(), "private 임시 output")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(outputDir, "roborev.yml")
	events := make([]string, 0, 8)
	runner := &ciWorkflowRecordingRunner{content: []byte("name: RoboRev\n"), config: v064CIWorkflowConfigListFixture, events: &events}
	client, err := NewClient(binary, runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.GenerateCIWorkflow(context.Background(), repo, output, func(context.Context) error {
		events = append(events, "FINAL_TEMP_GUARD")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"VERSION_PROBE", "CONFIG_PREFLIGHT", "VERSION_PROBE", "FINAL_TEMP_GUARD", "GENERATE", "VERSION_PROBE"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("event sequence=%#v want=%#v", events, want)
	}
	commands := runner.snapshot()
	if len(commands) != 5 {
		t.Fatalf("commands=%d %#v", len(commands), commands)
	}
	if !reflect.DeepEqual(commands[0].Args, []string{"version", "--json"}) || !reflect.DeepEqual(commands[1].Args, ConfigListArgs()) || !reflect.DeepEqual(commands[2].Args, []string{"version", "--json"}) || !reflect.DeepEqual(commands[4].Args, []string{"version", "--json"}) {
		t.Fatalf("probe sequence=%#v", commands)
	}
	wantArgs := []string{"init", "gh-action", "--output", output, "--roborev-version", "0.64.0"}
	if !reflect.DeepEqual(commands[3].Args, wantArgs) {
		t.Fatalf("generation args=%#v want=%#v", commands[3].Args, wantArgs)
	}
	if commands[1].Dir != repo || commands[3].Dir != repo {
		t.Fatalf("cwd config=%q generation=%q want=%q", commands[1].Dir, commands[3].Dir, repo)
	}
	for _, command := range commands {
		if command.Executable != binary {
			t.Fatalf("executable=%q want=%q", command.Executable, binary)
		}
		for _, forbidden := range []string{"--force", "--agent", "shell", "daemon", "--browser", "--version", "--path"} {
			for _, arg := range command.Args {
				if arg == forbidden {
					t.Fatalf("forbidden %q in %#v", forbidden, command.Args)
				}
			}
		}
	}
	rootInitCount := 0
	ghActionCount := 0
	for _, command := range commands {
		for index, arg := range command.Args {
			if arg == "init" {
				if index == 0 && len(command.Args) >= 2 && command.Args[1] == "gh-action" {
					ghActionCount++
				} else {
					rootInitCount++
				}
			}
			if arg == "cmd" || arg == "/c" || arg == "powershell" || arg == "pwsh" || arg == "sh" || arg == "-c" {
				t.Fatalf("shell wrapper marker in command: %#v", command.Args)
			}
		}
	}
	if rootInitCount != 0 || ghActionCount != 1 {
		t.Fatalf("root init occurrences=%d gh-action occurrences=%d commands=%#v", rootInitCount, ghActionCount, commands)
	}
}

func TestCIWorkflowPreviewArgsRejectsNonCanonicalOrBrowserStyleOutput(t *testing.T) {
	abs := filepath.Join(t.TempDir(), ciWorkflowOutputName)
	want := []string{"init", "gh-action", "--output", abs, "--roborev-version", "0.64.0"}
	got, err := CIWorkflowPreviewArgs(abs)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%#v error=%v want=%#v", got, err, want)
	}
	nonCanonical := filepath.Join(t.TempDir(), "nested") + string(filepath.Separator) + ".." + string(filepath.Separator) + ciWorkflowOutputName
	for _, invalid := range []string{"", ciWorkflowOutputName, filepath.Join(t.TempDir(), "other.yml"), nonCanonical} {
		if _, err := CIWorkflowPreviewArgs(invalid); !errors.Is(err, ErrCIWorkflowOutput) {
			t.Fatalf("output=%q error=%v", invalid, err)
		}
	}
}

func TestGenerateCIWorkflowMalformedConfigPreventsGeneration(t *testing.T) {
	client, runner, root, output := newCIWorkflowTestClient(t)
	runner.config = "not a valid config row\n"
	if err := client.GenerateCIWorkflow(context.Background(), root, output, allowCIWorkflowGuard); !errors.Is(err, ErrCIWorkflowConfig) {
		t.Fatalf("error=%v", err)
	}
	for _, command := range runner.snapshot() {
		if len(command.Args) > 0 && command.Args[0] == "init" {
			t.Fatalf("generation ran after malformed config: %#v", command)
		}
	}
}

func TestGenerateCIWorkflowPreLaunchGuardFailurePreventsGeneration(t *testing.T) {
	client, runner, root, output := newCIWorkflowTestClient(t)
	guardErr := errors.New(`private temp path C:\Users\secret\roborev-dashboard-ci-guard\roborev.yml`)
	guardCalls := 0
	if err := client.GenerateCIWorkflow(context.Background(), root, output, func(context.Context) error {
		guardCalls++
		return guardErr
	}); !errors.Is(err, ErrCIWorkflowOutput) {
		t.Fatalf("error=%v", err)
	} else if strings.Contains(err.Error(), guardErr.Error()) {
		t.Fatalf("guard error leaked: %v", err)
	}
	if guardCalls != 1 {
		t.Fatalf("guard calls=%d want=1", guardCalls)
	}
	for _, command := range runner.snapshot() {
		if len(command.Args) > 0 && command.Args[0] == "init" {
			t.Fatalf("generation ran after final temp guard failure: %#v", command)
		}
	}
}

func TestGenerateCIWorkflowAcceptsV064DottedConfigFixture(t *testing.T) {
	client, runner, root, output := newCIWorkflowTestClient(t)
	runner.config = v064CIWorkflowConfigListFixture
	if err := client.GenerateCIWorkflow(context.Background(), root, output, allowCIWorkflowGuard); err != nil {
		t.Fatalf("dotted v0.64 config fixture prevented generation: %v", err)
	}
	for _, command := range runner.snapshot() {
		if len(command.Args) > 0 && command.Args[0] == "init" {
			return
		}
	}
	t.Fatal("generation command was not reached after dotted config preflight")
}

func TestGenerateCIWorkflowRejectsUnsupportedVersionBeforeGeneration(t *testing.T) {
	client, runner, root, output := newCIWorkflowTestClient(t)
	runner.versions = []string{"v0.63.0"}
	if err := client.GenerateCIWorkflow(context.Background(), root, output, allowCIWorkflowGuard); !errors.Is(err, ErrCIWorkflowUnavailable) {
		t.Fatalf("error=%v", err)
	}
	if len(runner.snapshot()) != 1 {
		t.Fatalf("commands=%#v", runner.snapshot())
	}
}

func TestGenerateCIWorkflowRejectsPostGenerationBindingDrift(t *testing.T) {
	client, runner, root, output := newCIWorkflowTestClient(t)
	runner.versions = []string{"v0.64.0", "v0.64.0", "v0.63.0"}
	if err := client.GenerateCIWorkflow(context.Background(), root, output, allowCIWorkflowGuard); !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("error=%v", err)
	}
	for _, command := range runner.snapshot() {
		if len(command.Args) > 0 && command.Args[0] == "init" {
			return
		}
	}
	t.Fatal("generation did not run before final binding drift")
}

func TestGenerateCIWorkflowRejectsPreexistingOutputWithoutRunning(t *testing.T) {
	client, runner, root, output := newCIWorkflowTestClient(t)
	if err := os.WriteFile(output, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.GenerateCIWorkflow(context.Background(), root, output, allowCIWorkflowGuard); !errors.Is(err, ErrCIWorkflowOutput) {
		t.Fatalf("error=%v", err)
	}
	if len(runner.snapshot()) != 0 {
		t.Fatalf("commands=%#v", runner.snapshot())
	}
}

func TestGenerateCIWorkflowRejectsRelativeRepositoryAndOutputBeforeRunning(t *testing.T) {
	client, runner, root, output := newCIWorkflowTestClient(t)
	for _, test := range []struct {
		root   string
		output string
	}{
		{root: filepath.Base(root), output: output},
		{root: root, output: ciWorkflowOutputName},
	} {
		if err := client.GenerateCIWorkflow(context.Background(), test.root, test.output, allowCIWorkflowGuard); err == nil {
			t.Fatalf("root=%q output=%q unexpectedly accepted", test.root, test.output)
		}
	}
	if len(runner.snapshot()) != 0 {
		t.Fatalf("commands=%#v", runner.snapshot())
	}
}

func newCIWorkflowTestClient(t *testing.T) (*Client, *ciWorkflowRecordingRunner, string, string) {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "roborev.exe")
	if err := os.WriteFile(binary, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	output := filepath.Join(t.TempDir(), "roborev.yml")
	runner := &ciWorkflowRecordingRunner{content: []byte("name: RoboRev\n")}
	client, err := NewClient(binary, runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	return client, runner, root, output
}

func TestCIWorkflowEnvKeepsLocationsAndDropsRoutingAndSecrets(t *testing.T) {
	keys := []string{"ROBOREV_DATA_DIR", "GIT_DIR", "GIT_CONFIG_GLOBAL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "XAI_API_KEY", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "ROBOREV_SECRET", "HOME", "USERPROFILE"}
	old := make(map[string]string)
	present := make(map[string]bool)
	for _, key := range keys {
		old[key], present[key] = os.LookupEnv(key)
	}
	defer func() {
		for _, key := range keys {
			if present[key] {
				value := old[key]
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}()
	_ = os.Setenv("ROBOREV_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	_ = os.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "redirect"))
	_ = os.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "config"))
	_ = os.Setenv("OPENAI_API_KEY", "secret-value")
	_ = os.Setenv("ANTHROPIC_API_KEY", "secret-value")
	_ = os.Setenv("XAI_API_KEY", "secret-value")
	_ = os.Setenv("AWS_ACCESS_KEY_ID", "secret-value")
	_ = os.Setenv("AWS_SECRET_ACCESS_KEY", "SUPER_PRIVATE_AWS")
	_ = os.Setenv("ROBOREV_SECRET", "secret-value")
	env := ciWorkflowEnvFromProcess()
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "GIT_DIR", "GIT_CONFIG_GLOBAL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "XAI_API_KEY", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "ROBOREV_SECRET":
			t.Fatalf("forbidden env entry=%q", entry)
		}
	}
	if !containsEnvName(env, "ROBOREV_DATA_DIR") {
		t.Fatalf("ROBOREV_DATA_DIR missing from %#v", env)
	}
}

func TestGenerateCIWorkflowGenerationCommandEnvContainsNoSecretMarkers(t *testing.T) {
	keys := []string{"ROBOREV_DATA_DIR", "GIT_DIR", "GIT_CONFIG_GLOBAL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "XAI_API_KEY", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "ROBOREV_SECRET"}
	old := make(map[string]string, len(keys))
	present := make(map[string]bool, len(keys))
	for _, key := range keys {
		old[key], present[key] = os.LookupEnv(key)
	}
	defer func() {
		for _, key := range keys {
			if present[key] {
				_ = os.Setenv(key, old[key])
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}()
	_ = os.Setenv("ROBOREV_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	for _, key := range []string{"GIT_DIR", "GIT_CONFIG_GLOBAL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "XAI_API_KEY", "AWS_ACCESS_KEY_ID", "ROBOREV_SECRET"} {
		_ = os.Setenv(key, "PRIVATE_"+key)
	}
	_ = os.Setenv("AWS_SECRET_ACCESS_KEY", "SUPER_PRIVATE_AWS")
	client, runner, root, output := newCIWorkflowTestClient(t)
	if err := client.GenerateCIWorkflow(context.Background(), root, output, allowCIWorkflowGuard); err != nil {
		t.Fatal(err)
	}
	var generation proc.Command
	for _, command := range runner.snapshot() {
		if len(command.Args) > 0 && command.Args[0] == "init" {
			generation = command
			break
		}
	}
	if len(generation.Args) == 0 {
		t.Fatal("generation command was not recorded")
	}
	for _, entry := range generation.Env {
		name, value, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "GIT_DIR", "GIT_CONFIG_GLOBAL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "XAI_API_KEY", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "ROBOREV_SECRET":
			t.Fatalf("secret/routing env leaked: %q=%q", name, value)
		}
		if value == "SUPER_PRIVATE_AWS" || strings.HasPrefix(value, "PRIVATE_") {
			t.Fatalf("secret env value leaked: %q", value)
		}
	}
}

func allowCIWorkflowGuard(context.Context) error { return nil }

func containsEnvName(env []string, want string) bool {
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, want) {
			return true
		}
	}
	return false
}
