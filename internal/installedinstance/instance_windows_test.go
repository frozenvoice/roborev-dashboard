//go:build windows

package installedinstance

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func testInstanceConfig(t *testing.T) Config {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{ExecutablePath: exe, RequestTimeout: 2 * time.Second, StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second}
}

func TestAcquireRejectsDuplicateAndRoundTripsStatus(t *testing.T) {
	config := testInstanceConfig(t)
	manager, err := Acquire(config, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.SetStatus(Status{URL: "http://127.0.0.1:7410"})
	runDone := make(chan struct{})
	var runErr error
	go func() {
		runErr = manager.Run(context.Background())
		close(runDone)
	}()

	duplicate, err := Acquire(config, Callbacks{})
	if duplicate != nil {
		_ = duplicate.Close()
	}
	if err != ErrAlreadyRunning {
		t.Fatalf("duplicate acquire error = %v, want %v", err, ErrAlreadyRunning)
	}

	var status Status
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		status, err = Query(context.Background(), config)
		if err == nil && status.Running && status.URL != "" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || status.PID != uint32(os.Getpid()) || status.URL != "http://127.0.0.1:7410" || status.InstanceID == "" {
		t.Fatalf("status = %+v, run error = %v", status, runErr)
	}
	_ = manager.Close()
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("control server did not stop")
	}
}

func TestAcquireFailsClosedWhenControlPipeCannotBeClaimed(t *testing.T) {
	config := testInstanceConfig(t)
	exe, err := canonicalPath(config.ExecutablePath)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	pipeName := `\\.\pipe\RoboRevDashboard-` + scopedHash(sid, exe)
	listener, err := createControlListener(pipeName, sid)
	if err != nil {
		t.Fatal(err)
	}
	if manager, acquireErr := Acquire(config, Callbacks{}); acquireErr == nil {
		_ = manager.Close()
		t.Fatal("Acquire claimed a pipe already owned by another listener")
	}
	_ = listener.Close()
	manager, err := Acquire(config, Callbacks{})
	if err != nil {
		t.Fatalf("Acquire after failed claim = %v", err)
	}
	_ = manager.Close()
}

func TestStopStopsExactChildProcess(t *testing.T) {
	if os.Getenv("ROBODASH_INSTALLED_CHILD") == "1" {
		runInstalledChild(t)
		return
	}
	child := exec.Command(os.Args[0], "-test.run=TestStopStopsExactChildProcess", "--")
	child.Env = append(os.Environ(), "ROBODASH_INSTALLED_CHILD=1")
	child.Stdout = io.Discard
	child.Stderr = io.Discard
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	config := testInstanceConfig(t)
	var status Status
	var err error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		status, err = Query(context.Background(), config)
		if err == nil && status.Running && status.PID == uint32(child.Process.Pid) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatal(err)
	}
	if !status.Running || status.PID != uint32(child.Process.Pid) {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatalf("child status = %+v", status)
	}
	if err := Stop(context.Background(), config); err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("child exit = %v", err)
	}
	if err := Stop(context.Background(), config); err != nil {
		t.Fatalf("missing instance stop = %v", err)
	}
}

func TestPeerWithDifferentExecutableIsRejected(t *testing.T) {
	if os.Getenv("ROBODASH_WRONG_PEER") == "1" {
		config := testInstanceConfig(t)
		config.ExecutablePath = os.Getenv("ROBODASH_TARGET_EXE")
		if _, err := Query(context.Background(), config); err == nil {
			t.FailNow()
		}
		return
	}
	config := testInstanceConfig(t)
	manager, err := Acquire(config, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.SetStatus(Status{URL: "http://127.0.0.1:7410"})
	runDone := make(chan struct{})
	go func() {
		_ = manager.Run(context.Background())
		close(runDone)
	}()

	copyPath := filepath.Join(t.TempDir(), "different-dashboard-test.exe")
	contents, err := os.ReadFile(config.ExecutablePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(copyPath, "-test.run=TestPeerWithDifferentExecutable", "--")
	child.Env = append(os.Environ(), "ROBODASH_WRONG_PEER=1", "ROBODASH_TARGET_EXE="+config.ExecutablePath)
	child.Stdout = io.Discard
	child.Stderr = io.Discard
	if err := child.Run(); err != nil {
		t.Fatalf("different executable child = %v", err)
	}
	_ = manager.Close()
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("control server did not stop")
	}
}

func TestStrictControlJSONRejectsUnknownDuplicateAndTrailing(t *testing.T) {
	for _, encoded := range []string{
		`{"version":1,"operation":"status","unexpected":true}`,
		`{"version":1,"operation":"status","operation":"shutdown"}`,
		`{"version":1,"operation":"status"}{"version":1}`,
	} {
		var request pipeRequest
		if err := decodeStrict([]byte(encoded), &request); err == nil {
			t.Fatalf("decodeStrict accepted %s", encoded)
		}
	}
}

func TestSetStatusCannotOverrideInstanceIdentity(t *testing.T) {
	config := testInstanceConfig(t)
	manager, err := Acquire(config, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	initial := manager.Status()
	manager.SetStatus(Status{PID: 999, InstanceID: "spoofed", ExecutablePath: `C:\spoofed.exe`, Running: false, URL: "http://127.0.0.1:7410"})
	status := manager.Status()
	if status.PID != initial.PID || status.InstanceID != initial.InstanceID || status.ExecutablePath != initial.ExecutablePath || !status.Running {
		t.Fatalf("SetStatus changed immutable identity: initial=%+v status=%+v", initial, status)
	}
}

func TestShutdownRejectsStaleInstanceID(t *testing.T) {
	config := testInstanceConfig(t)
	manager, err := Acquire(config, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.SetStatus(Status{URL: "http://127.0.0.1:7410"})
	runDone := make(chan struct{})
	go func() {
		_ = manager.Run(context.Background())
		close(runDone)
	}()
	status := manager.Status()
	response, err := request(context.Background(), config, pipeRequest{Version: 1, Operation: "shutdown", PID: status.PID, InstanceID: "stale-instance-id"})
	if err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error != "instance identity mismatch" {
		t.Fatalf("stale shutdown response = %+v", response)
	}
	_ = manager.Close()
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("control server did not stop")
	}
}

func TestQuietActivationWaitsReadyWithoutOpeningBrowser(t *testing.T) {
	config := testInstanceConfig(t)
	activated := make(chan struct{}, 1)
	manager, err := Acquire(config, Callbacks{Activate: func(Status) { activated <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.SetStatus(Status{URL: "http://127.0.0.1:7410"})
	runDone := make(chan struct{})
	go func() {
		_ = manager.Run(context.Background())
		close(runDone)
	}()
	if _, err := ActivateQuiet(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	select {
	case <-activated:
		t.Fatal("quiet activation invoked browser callback")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := Activate(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	select {
	case <-activated:
	case <-time.After(2 * time.Second):
		t.Fatal("activation did not invoke callback")
	}
	_ = manager.Close()
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("control server did not stop")
	}
}

func runInstalledChild(t *testing.T) {
	t.Helper()
	config := testInstanceConfig(t)
	stopped := make(chan struct{}, 1)
	manager, err := Acquire(config, Callbacks{Shutdown: func(context.Context) error {
		select {
		case stopped <- struct{}{}:
		default:
		}
		return nil
	}})
	if err != nil {
		t.FailNow()
	}
	manager.SetStatus(Status{URL: "http://127.0.0.1:7410"})
	runDone := make(chan struct{})
	go func() {
		_ = manager.Run(context.Background())
		close(runDone)
	}()
	<-stopped
	_ = manager.Close()
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.FailNow()
	}
}
