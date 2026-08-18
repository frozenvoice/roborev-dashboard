package roborev

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

type rebindRunner struct {
	stdout string
	err    error
}

func (r rebindRunner) Run(context.Context, proc.Command) (proc.Result, error) {
	return proc.Result{Stdout: r.stdout}, r.err
}

func TestRebindVerifiesCandidateBeforeAtomicSwap(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.exe")
	newPath := filepath.Join(dir, "new.exe")
	if err := os.WriteFile(old, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(old, rebindRunner{stdout: `{"name":"roborev","version":"v0.64.0"}`}, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Rebind(context.Background(), newPath); err != nil {
		t.Fatal(err)
	}
	if got := client.BinaryPath(); got != newPath {
		t.Fatalf("binary = %q, want %q", got, newPath)
	}
	for _, test := range []struct {
		name   string
		stdout string
		want   error
	}{
		{"unsupported version", `{"name":"roborev","version":"v0.65.0"}`, roborevcompat.ErrUnsupportedVersion},
		{"wrong name", `{"name":"other","version":"v0.64.0"}`, roborevcompat.ErrUnsupportedVersion},
		{"malformed", `{not-json`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			client.Runner = rebindRunner{stdout: test.stdout}
			err := client.Rebind(context.Background(), old)
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("rebind error = %v", err)
			}
			if err == nil {
				t.Fatal("malformed rebind unexpectedly succeeded")
			}
			if got := client.BinaryPath(); got != newPath {
				t.Fatalf("failed rebind changed binding to %q", got)
			}
		})
	}
}

type generationRaceRunner struct {
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	stdout  string
}

func (r *generationRaceRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	if len(command.Args) > 0 && command.Args[0] == "version" && r.started != nil {
		select {
		case <-r.started:
		default:
			close(r.started)
		}
		<-r.release
	}
	r.mu.Lock()
	stdout := r.stdout
	r.mu.Unlock()
	return proc.Result{Stdout: stdout}, nil
}

func TestVersionCacheRejectsStaleGenerationResult(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.exe")
	newPath := filepath.Join(dir, "new.exe")
	if err := os.WriteFile(old, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &generationRaceRunner{started: make(chan struct{}), release: make(chan struct{}), stdout: `{"name":"roborev","version":"v0.64.0"}`}
	client, err := NewClient(old, runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := client.detectVersion(context.Background()); result <- err }()
	<-runner.started
	client.bindingMu.Lock()
	client.binding.binary = newPath
	client.binding.generation++
	client.bindingMu.Unlock()
	close(runner.release)
	if err := <-result; !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("stale result error = %v", err)
	}
	runner.started = nil
	if version, err := client.detectVersion(context.Background()); err != nil || version != "v0.64.0" {
		t.Fatalf("fresh generation probe = %q, %v", version, err)
	}
}
