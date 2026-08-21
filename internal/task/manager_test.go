package task

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type fakeRegistry struct {
	path         string
	identity     string
	repo         gitx.Repository
	selectedPath string
	selectedID   string
}

func (f *fakeRegistry) ResolveIdentity(identity string) (string, error) {
	if identity != f.identity {
		return "", errors.New("not found")
	}
	return f.path, nil
}
func (f *fakeRegistry) InspectIdentity(context.Context, string) (gitx.Repository, error) {
	return f.repo, nil
}
func (f *fakeRegistry) Identity(path string) (string, error) {
	if path != f.path {
		if path == f.selectedPath && f.selectedID != "" {
			return f.selectedID, nil
		}
		return "", errors.New("not found")
	}
	return f.identity, nil
}
func (f *fakeRegistry) Selected() string {
	if f.selectedPath != "" {
		return f.selectedPath
	}
	return f.path
}

type fakeAdapter struct {
	preflight roborev.TaskPreflight
	safety    Safety
	receipt   roborev.TaskReceipt
	err       error
	called    int
	request   roborev.TaskRequest
	mu        sync.Mutex
}

func (f *fakeAdapter) TaskPreflight(context.Context) (roborev.TaskPreflight, error) {
	return f.preflight, nil
}
func (f *fakeAdapter) RunTask(_ context.Context, request roborev.TaskRequest) (roborev.TaskReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called++
	f.request = request
	return f.receipt, f.err
}

func testManager(t *testing.T) (*Manager, *fakeRegistry, *fakeAdapter) {
	t.Helper()
	registry := &fakeRegistry{path: `C:\repo`, identity: `c:\repo`, repo: gitx.Repository{Path: `C:\repo`, Head: "abc", Branch: "main"}}
	adapter := &fakeAdapter{preflight: roborev.TaskPreflight{Binary: `C:\roborev.exe`, Version: "v0.64.0", DaemonVersion: "v0.64.0", BindingIdentity: "1:binary", Safety: Safety{Known: true}}, safety: Safety{Known: true}, receipt: roborev.TaskReceipt{JobID: 42, JobUUID: "123e4567-e89b-12d3-a456-426614174000", GitRef: "run", Status: "queued"}}
	m, err := NewManager(Dependencies{Registry: registry, RoboRev: adapter, Gate: operation.NewGate(), Secret: []byte("01234567890123456789012345678901")})
	if err != nil {
		t.Fatal(err)
	}
	return m, registry, adapter
}

func TestValidateTaskAllowsPromptBytesAndRejectsControls(t *testing.T) {
	if err := ValidateTask("한글\n\tquotes ' \" & | $(x)"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "  \n\t", "nul\x00", "bad\x01"} {
		if err := ValidateTask(value); err == nil {
			t.Fatalf("ValidateTask(%q) unexpectedly succeeded", value)
		}
	}
	if err := ValidateTask(string(make([]byte, MaxTaskBytes+1))); err == nil {
		t.Fatal("oversized task unexpectedly succeeded")
	}
}

func TestPrepareAndStartUsesExactStdinAndFixedArgs(t *testing.T) {
	m, _, adapter := testManager(t)
	input := "line one\n한글 ' \" & | $(echo no)"
	preview, err := m.Prepare(context.Background(), Request{RepositoryIdentity: `c:\repo`, Task: input})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := preview.Command.Args, []string{"run", "--agent", "codex", "--json"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("args = %#v", got)
	}
	if preview.Command.Stdin != input {
		t.Fatalf("stdin changed: %q", preview.Command.Stdin)
	}
	receipt, err := m.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: `c:\repo`, Task: input}, PreviewToken: preview.PreviewToken})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.JobID != 42 || adapter.called != 1 || adapter.request.Task != input || adapter.request.Agent != "codex" {
		t.Fatalf("receipt/request = %#v %#v", receipt, adapter.request)
	}
}

func TestPreviewTokenIsOneTimeAndBindsRepositoryAndSafety(t *testing.T) {
	m, registry, adapter := testManager(t)
	preview, err := m.Prepare(context.Background(), Request{RepositoryIdentity: `c:\repo`, Task: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: `c:\repo`, Task: "inspect"}, PreviewToken: preview.PreviewToken}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: `c:\repo`, Task: "inspect"}, PreviewToken: preview.PreviewToken}); err == nil {
		t.Fatal("second use unexpectedly succeeded")
	} else if typedErr, ok := err.(*Error); !ok || typedErr.Kind != ErrorConflict {
		t.Fatalf("second use = %v", err)
	}
	// A new token is rejected if the selected repository or effective safety
	// snapshot changed after preview.
	preview, err = m.Prepare(context.Background(), Request{RepositoryIdentity: `c:\repo`, Task: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	registry.path = `C:\other`
	registry.repo.Path = `C:\other`
	if _, err := m.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: `c:\repo`, Task: "inspect"}, PreviewToken: preview.PreviewToken}); err == nil {
		t.Fatal("repository change unexpectedly accepted")
	}
	registry.path, registry.repo.Path = `C:\repo`, `C:\repo`
	adapter.preflight.Safety.AllowUnsafeAgents = true
	if _, err := m.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: `c:\repo`, Task: "inspect"}, PreviewToken: preview.PreviewToken}); err == nil {
		t.Fatal("unsafe safety state unexpectedly accepted")
	}
}

func TestStartMapsEnqueueFailureToAmbiguousWithoutTaskBody(t *testing.T) {
	m, _, adapter := testManager(t)
	adapter.err = errors.New("daemon accepted after transport failure: secret task body")
	preview, err := m.Prepare(context.Background(), Request{RepositoryIdentity: `c:\repo`, Task: "do not echo"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: `c:\repo`, Task: "do not echo"}, PreviewToken: preview.PreviewToken})
	var typedErr *Error
	if !errors.As(err, &typedErr) || typedErr.Kind != ErrorAmbiguous || stringsContains(typedErr.Error(), "do not echo") {
		t.Fatalf("ambiguous error = %v", err)
	}
}

func TestStartPreservesTypedSafetyAndBindingErrors(t *testing.T) {
	m, _, adapter := testManager(t)
	preview, err := m.Prepare(context.Background(), Request{RepositoryIdentity: `c:\repo`, Task: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	adapter.err = &roborev.TaskError{Kind: roborev.TaskErrorUnsafe, Err: roborev.ErrTaskUnsafeConfig}
	_, err = m.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: `c:\repo`, Task: "inspect"}, PreviewToken: preview.PreviewToken})
	var typedErr *Error
	if !errors.As(err, &typedErr) || typedErr.Kind != ErrorConflict || typedErr.Code != "unsafe_task_configuration" {
		t.Fatalf("unsafe error = %v", err)
	}

	preview, err = m.Prepare(context.Background(), Request{RepositoryIdentity: `c:\repo`, Task: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	adapter.err = &roborev.TaskError{Kind: roborev.TaskErrorConflict, Err: roborev.ErrBindingChanged}
	_, err = m.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: `c:\repo`, Task: "inspect"}, PreviewToken: preview.PreviewToken})
	if !errors.As(err, &typedErr) || typedErr.Kind != ErrorConflict {
		t.Fatalf("binding error = %v", err)
	}
}

func TestStartRejectsHeadBindingVersionAndSelectedChangesBeforeRun(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeRegistry, *fakeAdapter)
	}{
		{name: "head", mutate: func(registry *fakeRegistry, _ *fakeAdapter) {
			registry.repo.Head = "changed-head"
		}},
		{name: "binding", mutate: func(_ *fakeRegistry, adapter *fakeAdapter) {
			adapter.preflight.BindingIdentity = "2:binary"
		}},
		{name: "version", mutate: func(_ *fakeRegistry, adapter *fakeAdapter) {
			adapter.preflight.Version = "v0.64.1"
			adapter.preflight.DaemonVersion = "v0.64.1"
		}},
		{name: "selected repository", mutate: func(registry *fakeRegistry, _ *fakeAdapter) {
			registry.selectedPath = `C:\other`
			registry.selectedID = `c:\other`
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, registry, adapter := testManager(t)
			preview, err := m.Prepare(context.Background(), Request{RepositoryIdentity: `c:\repo`, Task: "inspect"})
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(registry, adapter)
			if _, err := m.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: `c:\repo`, Task: "inspect"}, PreviewToken: preview.PreviewToken}); err == nil {
				t.Fatal("stale preview unexpectedly accepted")
			}
			if adapter.called != 0 {
				t.Fatalf("RunTask calls = %d", adapter.called)
			}
		})
	}
}

func stringsContains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestReceiptValidation(t *testing.T) {
	if err := ValidateReceipt(Receipt{JobID: 1, JobUUID: "123e4567-e89b-12d3-a456-426614174000", GitRef: "run", Status: "queued"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReceipt(Receipt{Skipped: true, Reason: "already queued"}); err != nil {
		t.Fatal(err)
	}
	for _, receipt := range []Receipt{{}, {JobID: -1}, {JobID: 1, JobUUID: "bad", GitRef: "run", Status: "queued"}, {JobID: 1, JobUUID: "123e4567-e89b-12d3-a456-426614174000", GitRef: "review", Status: "queued"}, {JobID: 1, JobUUID: "123e4567-e89b-12d3-a456-426614174000", GitRef: "run", Status: "running"}} {
		if err := ValidateReceipt(receipt); err == nil {
			t.Fatalf("receipt %#v unexpectedly valid", receipt)
		}
	}
}

func TestShutdownCancelsActiveEnqueue(t *testing.T) {
	m, _, adapter := testManager(t)
	block := make(chan struct{})
	adapter.err = nil
	// Replace the fake's operation with a context-aware adapter by using a
	// small wrapper; the manager must wait until the enqueue returns.
	service := &blockingAdapter{fakeAdapter: adapter, block: block}
	m.service = service
	preview, err := m.Prepare(context.Background(), Request{RepositoryIdentity: `c:\repo`, Task: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := m.Start(context.Background(), StartRequest{Request: Request{RepositoryIdentity: `c:\repo`, Task: "inspect"}, PreviewToken: preview.PreviewToken})
		done <- err
	}()
	deadline := time.After(time.Second)
	for {
		service.mu.Lock()
		called := service.called
		service.mu.Unlock()
		if called > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("enqueue did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(block)
	if err := <-done; err == nil {
		t.Fatal("canceled enqueue unexpectedly succeeded")
	}
}

func TestAcceptedEnqueueOutlivesCallerCancellation(t *testing.T) {
	m, _, adapter := testManager(t)
	block := make(chan struct{})
	service := &blockingAdapter{fakeAdapter: adapter, block: block}
	m.service = service
	preview, err := m.Prepare(context.Background(), Request{RepositoryIdentity: `c:\repo`, Task: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	callerCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := m.Start(callerCtx, StartRequest{Request: Request{RepositoryIdentity: `c:\repo`, Task: "inspect"}, PreviewToken: preview.PreviewToken})
		done <- err
	}()
	deadline := time.After(time.Second)
	for {
		service.mu.Lock()
		called := service.called
		service.mu.Unlock()
		if called > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("enqueue did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	close(block)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("accepted enqueue canceled with caller: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("enqueue did not complete")
	}
}

type blockingAdapter struct {
	*fakeAdapter
	block chan struct{}
}

func (b *blockingAdapter) RunTask(ctx context.Context, request roborev.TaskRequest) (roborev.TaskReceipt, error) {
	b.mu.Lock()
	b.called++
	b.request = request
	b.mu.Unlock()
	select {
	case <-b.block:
		return b.receipt, b.err
	case <-ctx.Done():
		return roborev.TaskReceipt{}, ctx.Err()
	}
}
