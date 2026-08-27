// Package repositorytracking contains server-only state for the explicit
// RoboRev repository registration flow. It has no HTTP or browser DTOs; paths,
// filesystem witnesses, Git evidence, and daemon bindings remain in memory.
package repositorytracking

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

const (
	Version = "v0.64.0"
	TTL     = 3 * time.Minute
	Max     = 64
)

type Record struct {
	Alias                 string
	Identity              string
	SelectedRoot          string
	EffectiveRoot         string
	SelectedRootSnapshot  pathsafety.Snapshot
	EffectiveRootSnapshot pathsafety.Snapshot
	Binding               roborev.RepositoryTrackingBinding
	Inventory             repoinventory.Inventory
	ExpiresAt             time.Time
}

type Store struct {
	mu      sync.Mutex
	pending map[string]Record
	now     func() time.Time
	random  io.Reader
}

func NewStore() *Store { return NewStoreWith(rand.Reader, time.Now) }

func NewStoreWith(random io.Reader, now func() time.Time) *Store {
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &Store{pending: make(map[string]Record), random: random, now: now}
}

// ExpiresAt uses the same injected clock as Put, keeping preview DTO expiry
// and the server-side token record exactly aligned in deterministic tests.
func (s *Store) ExpiresAt() time.Time {
	if s == nil || s.now == nil {
		return time.Now().Add(TTL)
	}
	return s.now().Add(TTL)
}

func (s *Store) Put(record Record) (string, error) {
	if s == nil || record.Alias == "" || record.Identity == "" || record.SelectedRoot == "" || record.EffectiveRoot == "" || !record.Binding.Valid() {
		return "", errors.New("invalid repository tracking record")
	}
	if record.ExpiresAt.IsZero() {
		record.ExpiresAt = s.ExpiresAt()
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
		for len(s.pending) >= Max {
			var oldest string
			var expiry time.Time
			for candidate, value := range s.pending {
				if oldest == "" || value.ExpiresAt.Before(expiry) {
					oldest, expiry = candidate, value.ExpiresAt
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
	return "", errors.New("could not create repository tracking token")
}

// Claim consumes a token before alias, gate, lease, or fresh-probe checks.
// This is intentionally single-use even when execute later rejects the
// request as stale or busy.
func (s *Store) Claim(token string) (Record, bool) {
	if s == nil {
		return Record{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(s.now())
	record, ok := s.pending[token]
	if !ok {
		return Record{}, false
	}
	delete(s.pending, token)
	return record, true
}

func (s *Store) expireLocked(now time.Time) {
	for token, record := range s.pending {
		if !now.Before(record.ExpiresAt) {
			delete(s.pending, token)
		}
	}
}
