package process

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExecRunnerStdinHelperProcess(t *testing.T) {
	if os.Getenv("ROBOREV_STDIN_HELPER") == "1" {
		data, _ := io.ReadAll(os.Stdin)
		_, _ = os.Stdout.Write(data)
		os.Exit(0)
	}
	payload := "한글\nquotes \" ' & | ; $(literal) `literal`\tend"
	env := append(os.Environ(), "ROBOREV_STDIN_HELPER=1")
	result, err := (ExecRunner{}).Run(context.Background(), Command{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestExecRunnerStdinHelperProcess$"},
		Env:        env,
		Stdin:      payload,
		Timeout:    time.Second,
	})
	if err != nil || result.Stdout != payload || !result.Started {
		t.Fatalf("stdin result=%#v err=%v", result, err)
	}
}

func TestExecRunnerRejectsOversizedStdinBeforeStarting(t *testing.T) {
	result, err := (ExecRunner{}).Run(context.Background(), Command{
		Executable: "definitely-not-started",
		Stdin:      strings.Repeat("x", DefaultStdinLimit+1),
	})
	if err == nil || !strings.Contains(err.Error(), "stdin exceeds") {
		t.Fatalf("expected bounded stdin error, result=%#v err=%v", result, err)
	}
}

func TestExecRunnerStdinLimitCanBeSetForASeparateProtocol(t *testing.T) {
	if _, err := (ExecRunner{}).Run(context.Background(), Command{
		Executable: "definitely-not-started",
		Stdin:      strings.Repeat("x", DefaultStdinLimit+1),
		StdinLimit: DefaultStdinLimit + 1,
	}); err == nil || !strings.Contains(err.Error(), "start process") {
		t.Fatalf("expected process start error after custom bound, err=%v", err)
	}
}

func TestExecRunnerSeparatesArgsAndBoundsOutput(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Tier 1 argument test")
	}
	result, err := (ExecRunner{}).Run(context.Background(), Command{
		Executable:  os.Getenv("COMSPEC"),
		Args:        []string{"/d", "/s", "/c", "echo 123456789"},
		Timeout:     time.Second,
		OutputLimit: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "12345" || !result.StdoutTruncated {
		t.Fatalf("unexpected bounded output: %#v", result)
	}
}

func TestExecRunnerHonorsTimeout(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Tier 1 cancellation test")
	}
	started := time.Now()
	result, err := (ExecRunner{}).Run(context.Background(), Command{
		Executable: "powershell.exe",
		Args:       []string{"-NoProfile", "-Command", "Start-Sleep -Seconds 5"},
		Timeout:    100 * time.Millisecond,
	})
	if err == nil || result.ExitCode != -1 {
		t.Fatalf("expected timeout cancellation, got result=%#v err=%v", result, err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("timed out process was not terminated promptly")
	}
}

func TestExecRunnerTimeoutKillsWindowsDescendants(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows process-tree test")
	}
	temp := t.TempDir()
	marker := filepath.Join(temp, "child-survived.txt")
	childScript := filepath.Join(temp, "delayed-child.cmd")
	script := "@echo off\r\nping 127.0.0.1 -n 4 >nul\r\necho child>\"" + marker + "\"\r\n"
	if err := os.WriteFile(childScript, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	parentCommand := "Start-Process -WindowStyle Hidden -FilePath '" + childScript + "'; Start-Sleep -Seconds 10"
	_, err := (ExecRunner{}).Run(context.Background(), Command{Executable: "powershell.exe", Args: []string{"-NoProfile", "-Command", parentCommand}, Timeout: time.Second})
	if err == nil {
		t.Fatal("expected parent timeout")
	}
	time.Sleep(4 * time.Second)
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("descendant survived cancellation: %v", statErr)
	}
}
