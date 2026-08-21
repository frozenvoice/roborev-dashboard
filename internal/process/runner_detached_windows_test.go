//go:build windows

package process

import (
	"context"
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
)

// TestExecRunnerDetachedParentHelper is the direct child used by
// TestExecRunnerAllowDetachedChildrenLeavesDaemonChildAlive. It starts a
// second test process, publishes readiness, then keeps updating its own
// heartbeat while waiting. The test runner must kill this direct parent while
// leaving the child process alive.
func TestExecRunnerDetachedParentHelper(t *testing.T) {
	if os.Getenv("ROBOREV_DETACHED_PARENT_HELPER") != "1" {
		return
	}
	args := helperArgsAfterSeparator()
	if len(args) != 3 {
		os.Exit(2)
	}
	parentHeartbeat, childPID, childHeartbeat := args[0], args[1], args[2]
	child := exec.Command(os.Args[0], "-test.run=^TestExecRunnerDetachedChildHelper$", "--", childPID, childHeartbeat)
	child.Env = append(os.Environ(), "ROBOREV_DETACHED_CHILD_HELPER=1")
	child.Stdout = io.Discard
	child.Stderr = io.Discard
	if err := child.Start(); err != nil {
		os.Exit(3)
	}
	if !waitForDetachedFile(childPID) {
		os.Exit(4)
	}
	if err := os.WriteFile(parentHeartbeat, []byte("0"), 0o600); err != nil {
		os.Exit(5)
	}
	var counter uint64
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		counter++
		_ = os.WriteFile(parentHeartbeat, []byte(strconv.FormatUint(counter, 10)), 0o600)
	}
}

// TestExecRunnerDetachedChildHelper is deliberately independent of the
// direct parent. It keeps a heartbeat until the outer test kills and waits
// for this PID during cleanup.
func TestExecRunnerDetachedChildHelper(t *testing.T) {
	if os.Getenv("ROBOREV_DETACHED_CHILD_HELPER") != "1" {
		return
	}
	args := helperArgsAfterSeparator()
	if len(args) != 2 {
		os.Exit(2)
	}
	childPID, heartbeat := args[0], args[1]
	if err := os.WriteFile(childPID, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(heartbeat, []byte("0"), 0o600); err != nil {
		os.Exit(4)
	}
	var counter uint64
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		counter++
		_ = os.WriteFile(heartbeat, []byte(strconv.FormatUint(counter, 10)), 0o600)
	}
}

func helperArgsAfterSeparator() []string {
	for index, arg := range os.Args {
		if arg == "--" {
			return os.Args[index+1:]
		}
	}
	return nil
}

func waitForDetachedFile(path string) bool {
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		select {
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

func waitForDetachedPID(path string) (int, error) {
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if data, err := os.ReadFile(path); err == nil {
			if pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data))); parseErr == nil && pid > 0 {
				return pid, nil
			}
		}
		select {
		case <-deadline.C:
			return 0, errors.New("child PID was not published")
		case <-ticker.C:
		}
	}
}

func readDetachedCounter(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse heartbeat: %w", err)
	}
	return value, nil
}

func waitForDetachedCounterChange(path string, previous uint64, timeout time.Duration) (uint64, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if value, err := readDetachedCounter(path); err == nil && value != previous {
			return value, nil
		}
		select {
		case <-deadline.C:
			return 0, errors.New("heartbeat did not advance")
		case <-ticker.C:
		}
	}
}

func waitForDetachedCounterStable(path string, previous uint64, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if value, err := readDetachedCounter(path); err == nil && value != previous {
			return errors.New("heartbeat advanced after direct parent cancellation")
		}
		select {
		case <-deadline.C:
			return nil
		case <-ticker.C:
		}
	}
}

func killAndWaitDetachedPID(pid int) {
	if pid <= 0 {
		return
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = process.Kill()
	done := make(chan struct{})
	go func() {
		_, _ = process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

func TestExecRunnerAllowDetachedChildrenLeavesDaemonChildAlive(t *testing.T) {
	temp := t.TempDir()
	parentHeartbeat := filepath.Join(temp, "parent-heartbeat.txt")
	childPIDPath := filepath.Join(temp, "child-pid.txt")
	childHeartbeat := filepath.Join(temp, "child-heartbeat.txt")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := Command{
		Executable: os.Args[0],
		Args: []string{
			"-test.run=^TestExecRunnerDetachedParentHelper$",
			"--", parentHeartbeat, childPIDPath, childHeartbeat,
		},
		Env:                   append(os.Environ(), "ROBOREV_DETACHED_PARENT_HELPER=1"),
		Timeout:               2 * time.Second,
		WaitDelay:             500 * time.Millisecond,
		AllowDetachedChildren: true,
	}
	t.Cleanup(func() {
		if data, err := os.ReadFile(childPIDPath); err == nil {
			if pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data))); parseErr == nil {
				killAndWaitDetachedPID(pid)
			}
		}
	})
	type runResult struct {
		result Result
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		result, err := (ExecRunner{}).Run(ctx, command)
		done <- runResult{result: result, err: err}
	}()

	if !waitForDetachedFile(parentHeartbeat) || !waitForDetachedFile(childPIDPath) || !waitForDetachedFile(childHeartbeat) {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		t.Fatal("detached helper processes did not publish readiness")
	}
	if _, err := waitForDetachedPID(childPIDPath); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case outcome := <-done:
		if outcome.err == nil || !errors.Is(outcome.err, context.Canceled) || !outcome.result.Started || outcome.result.ExitCode != -1 {
			t.Fatalf("direct parent result=%#v err=%v", outcome.result, outcome.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("direct parent did not exit after cancellation")
	}
	parentAfterCancel, err := readDetachedCounter(parentHeartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForDetachedCounterStable(parentHeartbeat, parentAfterCancel, 250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	childAfterCancel, err := readDetachedCounter(childHeartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := waitForDetachedCounterChange(childHeartbeat, childAfterCancel, 250*time.Millisecond); err != nil {
		t.Fatalf("detached child did not survive direct parent cancellation: %v", err)
	}
}
