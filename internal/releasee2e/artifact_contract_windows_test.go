//go:build windows

package releasee2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/buildinfo"
	proc "github.com/froze/roborev-dashboard/internal/process"
	"golang.org/x/sys/windows"
)

const (
	localVerifyArtifactPath       = "LOCAL_VERIFY_ARTIFACT_PATH"
	localVerifyExpectedVersion    = "LOCAL_VERIFY_EXPECTED_VERSION"
	localVerifyExpectedCommit     = "LOCAL_VERIFY_EXPECTED_COMMIT"
	localVerifyExpectedRelease    = "LOCAL_VERIFY_EXPECTED_RELEASE"
	artifactFixtureHelperEnv      = "RELEASE_E2E_ARTIFACT_FIXTURE_HELPER"
	artifactFixtureSecretEnv      = "RELEASE_E2E_SYNTHETIC_SECRET"
	artifactFixtureOutputLimit    = 128 << 10
	artifactFixtureCommandTimeout = 5 * time.Second
	artifactFixtureWaitTimeout    = 0x00000102
)

type artifactMetadataExpectation struct {
	Version string
	Commit  string
	Release bool
}

func TestArtifactCommandContract(t *testing.T) {
	artifactValue := strings.TrimSpace(os.Getenv(localVerifyArtifactPath))
	if artifactValue == "" {
		t.Skip("set LOCAL_VERIFY_ARTIFACT_PATH to run the local artifact command contract")
	}
	expected := artifactMetadataExpectation{
		Version: requiredArtifactEnvironment(t, localVerifyExpectedVersion),
		Commit:  requiredArtifactEnvironment(t, localVerifyExpectedCommit),
		Release: requiredArtifactRelease(t),
	}
	if !buildinfo.IsStrictSemVer(expected.Version) && expected.Version != "dev" {
		t.Fatalf("%s is not a valid version: %q", localVerifyExpectedVersion, expected.Version)
	}
	if expected.Release && expected.Version == "dev" {
		t.Fatalf("release artifact cannot use version %q", expected.Version)
	}
	if expected.Commit != "unknown" && !isLowerHexCommit(expected.Commit) {
		t.Fatalf("%s is not a valid commit: %q", localVerifyExpectedCommit, expected.Commit)
	}

	artifact, err := filepath.Abs(artifactValue)
	if err != nil {
		t.Fatalf("resolve artifact path: %v", err)
	}
	info, err := os.Stat(artifact)
	if err != nil || info.IsDir() || !info.Mode().IsRegular() {
		t.Fatalf("artifact is unavailable or not a regular file: %s (%v)", artifact, err)
	}

	fixtureRoot := t.TempDir()
	home := filepath.Join(fixtureRoot, "home")
	config := filepath.Join(fixtureRoot, "config")
	working := filepath.Join(fixtureRoot, "working")
	for _, path := range []string{home, config, working} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := isolatedWindowsEnvironment(t, home)
	assertArtifactIsolationEnvironment(t, env, home)
	copiedArtifact := copyArtifact(t, artifact, fixtureRoot)
	assertArtifactCommandContract(t, copiedArtifact, working, env, expected)
}

func requiredArtifactEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required when %s is set", name, localVerifyArtifactPath)
	}
	return value
}

func requiredArtifactRelease(t *testing.T) bool {
	t.Helper()
	switch value := strings.TrimSpace(os.Getenv(localVerifyExpectedRelease)); value {
	case "true":
		return true
	case "false":
		return false
	default:
		t.Fatalf("%s must be exactly true or false, got %q", localVerifyExpectedRelease, value)
		return false
	}
}

func assertArtifactIsolationEnvironment(t *testing.T, env []string, home string) {
	t.Helper()
	if !samePath(environmentValue(env, "HOME"), home) || !samePath(environmentValue(env, "USERPROFILE"), home) {
		t.Fatalf("artifact command environment does not use private HOME: %#v", env)
	}
	if !samePath(environmentValue(env, "TEMP"), filepath.Join(home, "tmp")) || !samePath(environmentValue(env, "TMP"), filepath.Join(home, "tmp")) {
		t.Fatalf("artifact command environment does not use private TEMP/TMP: %#v", env)
	}
	pathValue := strings.ToLower(environmentValue(env, "PATH"))
	for _, marker := range []string{"\\go\\", "\\node", "\\roborev"} {
		if strings.Contains(pathValue, marker) {
			t.Fatalf("artifact command PATH contains forbidden toolchain marker %q: %q", marker, pathValue)
		}
	}
}

func assertArtifactCommandContract(t *testing.T, artifact, workingDirectory string, env []string, expected artifactMetadataExpectation) {
	t.Helper()
	var metadata buildinfo.Metadata
	var humanVersion string
	for _, args := range [][]string{{"--help"}, {"--version"}, {"--version-json"}} {
		result, err := runArtifactCommand(context.Background(), artifact, args, workingDirectory, env, artifactFixtureCommandTimeout, artifactFixtureOutputLimit)
		if err != nil {
			t.Fatalf("artifact %v failed: %v\nstdout=%q\nstderr=%q", args, err, result.Stdout, result.Stderr)
		}
		if result.StdoutTruncated || result.StderrTruncated {
			t.Fatalf("artifact %v exceeded bounded output", args)
		}
		switch args[0] {
		case "--help":
			if result.Stdout != "" {
				t.Fatalf("artifact --help wrote unexpected stdout: %q", result.Stdout)
			}
			assertDashboardHelp(t, artifact, result.Stderr)
		case "--version":
			if result.Stderr != "" {
				t.Fatalf("artifact --version wrote unexpected stderr: %q", result.Stderr)
			}
			humanVersion = result.Stdout
		case "--version-json":
			if result.Stderr != "" {
				t.Fatalf("artifact --version-json wrote unexpected stderr: %q", result.Stderr)
			}
			var decodeErr error
			metadata, decodeErr = decodeArtifactMetadata([]byte(result.Stdout))
			if decodeErr != nil {
				t.Fatalf("artifact --version-json metadata is invalid: %v\n%s", decodeErr, result.Stdout)
			}
			assertArtifactMetadata(t, metadata, expected)
		}
	}
	if humanVersion != metadata.Human() {
		t.Fatalf("artifact --version did not match --version-json metadata: got %q want %q", humanVersion, metadata.Human())
	}
}

func runArtifactCommand(parent context.Context, executable string, args []string, workingDirectory string, env []string, timeout time.Duration, outputLimit int) (proc.Result, error) {
	return (proc.ExecRunner{}).Run(parent, proc.Command{
		Executable:  executable,
		Args:        args,
		Dir:         workingDirectory,
		Env:         env,
		Timeout:     timeout,
		OutputLimit: outputLimit,
	})
}

func decodeArtifactMetadata(data []byte) (buildinfo.Metadata, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return buildinfo.Metadata{}, err
	}
	expectedFields := map[string]bool{
		"name":                 true,
		"version":              true,
		"commit":               true,
		"release":              true,
		"target":               true,
		"roborevCompatibility": true,
	}
	if len(fields) != len(expectedFields) {
		return buildinfo.Metadata{}, fmt.Errorf("expected exactly six metadata fields, got %d", len(fields))
	}
	for field := range fields {
		if !expectedFields[field] {
			return buildinfo.Metadata{}, fmt.Errorf("unexpected metadata field %q", field)
		}
		if bytes.Equal(bytes.TrimSpace(fields[field]), []byte("null")) {
			return buildinfo.Metadata{}, fmt.Errorf("metadata field %q must not be null", field)
		}
	}
	var metadata buildinfo.Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return buildinfo.Metadata{}, err
	}
	return metadata, nil
}

func TestDecodeArtifactMetadataRejectsNullRelease(t *testing.T) {
	valid := []byte(`{"name":"roborev-dashboard","version":"dev","commit":"unknown","release":false,"target":"windows/amd64","roborevCompatibility":"v0.64.0"}`)
	metadata, err := decodeArtifactMetadata(valid)
	if err != nil || metadata.Release {
		t.Fatalf("valid development metadata rejected: %#v %v", metadata, err)
	}
	nullRelease := bytes.Replace(valid, []byte(`"release":false`), []byte(`"release":null`), 1)
	if _, err := decodeArtifactMetadata(nullRelease); err == nil {
		t.Fatal("null release metadata was accepted")
	}
}

func assertArtifactMetadata(t *testing.T, metadata buildinfo.Metadata, expected artifactMetadataExpectation) {
	t.Helper()
	if metadata.Name != buildinfo.Name || expected.Version != "" && metadata.Version != expected.Version || expected.Commit != "" && metadata.Commit != expected.Commit || metadata.Release != expected.Release || metadata.Target != "windows/amd64" || metadata.RoboRevCompatibility != buildinfo.SupportedRoboRev {
		t.Fatalf("artifact metadata does not match expected contract: got=%#v want=%#v", metadata, expected)
	}
	if expected.Release {
		if metadata.Version == "dev" || !buildinfo.IsStrictSemVer(metadata.Version) || !isLowerHexCommit(metadata.Commit) {
			t.Fatalf("release artifact metadata is not valid: %#v", metadata)
		}
	} else if metadata.Version != "dev" && !buildinfo.IsStrictSemVer(metadata.Version) {
		t.Fatalf("development artifact metadata has invalid version: %#v", metadata)
	}
	if metadata.Commit != "unknown" && !isLowerHexCommit(metadata.Commit) {
		t.Fatalf("artifact metadata has invalid commit: %#v", metadata)
	}
}

func TestArtifactExecRunnerUsesPrivateEnvironment(t *testing.T) {
	t.Setenv(artifactFixtureSecretEnv, "PRIVATE_SYNTHETIC_MARKER")
	root := t.TempDir()
	home := filepath.Join(root, "home")
	working := filepath.Join(root, "working")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(working, 0o700); err != nil {
		t.Fatal(err)
	}
	env := envWith(isolatedWindowsEnvironment(t, home), map[string]string{artifactFixtureHelperEnv: "1"})
	result, err := runArtifactFixture(env, working, "environment")
	if err != nil {
		t.Fatalf("isolated fixture failed: %v\n%s", err, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "HOME="+home) || !strings.Contains(result.Stdout, "USERPROFILE="+home) || !strings.Contains(result.Stdout, "CWD="+working) || !strings.Contains(result.Stdout, "SECRET_PRESENT=false") || strings.Contains(result.Stdout, "PRIVATE_SYNTHETIC_MARKER") {
		t.Fatalf("isolated fixture observed unexpected environment: %q", result.Stdout)
	}
}

func TestArtifactExecRunnerBoundsOutput(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	env := envWith(isolatedWindowsEnvironment(t, home), map[string]string{artifactFixtureHelperEnv: "1"})
	result, err := runArtifactFixtureWithLimit(env, filepath.Join(root, "working"), "output", 32)
	if err != nil {
		t.Fatalf("output fixture failed: %v", err)
	}
	if result.Stdout != strings.Repeat("x", 32) || !result.StdoutTruncated || result.Stderr != "" {
		t.Fatalf("output fixture was not bounded: %#v", result)
	}
}

func TestArtifactExecRunnerHonorsTimeout(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	env := envWith(isolatedWindowsEnvironment(t, home), map[string]string{artifactFixtureHelperEnv: "1"})
	started := time.Now()
	result, err := runArtifactFixtureWithTimeout(env, filepath.Join(root, "working"), "sleep", 100*time.Millisecond)
	if err == nil || result.ExitCode != -1 {
		t.Fatalf("sleep fixture did not time out: result=%#v err=%v", result, err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("timed-out fixture was not terminated promptly")
	}
}

func TestArtifactExecRunnerFailedCommandCleansDescendant(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	startedPath := filepath.Join(root, "child-started.txt")
	releasePath := filepath.Join(root, "release-parent.txt")
	survivedPath := filepath.Join(root, "child-survived.txt")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	env := envWith(isolatedWindowsEnvironment(t, home), map[string]string{artifactFixtureHelperEnv: "1"})
	ctx, cancel := context.WithTimeout(context.Background(), artifactFixtureCommandTimeout)
	defer cancel()
	resultCh := make(chan fixtureResult, 1)
	go func() {
		result, err := runArtifactFixtureCommand(ctx, env, filepath.Join(root, "working"), []string{"spawn-failed", startedPath, releasePath, survivedPath}, artifactFixtureCommandTimeout, artifactFixtureOutputLimit)
		resultCh <- fixtureResult{result: result, err: err}
	}()
	if !waitForArtifactFixtureFile(startedPath, 2*time.Second) {
		t.Fatal("failed fixture did not start its descendant")
	}
	childHandle, err := openArtifactFixtureChild(t, startedPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupArtifactFixtureChild(childHandle) })
	if err := os.WriteFile(releasePath, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	var outcome fixtureResult
	select {
	case outcome = <-resultCh:
	case <-time.After(artifactFixtureCommandTimeout + time.Second):
		t.Fatal("failed fixture parent did not exit after release")
	}
	result, err := outcome.result, outcome.err
	if err == nil || result.ExitCode != 23 {
		t.Fatalf("failed fixture did not return its expected failure: result=%#v err=%v", result, err)
	}
	status, waitErr := windows.WaitForSingleObject(childHandle, 2000)
	if waitErr != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("failed fixture descendant was not terminated by ExecRunner: status=%d err=%v", status, waitErr)
	}
	if _, statErr := os.Stat(survivedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed fixture descendant wrote its post-exit marker: %v", statErr)
	}
}

type fixtureResult struct {
	result proc.Result
	err    error
}

func openArtifactFixtureChild(t *testing.T, startedPath string) (windows.Handle, error) {
	t.Helper()
	data, err := os.ReadFile(startedPath)
	if err != nil {
		return 0, fmt.Errorf("read descendant PID: %w", err)
	}
	pidValue, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 32)
	if err != nil || pidValue == 0 {
		return 0, fmt.Errorf("invalid descendant PID %q", strings.TrimSpace(string(data)))
	}
	access := uint32(windows.SYNCHRONIZE | windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.PROCESS_TERMINATE)
	handle, err := windows.OpenProcess(access, false, uint32(pidValue))
	if err != nil {
		return 0, fmt.Errorf("open descendant process: %w", err)
	}
	imagePath, err := artifactFixtureProcessImage(handle)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return 0, fmt.Errorf("query descendant process image: %w", err)
	}
	expectedImage, err := filepath.Abs(os.Args[0])
	if err != nil || !samePath(imagePath, expectedImage) {
		_ = windows.CloseHandle(handle)
		return 0, fmt.Errorf("descendant image = %q, want test binary %q", imagePath, expectedImage)
	}
	status, err := windows.WaitForSingleObject(handle, 0)
	if err != nil || status != artifactFixtureWaitTimeout {
		_ = windows.CloseHandle(handle)
		return 0, fmt.Errorf("descendant was not alive before parent release: status=%d err=%v", status, err)
	}
	return handle, nil
}

func artifactFixtureProcessImage(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:size]), nil
}

func cleanupArtifactFixtureChild(handle windows.Handle) {
	if handle == 0 {
		return
	}
	status, err := windows.WaitForSingleObject(handle, 0)
	if err == nil && status != windows.WAIT_OBJECT_0 {
		_ = windows.TerminateProcess(handle, 1)
		_, _ = windows.WaitForSingleObject(handle, 2000)
	}
	_ = windows.CloseHandle(handle)
}

func runArtifactFixture(env []string, workingDirectory, mode string) (proc.Result, error) {
	return runArtifactFixtureWithTimeout(env, workingDirectory, mode, artifactFixtureCommandTimeout)
}

func runArtifactFixtureWithLimit(env []string, workingDirectory, mode string, outputLimit int) (proc.Result, error) {
	return runArtifactFixtureCommand(context.Background(), env, workingDirectory, []string{mode}, artifactFixtureCommandTimeout, outputLimit)
}

func runArtifactFixtureWithTimeout(env []string, workingDirectory, mode string, timeout time.Duration) (proc.Result, error) {
	return runArtifactFixtureCommand(context.Background(), env, workingDirectory, []string{mode}, timeout, artifactFixtureOutputLimit)
}

func runArtifactFixtureCommand(parent context.Context, env []string, workingDirectory string, fixtureArgs []string, timeout time.Duration, outputLimit int) (proc.Result, error) {
	if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
		return proc.Result{}, err
	}
	args := append([]string{"-test.run=^TestArtifactExecRunnerHelper$", "-test.count=1", "--"}, fixtureArgs...)
	return runArtifactCommand(parent, os.Args[0], args, workingDirectory, env, timeout, outputLimit)
}

func TestArtifactExecRunnerHelper(t *testing.T) {
	if os.Getenv(artifactFixtureHelperEnv) != "1" {
		return
	}
	args := artifactFixtureArguments()
	if len(args) == 0 {
		return
	}
	switch args[0] {
	case "environment":
		fmt.Fprintf(os.Stdout, "HOME=%s\nUSERPROFILE=%s\nCWD=%s\nSECRET_PRESENT=%t\n", os.Getenv("HOME"), os.Getenv("USERPROFILE"), mustFixtureWorkingDirectory(), os.Getenv(artifactFixtureSecretEnv) != "")
	case "output":
		_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 4096))
	case "sleep":
		time.Sleep(10 * time.Second)
	case "spawn-failed":
		if len(args) != 4 {
			os.Exit(24)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestArtifactExecRunnerHelper$", "-test.count=1", "--", "grandchild", args[1], args[3])
		child.Env = append(os.Environ(), artifactFixtureHelperEnv+"=1")
		child.Stdout = io.Discard
		child.Stderr = io.Discard
		if err := child.Start(); err != nil {
			os.Exit(25)
		}
		if !waitForArtifactFixtureFile(args[1], 2*time.Second) {
			os.Exit(26)
		}
		if !waitForArtifactFixtureFile(args[2], 2*time.Second) {
			os.Exit(28)
		}
		os.Exit(23)
	case "grandchild":
		if len(args) != 3 {
			os.Exit(27)
		}
		_ = os.WriteFile(args[1], []byte(strconv.Itoa(os.Getpid())), 0o600)
		time.Sleep(10 * time.Second)
		_ = os.WriteFile(args[2], []byte("survived"), 0o600)
	}
}

func artifactFixtureArguments() []string {
	for index, arg := range os.Args {
		if arg == "--" && index+1 < len(os.Args) {
			return os.Args[index+1:]
		}
	}
	return nil
}

func mustFixtureWorkingDirectory() string {
	working, err := os.Getwd()
	if err != nil {
		return "<error>"
	}
	return working
}

func waitForArtifactFixtureFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}
