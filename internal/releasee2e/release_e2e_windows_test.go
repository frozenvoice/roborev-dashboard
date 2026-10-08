//go:build windows

package releasee2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	startupTimeout = 15 * time.Second
	httpTimeout    = 5 * time.Second
	stopTimeout    = 10 * time.Second
)

func TestReleaseArtifactE2E(t *testing.T) {
	artifact := releaseArtifactPath()
	if artifact == "" {
		t.Skip("set RELEASE_E2E_ARTIFACT_PATH (or ROBOREV_DASHBOARD_E2E_ARTIFACT) to run the built-artifact release E2E")
	}
	artifact, err := filepath.Abs(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(artifact); err != nil || info.IsDir() {
		t.Fatalf("release artifact is unavailable: %s (%v)", artifact, err)
	}

	root := repositoryRoot(t)
	fixtureRoot := t.TempDir()
	repo := createFixtureRepository(t, filepath.Join(fixtureRoot, "repo"))
	home := filepath.Join(fixtureRoot, "home")
	config := filepath.Join(fixtureRoot, "config")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	projectRoot := repositoryRoot(t)
	beforeStatus := projectGitStatus(t, projectRoot)
	beforeManaged := captureFileSentinel(t, filepath.Join(mustUserHome(t), ".roborev", "bin", "roborev.exe"))
	beforeConfig := captureFileSentinel(t, filepath.Join(mustUserConfigDir(t), "roborev-dashboard", "config.json"))
	t.Cleanup(func() {
		if got := projectGitStatus(t, projectRoot); got != beforeStatus {
			t.Errorf("source repository Git status changed during release E2E: before=%q after=%q", beforeStatus, got)
		}
		assertFileSentinelUnchanged(t, beforeManaged)
		assertFileSentinelUnchanged(t, beforeConfig)
	})

	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("Go is required only to compile temporary test fixtures: %v", err)
	}
	fakeCLI := buildFixture(t, goExe, filepath.Join(root, "testdata", "releasee2e", "fakeroborev"), filepath.Join(fixtureRoot, "roborev.exe"))
	fakeDaemon := buildFixture(t, goExe, filepath.Join(root, "testdata", "releasee2e", "fakedaemon"), filepath.Join(fixtureRoot, "fakedaemon.exe"))

	childEnv := isolatedWindowsEnvironment(t, home)
	copiedArtifact := copyArtifact(t, artifact, fixtureRoot)
	assertToolchainFreeCopy(t, copiedArtifact, childEnv)
	assertCleanMachine(t, copiedArtifact, repo, config, fixtureRoot, childEnv)

	daemonLog := filepath.Join(fixtureRoot, "daemon-v064.log")
	descriptor := startFakeDaemon(t, fakeDaemon, childEnv, map[string]string{
		"RELEASE_E2E_FAKE_DAEMON_LOG":  daemonLog,
		"RELEASE_E2E_FAKE_DAEMON_REPO": repo,
	}, "v0.64.0")
	assertSupportedRuntime(t, copiedArtifact, fakeCLI, descriptor, repo, config, fixtureRoot, childEnv, daemonLog)
	assertVersionMismatch(t, copiedArtifact, fakeCLI, repo, filepath.Join(fixtureRoot, "mismatch-config"), fixtureRoot, childEnv)
	assertRestartAndIdleRecovery(t, copiedArtifact, repo, filepath.Join(fixtureRoot, "recovery-config"), fixtureRoot, childEnv)
	assertMalformedConfig(t, copiedArtifact, filepath.Join(fixtureRoot, "malformed-config"), fixtureRoot, childEnv)
}

func releaseArtifactPath() string {
	for _, key := range []string{"RELEASE_E2E_ARTIFACT_PATH", "ROBOREV_DASHBOARD_E2E_ARTIFACT", "RELEASE_E2E_ARTIFACT"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate release E2E source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func buildFixture(t *testing.T, goExe, sourceDir, output string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, goExe, "build", "-trimpath", "-buildvcs=false", "-o", output, sourceDir)
	cmd.Env = os.Environ()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build temporary fixture: %v\n%s", err, output)
	}
	return outputPath(t, output)
}

func copyArtifact(t *testing.T, artifact, fixtureRoot string) string {
	t.Helper()
	copyPath := filepath.Join(fixtureRoot, "artifact", "roborev-dashboard.exe")
	if err := os.MkdirAll(filepath.Dir(copyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	copyFile(t, artifact, copyPath)
	return copyPath
}

func outputPath(t *testing.T, path string) string {
	t.Helper()
	full, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(full); err != nil || info.IsDir() {
		t.Fatalf("fixture output unavailable: %s (%v)", full, err)
	}
	return full
}

func createFixtureRepository(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("Git is required for the temporary fixture repository: %v", err)
	}
	run := func(args ...string) {
		cmd := exec.Command(git, args...)
		cmd.Dir = path
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=NUL")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "RoboRev Dashboard release E2E")
	run("config", "user.email", "release-e2e@example.invalid")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("temporary release fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "fixture")
	return path
}

func isolatedWindowsEnvironment(t *testing.T, home string) []string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	entries := []string{filepath.Dir(gitPath), filepath.Join(systemRoot, "System32"), systemRoot}
	pathValue := strings.Join(entries, string(os.PathListSeparator))
	dir := filepath.Join(home, "tmp")
	appData := filepath.Join(home, "AppData", "Roaming")
	localAppData := filepath.Join(home, "AppData", "Local")
	for _, item := range []string{dir, appData, localAppData} {
		if err := os.MkdirAll(item, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return []string{
		"SystemRoot=" + systemRoot,
		"SystemDrive=" + os.Getenv("SystemDrive"),
		"ComSpec=" + filepath.Join(systemRoot, "System32", "cmd.exe"),
		"PATHEXT=.COM;.EXE;.BAT;.CMD",
		"PATH=" + pathValue,
		"USERPROFILE=" + home,
		"HOME=" + home,
		"HOMEDRIVE=" + filepath.VolumeName(home),
		"HOMEPATH=" + strings.TrimPrefix(home, filepath.VolumeName(home)),
		"APPDATA=" + appData,
		"LOCALAPPDATA=" + localAppData,
		"TEMP=" + dir,
		"TMP=" + dir,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=NUL",
		"NO_COLOR=1",
	}
}

func envWith(values []string, overrides map[string]string) []string {
	result := append([]string(nil), values...)
	for key, value := range overrides {
		found := false
		for index, entry := range result {
			name, _, ok := strings.Cut(entry, "=")
			if ok && strings.EqualFold(name, key) {
				result[index] = key + "=" + value
				found = true
				break
			}
		}
		if !found {
			result = append(result, key+"="+value)
		}
	}
	return result
}

type runningProcess struct {
	cmd      *exec.Cmd
	done     chan error
	lines    chan string
	outputMu sync.Mutex
	output   bytes.Buffer
	stopOnce sync.Once
	stopErr  error
}

func startProcess(t *testing.T, executable string, args []string, env []string, prefix string) *runningProcess {
	return startProcessInDir(t, executable, args, env, "", prefix)
}

func startProcessInDir(t *testing.T, executable string, args []string, env []string, directory, prefix string) *runningProcess {
	t.Helper()
	cmd := exec.Command(executable, args...)
	cmd.Env = env
	cmd.Dir = directory
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	process := &runningProcess{cmd: cmd, done: make(chan error, 1), lines: make(chan string, 16)}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", executable, err)
	}
	go process.readOutput(stdout)
	go process.readOutput(stderr)
	go func() { process.done <- cmd.Wait() }()
	deadline := time.NewTimer(startupTimeout)
	defer deadline.Stop()
	for {
		select {
		case line := <-process.lines:
			if strings.HasPrefix(line, prefix) {
				return process
			}
		case err := <-process.done:
			t.Fatalf("%s exited before startup: %v\n%s", executable, err, process.outputString())
		case <-deadline.C:
			_ = process.stop()
			t.Fatalf("timed out waiting for %s startup\n%s", executable, process.outputString())
		}
	}
}

func (p *runningProcess) readOutput(reader io.Reader) {
	scanner := bufio.NewScanner(io.LimitReader(reader, 1<<20))
	for scanner.Scan() {
		line := scanner.Text()
		p.outputMu.Lock()
		p.output.WriteString(line)
		p.output.WriteByte('\n')
		p.outputMu.Unlock()
		select {
		case p.lines <- line:
		default:
		}
	}
}

func (p *runningProcess) outputString() string {
	p.outputMu.Lock()
	defer p.outputMu.Unlock()
	return p.output.String()
}

func (p *runningProcess) stop() error {
	p.stopOnce.Do(func() {
		p.stopErr = p.terminate(false)
	})
	return p.stopErr
}

func (p *runningProcess) killNow() error {
	p.stopOnce.Do(func() { p.stopErr = p.terminate(true) })
	return p.stopErr
}

func (p *runningProcess) terminate(force bool) error {
	if p.cmd.Process == nil || p.cmd.ProcessState != nil {
		return nil
	}
	waitDone := func(wait time.Duration) bool {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-p.done:
			return true
		case <-timer.C:
			return false
		}
	}
	if !force {
		// A console interrupt is cooperative where supported. A bounded wait is
		// followed by taskkill /T /F so a child tree cannot leak into later tests.
		_ = p.cmd.Process.Signal(os.Interrupt)
		if waitDone(2 * time.Second) {
			return nil
		}
	}
	// Process.Kill closes the Dashboard's pipes promptly. The tree kill below
	// remains the fallback for any descendant that outlives the parent.
	_ = p.cmd.Process.Kill()
	if waitDone(3 * time.Second) {
		return nil
	}
	if taskkill, err := exec.LookPath("taskkill"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		_ = exec.CommandContext(ctx, taskkill, "/PID", strconv.Itoa(p.cmd.Process.Pid), "/T", "/F").Run()
		cancel()
	}
	if waitDone(3 * time.Second) {
		return nil
	}
	return errors.New("child process did not exit within bounded cleanup")
}

func startFakeDaemon(t *testing.T, executable string, env []string, overrides map[string]string, version string) *runningProcess {
	t.Helper()
	process := startProcess(t, executable, []string{"--port", "0", "--version", version, "--log", overrides["RELEASE_E2E_FAKE_DAEMON_LOG"], "--repo", overrides["RELEASE_E2E_FAKE_DAEMON_REPO"]}, envWith(env, overrides), "FAKE_DAEMON: ")
	t.Cleanup(func() {
		if err := process.stop(); err != nil {
			t.Errorf("fake daemon cleanup failed: %v", err)
		}
	})
	return process
}

func daemonAddress(process *runningProcess) string {
	for _, line := range strings.Split(process.outputString(), "\n") {
		if strings.HasPrefix(line, "FAKE_DAEMON: ") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "FAKE_DAEMON: "))
			if validLoopbackURL(value) {
				return value
			}
		}
	}
	return ""
}

func assertToolchainFreeCopy(t *testing.T, artifact string, env []string) {
	t.Helper()
	assertArtifactCommandContract(t, artifact, filepath.Dir(artifact), env, artifactMetadataExpectation{
		Version: strings.TrimSpace(os.Getenv("RELEASE_E2E_EXPECTED_VERSION")),
		Release: true,
	})
}

func assertDashboardHelp(t *testing.T, artifact, output string) {
	t.Helper()
	header, body, ok := strings.Cut(output, "\n")
	if !ok || !strings.HasPrefix(header, "Usage of ") || !strings.HasSuffix(header, ":") {
		t.Fatalf("copied artifact --help has invalid usage header: %q", output)
	}
	invokedPath := strings.TrimSuffix(strings.TrimPrefix(header, "Usage of "), ":")
	if !samePath(invokedPath, artifact) {
		t.Fatalf("copied artifact --help usage path = %q, want %q", invokedPath, artifact)
	}

	// Keep the public contract in lockstep with the key flags declared in
	// cmd/dashboard/main.go without snapshotting every human-facing flag
	// description and its standard-library formatting.
	for _, required := range []string{
		"  -bind string\n    \tloopback bind address (default \"127.0.0.1\")",
		"  -port int\n    \tstarting local port (default 7410)",
		"  -version\n    \tprint Dashboard version",
		"  -version-json\n    \tprint Dashboard version as JSON",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("copied artifact --help missing required contract %q:\n%s", required, body)
		}
	}
}

func assertCleanMachine(t *testing.T, artifact, repo, config, fixtureRoot string, env []string) {
	t.Helper()
	working := filepath.Join(fixtureRoot, "clean-working-directory")
	if err := os.MkdirAll(working, 0o700); err != nil {
		t.Fatal(err)
	}
	process := startDashboard(t, artifact, env, map[string]string{"--config-dir": config, "--repo": repo, "__dir": working})
	base := requireDashboardAddress(t, process)
	if base == "" {
		t.Fatal("Dashboard did not announce an address")
	}
	defer process.stop()
	rootResponse := requestResponse(t, base, "/")
	if rootResponse.status != http.StatusOK || !strings.Contains(strings.ToLower(rootResponse.contentType), "text/html") || !strings.Contains(rootResponse.body, "<html") {
		t.Fatalf("clean-machine root = %d content-type=%q: %s", rootResponse.status, rootResponse.contentType, rootResponse.body)
	}
	status, body := request(t, base, "/api/bootstrap")
	if status != http.StatusOK {
		t.Fatalf("clean-machine bootstrap = %d: %s", status, body)
	}
	var bootstrap struct {
		SelectedRepository string `json:"selectedRepository"`
		Roborev            struct {
			Installed bool `json:"installed"`
		} `json:"roborev"`
	}
	if err := json.Unmarshal([]byte(body), &bootstrap); err != nil {
		t.Fatal(err)
	}
	if !samePath(bootstrap.SelectedRepository, repo) || bootstrap.Roborev.Installed {
		t.Fatalf("clean-machine bootstrap = %#v, want selected fixture and installed=false", bootstrap)
	}
	if _, err := os.Stat(filepath.Join(config, "config.json")); err != nil {
		t.Fatalf("temporary Dashboard config was not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(environmentValue(env, "USERPROFILE"), ".roborev", "bin", "roborev.exe")); err == nil {
		// This branch should never be reached with the isolated child HOME; keep
		// the check explicit so a future environment regression fails closed.
		t.Fatalf("clean-machine scenario unexpectedly created a managed RoboRev target")
	}
}

func environmentValue(environment []string, key string) string {
	for _, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}

func assertSupportedRuntime(t *testing.T, artifact, fakeCLI string, daemon *runningProcess, repo, config, fixtureRoot string, env []string, daemonLog string) {
	t.Helper()
	base := requireDaemonAddress(t, daemon)
	cliLog := filepath.Join(fixtureRoot, "fakeroborev-v064.log")
	process := startDashboard(t, artifact, env, map[string]string{"--roborev": fakeCLI, "--roborev-server": strings.TrimPrefix(base, "http://"), "--repo": repo, "--config-dir": config, "__env:RELEASE_E2E_FAKE_ROBOREV_LOG": cliLog, "__dir": filepath.Join(fixtureRoot, "supported-working-directory")})
	defer process.stop()
	address := requireDashboardAddress(t, process)
	status, body := request(t, address, "/api/bootstrap")
	if status != http.StatusOK {
		t.Fatalf("supported bootstrap = %d: %s", status, body)
	}
	var bootstrap struct {
		SelectedRepositoryIdentity string `json:"selectedRepositoryIdentity"`
		Roborev                    struct {
			Installed bool   `json:"installed"`
			Version   string `json:"version"`
		} `json:"roborev"`
	}
	if err := json.Unmarshal([]byte(body), &bootstrap); err != nil {
		t.Fatal(err)
	}
	if !bootstrap.Roborev.Installed || bootstrap.Roborev.Version != "v0.64.0" || bootstrap.SelectedRepositoryIdentity == "" {
		t.Fatalf("supported bootstrap = %#v", bootstrap)
	}
	status, body = request(t, address, "/api/runtime")
	if status != http.StatusOK || !strings.Contains(body, `"installed":true`) {
		t.Fatalf("supported runtime = %d: %s", status, body)
	}
	status, body = request(t, address, "/api/reviews?repo="+urlEscape(repo))
	if status != http.StatusOK || !strings.Contains(body, `"id":42`) {
		t.Fatalf("supported review list = %d: %s", status, body)
	}
	escapedRepoJSON, err := json.Marshal(repo)
	if err != nil || len(escapedRepoJSON) < 2 {
		t.Fatalf("encode fixture repository privacy marker: %v", err)
	}
	escapedRepo := string(escapedRepoJSON[1 : len(escapedRepoJSON)-1])
	if escapedRepo == repo {
		t.Fatalf("fixture repository did not exercise JSON-escaped Windows path handling: %q", repo)
	}
	privacyMarkers := []string{"repo_path", "prompt", "PRIVATE_", repo, escapedRepo}
	for _, marker := range privacyMarkers {
		if strings.Contains(body, marker) {
			t.Fatalf("supported list leaked %q: %s", marker, body)
		}
	}
	status, body = request(t, address, "/api/reviews/42?repositoryIdentity="+urlEscape(bootstrap.SelectedRepositoryIdentity))
	if status != http.StatusOK {
		t.Fatalf("supported review detail = %d: %s", status, body)
	}
	for _, marker := range privacyMarkers {
		if strings.Contains(body, marker) {
			t.Fatalf("supported detail leaked %q: %s", marker, body)
		}
	}
	if err := assertLogGETOnly(daemonLog); err != nil {
		t.Fatal(err)
	}
	if err := assertFakeCLIReadOnly(cliLog); err != nil {
		t.Fatal(err)
	}
}

func assertVersionMismatch(t *testing.T, artifact, fakeCLI, repo, config, fixtureRoot string, env []string) {
	t.Helper()
	logPath := filepath.Join(fixtureRoot, "daemon-v065.log")
	cliLog := filepath.Join(fixtureRoot, "fakeroborev-v065.log")
	daemonSource := filepath.Join(repositoryRoot(t), "testdata", "releasee2e", "fakedaemon")
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	fakeDaemon := buildFixture(t, goExe, daemonSource, filepath.Join(fixtureRoot, "fakedaemon-v065.exe"))
	daemon := startFakeDaemon(t, fakeDaemon, env, map[string]string{"RELEASE_E2E_FAKE_DAEMON_LOG": logPath, "RELEASE_E2E_FAKE_DAEMON_REPO": repo}, "v0.65.0")
	defer daemon.stop()
	base := requireDaemonAddress(t, daemon)
	if status, body := request(t, base, "/api/health"); status != http.StatusOK || !strings.Contains(body, `"v0.65.0"`) {
		t.Fatalf("v0.65 fake daemon direct health = %d: %s", status, body)
	}
	if info, err := os.Stat(logPath); err != nil || info.IsDir() {
		t.Fatalf("v0.65 fake daemon did not create request log after direct health: %v", err)
	}
	process := startDashboard(t, artifact, env, map[string]string{"--roborev": fakeCLI, "--roborev-server": strings.TrimPrefix(base, "http://"), "--repo": repo, "--config-dir": config, "__env:RELEASE_E2E_FAKE_ROBOREV_LOG": cliLog, "__dir": filepath.Join(fixtureRoot, "mismatch-working-directory")})
	defer process.stop()
	dashboard := requireDashboardAddress(t, process)
	if status, body := request(t, dashboard, "/api/runtime"); status != http.StatusOK || !strings.Contains(body, `"installed":true`) {
		t.Fatalf("v0.65 runtime probe = %d: %s", status, body)
	}
	status, body := request(t, dashboard, "/api/reviews?repo="+urlEscape(repo))
	if status == http.StatusOK {
		t.Fatal("v0.65 daemon unexpectedly served reviews")
	}
	if body == "" {
		t.Logf("v0.65 review response status=%d with empty body", status)
	}
	t.Logf("v0.65 review response status=%d body=%s daemon=%s", status, body, daemon.outputString())
	if err := assertMismatchDaemonLog(logPath); err != nil {
		t.Fatal(err)
	}
	if err := assertFakeCLIReadOnly(cliLog); err != nil {
		t.Fatal(err)
	}
}

func assertRestartAndIdleRecovery(t *testing.T, artifact, repo, config, fixtureRoot string, env []string) {
	t.Helper()
	working := filepath.Join(fixtureRoot, "recovery-working-directory")
	if err := os.MkdirAll(working, 0o700); err != nil {
		t.Fatal(err)
	}
	first := startDashboard(t, artifact, env, map[string]string{"--repo": repo, "--config-dir": config, "__dir": working})
	firstAddress := requireDashboardAddress(t, first)
	if status, _ := request(t, firstAddress, "/api/bootstrap"); status != http.StatusOK {
		t.Fatalf("first recovery bootstrap = %d", status)
	}
	if err := first.stop(); err != nil {
		t.Fatal(err)
	}
	second := startDashboard(t, artifact, env, map[string]string{"--config-dir": config, "__dir": working})
	secondAddress := requireDashboardAddress(t, second)
	defer second.stop()
	status, body := request(t, secondAddress, "/api/bootstrap")
	var secondBootstrap struct {
		SelectedRepository string `json:"selectedRepository"`
	}
	if json.Unmarshal([]byte(body), &secondBootstrap) != nil || status != http.StatusOK || !samePath(secondBootstrap.SelectedRepository, repo) {
		t.Fatalf("restart recovery bootstrap = %d: %s", status, body)
	}
	if err := assertConfigHealthy(config, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(config, "config.json.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery left temporary config: %v", err)
	}
	if err := second.killNow(); err != nil {
		t.Fatal(err)
	}
	third := startDashboard(t, artifact, env, map[string]string{"--config-dir": config, "__dir": working})
	defer third.stop()
	thirdAddress := requireDashboardAddress(t, third)
	status, body = request(t, thirdAddress, "/api/bootstrap")
	var thirdBootstrap struct {
		SelectedRepository string `json:"selectedRepository"`
	}
	if json.Unmarshal([]byte(body), &thirdBootstrap) != nil || status != http.StatusOK || !samePath(thirdBootstrap.SelectedRepository, repo) {
		t.Fatalf("idle crash recovery bootstrap = %d: %s", status, body)
	}
	if status, _ := request(t, thirdAddress, "/"); status != http.StatusOK {
		t.Fatalf("idle crash recovery root = %d", status)
	}
	_ = fixtureRoot
}

func assertMalformedConfig(t *testing.T, artifact, config, fixtureRoot string, env []string) {
	t.Helper()
	if err := os.MkdirAll(config, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "config.json")
	original := []byte("{malformed config\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	working := filepath.Join(fixtureRoot, "not-a-git-working-directory")
	if err := os.MkdirAll(working, 0o700); err != nil {
		t.Fatal(err)
	}
	process := startDashboard(t, artifact, envWith(env, map[string]string{"RELEASE_E2E_FAKE_ROBOREV_VERSION": "v0.65.0"}), map[string]string{"--config-dir": config, "__dir": working})
	status, _ := request(t, requireDashboardAddress(t, process), "/")
	if status != http.StatusOK {
		t.Fatalf("malformed config root = %d", status)
	}
	if err := process.stop(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("malformed config changed: err=%v bytes=%q", err, got)
	}
}

func startDashboard(t *testing.T, artifact string, env []string, options map[string]string) *runningProcess {
	t.Helper()
	args := []string{"--bind", "127.0.0.1", "--port", "0", "--no-open"}
	for _, key := range []string{"--roborev", "--roborev-server", "--repo", "--config-dir"} {
		if value := options[key]; value != "" {
			args = append(args, key, value)
		}
	}
	workingDirectory := options["__dir"]
	if workingDirectory != "" {
		if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	envOverrides := map[string]string{}
	for key, value := range options {
		if strings.HasPrefix(key, "__env:") {
			envOverrides[strings.TrimPrefix(key, "__env:")] = value
		}
	}
	process := startProcessInDir(t, artifact, args, envWith(env, envOverrides), workingDirectory, "RoboRev Dashboard: ")
	t.Cleanup(func() {
		if err := process.stop(); err != nil {
			t.Errorf("Dashboard cleanup failed: %v", err)
		}
	})
	return process
}

func dashboardAddress(process *runningProcess) string {
	for _, line := range strings.Split(process.outputString(), "\n") {
		if strings.HasPrefix(line, "RoboRev Dashboard: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "RoboRev Dashboard: "))
		}
	}
	return ""
}

func requireDashboardAddress(t *testing.T, process *runningProcess) string {
	t.Helper()
	if address := dashboardAddress(process); validLoopbackURL(address) {
		return address
	}
	t.Fatalf("Dashboard did not announce a strict loopback URL\n%s", process.outputString())
	return ""
}

func requireDaemonAddress(t *testing.T, process *runningProcess) string {
	t.Helper()
	if address := daemonAddress(process); validLoopbackURL(address) {
		return address
	}
	t.Fatalf("fake daemon did not announce a strict loopback URL\n%s", process.outputString())
	return ""
}

func validLoopbackURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Hostname() != "127.0.0.1" {
		return false
	}
	port := parsed.Port()
	if port == "" || parsed.Host != net.JoinHostPort("127.0.0.1", port) {
		return false
	}
	parsedPort, err := strconv.Atoi(port)
	return err == nil && parsedPort > 0 && parsedPort <= 65535
}

func request(t *testing.T, base, path string) (int, string) {
	t.Helper()
	response := requestResponse(t, base, path)
	return response.status, response.body
}

type httpResponse struct {
	status      int
	body        string
	contentType string
}

func requestResponse(t *testing.T, base, path string) httpResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: httpTimeout, Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	return httpResponse{status: resp.StatusCode, body: string(body), contentType: resp.Header.Get("Content-Type")}
}

func assertLogGETOnly(path string) error {
	return validateDaemonLog(path, true, true)
}

func assertMismatchDaemonLog(path string) error {
	return validateDaemonLog(path, false, false)
}

func validateDaemonLog(path string, requireJobs, requireReview bool) error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, statErr := os.Stat(path); statErr == nil {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read fake daemon log: %w", err)
	}
	health, jobs, review := false, false, false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 || parts[0] != http.MethodGet {
			return fmt.Errorf("fake daemon observed unexpected request: %s", line)
		}
		parsed, err := url.ParseRequestURI(parts[1])
		if err != nil || parsed.Path == "" {
			return fmt.Errorf("fake daemon observed malformed request target: %s", line)
		}
		switch parsed.Path {
		case "/api/health":
			health = true
		case "/api/jobs":
			jobs = true
		case "/api/review":
			review = true
		default:
			return fmt.Errorf("fake daemon observed unexpected GET: %s", line)
		}
	}
	if !health || requireJobs && !jobs || requireReview && !review {
		return fmt.Errorf("fake daemon reads did not match contract: health=%t jobs=%t review=%t", health, jobs, review)
	}
	if !requireJobs && jobs {
		return errors.New("v0.65 mismatch unexpectedly reached /api/jobs")
	}
	if !requireReview && review {
		return errors.New("v0.65 mismatch unexpectedly reached /api/review")
	}
	return nil
}

func assertFakeCLIReadOnly(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read fake RoboRev log: %w", err)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		count++
		if line != "version --json" {
			return fmt.Errorf("fake RoboRev observed unexpected argv: %s", line)
		}
	}
	if count == 0 {
		return errors.New("fake RoboRev observed no version probe")
	}
	return nil
}

func assertConfigHealthy(path, selectedRepo string) error {
	data, err := os.ReadFile(filepath.Join(path, "config.json"))
	if err != nil {
		return fmt.Errorf("read Dashboard config: %w", err)
	}
	if len(data) == 0 || len(data) > 1<<20 {
		return fmt.Errorf("Dashboard config size is outside bound: %d", len(data))
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("Dashboard config is malformed: %w", err)
	}
	for key := range value {
		if key != "recentRepositories" && key != "lastSelectedRepository" && key != "panelSelections" {
			return fmt.Errorf("Dashboard config contains unexpected key %q", key)
		}
	}
	if selected, _ := value["lastSelectedRepository"].(string); !samePath(selected, selectedRepo) {
		return fmt.Errorf("Dashboard config selected repository = %q, want %q", selected, selectedRepo)
	}
	for _, marker := range []string{"PRIVATE_", "password", "token", "secret"} {
		if strings.Contains(strings.ToLower(string(data)), strings.ToLower(marker)) {
			return fmt.Errorf("Dashboard config contains private marker %q", marker)
		}
	}
	return nil
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	return strings.EqualFold(left, right)
}

func isLowerHexCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func urlEscape(value string) string {
	const hexDigits = "0123456789ABCDEF"
	var builder strings.Builder
	for _, char := range []byte(value) {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-_.~", rune(char)) {
			builder.WriteByte(char)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(hexDigits[char>>4])
		builder.WriteByte(hexDigits[char&0x0f])
	}
	return builder.String()
}

func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

type fileSentinel struct {
	path    string
	exists  bool
	regular bool
	size    int64
	mtime   int64
	digest  string
}

func mustUserHome(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		t.Fatalf("resolve actual user HOME: %v", err)
	}
	return home
}

func mustUserConfigDir(t *testing.T) string {
	t.Helper()
	config, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(config) == "" {
		t.Fatalf("resolve actual user config directory: %v", err)
	}
	return config
}

func captureFileSentinel(t *testing.T, path string) fileSentinel {
	t.Helper()
	sentinel := fileSentinel{path: path}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return sentinel
	}
	if err != nil {
		t.Fatalf("inspect sentinel %s: %v", path, err)
	}
	sentinel.exists = true
	sentinel.regular = info.Mode().IsRegular()
	sentinel.size = info.Size()
	sentinel.mtime = info.ModTime().UnixNano()
	if sentinel.regular && sentinel.size <= 64<<20 {
		sentinel.digest, err = fileDigest(path)
		if err != nil {
			t.Fatalf("hash sentinel %s: %v", path, err)
		}
	}
	return sentinel
}

func assertFileSentinelUnchanged(t *testing.T, before fileSentinel) {
	t.Helper()
	after := captureFileSentinel(t, before.path)
	if before.exists != after.exists || before.regular != after.regular || before.size != after.size || before.mtime != after.mtime || before.digest != after.digest {
		t.Errorf("user-side sentinel changed: %s before=%#v after=%#v", before.path, before, after)
	}
}

func projectGitStatus(t *testing.T, root string) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("locate Git for source sentinel: %v", err)
	}
	cmd := exec.Command(git, "-C", root, "status", "--short", "--untracked-files=all")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("source Git status: %v\n%s", err, output)
	}
	return string(output)
}
