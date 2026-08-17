package process

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

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
