package setupreadiness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
)

func TestResolveSnapshotDir(t *testing.T) {
	tests := []struct {
		name  string
		value string
		ok    bool
	}{
		{"default", "", true}, {"spaces", "cache/my snapshots", true},
		{"absolute", filepath.Join(string(filepath.Separator), "tmp"), false}, {"parent", "../tmp", false},
		{"trimmed default", "   ", true}, {"windows absolute", `C:\\private\\snapshot`, false}, {"UNC absolute", `\\\\server\\share\\snapshot`, false},
		{"git forward", ".git/something", false}, {"invalid utf8", string([]byte{0xff}), false},
		{"dot", ".", false}, {"git", ".git", false}, {"git case", ".GIT\u005cdata", false}, {"nested git is allowed", "cache/.git/data", true}, {"control", "snap\nshots", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ResolveSnapshotDir(test.value)
			if (err == nil) != test.ok {
				t.Fatalf("err=%v want success=%v", err, test.ok)
			}
		})
	}
}

func TestCheckDerivesNestedStatesAndOverall(t *testing.T) {
	root := t.TempDir()
	calls := []string{}
	checker := New(Dependencies{
		Configuration: func(context.Context, string) (string, error) {
			calls = append(calls, "config")
			return "cache/snap shots", nil
		},
		Snapshot: func(context.Context, string, string) (SnapshotObservation, error) {
			calls = append(calls, "snapshot")
			return SnapshotObservation{Ignored: true}, nil
		},
		Hooks: func(context.Context, string, string) (GitHooks, error) {
			calls = append(calls, "hooks")
			return GitHooksReady, nil
		},
		Daemon: func(context.Context) (DaemonObservation, error) {
			calls = append(calls, "daemon")
			return DaemonObservation{State: DaemonOnline, InventoryAvailable: true, InventoryRoots: []string{root}}, nil
		},
	})
	result, err := checker.Check(context.Background(), "repo-id", root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Overall != OverallReady || result.Configuration.Status != ConfigurationReady || result.Snapshot.Status != SnapshotReady || result.Tracking.Status != TrackingTracked {
		t.Fatalf("result=%#v", result)
	}
	if got, want := len(calls), 4; got != want {
		t.Fatalf("calls=%v want %d", calls, want)
	}
}

func TestCheckKeepsComponentsIndependentOnConfigurationFailure(t *testing.T) {
	root := t.TempDir()
	calledHooks, calledDaemon := false, false
	checker := New(Dependencies{
		Configuration: func(context.Context, string) (string, error) { return "", os.ErrInvalid },
		Hooks:         func(context.Context, string, string) (GitHooks, error) { calledHooks = true; return GitHooksReady, nil },
		Daemon: func(context.Context) (DaemonObservation, error) {
			calledDaemon = true
			return DaemonObservation{State: DaemonOffline}, nil
		},
	})
	result, err := checker.Check(context.Background(), "repo-id", root)
	if err != nil {
		t.Fatal(err)
	}
	if !calledHooks || !calledDaemon || result.Configuration.Status != ConfigurationUnavailable || result.Snapshot.Status != SnapshotUnavailable || result.Daemon.Status != DaemonOffline {
		t.Fatalf("result=%#v hooks=%v daemon=%v", result, calledHooks, calledDaemon)
	}
	if result.Overall != OverallUnavailable {
		t.Fatalf("overall=%q", result.Overall)
	}
}

func TestCheckTrackedSnapshotTakesPrecedence(t *testing.T) {
	root := t.TempDir()
	checker := New(Dependencies{
		Configuration: func(context.Context, string) (string, error) { return ".roborev", nil },
		Snapshot: func(context.Context, string, string) (SnapshotObservation, error) {
			return SnapshotObservation{Tracked: true, Ignored: true}, nil
		},
		Hooks: func(context.Context, string, string) (GitHooks, error) { return GitHooksReady, nil },
		Daemon: func(context.Context) (DaemonObservation, error) {
			return DaemonObservation{State: DaemonOnline, InventoryAvailable: true, InventoryRoots: []string{root}}, nil
		},
	})
	result, err := checker.Check(context.Background(), "repo-id", root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot.Status != SnapshotTrackedFiles || result.Overall != OverallNeedsAttention {
		t.Fatalf("result=%#v", result)
	}
}

func TestCheckMapsNotIgnoredSnapshotToNeedsAttention(t *testing.T) {
	root := t.TempDir()
	checker := New(Dependencies{
		Configuration: func(context.Context, string) (string, error) { return ".roborev", nil },
		Snapshot: func(context.Context, string, string) (SnapshotObservation, error) {
			return SnapshotObservation{}, nil
		},
		Hooks: func(context.Context, string, string) (GitHooks, error) { return GitHooksReady, nil },
		Daemon: func(context.Context) (DaemonObservation, error) {
			return DaemonObservation{State: DaemonOnline, InventoryAvailable: true, InventoryRoots: []string{root}}, nil
		},
	})
	result, err := checker.Check(context.Background(), "repo-id", root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot.Status != SnapshotNotIgnored || result.Overall != OverallNeedsAttention {
		t.Fatalf("result=%#v", result)
	}
}

func TestCheckMapsAllComponentStatesAndInventoryCardinality(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name               string
		hooks              GitHooks
		daemon             Daemon
		inventory          []string
		inventoryAvailable bool
		daemonErr          error
		wantSnapshot       Snapshot
		wantTracking       Tracking
		wantOverall        Overall
	}{
		{name: "ready", hooks: GitHooksReady, daemon: DaemonOnline, inventory: []string{root}, inventoryAvailable: true, wantSnapshot: SnapshotReady, wantTracking: TrackingTracked, wantOverall: OverallReady},
		{name: "hooks needs attention", hooks: GitHooksNeedsAttention, daemon: DaemonOnline, inventory: []string{root}, inventoryAvailable: true, wantSnapshot: SnapshotReady, wantTracking: TrackingTracked, wantOverall: OverallNeedsAttention},
		{name: "hooks not installed", hooks: GitHooksNotInstalled, daemon: DaemonOnline, inventory: []string{root}, inventoryAvailable: true, wantSnapshot: SnapshotReady, wantTracking: TrackingTracked, wantOverall: OverallNeedsAttention},
		{name: "hooks unknown", hooks: GitHooksUnknown, daemon: DaemonOnline, inventory: []string{root}, inventoryAvailable: true, wantSnapshot: SnapshotReady, wantTracking: TrackingTracked, wantOverall: OverallUnavailable},
		{name: "daemon offline", hooks: GitHooksReady, daemon: DaemonOffline, wantSnapshot: SnapshotReady, wantTracking: TrackingUnavailable, wantOverall: OverallNeedsAttention},
		{name: "daemon unavailable", hooks: GitHooksReady, daemon: DaemonUnavailable, daemonErr: errors.New("daemon unavailable"), wantSnapshot: SnapshotReady, wantTracking: TrackingUnavailable, wantOverall: OverallUnavailable},
		{name: "online inventory unavailable", hooks: GitHooksReady, daemon: DaemonOnline, wantSnapshot: SnapshotReady, wantTracking: TrackingUnavailable, wantOverall: OverallUnavailable},
		{name: "inventory miss", hooks: GitHooksReady, daemon: DaemonOnline, inventory: []string{filepath.Join(root, "other")}, inventoryAvailable: true, wantSnapshot: SnapshotReady, wantTracking: TrackingNotTracked, wantOverall: OverallNeedsAttention},
		{name: "inventory empty", hooks: GitHooksReady, daemon: DaemonOnline, inventory: []string{}, inventoryAvailable: true, wantSnapshot: SnapshotReady, wantTracking: TrackingNotTracked, wantOverall: OverallNeedsAttention},
		{name: "duplicate case equivalent", hooks: GitHooksReady, daemon: DaemonOnline, inventory: []string{root, strings.ToUpper(root)}, inventoryAvailable: true, wantSnapshot: SnapshotReady, wantTracking: TrackingUnavailable, wantOverall: OverallUnavailable},
		{name: "relative inventory root", hooks: GitHooksReady, daemon: DaemonOnline, inventory: []string{"relative/repo"}, inventoryAvailable: true, wantSnapshot: SnapshotReady, wantTracking: TrackingUnavailable, wantOverall: OverallUnavailable},
		{name: "control inventory root", hooks: GitHooksReady, daemon: DaemonOnline, inventory: []string{root + "\x00"}, inventoryAvailable: true, wantSnapshot: SnapshotReady, wantTracking: TrackingUnavailable, wantOverall: OverallUnavailable},
		{name: "invalid utf8 inventory root", hooks: GitHooksReady, daemon: DaemonOnline, inventory: []string{string([]byte{'C', ':', '\\', 0xff})}, inventoryAvailable: true, wantSnapshot: SnapshotReady, wantTracking: TrackingUnavailable, wantOverall: OverallUnavailable},
		{name: "duplicate unrelated roots", hooks: GitHooksReady, daemon: DaemonOnline, inventory: []string{filepath.Join(root, "other"), strings.ToUpper(filepath.Join(root, "other"))}, inventoryAvailable: true, wantSnapshot: SnapshotReady, wantTracking: TrackingUnavailable, wantOverall: OverallUnavailable},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			checker := New(Dependencies{
				Configuration: func(context.Context, string) (string, error) { return ".roborev", nil },
				Snapshot: func(context.Context, string, string) (SnapshotObservation, error) {
					return SnapshotObservation{Ignored: true}, nil
				},
				Hooks: func(context.Context, string, string) (GitHooks, error) { return test.hooks, nil },
				Daemon: func(context.Context) (DaemonObservation, error) {
					return DaemonObservation{State: test.daemon, InventoryAvailable: test.inventoryAvailable, InventoryRoots: test.inventory}, test.daemonErr
				},
			})
			result, err := checker.Check(context.Background(), "opaque", root)
			if err != nil {
				t.Fatal(err)
			}
			if result.Snapshot.Status != test.wantSnapshot || result.Tracking.Status != test.wantTracking || result.Overall != test.wantOverall {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestTrackingAcceptsWindowsCanonicalCaseEquivalenceAcrossSeparators(t *testing.T) {
	if got := trackingForRoot([]string{`C:/Workspace/Repo`}, `c:\workspace\repo`); got != TrackingTracked {
		t.Fatalf("drive tracking=%q", got)
	}
	if got := trackingForRoot([]string{`\\server\share\Repo`}, `\\SERVER\SHARE\repo`); got != TrackingTracked {
		t.Fatalf("UNC tracking=%q", got)
	}
}

func TestCheckKeepsSnapshotFailureIndependentFromHooksAndDaemon(t *testing.T) {
	root := t.TempDir()
	calledHooks, calledDaemon := false, false
	checker := New(Dependencies{
		Configuration: func(context.Context, string) (string, error) { return "cache", nil },
		Snapshot: func(context.Context, string, string) (SnapshotObservation, error) {
			return SnapshotObservation{}, errors.New("git probe failed")
		},
		Hooks: func(context.Context, string, string) (GitHooks, error) { calledHooks = true; return GitHooksReady, nil },
		Daemon: func(context.Context) (DaemonObservation, error) {
			calledDaemon = true
			return DaemonObservation{State: DaemonOnline, InventoryAvailable: true, InventoryRoots: []string{root}}, nil
		},
	})
	result, err := checker.Check(context.Background(), "opaque", root)
	if err != nil {
		t.Fatal(err)
	}
	if !calledHooks || !calledDaemon || result.Snapshot.Status != SnapshotUnavailable || result.Daemon.Status != DaemonOnline || result.Tracking.Status != TrackingTracked || result.Overall != OverallUnavailable {
		t.Fatalf("result=%#v hooks=%v daemon=%v", result, calledHooks, calledDaemon)
	}
}

func TestCheckFailsClosedWhenFilesystemIdentityChanges(t *testing.T) {
	root := t.TempDir()
	checker := New(Dependencies{
		Configuration: func(context.Context, string) (string, error) { return "cache", nil },
		Snapshot: func(context.Context, string, string) (SnapshotObservation, error) {
			return SnapshotObservation{Ignored: true}, nil
		},
		FS: &driftingFileSystem{root: root},
	})
	result, err := checker.Check(context.Background(), "opaque", root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot.Status != SnapshotUnsafe {
		t.Fatalf("snapshot=%q want unsafe", result.Snapshot.Status)
	}
}

func TestCheckRejectsSymlinkSnapshotDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "outside")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "cache")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	checker := New(Dependencies{
		Configuration: func(context.Context, string) (string, error) { return "cache", nil },
		Snapshot: func(context.Context, string, string) (SnapshotObservation, error) {
			t.Fatal("unsafe path must not invoke Git")
			return SnapshotObservation{}, nil
		},
	})
	result, err := checker.Check(context.Background(), "opaque", root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot.Status != SnapshotUnsafe {
		t.Fatalf("snapshot=%q want unsafe", result.Snapshot.Status)
	}
}

func TestCheckRejectsSnapshotAliasesAndReparsePointsWithInjectedFS(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "cache")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		alias   string
		reparse bool
	}{
		{name: "alias", alias: filepath.Join(t.TempDir(), "outside")},
		{name: "reparse", reparse: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			checker := New(Dependencies{
				Configuration: func(context.Context, string) (string, error) { return "cache", nil },
				Snapshot: func(context.Context, string, string) (SnapshotObservation, error) {
					called = true
					return SnapshotObservation{Ignored: true}, nil
				},
				FS: &markedFileSystem{marked: target, alias: test.alias, reparse: test.reparse},
			})
			result, err := checker.Check(context.Background(), "opaque", root)
			if err != nil {
				t.Fatal(err)
			}
			if called || result.Snapshot.Status != SnapshotUnsafe {
				t.Fatalf("called=%v snapshot=%q", called, result.Snapshot.Status)
			}
		})
	}
}

func TestResultValidateRejectsInvalidCrossFieldCombinations(t *testing.T) {
	base := Result{Version: Version, RepositoryIdentity: "opaque", Overall: OverallNeedsAttention,
		Configuration: StatusComponent[Configuration]{Status: ConfigurationReady}, Snapshot: StatusComponent[Snapshot]{Status: SnapshotNotIgnored},
		GitHooks: StatusComponent[GitHooks]{Status: GitHooksReady}, Daemon: StatusComponent[Daemon]{Status: DaemonOnline}, Tracking: StatusComponent[Tracking]{Status: TrackingNotTracked}}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []Result{
		{Version: Version, RepositoryIdentity: "opaque", Overall: OverallUnavailable, Configuration: StatusComponent[Configuration]{Status: ConfigurationUnavailable}, Snapshot: StatusComponent[Snapshot]{Status: SnapshotReady}, GitHooks: base.GitHooks, Daemon: StatusComponent[Daemon]{Status: DaemonOffline}, Tracking: StatusComponent[Tracking]{Status: TrackingUnavailable}},
		{Version: Version, RepositoryIdentity: "opaque", Overall: OverallNeedsAttention, Configuration: base.Configuration, Snapshot: base.Snapshot, GitHooks: base.GitHooks, Daemon: StatusComponent[Daemon]{Status: DaemonOnline}, Tracking: StatusComponent[Tracking]{Status: TrackingUnavailable}},
		{Version: Version, RepositoryIdentity: "opaque", Overall: OverallUnavailable, Configuration: base.Configuration, Snapshot: StatusComponent[Snapshot]{Status: SnapshotReady}, GitHooks: base.GitHooks, Daemon: StatusComponent[Daemon]{Status: DaemonOnline}, Tracking: StatusComponent[Tracking]{Status: TrackingTracked}},
	}
	for index, value := range cases {
		if err := value.Validate(); err == nil {
			t.Fatalf("case %d unexpectedly valid: %#v", index, value)
		}
	}
}

type driftingFileSystem struct {
	pathsafety.OSFileSystem
	root  string
	count int
}

type markedFileSystem struct {
	pathsafety.OSFileSystem
	marked  string
	alias   string
	reparse bool
}

func (f *markedFileSystem) EvalSymlinks(path string) (string, error) {
	if filepath.Clean(path) == filepath.Clean(f.marked) && f.alias != "" {
		return f.alias, nil
	}
	return f.OSFileSystem.EvalSymlinks(path)
}

func (f *markedFileSystem) IsReparse(path string, info os.FileInfo) bool {
	return f.reparse && filepath.Clean(path) == filepath.Clean(f.marked) || f.OSFileSystem.IsReparse(path, info)
}

func (f *driftingFileSystem) Identity(path string, info os.FileInfo) (string, error) {
	if filepath.Clean(path) == filepath.Clean(f.root) {
		f.count++
		return fmt.Sprintf("root-%d", f.count), nil
	}
	return f.OSFileSystem.Identity(path, info)
}
