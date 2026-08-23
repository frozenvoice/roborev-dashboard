package insights

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
)

type workflowRegistry struct{ repo gitx.Repository }

func (r workflowRegistry) ResolveIdentity(string) (string, error) { return r.repo.Path, nil }
func (r workflowRegistry) InspectIdentity(context.Context, string) (gitx.Repository, error) {
	return r.repo, nil
}

type workflowAdapter struct {
	mu         sync.Mutex
	preflights int
	launches   int
	requests   []Request
	commands   []Command
	result     LaunchResult
	binding    string
}

func (a *workflowAdapter) InsightsPreflight(_ context.Context, request Request) (Preflight, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.preflights++
	binding := a.binding
	if binding == "" {
		binding = "1:roborev"
	}
	a.requests = append(a.requests, request)
	command, err := BuildCommand("roborev", request)
	if err != nil {
		return Preflight{}, err
	}
	a.commands = append(a.commands, command)
	return Preflight{Command: command, Binding: binding, CLIVersion: "v0.64.0", DaemonVersion: "v0.64.0"}, nil
}
func (a *workflowAdapter) Insights(context.Context, Request, Preflight) (LaunchResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.launches++
	return a.result, nil
}

func TestManagerTokenIsSingleUseUnderConcurrentConfirm(t *testing.T) {
	adapter := &workflowAdapter{result: LaunchResult{Receipt: &Receipt{JobID: 42, Agent: "codex", Since: time.Now()}}}
	manager, err := NewManager(adapter, workflowRegistry{repo: gitx.Repository{Name: "repo", Path: `E:\repo`, Branch: "main"}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := manager.Preview(context.Background(), PreviewRequest{RepositoryIdentity: "repo-a", Window: Window30d, BranchScope: BranchAll})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]StartResponse, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], errs[index] = manager.Start(context.Background(), preview.Token)
		}(i)
	}
	wg.Wait()
	adapter.mu.Lock()
	launches := adapter.launches
	adapter.mu.Unlock()
	if launches != 1 {
		t.Fatalf("launches=%d, want 1", launches)
	}
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("expected one success and one consumed-token error: %#v %#v", errs[0], errs[1])
	}
}

func TestManagerDoesNotRestoreTokenAfterLaunchAmbiguity(t *testing.T) {
	adapter := &workflowAdapter{result: LaunchResult{Unverified: true, Message: "ambiguous"}}
	manager, err := NewManager(adapter, workflowRegistry{repo: gitx.Repository{Name: "repo", Path: `E:\repo`, Branch: "main"}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := manager.Preview(context.Background(), PreviewRequest{RepositoryIdentity: "repo-a", Window: Window30d, BranchScope: BranchAll})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.Start(context.Background(), preview.Token)
	if err != nil || result.Status != "unverified" {
		t.Fatalf("ambiguous launch=%#v err=%v", result, err)
	}
	if _, err := manager.Start(context.Background(), preview.Token); err == nil {
		t.Fatal("ambiguous token was restored")
	}
}

func TestManagerRejectsCurrentBranchDriftBeforeLaunch(t *testing.T) {
	registry := &changingRegistry{repo: gitx.Repository{Name: "repo", Path: `E:\repo`, Branch: "main"}}
	adapter := &workflowAdapter{result: LaunchResult{Receipt: &Receipt{JobID: 42, Agent: "codex", Since: time.Now()}}}
	manager, err := NewManager(adapter, registry)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := manager.Preview(context.Background(), PreviewRequest{RepositoryIdentity: "repo-a", Window: Window30d, BranchScope: BranchCurrent})
	if err != nil {
		t.Fatal(err)
	}
	registry.repo.Branch = "feature"
	if _, err := manager.Start(context.Background(), preview.Token); err == nil {
		t.Fatal("branch drift was accepted")
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.launches != 0 {
		t.Fatal("branch drift launched Insights")
	}
}

func TestManagerRejectsDetachedCurrentPreviewBeforePreflight(t *testing.T) {
	adapter := &workflowAdapter{result: LaunchResult{Receipt: &Receipt{JobID: 42}}}
	manager, err := NewManager(adapter, workflowRegistry{repo: gitx.Repository{Name: "repo", Path: `E:\repo`, Branch: "detached", Detached: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Preview(context.Background(), PreviewRequest{RepositoryIdentity: "repo-a", Window: Window30d, BranchScope: BranchCurrent}); err == nil {
		t.Fatal("detached current preview was accepted")
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.preflights != 0 {
		t.Fatalf("preflights=%d, want 0", adapter.preflights)
	}
	if adapter.launches != 0 {
		t.Fatalf("launches=%d, want 0", adapter.launches)
	}
}

func TestManagerRejectsDetachedCurrentStartBeforeLaunch(t *testing.T) {
	registry := &changingRegistry{repo: gitx.Repository{Name: "repo", Path: `E:\repo`, Branch: "main"}}
	adapter := &workflowAdapter{result: LaunchResult{Receipt: &Receipt{JobID: 42}}}
	manager, err := NewManager(adapter, registry)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := manager.Preview(context.Background(), PreviewRequest{RepositoryIdentity: "repo-a", Window: Window30d, BranchScope: BranchCurrent})
	if err != nil {
		t.Fatal(err)
	}
	registry.repo.Branch = "detached"
	registry.repo.Detached = true
	if _, err := manager.Start(context.Background(), preview.Token); err == nil {
		t.Fatal("detached current start was accepted")
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.preflights != 1 {
		t.Fatalf("preflights=%d, want preview-only count 1", adapter.preflights)
	}
	if adapter.launches != 0 {
		t.Fatalf("launches=%d, want 0", adapter.launches)
	}
}

func TestManagerAllowsAllBranchesWhenDetachedWithoutBranch(t *testing.T) {
	adapter := &workflowAdapter{result: LaunchResult{Receipt: &Receipt{JobID: 42}}}
	manager, err := NewManager(adapter, workflowRegistry{repo: gitx.Repository{Name: "repo", Path: `E:\repo`, Branch: "detached", Detached: true}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := manager.Preview(context.Background(), PreviewRequest{RepositoryIdentity: "repo-a", Window: Window30d, BranchScope: BranchAll})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Branch != "" {
		t.Fatalf("preview branch=%q, want empty", preview.Branch)
	}
	if _, err := manager.Start(context.Background(), preview.Token); err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.preflights != 2 {
		t.Fatalf("preflights=%d, want preview and start count 2", adapter.preflights)
	}
	if adapter.launches != 1 {
		t.Fatalf("launches=%d, want 1", adapter.launches)
	}
	if len(adapter.requests) != 2 {
		t.Fatalf("requests=%d, want preview and start preflight", len(adapter.requests))
	}
	for i, request := range adapter.requests {
		if request.Branch != "" {
			t.Errorf("request[%d] branch=%q, want empty", i, request.Branch)
		}
		for _, arg := range adapter.commands[i].Args {
			if arg == "--branch" {
				t.Errorf("request[%d] command unexpectedly contains --branch: %#v", i, adapter.commands[i].Args)
			}
		}
	}
}

func TestManagerAllowsLiteralDetachedBranchForCurrentScope(t *testing.T) {
	adapter := &workflowAdapter{result: LaunchResult{Receipt: &Receipt{JobID: 42}}}
	manager, err := NewManager(adapter, workflowRegistry{repo: gitx.Repository{Name: "repo", Path: `E:\repo`, Branch: "detached"}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := manager.Preview(context.Background(), PreviewRequest{RepositoryIdentity: "repo-a", Window: Window30d, BranchScope: BranchCurrent})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Branch != "detached" {
		t.Fatalf("preview branch=%q, want literal detached", preview.Branch)
	}
	branchArg := false
	for i := 0; i+1 < len(preview.Argv); i++ {
		if preview.Argv[i] == "--branch" && preview.Argv[i+1] == "detached" {
			branchArg = true
			break
		}
	}
	if !branchArg {
		t.Fatalf("preview argv=%#v, want --branch detached", preview.Argv)
	}
	result, err := manager.Start(context.Background(), preview.Token)
	if err != nil || result.Status != "queued" || result.JobID != 42 {
		t.Fatalf("start=%#v err=%v", result, err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.launches != 1 {
		t.Fatalf("launches=%d, want 1", adapter.launches)
	}
	for i, request := range adapter.requests {
		if request.Branch != "detached" {
			t.Errorf("request[%d] branch=%q, want detached", i, request.Branch)
		}
	}
}

func TestManagerRejectsMainToLiteralDetachedBranchAsDrift(t *testing.T) {
	registry := &changingRegistry{repo: gitx.Repository{Name: "repo", Path: `E:\repo`, Branch: "main"}}
	adapter := &workflowAdapter{result: LaunchResult{Receipt: &Receipt{JobID: 42}}}
	manager, err := NewManager(adapter, registry)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := manager.Preview(context.Background(), PreviewRequest{RepositoryIdentity: "repo-a", Window: Window30d, BranchScope: BranchCurrent})
	if err != nil {
		t.Fatal(err)
	}
	registry.repo.Branch = "detached"
	registry.repo.Detached = false
	if _, err := manager.Start(context.Background(), preview.Token); err == nil {
		t.Fatal("literal detached branch drift was accepted")
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.launches != 0 {
		t.Fatalf("launches=%d, want 0", adapter.launches)
	}
}

func TestManagerRejectsExpiredAndBindingDriftTokens(t *testing.T) {
	adapter := &workflowAdapter{result: LaunchResult{Receipt: &Receipt{JobID: 42, Agent: "codex", Since: time.Now()}}}
	manager, err := NewManager(adapter, workflowRegistry{repo: gitx.Repository{Name: "repo", Path: `E:\repo`, Branch: "main"}})
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	manager.now = func() time.Time { return clock }
	expired, err := manager.Preview(context.Background(), PreviewRequest{RepositoryIdentity: "repo-a", Window: Window30d, BranchScope: BranchAll})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(confirmationTTL + time.Second)
	if _, err := manager.Start(context.Background(), expired.Token); err == nil {
		t.Fatal("expired token was accepted")
	}
	clock = time.Now()
	valid, err := manager.Preview(context.Background(), PreviewRequest{RepositoryIdentity: "repo-a", Window: Window30d, BranchScope: BranchAll})
	if err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	adapter.binding = "2:roborev"
	adapter.mu.Unlock()
	if _, err := manager.Start(context.Background(), valid.Token); err == nil {
		t.Fatal("binding drift was accepted")
	}
}

type changingRegistry struct{ repo gitx.Repository }

func (r *changingRegistry) ResolveIdentity(string) (string, error) { return r.repo.Path, nil }
func (r *changingRegistry) InspectIdentity(context.Context, string) (gitx.Repository, error) {
	return r.repo, nil
}
