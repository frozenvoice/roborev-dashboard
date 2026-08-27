// Package agentconfiguration contains the small, server-side policy and
// confirmation-token store for the dedicated RoboRev agent setting surface.
// It intentionally does not share the generic Configuration allowlist: agent
// selection is a separate product surface with a separate wire contract.
package agentconfiguration

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

const (
	Version        = "v0.64.0"
	TokenTTL       = 3 * time.Minute
	MaxTokenLength = 64
)

// Agents is the canonical browser and CLI allowlist. Order is part of the
// contract and is kept stable for deterministic UI rendering.
var canonicalAgents = []string{
	"codex", "claude-code", "gemini", "copilot", "opencode", "cursor",
	"kiro", "kilo", "droid", "pi", "grok",
}

// Catalog returns a defensive copy of the canonical catalog.
func Catalog() []string { return append([]string(nil), canonicalAgents...) }

type Scope string

const (
	ScopeGlobal     Scope = "global"
	ScopeRepository Scope = "repository"
)

func (s Scope) Valid() bool { return s == ScopeGlobal || s == ScopeRepository }

func Key(scope Scope) string {
	if scope == ScopeGlobal {
		return "default_agent"
	}
	return "agent"
}

func RoboRevScope(scope Scope) roborev.ConfigScope {
	if scope == ScopeGlobal {
		return roborev.ConfigScopeGlobal
	}
	return roborev.ConfigScopeLocal
}

// ValidateAgent deliberately does not trim or normalize. A browser must send
// one exact catalog entry; aliases, case variants and custom values are not
// accepted as mutation targets.
func ValidateAgent(agent string) bool {
	for _, allowed := range canonicalAgents {
		if agent == allowed {
			return true
		}
	}
	return false
}

type Status string

const (
	StatusUnset     Status = "unset"
	StatusCanonical Status = "canonical"
	StatusCustom    Status = "custom"
)

// Classify never returns the custom value. Custom values are intentionally
// treated as a privacy-sensitive opaque state at the HTTP boundary.
// ScopedValue distinguishes an absent setting from an explicitly empty value.
// Value is retained only server-side; callers must not serialize it when the
// status is custom.
type ScopedValue struct {
	Set   bool
	Value string
}

func Classify(value ScopedValue) (Status, string) {
	if !value.Set {
		return StatusUnset, ""
	}
	for _, agent := range canonicalAgents {
		if value.Value == agent {
			return StatusCanonical, agent
		}
	}
	return StatusCustom, ""
}

// Record contains every binding needed to revalidate a prepared mutation.
// It is never serialized; only its random token leaves the process.
type Record struct {
	Alias          string
	LegacyIdentity string
	Root           string
	RootSnapshot   pathsafety.Snapshot
	Scope          Scope
	Agent          string
	Binding        roborev.RepositoryAgentConfigurationBinding
	ConfigDigest   string
	CurrentStatus  Status
	CurrentAgent   string
	CurrentSet     bool
	CurrentValue   string
	Head           string
	Branch         string
	Detached       bool
	Dirty          bool
	ExpiresAt      time.Time
}

type Store struct {
	mu      sync.Mutex
	pending map[string]storedRecord
	clock   func() time.Time
	random  func([]byte) (int, error)
	next    uint64
}

type storedRecord struct {
	record Record
	order  uint64
}

type StoreOption func(*Store)

func WithClock(clock func() time.Time) StoreOption {
	return func(s *Store) {
		if clock != nil {
			s.clock = clock
		}
	}
}

func WithRandom(random func([]byte) (int, error)) StoreOption {
	return func(s *Store) {
		if random != nil {
			s.random = random
		}
	}
}

func NewStore(options ...StoreOption) *Store {
	s := &Store{pending: make(map[string]storedRecord), clock: time.Now, random: rand.Read}
	for _, option := range options {
		option(s)
	}
	return s
}

func (s *Store) Put(record Record) (string, error) {
	if s == nil || !record.Scope.Valid() || !ValidateAgent(record.Agent) || strings.TrimSpace(record.Alias) != record.Alias || strings.TrimSpace(record.LegacyIdentity) != record.LegacyIdentity || strings.TrimSpace(record.Alias) == "" || strings.TrimSpace(record.LegacyIdentity) == "" || strings.TrimSpace(record.Root) == "" || !filepath.IsAbs(record.Root) || !record.Binding.Valid() || record.RootSnapshot.Path == "" || !filepath.IsAbs(record.RootSnapshot.Path) || !gitx.SameRepositoryPath(record.RootSnapshot.Path, record.Root) || !record.RootSnapshot.Exists || record.RootSnapshot.Kind != pathsafety.Directory || strings.TrimSpace(record.RootSnapshot.FileIdentity) == "" || record.ConfigDigest == "" {
		return "", errors.New("invalid agent configuration record")
	}
	now := s.clock()
	if record.ExpiresAt.IsZero() {
		record.ExpiresAt = now.Add(TokenTTL)
	}
	if !record.ExpiresAt.After(now) {
		return "", errors.New("agent configuration record is expired")
	}
	if record.ExpiresAt.After(now.Add(TokenTTL)) {
		record.ExpiresAt = now.Add(TokenTTL)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(now)
	for attempt := 0; attempt < 8; attempt++ {
		bytes := make([]byte, 32)
		filled := 0
		for filled < len(bytes) {
			n, err := s.random(bytes[filled:])
			if err != nil {
				return "", err
			}
			if n <= 0 || n > len(bytes)-filled {
				return "", errors.New("random source returned invalid length")
			}
			filled += n
		}
		token := base64.RawURLEncoding.EncodeToString(bytes)
		if len(token) > MaxTokenLength {
			return "", errors.New("generated token exceeds limit")
		}
		if _, exists := s.pending[token]; exists {
			continue
		}
		if len(s.pending) >= 64 {
			var oldest string
			var oldestOrder uint64
			for candidate, pending := range s.pending {
				if oldest == "" || pending.order < oldestOrder {
					oldest, oldestOrder = candidate, pending.order
				}
			}
			if oldest != "" {
				delete(s.pending, oldest)
			}
		}
		s.next++
		s.pending[token] = storedRecord{record: record, order: s.next}
		return token, nil
	}
	return "", errors.New("generated token collision")
}

// Claim atomically consumes a token. Failed authority, busy gates, stale
// state, and process errors never restore it: execute is single-use.
func (s *Store) Claim(token string) (Record, bool) {
	if s == nil || token == "" || strings.TrimSpace(token) != token || len(token) > MaxTokenLength {
		return Record{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[token]
	if !ok {
		return Record{}, false
	}
	delete(s.pending, token)
	if !s.clock().Before(pending.record.ExpiresAt) {
		return Record{}, false
	}
	return pending.record, true
}

func (s *Store) expireLocked(now time.Time) {
	for token, pending := range s.pending {
		if !now.Before(pending.record.ExpiresAt) {
			delete(s.pending, token)
		}
	}
}
