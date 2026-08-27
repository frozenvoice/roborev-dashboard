package app

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/snapshotignoremanagement"
)

func TestSnapshotIgnoreWriteStoreExpiryCapacityAndSingleUse(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := snapshotignoremanagement.New(snapshotignoremanagement.OSFileSystem{}).Plan(context.Background(), root, ".roborev")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	clock := now
	store := newSnapshotIgnoreWriteStoreWith(&snapshotIgnoreSequenceReader{}, func() time.Time { return clock })
	record := snapshotIgnoreWriteRecord{Identity: "legacy", Root: root, Plan: plan, Expires: now.Add(time.Minute)}
	var tokens []string
	for i := 0; i < snapshotIgnoreWriteMax+1; i++ {
		record.Expires = now.Add(time.Duration(i+1) * time.Second)
		token, err := store.put(record)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	if got := len(store.pending); got != snapshotIgnoreWriteMax {
		t.Fatalf("pending=%d want=%d", got, snapshotIgnoreWriteMax)
	}
	if _, ok := store.claim(tokens[len(tokens)-1]); !ok {
		t.Fatal("newest token was evicted")
	}
	if _, ok := store.claim(tokens[len(tokens)-1]); ok {
		t.Fatal("token was reusable")
	}

	ttlToken, err := store.put(snapshotIgnoreWriteRecord{Identity: "legacy", Root: root, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.pending[ttlToken].Expires; !got.Equal(now.Add(snapshotIgnoreWriteTTL)) {
		t.Fatalf("token expiry=%s want=%s", got, now.Add(snapshotIgnoreWriteTTL))
	}
	expires, err := store.put(record)
	if err != nil {
		t.Fatal(err)
	}
	clock = now.Add(snapshotIgnoreWriteTTL)
	if _, ok := store.claim(expires); ok {
		t.Fatal("expired token was claimable")
	}
}

func TestSnapshotIgnoreWriteStoreClaimsConcurrentlyOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".roborev"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := snapshotignoremanagement.New(snapshotignoremanagement.OSFileSystem{}).Plan(context.Background(), root, ".roborev")
	if err != nil {
		t.Fatal(err)
	}
	store := newSnapshotIgnoreWriteStoreWith(&snapshotIgnoreSequenceReader{}, time.Now)
	token, err := store.put(snapshotIgnoreWriteRecord{Identity: "legacy", Root: root, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	claims := 0
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := store.claim(token); ok {
				mu.Lock()
				claims++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if claims != 1 {
		t.Fatalf("claims=%d want=1", claims)
	}
}

type snapshotIgnoreSequenceReader struct{ next uint64 }

func (r *snapshotIgnoreSequenceReader) Read(p []byte) (int, error) {
	for offset := 0; offset < len(p); offset += 8 {
		var value [8]byte
		binary.BigEndian.PutUint64(value[:], r.next)
		r.next++
		count := len(p) - offset
		if count > len(value) {
			count = len(value)
		}
		copy(p[offset:offset+count], value[:count])
	}
	return len(p), nil
}
