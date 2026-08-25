package hookstatus

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type fakeVersion struct {
	version string
	err     error
	calls   int
}

func (f *fakeVersion) ProbeVersion(context.Context) (string, error) {
	f.calls++
	return f.version, f.err
}

type fakeHooks struct {
	path  string
	err   error
	calls int
}

type productionClientRunner struct {
	commands []proc.Command
}

func (r *productionClientRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	if len(command.Args) == 2 && command.Args[0] == "version" && command.Args[1] == "--json" {
		return proc.Result{Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
	}
	return proc.Result{}, errors.New("unexpected RoboRev command")
}

type failingRoundTripper struct {
	calls int
}

func (r *failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls++
	return nil, errors.New("daemon intentionally offline")
}

func (f *fakeHooks) HooksPath(context.Context, string) (string, error) {
	f.calls++
	return f.path, f.err
}

type memoryInfo struct {
	name string
	size int64
	mode os.FileMode
	when time.Time
}

func (i memoryInfo) Name() string       { return i.name }
func (i memoryInfo) Size() int64        { return i.size }
func (i memoryInfo) Mode() os.FileMode  { return i.mode }
func (i memoryInfo) ModTime() time.Time { return i.when }
func (i memoryInfo) IsDir() bool        { return i.mode.IsDir() }
func (i memoryInfo) Sys() any           { return nil }

type memoryFile struct {
	*strings.Reader
	info memoryInfo
}

func (f *memoryFile) Close() error               { return nil }
func (f *memoryFile) Stat() (os.FileInfo, error) { return f.info, nil }

type memoryEntry struct {
	content []byte
	info    memoryInfo
	openErr error
}

type memoryFS struct {
	entries    map[string]memoryEntry
	lstatCalls int
	openCalls  int
}

func (f *memoryFS) Lstat(path string) (os.FileInfo, error) {
	f.lstatCalls++
	entry, ok := f.entries[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return entry.info, nil
}

func (f *memoryFS) Open(path string) (File, error) {
	f.openCalls++
	entry, ok := f.entries[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	if entry.openErr != nil {
		return nil, entry.openErr
	}
	return &memoryFile{Reader: strings.NewReader(string(entry.content)), info: entry.info}, nil
}

func newMemoryFS(hooksDir string, values map[string]string) *memoryFS {
	f := &memoryFS{entries: make(map[string]memoryEntry)}
	when := time.Unix(123, 0)
	for name, content := range values {
		path := filepath.Join(hooksDir, name)
		f.entries[path] = memoryEntry{content: []byte(content), info: memoryInfo{name: name, size: int64(len(content)), mode: 0o700, when: when}}
	}
	return f
}

func inspectFixture(t *testing.T, version string, values map[string]string) (Response, *fakeVersion, *fakeHooks, *memoryFS) {
	t.Helper()
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	versionProbe := &fakeVersion{version: version}
	hooks := &fakeHooks{path: hooksDir}
	fs := newMemoryFS(hooksDir, values)
	result, err := New(versionProbe, hooks, fs).Inspect(context.Background(), "repo-opaque", `C:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	return result, versionProbe, hooks, fs
}

func TestInspectGitHookTruthTable(t *testing.T) {
	const currentCommit = "#!/bin/sh\n# roborev post-commit hook v4\n"
	const currentRewrite = "#!/bin/sh\n# roborev post-rewrite hook v2\n"
	for _, test := range []struct {
		name                    string
		values                  map[string]string
		wantSummary             SummaryStatus
		wantCommit, wantRewrite Status
	}{
		{"current both", map[string]string{"post-commit": currentCommit, "post-rewrite": currentRewrite}, Ready, Current, Current},
		{"outdated commit", map[string]string{"post-commit": "# ROBorev old marker", "post-rewrite": currentRewrite}, NeedsAttention, Outdated, Current},
		{"missing rewrite", map[string]string{"post-commit": currentCommit}, NeedsAttention, Current, Missing},
		{"absent commit", map[string]string{}, NotInstalledSummary, NotInstalled, NotInstalled},
		{"unrelated commit", map[string]string{"post-commit": "#!/bin/sh\necho user hook\n"}, NotInstalledSummary, NotInstalled, NotInstalled},
		{"embedded current", map[string]string{"post-commit": "#!/bin/sh\necho user\n# roborev post-commit hook v4\n", "post-rewrite": "user\n# roborev post-rewrite hook v2\n"}, Ready, Current, Current},
		{"embedded old", map[string]string{"post-commit": "#!/bin/sh\n# roborev post-commit hook v3\n", "post-rewrite": currentRewrite}, NeedsAttention, Outdated, Current},
		{"rewrite unrelated", map[string]string{"post-commit": currentCommit, "post-rewrite": "#!/bin/sh\necho user\n"}, NeedsAttention, Current, Missing},
		{"rewrite current while commit absent", map[string]string{"post-rewrite": currentRewrite}, NotInstalledSummary, NotInstalled, Current},
		{"rewrite outdated while commit absent", map[string]string{"post-rewrite": "# roborev post-rewrite hook v1"}, NotInstalledSummary, NotInstalled, Outdated},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, version, hooks, _ := inspectFixture(t, "v0.64.0", test.values)
			if result.Status != test.wantSummary || result.PostCommit.Status != test.wantCommit || result.PostRewrite.Status != test.wantRewrite {
				t.Fatalf("result=%+v want summary=%s commit=%s rewrite=%s", result, test.wantSummary, test.wantCommit, test.wantRewrite)
			}
			if version.calls != 1 || hooks.calls != 1 {
				t.Fatalf("version calls=%d hooks calls=%d", version.calls, hooks.calls)
			}
		})
	}
}

func TestInspectVersionGatePreventsHooksInspection(t *testing.T) {
	for _, test := range []struct {
		name    string
		version string
		err     error
	}{
		{"unsupported", "v0.63.0", nil},
		{"missing", "", errors.New("version unavailable")},
		{"malformed", "0.64.0", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			hooksDir := filepath.Join(t.TempDir(), "hooks")
			version := &fakeVersion{version: test.version, err: test.err}
			hooks := &fakeHooks{path: hooksDir}
			fs := newMemoryFS(hooksDir, map[string]string{"post-commit": "roborev post-commit hook v4"})
			result, err := New(version, hooks, fs).Inspect(context.Background(), "opaque", `C:\repo`)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != Unavailable || result.PostCommit.Status != Unknown || result.PostRewrite.Status != Unknown {
				t.Fatalf("unsupported result=%+v", result)
			}
			if hooks.calls != 0 || fs.lstatCalls != 0 || fs.openCalls != 0 {
				t.Fatalf("inspection continued: hooks=%d lstat=%d open=%d", hooks.calls, fs.lstatCalls, fs.openCalls)
			}
		})
	}
}

func TestInspectReadErrorsAndOversizeAreUnknown(t *testing.T) {
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	version := &fakeVersion{version: "v0.64.0"}
	hooks := &fakeHooks{path: hooksDir}
	when := time.Unix(123, 0)
	fs := &memoryFS{entries: map[string]memoryEntry{
		filepath.Join(hooksDir, "post-commit"):  {info: memoryInfo{name: "post-commit", size: 3, mode: 0o700, when: when}, openErr: os.ErrPermission},
		filepath.Join(hooksDir, "post-rewrite"): {info: memoryInfo{name: "post-rewrite", size: MaxHookBytes + 1, mode: 0o700, when: when}, content: []byte("oversized")},
	}}
	result, err := New(version, hooks, fs).Inspect(context.Background(), "opaque", `C:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Unavailable || result.PostCommit.Status != Unknown || result.PostRewrite.Status != Unknown {
		t.Fatalf("result=%+v", result)
	}
}

func TestInspectMixedReadErrorPreservesPositiveRewriteFact(t *testing.T) {
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	when := time.Unix(123, 0)
	fs := &memoryFS{entries: map[string]memoryEntry{
		filepath.Join(hooksDir, "post-commit"):  {info: memoryInfo{name: "post-commit", size: 3, mode: 0o700, when: when}, openErr: os.ErrPermission},
		filepath.Join(hooksDir, "post-rewrite"): {content: []byte("# roborev post-rewrite hook v2"), info: memoryInfo{name: "post-rewrite", size: 32, mode: 0o700, when: when}},
	}}
	result, err := New(&fakeVersion{version: "v0.64.0"}, &fakeHooks{path: hooksDir}, fs).Inspect(context.Background(), "opaque", `C:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Unavailable || result.PostCommit.Status != Unknown || result.PostRewrite.Status != Current {
		t.Fatalf("result=%+v", result)
	}
}

func TestInspectUnknownCommitAndAbsentRewriteRemainUnavailable(t *testing.T) {
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	when := time.Unix(123, 0)
	fs := &memoryFS{entries: map[string]memoryEntry{
		filepath.Join(hooksDir, "post-commit"): {info: memoryInfo{name: "post-commit", size: 3, mode: 0o700, when: when}, openErr: os.ErrPermission},
	}}
	result, err := New(&fakeVersion{version: "v0.64.0"}, &fakeHooks{path: hooksDir}, fs).Inspect(context.Background(), "opaque", `C:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Unavailable || result.PostCommit.Status != Unknown || result.PostRewrite.Status != Unknown {
		t.Fatalf("result=%+v", result)
	}
}

func TestInspectRejectsUnsafeFilesystemObjects(t *testing.T) {
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	when := time.Unix(123, 0)
	fs := &memoryFS{entries: map[string]memoryEntry{
		filepath.Join(hooksDir, "post-commit"): {info: memoryInfo{name: "post-commit", size: 1, mode: os.ModeDir | 0o700, when: when}},
	}}
	result, err := New(&fakeVersion{version: "v0.64.0"}, &fakeHooks{path: hooksDir}, fs).Inspect(context.Background(), "opaque", `C:\repo`)
	if err != nil || result.PostCommit.Status != Unknown || result.Status != Unavailable {
		t.Fatalf("unsafe object result=%+v err=%v", result, err)
	}
}

func TestInspectPropagatesContextLifecycleErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := New(&fakeVersion{version: "v0.64.0"}, &fakeHooks{}, newMemoryFS("", nil)).Inspect(ctx, "opaque", `C:\repo`)
	if !errors.Is(err, context.Canceled) || result.Status != Unavailable {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestInspectWithProductionClientUsesOnlyVersionProbeAndNoDaemon(t *testing.T) {
	runner := &productionClientRunner{}
	client, err := roborev.NewClient("roborev", runner, "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	transport := &failingRoundTripper{}
	client.HTTP.Transport = transport
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	fs := newMemoryFS(hooksDir, map[string]string{
		"post-commit":  "# roborev post-commit hook v4",
		"post-rewrite": "# roborev post-rewrite hook v2",
	})
	result, err := New(client, &fakeHooks{path: hooksDir}, fs).Inspect(context.Background(), "opaque", `C:\repo`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Ready || result.PostCommit.Status != Current || result.PostRewrite.Status != Current {
		t.Fatalf("result=%+v", result)
	}
	if transport.calls != 0 {
		t.Fatalf("daemon HTTP calls=%d, want 0", transport.calls)
	}
	if len(runner.commands) != 1 || len(runner.commands[0].Args) != 2 || runner.commands[0].Args[0] != "version" || runner.commands[0].Args[1] != "--json" {
		t.Fatalf("RoboRev command ledger=%#v", runner.commands)
	}
}

func TestResponseDoesNotContainRawHookDetails(t *testing.T) {
	result, _, _, _ := inspectFixture(t, "v0.64.0", map[string]string{"post-commit": "# roborev post-commit hook v4\nsecret-command", "post-rewrite": "# roborev post-rewrite hook v2"})
	encoded := string(result.PostCommit.Status) + string(result.PostRewrite.Status) + string(result.Status)
	if strings.Contains(encoded, "secret") {
		t.Fatal("hook content leaked into typed status")
	}
	if _, ok := any(result).(io.Reader); ok {
		t.Fatal("response unexpectedly exposes a reader")
	}
}
