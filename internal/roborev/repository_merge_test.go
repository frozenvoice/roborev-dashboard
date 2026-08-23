package roborev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/repomerge"
	"github.com/froze/roborev-dashboard/internal/repomove"
)

type repositoryMergeTestRunner struct {
	mu           sync.Mutex
	mergeCalls   int
	versionCalls int
	started      bool
	exitErr      error
	last         proc.Command
	versions     []string
	versionIndex int
}

func (r *repositoryMergeTestRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = command
	if len(command.Args) == 2 && command.Args[0] == "version" && command.Args[1] == "--json" {
		r.versionCalls++
		version := "v0.64.0"
		if len(r.versions) > 0 {
			version = r.versions[r.versionIndex]
			if r.versionIndex+1 < len(r.versions) {
				r.versionIndex++
			}
		}
		return proc.Result{Started: true, Stdout: fmt.Sprintf(`{"name":"roborev","version":%q}`, version)}, nil
	}
	if len(command.Args) == 5 && command.Args[0] == "repo" && command.Args[1] == "merge" {
		r.mergeCalls++
		return proc.Result{Started: r.started, ExitCode: 1}, r.exitErr
	}
	return proc.Result{}, errors.New("unexpected command")
}

type repositoryMergeAbsentFS struct{}

func (repositoryMergeAbsentFS) Lstat(string) (os.FileInfo, error) { return nil, os.ErrNotExist }

type repositoryMergeSequenceFS struct {
	calls  int
	absent int
}

func (f *repositoryMergeSequenceFS) Lstat(string) (os.FileInfo, error) {
	f.calls++
	if f.calls <= f.absent {
		return nil, os.ErrNotExist
	}
	return repositoryMergeExistsInfo{}, nil
}

type repositoryMergeExistsInfo struct{}

func (repositoryMergeExistsInfo) Name() string       { return "source" }
func (repositoryMergeExistsInfo) Size() int64        { return 0 }
func (repositoryMergeExistsInfo) Mode() os.FileMode  { return 0 }
func (repositoryMergeExistsInfo) ModTime() time.Time { return time.Time{} }
func (repositoryMergeExistsInfo) IsDir() bool        { return false }
func (repositoryMergeExistsInfo) Sys() any           { return nil }

func mergeInventoryJSON(source, target string, sourceCount, targetCount int64) string {
	return fmt.Sprintf(`{"repos":[{"name":"stale","root_path":%q,"count":%d},{"name":"target","root_path":%q,"count":%d}],"total_count":%d}`, source, sourceCount, target, targetCount, sourceCount+targetCount)
}

func mergeTerminalJobsJSON(source string, count int) string {
	rows := make([]string, count)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"id":%d,"status":"done","repo_path":%q}`, i+1, source)
	}
	return fmt.Sprintf(`{"jobs":[%s],"has_more":false,"stats":{}}`, strings.Join(rows, ","))
}

func setMergeJobsContentType(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
}

func TestRepositoryMergePreflightAndConfirmUseExactCommandAndReadback(t *testing.T) {
	source, _ := filepath.Abs(filepath.Join("stale", "merge-source"))
	target, _ := filepath.Abs(filepath.Join("tracked", "merge-target"))
	var reposCalls, jobsCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			reposCalls++
			if reposCalls == 3 {
				_, _ = io.WriteString(w, fmt.Sprintf(`{"repos":[{"name":"target","root_path":%q,"count":10}],"total_count":10}`, target))
			} else {
				_, _ = io.WriteString(w, mergeInventoryJSON(source, target, 7, 3))
			}
		case "/api/jobs":
			setMergeJobsContentType(w)
			jobsCalls++
			if request.URL.Query().Get("repo") != source || request.URL.Query().Get("limit") != "10000" || request.URL.Query().Get("omit_prompt") != "true" {
				t.Fatalf("unexpected jobs query: %s", request.URL.RawQuery)
			}
			_, _ = io.WriteString(w, mergeTerminalJobsJSON(source, 7))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	runner := &repositoryMergeTestRunner{started: true, exitErr: errors.New("exit 1")}
	client, err := NewClient("roborev", runner, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.repositoryMoveFS = repositoryMergeAbsentFS{}
	request := repomerge.Request{SourcePath: source, TargetPath: target}
	preview, err := client.RepositoryMergePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Command.WorkingDirectory != "" || preview.Command.Executable != "roborev" {
		t.Fatalf("command=%#v", preview.Command)
	}
	wantArgs := []string{"repo", "merge", "--yes", source, target}
	if fmt.Sprint(preview.Command.Args) != fmt.Sprint(wantArgs) {
		t.Fatalf("args=%#v want %#v", preview.Command.Args, wantArgs)
	}
	result, err := client.RepositoryMerge(context.Background(), request, preview)
	if err != nil || result != (repomerge.ExecutionResult{Started: true, Verified: true}) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	runner.mu.Lock()
	last := runner.last
	mergeCalls := runner.mergeCalls
	runner.mu.Unlock()
	if mergeCalls != 1 || reposCalls != 3 || jobsCalls != 2 {
		t.Fatalf("merge=%d repos=%d jobs=%d; want 1/3/2", mergeCalls, reposCalls, jobsCalls)
	}
	if last.Timeout != repositoryMergeTimeout || last.OutputLimit != repositoryMergeOutputLimit || fmt.Sprint(last.Args) != fmt.Sprint(wantArgs) || last.Dir != "" {
		t.Fatalf("process=%#v", last)
	}
}

func TestRepositoryMergeStartedNonzeroWithInvalidReadbackIsUnverified(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	target, _ := filepath.Abs("tracked/target")
	reposCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			reposCalls++
			if reposCalls < 3 {
				_, _ = io.WriteString(w, mergeInventoryJSON(source, target, 2, 3))
			} else {
				_, _ = io.WriteString(w, `{"repos":[],"total_count":0}`)
			}
		case "/api/jobs":
			setMergeJobsContentType(w)
			_, _ = io.WriteString(w, mergeTerminalJobsJSON(source, 2))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	runner := &repositoryMergeTestRunner{started: true, exitErr: errors.New("exit 1")}
	client, err := NewClient("roborev", runner, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.repositoryMoveFS = repositoryMergeAbsentFS{}
	request := repomerge.Request{SourcePath: source, TargetPath: target}
	preview, err := client.RepositoryMergePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.RepositoryMerge(context.Background(), request, preview)
	if err != nil || !result.Started || result.Verified {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestRepositoryMergeProcessNotStartedDoesNotReadback(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	target, _ := filepath.Abs("tracked/target")
	reposCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			reposCalls++
			_, _ = io.WriteString(w, mergeInventoryJSON(source, target, 2, 3))
		case "/api/jobs":
			setMergeJobsContentType(w)
			_, _ = io.WriteString(w, mergeTerminalJobsJSON(source, 2))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	runner := &repositoryMergeTestRunner{started: false}
	client, err := NewClient("roborev", runner, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.repositoryMoveFS = repositoryMergeAbsentFS{}
	request := repomerge.Request{SourcePath: source, TargetPath: target}
	preview, err := client.RepositoryMergePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.RepositoryMerge(context.Background(), request, preview)
	if result.Started || err == nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if reposCalls != 2 {
		t.Fatalf("inventory calls=%d want 2 (no readback when process did not start)", reposCalls)
	}
}

func TestRepositoryMergeReadbackTransportErrorIsUnverified(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	target, _ := filepath.Abs("tracked/target")
	reposCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			reposCalls++
			if reposCalls == 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, mergeInventoryJSON(source, target, 2, 3))
		case "/api/jobs":
			setMergeJobsContentType(w)
			_, _ = io.WriteString(w, mergeTerminalJobsJSON(source, 2))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	runner := &repositoryMergeTestRunner{started: true, exitErr: errors.New("exit 1")}
	client, err := NewClient("roborev", runner, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.repositoryMoveFS = repositoryMergeAbsentFS{}
	request := repomerge.Request{SourcePath: source, TargetPath: target}
	preview, err := client.RepositoryMergePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.RepositoryMerge(context.Background(), request, preview)
	if err != nil || result != (repomerge.ExecutionResult{Started: true}) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if reposCalls != 3 {
		t.Fatalf("inventory calls=%d want 3", reposCalls)
	}
}

func TestRepositoryMergeConfirmFreshPreflightDriftDoesNotLaunch(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	target, _ := filepath.Abs("tracked/target")
	base := mergeInventoryJSON(source, target, 7, 3)
	cases := []struct {
		name      string
		inventory string
		jobs      string
		want      error
	}{
		{name: "source missing", inventory: fmt.Sprintf(`{"repos":[{"name":"target","root_path":%q,"count":3}],"total_count":3}`, target), jobs: mergeTerminalJobsJSON(source, 7), want: repomerge.ErrSourceStale},
		{name: "source name", inventory: fmt.Sprintf(`{"repos":[{"name":"changed","root_path":%q,"count":7},{"name":"target","root_path":%q,"count":3}],"total_count":10}`, source, target), jobs: mergeTerminalJobsJSON(source, 7), want: nil},
		{name: "source count", inventory: mergeInventoryJSON(source, target, 8, 3), jobs: mergeTerminalJobsJSON(source, 8), want: nil},
		{name: "target name", inventory: fmt.Sprintf(`{"repos":[{"name":"stale","root_path":%q,"count":7},{"name":"changed","root_path":%q,"count":3}],"total_count":10}`, source, target), jobs: mergeTerminalJobsJSON(source, 7), want: nil},
		{name: "target count", inventory: mergeInventoryJSON(source, target, 7, 4), jobs: mergeTerminalJobsJSON(source, 7), want: nil},
		{name: "target missing", inventory: fmt.Sprintf(`{"repos":[{"name":"stale","root_path":%q,"count":7}],"total_count":7}`, source), jobs: mergeTerminalJobsJSON(source, 7), want: repomerge.ErrTargetStale},
		{name: "target duplicate", inventory: fmt.Sprintf(`{"repos":[{"name":"stale","root_path":%q,"count":7},{"name":"target","root_path":%q,"count":3},{"name":"target","root_path":%q,"count":3}],"total_count":13}`, source, target, target), jobs: mergeTerminalJobsJSON(source, 7), want: repomerge.ErrTargetStale},
		{name: "fallback appears", inventory: fmt.Sprintf(`{"repos":[{"name":"stale","root_path":%q,"count":7},{"name":"target","root_path":%q,"count":3},{"name":%q,"root_path":%q,"count":0}],"total_count":10}`, source, target, source, filepath.Join(filepath.Dir(target), "other")), jobs: mergeTerminalJobsJSON(source, 7), want: repomerge.ErrSourceFallbackAmbiguous},
		{name: "active job appears", inventory: mergeInventoryJSON(source, target, 7, 3), jobs: fmt.Sprintf(`{"jobs":[{"id":1,"status":"running","repo_path":%q}],"has_more":false}`, source), want: repomerge.ErrActiveSourceJobs},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			reposCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/api/health":
					_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
				case "/api/repos":
					reposCalls++
					if reposCalls == 1 {
						_, _ = io.WriteString(w, base)
					} else {
						_, _ = io.WriteString(w, test.inventory)
					}
				case "/api/jobs":
					setMergeJobsContentType(w)
					if reposCalls == 1 {
						_, _ = io.WriteString(w, mergeTerminalJobsJSON(source, 7))
					} else {
						_, _ = io.WriteString(w, test.jobs)
					}
				default:
					http.NotFound(w, request)
				}
			}))
			defer server.Close()
			runner := &repositoryMergeTestRunner{started: true}
			client, err := NewClient("roborev", runner, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client.repositoryMoveFS = repositoryMergeAbsentFS{}
			request := repomerge.Request{SourcePath: source, TargetPath: target}
			preview, err := client.RepositoryMergePreflight(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			result, confirmErr := client.RepositoryMerge(context.Background(), request, preview)
			if result.Started {
				t.Fatalf("drift launched process: result=%#v err=%v", result, confirmErr)
			}
			if confirmErr == nil {
				t.Fatal("accepted fresh preflight drift")
			}
			if test.want != nil && !errors.Is(confirmErr, test.want) {
				t.Fatalf("err=%v want errors.Is(...,%v)", confirmErr, test.want)
			}
			runner.mu.Lock()
			calls := runner.mergeCalls
			runner.mu.Unlock()
			if calls != 0 {
				t.Fatalf("merge calls=%d", calls)
			}
		})
	}
}

func TestRepositoryMergeConfirmRejectsSourceReappearing(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	target, _ := filepath.Abs("tracked/target")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			_, _ = io.WriteString(w, mergeInventoryJSON(source, target, 2, 3))
		case "/api/jobs":
			setMergeJobsContentType(w)
			_, _ = io.WriteString(w, mergeTerminalJobsJSON(source, 2))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	runner := &repositoryMergeTestRunner{started: true}
	client, err := NewClient("roborev", runner, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	fs := &repositoryMergeSequenceFS{absent: 2}
	client.repositoryMoveFS = fs
	request := repomerge.Request{SourcePath: source, TargetPath: target}
	preview, err := client.RepositoryMergePreflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.RepositoryMerge(context.Background(), request, preview)
	if !errors.Is(err, repomove.ErrSourceExists) || result.Started {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	runner.mu.Lock()
	calls := runner.mergeCalls
	runner.mu.Unlock()
	if calls != 0 {
		t.Fatalf("merge calls=%d", calls)
	}
	if fs.calls != 3 {
		t.Fatalf("Lstat calls=%d want 3 (preview, fresh preflight, adjacent process-boundary check)", fs.calls)
	}
}

func TestRepositoryMergeConfirmRejectsBindingAndVersionDrift(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	target, _ := filepath.Abs("tracked/target")
	cases := []struct {
		name          string
		versions      []string
		daemonVersion []string
		bindingDrift  bool
		want          error
	}{
		{name: "CLI version drift", versions: []string{"v0.64.0", "v0.63.0"}, want: ErrUnsupportedRepositoryMerge},
		{name: "daemon version drift", daemonVersion: []string{"v0.64.0", "v0.63.0"}, want: ErrUnsupportedRepositoryMerge},
		{name: "binding drift", bindingDrift: true, want: ErrBindingChanged},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			reposCalls, healthCalls := 0, 0
			var client *Client
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/api/health":
					healthCalls++
					version := "v0.64.0"
					if len(test.daemonVersion) > 0 {
						index := healthCalls - 1
						if index >= len(test.daemonVersion) {
							index = len(test.daemonVersion) - 1
						}
						version = test.daemonVersion[index]
					}
					_, _ = io.WriteString(w, fmt.Sprintf(`{"healthy":true,"version":%q}`, version))
				case "/api/repos":
					reposCalls++
					if test.bindingDrift && reposCalls == 2 {
						client.bindingMu.Lock()
						client.binding.generation++
						client.bindingMu.Unlock()
					}
					_, _ = io.WriteString(w, mergeInventoryJSON(source, target, 2, 3))
				case "/api/jobs":
					setMergeJobsContentType(w)
					_, _ = io.WriteString(w, mergeTerminalJobsJSON(source, 2))
				default:
					http.NotFound(w, request)
				}
			}))
			defer server.Close()
			runner := &repositoryMergeTestRunner{started: true, versions: test.versions}
			var err error
			client, err = NewClient("roborev", runner, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client.repositoryMoveFS = repositoryMergeAbsentFS{}
			request := repomerge.Request{SourcePath: source, TargetPath: target}
			preview, err := client.RepositoryMergePreflight(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			result, confirmErr := client.RepositoryMerge(context.Background(), request, preview)
			if !errors.Is(confirmErr, test.want) || result.Started {
				t.Fatalf("result=%#v err=%v want errors.Is(...,%v)", result, confirmErr, test.want)
			}
			runner.mu.Lock()
			calls := runner.mergeCalls
			runner.mu.Unlock()
			if calls != 0 {
				t.Fatalf("merge calls=%d", calls)
			}
		})
	}
}

func TestRepositoryMergeRejectsSourceEmptyAndActiveJobs(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	target, _ := filepath.Abs("tracked/target")
	tests := []struct {
		name      string
		inventory string
		jobs      string
		want      error
	}{
		{name: "empty", inventory: mergeInventoryJSON(source, target, 0, 3), jobs: `{"jobs":[],"has_more":false}`, want: repomerge.ErrSourceEmpty},
		{name: "active", inventory: mergeInventoryJSON(source, target, 1, 3), jobs: fmt.Sprintf(`{"jobs":[{"id":1,"status":"running","repo_path":%q,"prompt":"%s"}],"has_more":false}`, source, strings.Repeat("secret", 1000)), want: ErrRepositoryMergeActiveJobs},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/api/health":
					_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
				case "/api/repos":
					_, _ = io.WriteString(w, test.inventory)
				case "/api/jobs":
					setMergeJobsContentType(w)
					_, _ = io.WriteString(w, test.jobs)
				default:
					http.NotFound(w, request)
				}
			}))
			defer server.Close()
			runner := &repositoryMergeTestRunner{started: true}
			client, err := NewClient("roborev", runner, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client.repositoryMoveFS = repositoryMergeAbsentFS{}
			_, err = client.RepositoryMergePreflight(context.Background(), repomerge.Request{SourcePath: source, TargetPath: target})
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v want errors.Is(...,%v)", err, test.want)
			}
			runner.mu.Lock()
			calls := runner.mergeCalls
			runner.mu.Unlock()
			if calls != 0 {
				t.Fatalf("merge process launched %d times", calls)
			}
		})
	}
}

func TestRepositoryMergeReadbackMatrix(t *testing.T) {
	base := repomerge.Preflight{
		Source: repomerge.RepositorySnapshot{Path: `E:\old`, Name: "old", JobCount: 7},
		Target: repomerge.RepositorySnapshot{Path: `E:\new`, Name: "new", JobCount: 3},
	}
	tests := []struct {
		name      string
		inventory string
		want      bool
	}{
		{name: "exact consolidation", inventory: `{"repos":[{"name":"new","root_path":"E:\\new","count":10}],"total_count":10}`, want: true},
		{name: "external target growth", inventory: `{"repos":[{"name":"new","root_path":"E:\\new","count":11}],"total_count":11}`, want: true},
		{name: "under count", inventory: `{"repos":[{"name":"new","root_path":"E:\\new","count":9}],"total_count":9}`},
		{name: "source remains", inventory: `{"repos":[{"name":"old","root_path":"E:\\old","count":7},{"name":"new","root_path":"E:\\new","count":10}],"total_count":17}`},
		{name: "target missing", inventory: `{"repos":[],"total_count":0}`},
		{name: "target duplicate", inventory: `{"repos":[{"name":"new","root_path":"E:\\new","count":10},{"name":"new","root_path":"E:\\new","count":10}],"total_count":20}`},
		{name: "target renamed", inventory: `{"repos":[{"name":"other","root_path":"E:\\new","count":10}],"total_count":10}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inventory, err := parseTestMergeInventory(test.inventory)
			if err != nil {
				t.Fatal(err)
			}
			if got := validateMergeReadback(inventory, base); got != test.want {
				t.Fatalf("verified=%v want %v inventory=%#v", got, test.want, inventory)
			}
		})
	}
}

func parseTestMergeInventory(raw string) (repoinventory.Inventory, error) {
	return repoinventory.Parse([]byte(raw))
}

func TestRepositoryMergeCapabilityIsExactV064(t *testing.T) {
	if !capabilities("v0.64.0").RepositoryMerge {
		t.Fatal("v0.64.0 merge capability is false")
	}
	for _, version := range []string{"v0.63.0", "v0.64.1", "v0.65.0"} {
		if capabilities(version).RepositoryMerge {
			t.Fatalf("merge capability escaped exact v0.64 gate for %s", version)
		}
	}
}

func TestRepositoryMergePanelSummaryReconcilesMemberCount(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	jobs := fmt.Sprintf(`{"jobs":[{"id":1,"status":"done","repo_path":%q,"job_type":"synthesis","panel_role":"synthesis","panel_run_uuid":"run-1","panel_summary":{"panel_run_uuid":"run-1","members_total":3,"members_terminal":3}}],"has_more":false}`, source)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/jobs" {
			http.NotFound(w, request)
			return
		}
		setMergeJobsContentType(w)
		_, _ = io.WriteString(w, jobs)
	}))
	defer server.Close()
	client, err := NewClient("roborev", &repositoryMergeTestRunner{}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.repositoryMergeJobsRead(context.Background(), source, 4); err != nil {
		t.Fatal(err)
	}
	if err := client.repositoryMergeJobsRead(context.Background(), source, 3); err == nil {
		t.Fatal("accepted panel count mismatch")
	}
}

func TestRepositoryMergeFallbackGuardsUseExactSourceAndTargetKeys(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	target, _ := filepath.Abs("tracked/target")
	sourceKey, err := repomerge.SourceFallbackKey(source)
	if err != nil {
		t.Fatal(err)
	}
	targetKey, err := repomerge.TargetFallbackKey(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		row  repoinventory.Repository
		want error
	}{
		{name: "source exact fallback", row: repoinventory.Repository{Name: sourceKey, RootPath: filepath.Join(filepath.Dir(target), "other")}, want: repomerge.ErrSourceFallbackAmbiguous},
		{name: "target exact fallback", row: repoinventory.Repository{Name: targetKey, RootPath: filepath.Join(filepath.Dir(target), "other")}, want: repomerge.ErrTargetFallbackAmbiguous},
	} {
		t.Run(test.name, func(t *testing.T) {
			inventory := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "stale", RootPath: source, Count: 1}, {Name: "target", RootPath: target, Count: 1}, test.row}, TotalCount: 2}
			if err := validateMergeFallbacks(inventory, source, target); !errors.Is(err, test.want) {
				t.Fatalf("err=%v want errors.Is(...,%v)", err, test.want)
			}
		})
	}
	caseVariant := strings.ToUpper(sourceKey)
	if caseVariant == sourceKey {
		caseVariant = strings.ToLower(sourceKey)
	}
	inventory := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "stale", RootPath: source, Count: 1}, {Name: "target", RootPath: target, Count: 1}, {Name: caseVariant, RootPath: filepath.Join(filepath.Dir(target), "case-variant")}}, TotalCount: 3}
	if err := validateMergeFallbacks(inventory, source, target); err != nil {
		t.Fatalf("case-variant fallback was treated as an exact collision: %v", err)
	}
}

func TestRepositoryMergeJobsStatusBeforePromptRejectsActiveWithoutReadingTail(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/jobs" {
			http.NotFound(w, request)
			return
		}
		setMergeJobsContentType(w)
		_, _ = io.WriteString(w, fmt.Sprintf(`{"jobs":[{"id":1,"status":"running","repo_path":%q,"prompt":"`, source))
		_, _ = io.WriteString(w, strings.Repeat("not-retained", 1<<20))
		_, _ = io.WriteString(w, `"}],"has_more":false}`)
	}))
	defer server.Close()
	client, err := NewClient("roborev", &repositoryMergeTestRunner{}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.repositoryMergeJobsRead(context.Background(), source, 1); !errors.Is(err, ErrRepositoryMergeActiveJobs) {
		t.Fatalf("err=%v want active job error", err)
	}
}

func TestRepositoryMergeJobsRejectsMalformedAndIncompleteMatrix(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	row := fmt.Sprintf(`{"id":1,"status":"done","repo_path":%q}`, source)
	cases := []struct {
		name string
		body string
		want error
	}{
		{name: "has more", body: fmt.Sprintf(`{"jobs":[%s],"has_more":true}`, row), want: ErrRepositoryMergeMalformedJobs},
		{name: "missing has more", body: fmt.Sprintf(`{"jobs":[%s]}`, row), want: ErrRepositoryMergeMalformedJobs},
		{name: "unknown status", body: fmt.Sprintf(`{"jobs":[{"id":1,"status":"paused","repo_path":%q}],"has_more":false}`, source), want: ErrRepositoryMergeMalformedJobs},
		{name: "unknown top field", body: fmt.Sprintf(`{"jobs":[%s],"has_more":false,"unknown":true}`, row), want: ErrRepositoryMergeMalformedJobs},
		{name: "duplicate id", body: fmt.Sprintf(`{"jobs":[%s,%s],"has_more":false}`, row, row), want: ErrRepositoryMergeMalformedJobs},
		{name: "orphan panel member", body: fmt.Sprintf(`{"jobs":[{"id":1,"status":"done","repo_path":%q,"panel_role":"member","panel_run_uuid":"run"}],"has_more":false}`, source), want: ErrRepositoryMergeMalformedJobs},
		{name: "incomplete panel summary", body: fmt.Sprintf(`{"jobs":[{"id":1,"status":"done","repo_path":%q,"job_type":"synthesis","panel_role":"synthesis","panel_run_uuid":"run","panel_summary":{"panel_run_uuid":"run","members_total":2,"members_terminal":1}}],"has_more":false}`, source), want: ErrRepositoryMergeMalformedJobs},
		{name: "represented count mismatch", body: fmt.Sprintf(`{"jobs":[%s],"has_more":false}`, row), want: repomerge.ErrSourceJobMismatch},
		{name: "prompt before status", body: fmt.Sprintf(`{"jobs":[{"id":1,"prompt":"x","repo_path":%q,"status":"done"}],"has_more":false}`, source), want: ErrRepositoryMergeMalformedJobs},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/api/jobs" {
					setMergeJobsContentType(w)
					_, _ = io.WriteString(w, test.body)
					return
				}
				http.NotFound(w, request)
			}))
			defer server.Close()
			client, err := NewClient("roborev", &repositoryMergeTestRunner{}, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			err = client.repositoryMergeJobsRead(context.Background(), source, 2)
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v want errors.Is(...,%v)", err, test.want)
			}
		})
	}
}

func TestRepositoryMergeJobsRequiresJSONContentType(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/jobs" {
			_, _ = io.WriteString(w, fmt.Sprintf(`{"jobs":[{"id":1,"status":"done","repo_path":%q}],"has_more":false}`, source))
			return
		}
		http.NotFound(w, request)
	}))
	defer server.Close()
	client, err := NewClient("roborev", &repositoryMergeTestRunner{}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.repositoryMergeJobsRead(context.Background(), source, 1); !errors.Is(err, ErrRepositoryMergeMalformedJobs) {
		t.Fatalf("err=%v want malformed content-type error", err)
	}
}

func TestRepositoryMergeJobsRejectsBodyOverflow(t *testing.T) {
	source, _ := filepath.Abs("stale/source")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/jobs" {
			setMergeJobsContentType(w)
			_, _ = io.WriteString(w, `{"jobs":[],"has_more":false,"stats":"`)
			_, _ = io.WriteString(w, strings.Repeat("x", repositoryMergeJobsBodyLimit))
			_, _ = io.WriteString(w, `"}`)
			return
		}
		http.NotFound(w, request)
	}))
	defer server.Close()
	client, err := NewClient("roborev", &repositoryMergeTestRunner{}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.repositoryMergeJobsRead(context.Background(), source, 0); !errors.Is(err, ErrRepositoryMergeMalformedJobs) {
		t.Fatalf("err=%v want bounded overflow error", err)
	}
}
