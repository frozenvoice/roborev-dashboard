package app

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/ciworkflowmanagement"
	"github.com/froze/roborev-dashboard/internal/gitx"
)

const (
	ciWorkflowWriteTTL = 3 * time.Minute
	ciWorkflowWriteMax = 64
)

// ciWorkflowWriteRecord is intentionally server-only. In particular, Plan
// contains the fixed absolute target and filesystem witnesses and must never
// be serialized into an HTTP response.
type ciWorkflowWriteRecord struct {
	Identity string
	Root     string
	Git      gitx.CIWorkflowRepositoryState
	Plan     ciworkflowmanagement.Plan
	Content  []byte
	Digest   string
	Version  string
	Expires  time.Time
}

type ciWorkflowWriteStore struct {
	mu      sync.Mutex
	pending map[string]ciWorkflowWriteRecord
	now     func() time.Time
	random  io.Reader
}

func (s *ciWorkflowWriteStore) expiresAt() time.Time {
	if s == nil || s.now == nil {
		return time.Now().Add(ciWorkflowWriteTTL)
	}
	return s.now().Add(ciWorkflowWriteTTL)
}

func newCIWorkflowWriteStore() *ciWorkflowWriteStore {
	return newCIWorkflowWriteStoreWith(rand.Reader, time.Now)
}

// The injected clock and entropy source make expiry, capacity, and
// single-use behavior deterministic in app tests without weakening the
// production cryptographic token boundary.
func newCIWorkflowWriteStoreWith(random io.Reader, now func() time.Time) *ciWorkflowWriteStore {
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &ciWorkflowWriteStore{pending: make(map[string]ciWorkflowWriteRecord), random: random, now: now}
}

func (s *ciWorkflowWriteStore) put(record ciWorkflowWriteRecord) (string, error) {
	if s == nil || len(record.Content) == 0 || len(record.Content) > int(ciworkflowmanagement.MaxBytes) {
		return "", errors.New("invalid CI workflow write record")
	}
	if record.Expires.IsZero() {
		record.Expires = s.expiresAt()
	}
	record.Content = append([]byte(nil), record.Content...)
	for attempt := 0; attempt < 4; attempt++ {
		raw := make([]byte, 32)
		if _, err := io.ReadFull(s.random, raw); err != nil {
			return "", err
		}
		token := base64.RawURLEncoding.EncodeToString(raw)
		s.mu.Lock()
		now := s.now()
		s.expireLocked(now)
		if _, exists := s.pending[token]; exists {
			s.mu.Unlock()
			continue
		}
		for len(s.pending) >= ciWorkflowWriteMax {
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

// claim atomically removes a token before any repository authority or
// operation checks. Invalid, expired, busy, and stale executions therefore
// cannot replay the same write authority.
func (s *ciWorkflowWriteStore) claim(token string) (ciWorkflowWriteRecord, bool) {
	if s == nil {
		return ciWorkflowWriteRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(s.now())
	record, ok := s.pending[token]
	if !ok {
		return ciWorkflowWriteRecord{}, false
	}
	delete(s.pending, token)
	record.Content = append([]byte(nil), record.Content...)
	return record, true
}

func (s *ciWorkflowWriteStore) expireLocked(now time.Time) {
	for token, record := range s.pending {
		if !now.Before(record.Expires) {
			delete(s.pending, token)
		}
	}
}
