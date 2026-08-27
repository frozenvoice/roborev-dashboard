package agentconfiguration

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

func TestCatalogAndValidationAreExact(t *testing.T) {
	want := []string{"codex", "claude-code", "gemini", "copilot", "opencode", "cursor", "kiro", "kilo", "droid", "pi", "grok"}
	got := Catalog()
	if len(got) != len(want) {
		t.Fatalf("catalog length=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] || !ValidateAgent(got[i]) {
			t.Errorf("catalog[%d]=%q, want %q", i, got[i], want[i])
		}
	}
	for _, value := range []string{"Codex", " codex", "codex ", "claude", "agent", "grok-build", "acp", "test", "acp.foo", ""} {
		if ValidateAgent(value) {
			t.Errorf("ValidateAgent(%q)=true", value)
		}
	}
	got[0] = "private"
	if Catalog()[0] != "codex" {
		t.Fatal("Catalog returned mutable backing storage")
	}
}

func TestClassifyDistinguishesUnsetAndExplicitEmpty(t *testing.T) {
	if status, agent := Classify(ScopedValue{}); status != StatusUnset || agent != "" {
		t.Fatalf("unset=(%q,%q)", status, agent)
	}
	if status, agent := Classify(ScopedValue{Set: true, Value: "codex"}); status != StatusCanonical || agent != "codex" {
		t.Fatalf("canonical=(%q,%q)", status, agent)
	}
	if status, agent := Classify(ScopedValue{Set: true}); status != StatusCustom || agent != "" {
		t.Fatalf("explicit empty=(%q,%q)", status, agent)
	}
}

func testRecord() Record {
	root, _ := filepath.Abs("agent-config-repo")
	return Record{
		Alias: "alias", LegacyIdentity: "identity", Root: root,
		RootSnapshot: pathsafety.Snapshot{Path: root, Exists: true, Kind: pathsafety.Directory, FileIdentity: "root"},
		Scope:        ScopeGlobal, Agent: "codex", Binding: roborev.RepositoryAgentConfigurationBinding{Binary: "roborev", Version: Version, Generation: 1, FileID: "file"},
		ConfigDigest: "digest", CurrentStatus: StatusUnset, ExpiresAt: time.Now().Add(TokenTTL),
	}
}

func TestStoreClaimIsSingleUse(t *testing.T) {
	now := time.Now()
	store := NewStore(WithClock(func() time.Time { return now }), WithRandom(func(data []byte) (int, error) {
		for i := range data {
			data[i] = byte(i + 1)
		}
		return len(data), nil
	}))
	first, err := store.Put(testRecord())
	if err != nil {
		t.Fatal(err)
	}
	if record, ok := store.Claim(first); !ok || record.Agent != "codex" {
		t.Fatalf("first claim=(%#v,%v)", record, ok)
	}
	if _, ok := store.Claim(first); ok {
		t.Fatal("replayed token was accepted")
	}
	if _, err := store.Put(Record{Agent: "not-allowed"}); err == nil {
		t.Fatal("invalid record was accepted")
	}
}

func TestStoreEvictsOldestInsertionAtCapacity(t *testing.T) {
	now := time.Now()
	sequence := byte(0)
	store := NewStore(WithClock(func() time.Time { return now }), WithRandom(func(data []byte) (int, error) {
		sequence++
		for i := range data {
			data[i] = sequence
		}
		return len(data), nil
	}))
	tokens := make([]string, 0, 65)
	for i := 0; i < 65; i++ {
		token, err := store.Put(testRecord())
		if err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
		tokens = append(tokens, token)
	}
	if _, ok := store.Claim(tokens[0]); ok {
		t.Fatal("oldest pending token was not evicted")
	}
	if _, ok := store.Claim(tokens[1]); !ok {
		t.Fatal("second-oldest pending token was unexpectedly evicted")
	}
	badRoot := testRecord()
	badRoot.RootSnapshot.Path = filepath.Join(filepath.Dir(badRoot.Root), "another-root")
	if _, err := store.Put(badRoot); err == nil {
		t.Fatal("record with an unrelated root snapshot was accepted")
	}
	boundary := testRecord()
	boundary.ExpiresAt = now
	if _, err := store.Put(boundary); err == nil {
		t.Fatal("record expiring at now was accepted")
	}
}

func TestStoreHandlesShortRandomReadsAndTokenCollision(t *testing.T) {
	now := time.Now()
	calls := 0
	store := NewStore(WithClock(func() time.Time { return now }), WithRandom(func(data []byte) (int, error) {
		if len(data) == 0 {
			return 0, nil
		}
		calls++
		// Return one byte at a time. The first two 32-byte tokens are
		// intentionally identical, forcing Put to regenerate after a
		// collision; the third token is distinct.
		batch := (calls - 1) / 32
		if batch < 2 {
			data[0] = 1
		} else {
			data[0] = 2
		}
		return 1, nil
	}))
	first, err := store.Put(testRecord())
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Put(testRecord())
	if err != nil {
		t.Fatal(err)
	}
	if first == second || calls != 96 {
		t.Fatalf("tokens=%q/%q random calls=%d, want distinct tokens and 96 short reads", first, second, calls)
	}
	if _, ok := store.Claim(first); !ok {
		t.Fatal("first token was lost after collision retry")
	}
	if _, ok := store.Claim(second); !ok {
		t.Fatal("second token was not stored after collision retry")
	}
}
