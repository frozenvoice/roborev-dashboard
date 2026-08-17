package roborev

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

func buildFakeRoboRev(t *testing.T) string {
	t.Helper()
	name := "fake-roborev"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-o", output, "../../testdata/fakeroborev")
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fake roborev: %v\n%s", err, data)
	}
	return output
}

func TestClientUsesFakeExecutableMachineContracts(t *testing.T) {
	binary := buildFakeRoboRev(t)
	client, err := NewClient(binary, proc.ExecRunner{}, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	installation := client.Installation(ctx)
	if !installation.Installed || installation.Version != "v0.64.0" {
		t.Fatalf("unexpected installation: %#v", installation)
	}
	jobs, err := client.List(ctx, `E:\한글 프로젝트\repo`, 100)
	if err != nil || len(jobs) == 0 {
		t.Fatalf("list failed: %v %s", err, jobs)
	}
	prompt, err := client.Prompt(ctx, 42)
	if err != nil || prompt == "" {
		t.Fatalf("prompt failed: %v", err)
	}
	if err := client.Comment(ctx, 42, "pasted; $(text) & 한글\nline 2"); err != nil {
		t.Fatalf("comment failed: %v", err)
	}
}
