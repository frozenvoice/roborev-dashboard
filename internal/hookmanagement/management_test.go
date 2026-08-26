package hookmanagement

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/hookstatus"
)

type testLayout struct{ layout gitx.HookLayout }

func (l testLayout) ResolveHookLayout(context.Context, string) (gitx.HookLayout, error) {
	return l.layout, nil
}

type memoryFS struct{ files map[string][]byte }

func (f memoryFS) Lstat(path string) (os.FileInfo, error) {
	content, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return memoryInfo{name: filepath.Base(path), size: int64(len(content))}, nil
}
func (f memoryFS) Open(path string) (hookstatus.File, error) {
	content, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &memoryFile{Reader: strings.NewReader(string(content)), info: memoryInfo{name: filepath.Base(path), size: int64(len(content))}}, nil
}

type memoryInfo struct {
	name string
	size int64
}

func (i memoryInfo) Name() string       { return i.name }
func (i memoryInfo) Size() int64        { return i.size }
func (i memoryInfo) Mode() os.FileMode  { return 0o755 }
func (i memoryInfo) ModTime() time.Time { return time.Unix(1, 0) }
func (i memoryInfo) IsDir() bool        { return false }
func (i memoryInfo) Sys() any           { return nil }

type memoryFile struct {
	*strings.Reader
	info memoryInfo
}

func (f *memoryFile) Close() error               { return nil }
func (f *memoryFile) Stat() (os.FileInfo, error) { return f.info, nil }

func managementService(files map[string][]byte) *Service {
	return New(testLayout{gitx.HookLayout{Root: "repo", GitDir: "repo/.git", CommonDir: "repo/.git", HooksPath: "repo/.git/hooks", Safe: true}}, memoryFS{files: files})
}

func TestInstallAllowsAbsentAndPOSIXShellHooks(t *testing.T) {
	files := map[string][]byte{
		filepath.Join("repo/.git/hooks", PostCommit): []byte("#!/bin/sh\necho USER_BEFORE\necho USER_AFTER\n"),
	}
	preflight, err := managementService(files).Inspect(context.Background(), "opaque", "repo", `C:\bin\roborev.exe`)
	if err != nil {
		t.Fatal(err)
	}
	if !preflight.Install.Available || preflight.PostCommit.Classification != Unmanaged || preflight.PostRewrite.Classification != Absent {
		t.Fatalf("unexpected preflight: %#v", preflight)
	}
}

func TestVerifyInstallPreservesEmbeddedPOSIXShellUserContent(t *testing.T) {
	const binary = "roborev"
	const userHook = "#!/bin/sh\necho USER_BEFORE\necho USER_AFTER\n"
	const postCommitBlock = "# roborev post-commit hook v4\nROBOREV=\"roborev\"\n\"$ROBOREV\" post-commit\n"
	const postRewriteBlock = "# roborev post-rewrite hook v2\nROBOREV=\"roborev\"\n\"$ROBOREV\" remap --quiet\n"
	beforeFiles := map[string][]byte{
		filepath.Join("repo/.git/hooks", PostCommit):  []byte(userHook),
		filepath.Join("repo/.git/hooks", PostRewrite): []byte(userHook),
	}
	afterFiles := map[string][]byte{
		filepath.Join("repo/.git/hooks", PostCommit):  []byte("#!/bin/sh\necho USER_BEFORE\n" + postCommitBlock + "echo USER_AFTER\n"),
		filepath.Join("repo/.git/hooks", PostRewrite): []byte("#!/bin/sh\necho USER_BEFORE\n" + postRewriteBlock + "echo USER_AFTER\n"),
	}

	before, err := managementService(beforeFiles).Inspect(context.Background(), "opaque", "repo", binary)
	if err != nil {
		t.Fatal(err)
	}
	after, err := managementService(afterFiles).Inspect(context.Background(), "opaque", "repo", binary)
	if err != nil {
		t.Fatal(err)
	}
	if before.PostCommit.Classification != Unmanaged || before.PostRewrite.Classification != Unmanaged {
		t.Fatalf("expected unmanaged POSIX hooks before install: %#v", before)
	}
	if after.PostCommit.Classification != ManagedCurrent || after.PostRewrite.Classification != ManagedCurrent {
		t.Fatalf("expected current managed hooks after install: %#v", after)
	}
	if outcome := Verify(Install, before, after); outcome != VerifiedSuccess {
		t.Fatalf("install verification=%s want %s", outcome, VerifiedSuccess)
	}
	assertResidualPreserved(t, before.PostCommit, after.PostCommit, PostCommit)
	assertResidualPreserved(t, before.PostRewrite, after.PostRewrite, PostRewrite)
	assertSentinelsPreserved(t, after.PostCommit.Content)
	assertSentinelsPreserved(t, after.PostRewrite.Content)
}

func TestVerifyRemovePreservesEmbeddedPOSIXShellUserContent(t *testing.T) {
	const binary = "roborev"
	const userHook = "#!/bin/sh\necho USER_BEFORE\necho USER_AFTER\n"
	const postCommitBlock = "# roborev post-commit hook v4\nROBOREV=\"roborev\"\n\"$ROBOREV\" post-commit\n"
	const postRewriteBlock = "# roborev post-rewrite hook v2\nROBOREV=\"roborev\"\n\"$ROBOREV\" remap --quiet\n"
	beforeFiles := map[string][]byte{
		filepath.Join("repo/.git/hooks", PostCommit):  []byte("#!/bin/sh\necho USER_BEFORE\n" + postCommitBlock + "echo USER_AFTER\n"),
		filepath.Join("repo/.git/hooks", PostRewrite): []byte("#!/bin/sh\necho USER_BEFORE\n" + postRewriteBlock + "echo USER_AFTER\n"),
	}
	afterFiles := map[string][]byte{
		filepath.Join("repo/.git/hooks", PostCommit):  []byte(userHook),
		filepath.Join("repo/.git/hooks", PostRewrite): []byte(userHook),
	}

	before, err := managementService(beforeFiles).Inspect(context.Background(), "opaque", "repo", binary)
	if err != nil {
		t.Fatal(err)
	}
	after, err := managementService(afterFiles).Inspect(context.Background(), "opaque", "repo", binary)
	if err != nil {
		t.Fatal(err)
	}
	if before.PostCommit.Classification != ManagedCurrent || before.PostRewrite.Classification != ManagedCurrent {
		t.Fatalf("expected current managed hooks before remove: %#v", before)
	}
	if after.PostCommit.Classification != Unmanaged || after.PostRewrite.Classification != Unmanaged {
		t.Fatalf("expected unmanaged user hooks after remove: %#v", after)
	}
	if outcome := Verify(Remove, before, after); outcome != VerifiedSuccess {
		t.Fatalf("remove verification=%s want %s", outcome, VerifiedSuccess)
	}
	assertResidualPreserved(t, before.PostCommit, after.PostCommit, PostCommit)
	assertResidualPreserved(t, before.PostRewrite, after.PostRewrite, PostRewrite)
	assertSentinelsPreserved(t, after.PostCommit.Content)
	assertSentinelsPreserved(t, after.PostRewrite.Content)
}

func assertResidualPreserved(t *testing.T, before, after Hook, hookName string) {
	t.Helper()
	if before.ResidualFingerprint == "" || before.ResidualFingerprint != after.ResidualFingerprint {
		t.Fatalf("%s residual fingerprint changed: before=%q after=%q", hookName, before.ResidualFingerprint, after.ResidualFingerprint)
	}
	beforeResidual, beforeOK := residualContent(before, hookName)
	afterResidual, afterOK := residualContent(after, hookName)
	if !beforeOK || !afterOK || beforeResidual != afterResidual {
		t.Fatalf("%s residual content changed: before=%q after=%q", hookName, beforeResidual, afterResidual)
	}
}

func residualContent(hook Hook, hookName string) (string, bool) {
	if hook.Managed {
		return RemoveResidual(string(hook.Content), hookName)
	}
	if hook.Classification == Unmanaged {
		return normalizeResidual(string(hook.Content)), true
	}
	return "", false
}

func assertSentinelsPreserved(t *testing.T, content []byte) {
	t.Helper()
	text := string(content)
	before := strings.Index(text, "USER_BEFORE")
	after := strings.Index(text, "USER_AFTER")
	if before < 0 || after < 0 || before >= after {
		t.Fatalf("user sentinels were not preserved in order: %q", text)
	}
}

func TestInstallBlocksNonShellAndAmbiguousContent(t *testing.T) {
	files := map[string][]byte{
		filepath.Join("repo/.git/hooks", PostCommit):  []byte("#!/usr/bin/python\nprint('roborev')\n"),
		filepath.Join("repo/.git/hooks", PostRewrite): []byte("#!/bin/sh\necho roborev\n"),
	}
	preflight, err := managementService(files).Inspect(context.Background(), "opaque", "repo", "roborev")
	if err != nil {
		t.Fatal(err)
	}
	if preflight.Install.Available || !preflight.Safety.NonShellHookPresent || !preflight.Safety.AmbiguousRoborevContent {
		t.Fatalf("expected fail-closed install: %#v", preflight)
	}
}

func TestRepairDetectsStaleBinaryAndRemoveMarkerParserPreservesUser(t *testing.T) {
	content := "#!/bin/sh\necho USER_BEFORE\n# roborev post-commit hook v4 - auto-reviews every commit\nROBOREV=\"old\"\n\"$ROBOREV\" post-commit 2>/dev/null\necho USER_AFTER\n"
	files := map[string][]byte{filepath.Join("repo/.git/hooks", PostCommit): []byte(content)}
	preflight, err := managementService(files).Inspect(context.Background(), "opaque", "repo", "new")
	if err != nil {
		t.Fatal(err)
	}
	if !preflight.Repair.Available || !preflight.Remove.Available || preflight.PostCommit.Classification != ManagedStaleBinary {
		t.Fatalf("unexpected stale preflight: %#v", preflight)
	}
	residual, ok := RemoveResidual(content, PostCommit)
	if !ok || !strings.Contains(residual, "USER_BEFORE") || !strings.Contains(residual, "USER_AFTER") {
		t.Fatalf("residual=%q ok=%v", residual, ok)
	}
}

func TestMultipleManagedMarkersFailClosed(t *testing.T) {
	content := "#!/bin/sh\n# roborev post-commit hook v4\nROBOREV=\"new\"\n\"$ROBOREV\" post-commit\n# roborev post-commit hook v4\nROBOREV=\"old\"\n\"$ROBOREV\" post-commit\n"
	files := map[string][]byte{filepath.Join("repo/.git/hooks", PostCommit): []byte(content)}
	preflight, err := managementService(files).Inspect(context.Background(), "opaque", "repo", "new")
	if err == nil || preflight.PostCommit.Classification != Unreadable || preflight.Install.Available || preflight.Repair.Available || preflight.Remove.Available {
		t.Fatalf("multiple managed markers were not fail-closed: err=%v preflight=%#v", err, preflight)
	}
}

func TestBroadRoborevTokenWithoutOwnershipMarkerIsAmbiguous(t *testing.T) {
	path := filepath.Join("repo/.git/hooks", PostCommit)
	content := "#!/bin/sh\necho roborev\n"
	preflight, err := managementService(map[string][]byte{path: []byte(content)}).Inspect(context.Background(), "opaque", "repo", "old")
	if err != nil {
		t.Fatal(err)
	}
	if preflight.PostCommit.Classification != Ambiguous || preflight.Install.Available || preflight.Repair.Available || preflight.Remove.Available {
		t.Fatalf("broad marker was not exact-marker fail-closed: %#v", preflight)
	}
}

func TestOldManagedMarkerIsRepairableAndRemovable(t *testing.T) {
	path := filepath.Join("repo/.git/hooks", PostCommit)
	content := "#!/bin/sh\n# roborev post-commit hook v3\nROBOREV=\"old\"\n\"$ROBOREV\" post-commit\n"
	preflight, err := managementService(map[string][]byte{path: []byte(content)}).Inspect(context.Background(), "opaque", "repo", "new")
	if err != nil {
		t.Fatal(err)
	}
	if preflight.PostCommit.Classification != ManagedOutdated || !preflight.Repair.Available || !preflight.Remove.Available {
		t.Fatalf("old managed marker was not actionable: %#v", preflight)
	}
	if residual, ok := RemoveResidual(content, PostCommit); !ok || residual != "" {
		t.Fatalf("old managed marker residual=%q ok=%v", residual, ok)
	}
}

func TestWrongNameMarkerIsAmbiguous(t *testing.T) {
	path := filepath.Join("repo/.git/hooks", PostCommit)
	content := "#!/bin/sh\n# roborev post-rewrite hook v2\nROBOREV=\"old\"\n\"$ROBOREV\" remap --quiet\n"
	preflight, err := managementService(map[string][]byte{path: []byte(content)}).Inspect(context.Background(), "opaque", "repo", "new")
	if err != nil {
		t.Fatal(err)
	}
	if preflight.PostCommit.Classification != Ambiguous || preflight.Install.Available || preflight.Repair.Available || preflight.Remove.Available {
		t.Fatalf("wrong-name marker was not fail-closed: %#v", preflight)
	}
}

func TestMatchingAndOppositeOwnershipMarkersFailClosed(t *testing.T) {
	path := filepath.Join("repo/.git/hooks", PostCommit)
	content := "#!/bin/sh\n# roborev post-commit hook v3\nROBOREV=\"old\"\n\"$ROBOREV\" post-commit\n# roborev post-rewrite hook v2\nROBOREV=\"old\"\n\"$ROBOREV\" remap --quiet\n"
	preflight, err := managementService(map[string][]byte{path: []byte(content)}).Inspect(context.Background(), "opaque", "repo", "new")
	if err == nil || preflight.PostCommit.Classification != Unreadable || preflight.Install.Available || preflight.Repair.Available || preflight.Remove.Available {
		t.Fatalf("mixed ownership markers were not fail-closed: err=%v preflight=%#v", err, preflight)
	}
	if _, ok := RemoveResidual(content, PostCommit); ok {
		t.Fatal("RemoveResidual accepted mixed ownership markers")
	}
}

func TestEligibilityOrdersSnapshotSafetyBeforeRelativeConfig(t *testing.T) {
	managed := Hook{Managed: true, Classification: ManagedOutdated, Shell: true}
	cases := []struct {
		name                string
		safety              Safety
		postCommit          Hook
		wantInstallReason   string
		wantRepairAvailable bool
		wantRemoveAvailable bool
	}{
		{name: "unreadable wins", safety: Safety{HooksDirectorySafe: true, ConfigurationMutationRequired: true, UnreadableHookPresent: true}, postCommit: managed, wantInstallReason: "unreadable-hook"},
		{name: "unsafe wins", safety: Safety{HooksDirectorySafe: false, ConfigurationMutationRequired: true}, postCommit: managed, wantInstallReason: "unsafe-hooks-directory"},
		{name: "relative only blocks install", safety: Safety{HooksDirectorySafe: true, ConfigurationMutationRequired: true}, postCommit: managed, wantInstallReason: "relative-hooks-path-normalization", wantRepairAvailable: true, wantRemoveAvailable: true},
		{name: "non-shell install only", safety: Safety{HooksDirectorySafe: true, ConfigurationMutationRequired: true, NonShellHookPresent: true}, postCommit: managed, wantInstallReason: "non-shell-hook", wantRepairAvailable: true, wantRemoveAvailable: true},
		{name: "ambiguous blocks every action", safety: Safety{HooksDirectorySafe: true, ConfigurationMutationRequired: true, AmbiguousRoborevContent: true}, postCommit: managed, wantInstallReason: "ambiguous-roborev-content"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preflight := Preflight{Safety: tc.safety, PostCommit: tc.postCommit}
			install, repair, remove := availabilityFor(preflight)
			if install.Available || install.Reason != tc.wantInstallReason {
				t.Fatalf("install=%#v want blocked %q", install, tc.wantInstallReason)
			}
			if repair.Available != tc.wantRepairAvailable {
				t.Fatalf("repair=%#v", repair)
			}
			if remove.Available != tc.wantRemoveAvailable {
				t.Fatalf("remove=%#v", remove)
			}
		})
	}
}

func TestRepairIgnoresUnmanagedNonShellCompanion(t *testing.T) {
	preflight := Preflight{Safety: Safety{HooksDirectorySafe: true, NonShellHookPresent: true}, PostCommit: Hook{Managed: true, Shell: true, Classification: ManagedOutdated}, PostRewrite: Hook{Classification: Unmanaged, Present: true, Shell: false}}
	_, repair, remove := availabilityFor(preflight)
	if !repair.Available || !remove.Available {
		t.Fatalf("unmanaged non-shell companion blocked marker-only actions: repair=%#v remove=%#v", repair, remove)
	}
}

func TestManagedNonShellBlocksRepairButAllowsExactRemove(t *testing.T) {
	preflight := Preflight{Safety: Safety{HooksDirectorySafe: true, NonShellHookPresent: true}, PostCommit: Hook{Managed: true, Shell: false, Classification: ManagedOutdated}}
	install, repair, remove := availabilityFor(preflight)
	if install.Available || repair.Available || !remove.Available {
		t.Fatalf("managed non-shell action policy install=%#v repair=%#v remove=%#v", install, repair, remove)
	}
}

func TestVerifyRequiresExactResidualEqualityAndSafeReadback(t *testing.T) {
	before := Preflight{Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Classification: Absent, ResidualFingerprint: ""}, PostRewrite: Hook{Classification: Absent, ResidualFingerprint: ""}}
	after := before
	after.Safety.HooksDirectorySafe = true
	after.PostCommit = Hook{Classification: ManagedCurrent, ResidualFingerprint: "added-user-content", Fingerprint: "changed"}
	after.PostRewrite = Hook{Classification: ManagedCurrent, ResidualFingerprint: "", Fingerprint: "changed"}
	if outcome := Verify(Install, before, after); outcome == VerifiedSuccess {
		t.Fatal("added residual content passed an empty old residual digest")
	}
	unsafe := after
	unsafe.Safety.HooksDirectorySafe = false
	if outcome := Verify(Install, before, unsafe); outcome != FailedUnverified {
		t.Fatalf("unsafe readback outcome=%s", outcome)
	}
	unreadable := after
	unreadable.Safety.UnreadableHookPresent = true
	if outcome := Verify(Install, before, unreadable); outcome != FailedUnverified {
		t.Fatalf("unreadable readback outcome=%s", outcome)
	}
	drifted := after
	drifted.LayoutDigest = "different-layout"
	if outcome := Verify(Install, before, drifted); outcome != FailedUnverified {
		t.Fatalf("layout drift outcome=%s", outcome)
	}
	if outcome := VerifyExecution(Install, before, after, false); outcome != FailedUnverified {
		t.Fatalf("spawn failure outcome=%s", outcome)
	}
}

func TestVerifyExecutionPartialRequiresActionProgressAndResidualPreservation(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		before Preflight
		after  Preflight
		want   Outcome
	}{
		{
			name:   "install one current hook is partial",
			action: Install,
			before: Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Classification: Absent}, PostRewrite: Hook{Classification: Absent}},
			after:  Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Classification: ManagedCurrent}, PostRewrite: Hook{Classification: Absent}},
			want:   Partial,
		},
		{
			name:   "install unrelated change is unverified",
			action: Install,
			before: Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Classification: Absent, Fingerprint: "old"}, PostRewrite: Hook{Classification: Absent}},
			after:  Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Classification: Absent, Fingerprint: "new"}, PostRewrite: Hook{Classification: Absent}},
			want:   FailedUnverified,
		},
		{
			name:   "install changed user residual is unverified",
			action: Install,
			before: Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Classification: Unmanaged, ResidualFingerprint: "old"}, PostRewrite: Hook{Classification: Absent}},
			after:  Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Classification: ManagedCurrent, ResidualFingerprint: "new"}, PostRewrite: Hook{Classification: Absent}},
			want:   FailedUnverified,
		},
		{
			name:   "install ambiguous mutation is unverified",
			action: Install,
			before: Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Classification: Absent}, PostRewrite: Hook{Classification: Absent}},
			after:  Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true, AmbiguousRoborevContent: true}, PostCommit: Hook{Classification: ManagedCurrent}, PostRewrite: Hook{Classification: Absent}},
			want:   FailedUnverified,
		},
		{
			name:   "repair one stale target is partial",
			action: Repair,
			before: Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Managed: true, Classification: ManagedOutdated, ResidualFingerprint: "same"}, PostRewrite: Hook{Managed: true, Classification: ManagedOutdated, ResidualFingerprint: "other"}},
			after:  Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Managed: true, Classification: ManagedCurrent, ResidualFingerprint: "same"}, PostRewrite: Hook{Managed: true, Classification: ManagedOutdated, ResidualFingerprint: "other"}},
			want:   Partial,
		},
		{
			name:   "repair unrelated change is unverified",
			action: Repair,
			before: Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Managed: true, Classification: ManagedOutdated, ResidualFingerprint: "same", Fingerprint: "old"}, PostRewrite: Hook{Classification: Absent}},
			after:  Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Managed: true, Classification: ManagedOutdated, ResidualFingerprint: "same", Fingerprint: "new"}, PostRewrite: Hook{Classification: Absent}},
			want:   FailedUnverified,
		},
		{
			name:   "remove one managed target is partial",
			action: Remove,
			before: Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Managed: true, Classification: ManagedCurrent, ResidualFingerprint: "same"}, PostRewrite: Hook{Managed: true, Classification: ManagedCurrent, ResidualFingerprint: "other"}},
			after:  Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Classification: Unmanaged, ResidualFingerprint: "same"}, PostRewrite: Hook{Managed: true, Classification: ManagedCurrent, ResidualFingerprint: "other"}},
			want:   Partial,
		},
		{
			name:   "remove changed user residual is unverified",
			action: Remove,
			before: Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Managed: true, Classification: ManagedCurrent, ResidualFingerprint: "same"}, PostRewrite: Hook{Classification: Absent}},
			after:  Preflight{LayoutDigest: "layout", Safety: Safety{HooksDirectorySafe: true}, PostCommit: Hook{Classification: Unmanaged, ResidualFingerprint: "changed"}, PostRewrite: Hook{Classification: Absent}},
			want:   FailedUnverified,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := VerifyExecution(test.action, test.before, test.after, true); got != test.want {
				t.Fatalf("VerifyExecution(%s)=%s want %s", test.action, got, test.want)
			}
		})
	}
}

func TestRemovePreservesBroadRoborevResidual(t *testing.T) {
	markerContent := "#!/bin/sh\n# roborev post-commit hook v4\nROBOREV=\"old\"\n\"$ROBOREV\" post-commit\necho roborev-user"
	residualContent := "#!/bin/sh\necho roborev-user"
	path := filepath.Join("repo/.git/hooks", PostCommit)
	before, err := managementService(map[string][]byte{path: []byte(markerContent)}).Inspect(context.Background(), "opaque", "repo", "old")
	if err != nil {
		t.Fatal(err)
	}
	after, err := managementService(map[string][]byte{path: []byte(residualContent)}).Inspect(context.Background(), "opaque", "repo", "old")
	if err != nil {
		t.Fatal(err)
	}
	if after.PostCommit.Classification != Ambiguous || before.PostCommit.ResidualFingerprint == "" || after.PostCommit.ResidualFingerprint == "" {
		t.Fatalf("unexpected residual snapshots before=%#v after=%#v", before.PostCommit, after.PostCommit)
	}
	if outcome := Verify(Remove, before, after); outcome != VerifiedSuccess {
		t.Fatalf("broad residual was not preserved: %s before=%q after=%q classes=%s/%s fingerprints=%q/%q", outcome, before.PostCommit.ResidualFingerprint, after.PostCommit.ResidualFingerprint, before.PostCommit.Classification, after.PostCommit.Classification, before.PostCommit.Fingerprint, after.PostCommit.Fingerprint)
	}
}

func TestResidualFingerprintNormalizesShebangAndTerminalNewline(t *testing.T) {
	path := filepath.Join("repo/.git/hooks", PostCommit)
	service := managementService(map[string][]byte{path: []byte("#!/bin/sh\n  \t")})
	preflight, err := service.Inspect(context.Background(), "opaque", "repo", "old")
	if err != nil {
		t.Fatal(err)
	}
	if preflight.PostCommit.ResidualFingerprint != "" {
		t.Fatalf("shebang-only residual=%q", preflight.PostCommit.ResidualFingerprint)
	}
	service = managementService(map[string][]byte{path: []byte("#!/bin/sh\necho user")})
	preflight, err = service.Inspect(context.Background(), "opaque", "repo", "old")
	if err != nil {
		t.Fatal(err)
	}
	want := digestBytes([]byte("#!/bin/sh\necho user\n"))
	if preflight.PostCommit.ResidualFingerprint != want {
		t.Fatalf("terminal newline normalization=%q want=%q", preflight.PostCommit.ResidualFingerprint, want)
	}
}

var _ io.ReadCloser = (*memoryFile)(nil)
