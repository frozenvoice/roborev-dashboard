package app

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/snapshotignoremanagement"
)

const (
	snapshotIgnoreWriteTTL = 3 * time.Minute
	snapshotIgnoreWriteMax = 64
)

// snapshotIgnoreWriteRecord is server-only. Plan contains filesystem paths,
// bytes and identity witnesses and is never serialized into a response.
type snapshotIgnoreWriteRecord struct {
	Alias     string
	Identity  string
	Root      string
	ConfigRaw string
	Relative  string
	Git       gitx.SnapshotIgnoreRepositoryState
	Plan      snapshotignoremanagement.Plan
	Expires   time.Time
}

type snapshotIgnoreWriteStore struct {
	mu      sync.Mutex
	pending map[string]snapshotIgnoreWriteRecord
	now     func() time.Time
	random  io.Reader
}

func newSnapshotIgnoreWriteStore() *snapshotIgnoreWriteStore {
	return newSnapshotIgnoreWriteStoreWith(rand.Reader, time.Now)
}

func newSnapshotIgnoreWriteStoreWith(random io.Reader, now func() time.Time) *snapshotIgnoreWriteStore {
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &snapshotIgnoreWriteStore{pending: make(map[string]snapshotIgnoreWriteRecord), random: random, now: now}
}

func (s *snapshotIgnoreWriteStore) expiresAt() time.Time {
	if s == nil || s.now == nil {
		return time.Now().Add(snapshotIgnoreWriteTTL)
	}
	return s.now().Add(snapshotIgnoreWriteTTL)
}

func (s *snapshotIgnoreWriteStore) put(record snapshotIgnoreWriteRecord) (string, error) {
	if s == nil || record.Identity == "" || record.Root == "" || !record.Plan.Valid() {
		return "", errors.New("invalid snapshot ignore write record")
	}
	if record.Expires.IsZero() {
		record.Expires = s.expiresAt()
	}
	for attempt := 0; attempt < 4; attempt++ {
		raw := make([]byte, 32)
		if _, err := io.ReadFull(s.random, raw); err != nil {
			return "", err
		}
		token := base64.RawURLEncoding.EncodeToString(raw)
		s.mu.Lock()
		s.expireLocked(s.now())
		if _, exists := s.pending[token]; exists {
			s.mu.Unlock()
			continue
		}
		for len(s.pending) >= snapshotIgnoreWriteMax {
			var oldest string
			var oldestExpiry time.Time
			for candidate, pending := range s.pending {
				if oldest == "" || pending.Expires.Before(oldestExpiry) {
					oldest, oldestExpiry = candidate, pending.Expires
				}
			}
			if oldest == "" {
				break
			}
			delete(s.pending, oldest)
		}
		s.pending[token] = record
		s.mu.Unlock()
		return token, nil
	}
	return "", errors.New("could not create confirmation token")
}

// claim atomically consumes the token before authority, gates, or fresh
// probes. Busy/stale/expired executions therefore cannot be replayed.
func (s *snapshotIgnoreWriteStore) claim(token string) (snapshotIgnoreWriteRecord, bool) {
	if s == nil {
		return snapshotIgnoreWriteRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(s.now())
	record, ok := s.pending[token]
	if !ok {
		return snapshotIgnoreWriteRecord{}, false
	}
	delete(s.pending, token)
	return record, true
}

func (s *snapshotIgnoreWriteStore) expireLocked(now time.Time) {
	for token, record := range s.pending {
		if !now.Before(record.Expires) {
			delete(s.pending, token)
		}
	}
}
