package repositorytracking

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

type trackingSequenceReader struct{ next uint64 }

func (r *trackingSequenceReader) Read(p []byte) (int, error) {
	if len(p) >= 8 {
		binary.LittleEndian.PutUint64(p, r.next)
		for index := 8; index < len(p); index++ {
			p[index] = byte(r.next)
		}
	} else {
		for index := range p {
			p[index] = byte(r.next >> (8 * index))
		}
	}
	r.next++
	return len(p), nil
}

func TestStoreClaimIsSingleUseAndExpires(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	store := NewStoreWith(bytes.NewReader(bytes.Repeat([]byte{1}, 32*4)), func() time.Time { return now })
	record := Record{Alias: "alias", Identity: "legacy", SelectedRoot: `C:\repo`, EffectiveRoot: `C:\repo`, Binding: roborev.RepositoryTrackingBinding{Version: Version, Generation: 1, FileID: "file", DaemonID: "daemon"}}
	expires := store.ExpiresAt()
	record.ExpiresAt = expires
	token, err := store.Put(record)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := store.Claim(token); !ok || got.ExpiresAt != expires {
		t.Fatalf("claim=%#v ok=%v", got, ok)
	}
	if _, ok := store.Claim(token); ok {
		t.Fatal("token replay succeeded")
	}
	token, err = store.Put(record)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(TTL)
	if _, ok := store.Claim(token); ok {
		t.Fatal("expired token was accepted")
	}
}

func TestStoreEvictsOldestWhenCapacityIsFull(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	store := NewStoreWith(&trackingSequenceReader{}, func() time.Time { return now })
	base := Record{Alias: "alias", Identity: "legacy", SelectedRoot: `C:\repo`, EffectiveRoot: `C:\repo`, Binding: roborev.RepositoryTrackingBinding{Version: Version, Generation: 1, FileID: "file", DaemonID: "daemon"}}
	tokens := make([]string, 0, Max)
	for index := 0; index < Max; index++ {
		record := base
		record.ExpiresAt = now.Add(time.Duration(index+1) * time.Second)
		token, err := store.Put(record)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	if len(store.pending) != Max {
		t.Fatalf("pending=%d want=%d", len(store.pending), Max)
	}
	extra, err := store.Put(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.pending) != Max {
		t.Fatalf("pending after capacity insertion=%d want=%d", len(store.pending), Max)
	}
	if _, ok := store.Claim(tokens[0]); ok {
		t.Fatal("oldest token was not evicted")
	}
	if _, ok := store.Claim(extra); !ok {
		t.Fatal("new token was not retained")
	}
}
