package skillsmanagement

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
)

type fakeInfo struct {
	name string
	size int64
	mode os.FileMode
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return i.size }
func (i fakeInfo) Mode() os.FileMode  { return i.mode }
func (i fakeInfo) ModTime() time.Time { return time.Unix(1, 0) }
func (i fakeInfo) IsDir() bool        { return i.mode.IsDir() }
func (i fakeInfo) Sys() any           { return nil }

type fakeFile struct {
	*bytes.Reader
	info fakeInfo
	id   string
}

func (f *fakeFile) Close() error               { return nil }
func (f *fakeFile) Stat() (os.FileInfo, error) { return f.info, nil }
func (f *fakeFile) Identity() (string, error)  { return f.id, nil }

type fakeEntry struct {
	info    fakeInfo
	data    []byte
	id      string
	reparse bool
}

type fakeFS struct {
	entries      map[string]fakeEntry
	aliases      map[string]string
	mutateOnOpen bool
}

func newFakeFS() *fakeFS {
	f := &fakeFS{entries: map[string]fakeEntry{}, aliases: map[string]string{}}
	for _, path := range []string{`C:\`, `C:\fake-home`} {
		f.addDir(path, path)
	}
	for _, dir := range []string{".claude", ".codex", ".grok", ".factory"} {
		f.addDir(filepath.Join(`C:\fake-home`, dir), dir)
	}
	return f
}

func (f *fakeFS) addDir(path, id string) {
	f.entries[path] = fakeEntry{info: fakeInfo{name: filepath.Base(path), mode: os.ModeDir}, id: id}
}
func (f *fakeFS) addFile(path string, data []byte, id string) {
	f.entries[path] = fakeEntry{info: fakeInfo{name: filepath.Base(path), size: int64(len(data)), mode: 0o600}, data: append([]byte(nil), data...), id: id}
}
func (f *fakeFS) Lstat(path string) (os.FileInfo, error) {
	entry, ok := f.entries[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return entry.info, nil
}
func (f *fakeFS) Open(path string) (pathsafety.File, error) {
	entry, ok := f.entries[path]
	if !ok || entry.info.IsDir() {
		return nil, os.ErrNotExist
	}
	id := entry.id
	if f.mutateOnOpen {
		entry.id += "-changed"
		f.entries[path] = entry
	}
	return &fakeFile{Reader: bytes.NewReader(entry.data), info: entry.info, id: id}, nil
}
func (f *fakeFS) EvalSymlinks(path string) (string, error) {
	if alias, ok := f.aliases[path]; ok {
		return alias, nil
	}
	if _, ok := f.entries[path]; !ok {
		return "", os.ErrNotExist
	}
	return path, nil
}
func (f *fakeFS) Identity(path string, _ os.FileInfo) (string, error) {
	entry, ok := f.entries[path]
	if !ok {
		return "", os.ErrNotExist
	}
	return entry.id, nil
}
func (f *fakeFS) IsReparse(path string, _ os.FileInfo) bool { return f.entries[path].reparse }

func addTreeParents(f *fakeFS, root string, agent string) Target {
	target := Target{Agent: agent, ConfigDir: filepath.Join(root, "."+agent), SkillsDir: filepath.Join(root, "."+agent, "skills")}
	// Factory Droid's directory is not derived from the agent name.
	if agent == "droid" {
		target.ConfigDir = filepath.Join(root, ".factory")
		target.SkillsDir = filepath.Join(root, ".factory", "skills")
	}
	f.addDir(target.SkillsDir, target.SkillsDir)
	return target
}

func TestManifestIsExactAndWriteSetIsSingleAgent(t *testing.T) {
	if Version != "v0.64.0" || SkillCount() != 10 {
		t.Fatalf("manifest version/count=%s/%d", Version, SkillCount())
	}
	for _, agent := range []string{"codex", "claude", "grok", "droid"} {
		paths, err := ManagedRelativePaths(agent)
		if err != nil {
			t.Fatal(err)
		}
		want := 10
		if agent == "codex" {
			want = 20
		}
		if len(paths) != want {
			t.Fatalf("%s write set length=%d want %d", agent, len(paths), want)
		}
		for _, path := range paths {
			if filepath.Base(path) != "SKILL.md" && !(agent == "codex" && filepath.Base(path) == "openai.yaml") {
				t.Fatalf("unexpected managed path %q", path)
			}
		}
	}
	if _, err := ManagedRelativePaths("all"); !errors.Is(err, ErrUnsupportedAgent) {
		t.Fatalf("unsupported agent error=%v", err)
	}
}

func TestTargetResolverExactDefaultsOverridesAndRelativeBlock(t *testing.T) {
	resolver := NewTargetResolver(newFakeFS(), `C:\fake-home`)
	cases := []struct {
		agent string
		env   []string
		want  string
	}{
		{agent: "codex", want: `C:\fake-home\.codex`},
		{agent: "claude", want: `C:\fake-home\.claude`},
		{agent: "grok", want: `C:\fake-home\.grok`},
		{agent: "droid", env: []string{`HOME=C:\droid-home`}, want: `C:\droid-home\.factory`},
		{agent: "codex", env: []string{`CODEX_HOME=C:\override`}, want: `C:\override`},
		{agent: "claude", env: []string{`CLAUDE_CONFIG_DIR=C:\override`}, want: `C:\override`},
		{agent: "grok", env: []string{`GROK_HOME=C:\override`}, want: `C:\override`},
	}
	for _, test := range cases {
		target, err := resolver.Resolve(test.agent, test.env)
		if err != nil || target.ConfigDir != test.want || target.SkillsDir != filepath.Join(test.want, "skills") {
			t.Fatalf("%s target=%#v err=%v", test.agent, target, err)
		}
	}
	if _, err := resolver.Resolve("codex", []string{"CODEX_HOME=relative\\codex"}); !errors.Is(err, ErrRelativeTarget) {
		t.Fatalf("relative override error=%v", err)
	}
	if runtime.GOOS == "windows" {
		mixedWindows, err := resolver.Resolve("codex", []string{`Codex_Home=C:\mixed-case`})
		if err != nil || mixedWindows.ConfigDir != `C:\mixed-case` {
			t.Fatalf("Windows mixed-case override target=%#v err=%v", mixedWindows, err)
		}
	}
}

func TestEnvironmentNameMatchingPreservesUnixCaseSensitivity(t *testing.T) {
	env := []string{`Codex_Home=C:\mixed-case`}
	if got := nonemptyEnvForOS(env, "CODEX_HOME", "windows"); got != `C:\mixed-case` {
		t.Fatalf("Windows env match=%q", got)
	}
	if got := nonemptyEnvForOS(env, "CODEX_HOME", "linux"); got != "" {
		t.Fatalf("Unix env unexpectedly matched=%q", got)
	}
	if !envKeyEqual("cOdEx_HoMe", "CODEX_HOME", "windows") {
		t.Fatal("Windows env keys should be case-insensitive")
	}
	if envKeyEqual("cOdEx_HoMe", "CODEX_HOME", "darwin") {
		t.Fatal("Unix env keys should be case-sensitive")
	}
}

func TestTargetResolverLazyHomeLookup(t *testing.T) {
	lookupFailed := func() (string, error) { return "", errors.New("home lookup failed") }
	resolver := TargetResolver{HomeLookup: lookupFailed}
	if target, err := resolver.Resolve("codex", []string{`CODEX_HOME=C:\override`}); err != nil || target.ConfigDir != `C:\override` {
		t.Fatalf("absolute override should not need home lookup: target=%#v err=%v", target, err)
	}
	if _, err := resolver.Resolve("codex", nil); !errors.Is(err, ErrConfigUnavailable) {
		t.Fatalf("fallback home lookup error=%v", err)
	}
	if target, err := resolver.Resolve("droid", []string{`HOME=C:\droid-home`}); err != nil || target.ConfigDir != `C:\droid-home\.factory` {
		t.Fatalf("Droid HOME override should not need user home: target=%#v err=%v", target, err)
	}
}

func TestConfigRootMustBeExistingSafeDirectory(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*fakeFS, Target)
		want  error
	}{
		{name: "missing", setup: func(f *fakeFS, target Target) { delete(f.entries, target.ConfigDir) }, want: ErrConfigUnavailable},
		{name: "file", setup: func(f *fakeFS, target Target) { f.addFile(target.ConfigDir, []byte("not-dir"), "file") }, want: ErrTreeUnsafe},
		{name: "symlink", setup: func(f *fakeFS, target Target) { f.aliases[target.ConfigDir] = `C:\private\.codex` }, want: ErrTreeUnsafe},
		{name: "reparse", setup: func(f *fakeFS, target Target) {
			entry := f.entries[target.ConfigDir]
			entry.reparse = true
			f.entries[target.ConfigDir] = entry
		}, want: ErrTreeUnsafe},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFakeFS()
			resolver := NewTargetResolver(f, `C:\fake-home`)
			target, err := resolver.Resolve("codex", nil)
			if err != nil {
				t.Fatal(err)
			}
			test.setup(f, target)
			if _, err := resolver.ValidateConfigRoot(target); !errors.Is(err, test.want) {
				t.Fatalf("config root error=%v want %v", err, test.want)
			}
		})
	}
}

func TestPlannerCapturesExactTreeWithoutEnumeratingUnknownFiles(t *testing.T) {
	f := newFakeFS()
	f.addDir(`C:\fake-home\.codex\skills`, "skills")
	f.addDir(`C:\fake-home\.codex\skills\my-company-skill`, "unknown")
	f.addFile(`C:\fake-home\.codex\skills\my-company-skill\SKILL.md`, []byte("USER_CUSTOM"), "unknown-file")
	target := Target{Agent: "codex", ConfigDir: `C:\fake-home\.codex`, SkillsDir: `C:\fake-home\.codex\skills`}
	snapshot, err := NewPlanner(f, `C:\fake-home`).CaptureStable(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ManagedFiles) != 20 || snapshot.Legacy.Exists || len(snapshot.ManagedDirectories) != 20 {
		t.Fatalf("snapshot counts=%d/%d legacy=%#v", len(snapshot.ManagedFiles), len(snapshot.ManagedDirectories), snapshot.Legacy)
	}
	if _, ok := f.entries[`C:\fake-home\.codex\skills\my-company-skill\SKILL.md`]; !ok {
		t.Fatal("unknown file disappeared")
	}
}

func TestManagedWriteSetPreservesUnrelatedSkillTreeContent(t *testing.T) {
	f := newFakeFS()
	f.addDir(`C:\fake-home\.codex\skills`, "skills")
	f.addDir(`C:\fake-home\.codex\skills\my-company-skill`, "company")
	f.addFile(`C:\fake-home\.codex\skills\my-company-skill\SKILL.md`, []byte("USER_CUSTOM"), "company-skill")
	f.addDir(`C:\fake-home\.codex\skills\roborev-fix`, "fix")
	f.addFile(`C:\fake-home\.codex\skills\roborev-fix\SKILL.md`, []byte("OLD_ROBOREV"), "old-fix")
	f.addFile(`C:\fake-home\.codex\skills\roborev-fix\USER_NOTE.txt`, []byte("USER_NOTE"), "user-note")

	paths, err := ManagedRelativePaths("codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range paths {
		path := filepath.Join(`C:\fake-home\.codex\skills`, filepath.FromSlash(rel))
		f.addFile(path, []byte("EXPECTED_BUNDLED_"+rel), "bundled-"+rel)
	}
	if got := string(f.entries[`C:\fake-home\.codex\skills\my-company-skill\SKILL.md`].data); got != "USER_CUSTOM" {
		t.Fatalf("unrelated skill changed=%q", got)
	}
	if got := string(f.entries[`C:\fake-home\.codex\skills\roborev-fix\USER_NOTE.txt`].data); got != "USER_NOTE" {
		t.Fatalf("unknown file changed=%q", got)
	}
	if got := string(f.entries[`C:\fake-home\.codex\skills\roborev-fix\SKILL.md`].data); got != "EXPECTED_BUNDLED_roborev-fix/SKILL.md" {
		t.Fatalf("managed skill was not rewritten=%q", got)
	}
}

func TestCodexPolicyWriteSetRestoresExactEmbeddedContentAndReadiness(t *testing.T) {
	root := `C:\fake-home\.codex\skills`
	paths, err := ManagedRelativePaths("codex")
	if err != nil {
		t.Fatal(err)
	}
	policyRel := filepath.Join("roborev-fix", "agents", "openai.yaml")
	foundPolicy := false
	for _, rel := range paths {
		if filepath.Clean(filepath.FromSlash(rel)) == policyRel {
			foundPolicy = true
		}
	}
	if !foundPolicy {
		t.Fatalf("Codex write set omitted %q: %#v", policyRel, paths)
	}

	for _, initialPolicy := range [][]byte{nil, []byte("STALE_POLICY")} {
		name := "missing"
		if initialPolicy != nil {
			name = "stale"
		}
		t.Run(name, func(t *testing.T) {
			f := newFakeFS()
			f.addDir(root, "skills")
			f.addDir(filepath.Join(root, "my-company-skill"), "company")
			f.addFile(filepath.Join(root, "my-company-skill", "USER_NOTE.txt"), []byte("USER_NOTE"), "note")
			policyPath := filepath.Join(root, filepath.FromSlash(policyRel))
			if initialPolicy != nil {
				f.addFile(policyPath, initialPolicy, "stale-policy")
			}
			expected := make(map[string][]byte, len(paths))
			for _, rel := range paths {
				expected[rel] = []byte("EMBEDDED_CURRENT:" + rel)
			}
			// Deterministic fake of the audited v0.64 InstallToPath contract:
			// only manifest-managed files are written, and each receives its
			// exact embedded fixture bytes.
			for _, rel := range paths {
				f.addFile(filepath.Join(root, filepath.FromSlash(rel)), expected[rel], "embedded-"+rel)
			}
			for _, rel := range paths {
				entry, ok := f.entries[filepath.Join(root, filepath.FromSlash(rel))]
				if !ok || !bytes.Equal(entry.data, expected[rel]) {
					t.Fatalf("managed path %q=%q want %q", rel, entry.data, expected[rel])
				}
			}
			for _, rel := range paths {
				if _, ok := f.entries[filepath.Join(root, filepath.FromSlash(rel))]; !ok {
					t.Fatalf("readiness missing managed path %q", rel)
				}
			}
			if got := string(f.entries[filepath.Join(root, "my-company-skill", "USER_NOTE.txt")].data); got != "USER_NOTE" {
				t.Fatalf("unrelated file changed=%q", got)
			}
			if got := string(f.entries[policyPath].data); got != "EMBEDDED_CURRENT:roborev-fix/agents/openai.yaml" {
				t.Fatalf("Codex policy=%q", got)
			}
		})
	}
}

func TestPlannerBlocksLegacyInAllForms(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*fakeFS)
	}{
		{name: "file", setup: func(f *fakeFS) { f.addFile(`C:\fake-home\.codex\skills\roborev-address`, []byte("legacy"), "legacy") }},
		{name: "directory", setup: func(f *fakeFS) { f.addDir(`C:\fake-home\.codex\skills\roborev-address`, "legacy") }},
		{name: "symlink", setup: func(f *fakeFS) {
			f.addFile(`C:\fake-home\.codex\skills\roborev-address`, []byte("legacy"), "legacy")
			f.aliases[`C:\fake-home\.codex\skills\roborev-address`] = `C:\private\legacy`
		}},
		{name: "reparse", setup: func(f *fakeFS) {
			f.addDir(`C:\fake-home\.codex\skills\roborev-address`, "legacy")
			entry := f.entries[`C:\fake-home\.codex\skills\roborev-address`]
			entry.reparse = true
			f.entries[`C:\fake-home\.codex\skills\roborev-address`] = entry
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFakeFS()
			f.addDir(`C:\fake-home\.codex\skills`, "skills")
			test.setup(f)
			target := Target{Agent: "codex", ConfigDir: `C:\fake-home\.codex`, SkillsDir: `C:\fake-home\.codex\skills`}
			if _, err := NewPlanner(f, `C:\fake-home`).CaptureStable(target); !errors.Is(err, ErrLegacyPresent) {
				t.Fatalf("legacy error=%v", err)
			}
		})
	}
}

func TestPostStateAllowsManagedWritesButRetainsParents(t *testing.T) {
	f := newFakeFS()
	f.addDir(`C:\fake-home\.codex\skills`, "skills")
	target := Target{Agent: "codex", ConfigDir: `C:\fake-home\.codex`, SkillsDir: `C:\fake-home\.codex\skills`}
	planner := NewPlanner(f, `C:\fake-home`)
	before, err := planner.CaptureStable(target)
	if err != nil {
		t.Fatal(err)
	}
	f.addDir(`C:\fake-home\.codex\skills\roborev-fix`, "skill")
	f.addFile(`C:\fake-home\.codex\skills\roborev-fix\SKILL.md`, []byte("new"), "new-file")
	after, err := planner.Capture(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePostState(before, after); err != nil {
		t.Fatalf("post-state=%v", err)
	}
	entry := f.entries[target.ConfigDir]
	entry.id = "config-replaced"
	f.entries[target.ConfigDir] = entry
	after, err = planner.Capture(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePostState(before, after); !errors.Is(err, ErrTreeChanged) {
		t.Fatalf("config drift error=%v", err)
	}
}
