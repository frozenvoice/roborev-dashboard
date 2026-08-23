package insights

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
)

const confirmationTTL = 3 * time.Minute
const maxPendingTokens = 128

type RepositoryResolver interface {
	ResolveIdentity(string) (string, error)
	InspectIdentity(context.Context, string) (gitx.Repository, error)
}

type PreviewRequest struct {
	RepositoryIdentity string
	Window             Window
	BranchScope        BranchScope
}

func (r PreviewRequest) Validate() error {
	if strings.TrimSpace(r.RepositoryIdentity) == "" {
		return errors.New("repository identity is required")
	}
	if !r.Window.Valid() {
		return errors.New("unsupported Insights window")
	}
	if !r.BranchScope.Valid() {
		return errors.New("unsupported Insights branch scope")
	}
	return nil
}

type Preview struct {
	RepositoryIdentity string      `json:"repositoryIdentity"`
	RepositoryName     string      `json:"repositoryName"`
	Window             Window      `json:"window"`
	BranchScope        BranchScope `json:"branchScope"`
	Branch             string      `json:"branch,omitempty"`
	Command            Command     `json:"command"`
	Argv               []string    `json:"argv"`
	Warnings           []string    `json:"warnings"`
	Token              string      `json:"token"`
	ExpiresAt          time.Time   `json:"expiresAt"`
}

type StartResponse struct {
	Status  string    `json:"status"`
	JobID   int64     `json:"jobId,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Since   time.Time `json:"since,omitempty"`
	Skipped bool      `json:"skipped,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Message string    `json:"message,omitempty"`
}

type tokenState struct {
	request   Request
	identity  string
	path      string
	preflight Preflight
	expiresAt time.Time
}

type Manager struct {
	adapter  Adapter
	registry RepositoryResolver
	now      func() time.Time
	mu       sync.Mutex
	tokens   map[string]tokenState
}

func NewManager(adapter Adapter, registry RepositoryResolver) (*Manager, error) {
	if adapter == nil || registry == nil {
		return nil, errors.New("Insights adapter and repository registry are required")
	}
	return &Manager{adapter: adapter, registry: registry, now: time.Now, tokens: make(map[string]tokenState)}, nil
}

func (m *Manager) Preview(ctx context.Context, request PreviewRequest) (Preview, error) {
	if m == nil || m.adapter == nil || m.registry == nil {
		return Preview{}, errors.New("Guideline Insights is unavailable")
	}
	if err := request.Validate(); err != nil {
		return Preview{}, err
	}
	path, err := m.registry.ResolveIdentity(request.RepositoryIdentity)
	if err != nil {
		return Preview{}, err
	}
	repo, err := m.registry.InspectIdentity(ctx, request.RepositoryIdentity)
	if err != nil || strings.TrimSpace(repo.Path) == "" || !sameRepositoryPath(repo.Path, path) {
		if err == nil {
			err = errors.New("repository canonical root changed")
		}
		return Preview{}, err
	}
	launchRequest := Request{RepoPath: repo.Path, Window: request.Window, BranchScope: request.BranchScope}
	if request.BranchScope == BranchCurrent {
		branch, ok := usableCurrentBranch(repo.Branch)
		if !ok {
			return Preview{}, errors.New("current branch is unavailable")
		}
		launchRequest.Branch = branch
	}
	preflight, err := m.adapter.InsightsPreflight(ctx, launchRequest)
	if err != nil {
		return Preview{}, err
	}
	token, err := randomToken()
	if err != nil {
		return Preview{}, err
	}
	expires := m.now().Add(confirmationTTL)
	m.mu.Lock()
	// Bound the in-memory confirmation store and remove expired records before
	// adding a new preview. Tokens are intentionally not persisted.
	for token, state := range m.tokens {
		if !m.now().Before(state.expiresAt) {
			delete(m.tokens, token)
		}
	}
	for len(m.tokens) >= maxPendingTokens {
		var oldest string
		var oldestExpiry time.Time
		for token, state := range m.tokens {
			if oldest == "" || state.expiresAt.Before(oldestExpiry) {
				oldest, oldestExpiry = token, state.expiresAt
			}
		}
		if oldest == "" {
			break
		}
		delete(m.tokens, oldest)
	}
	m.tokens[token] = tokenState{request: launchRequest, identity: request.RepositoryIdentity, path: repo.Path, preflight: preflight, expiresAt: expires}
	m.mu.Unlock()
	// Warning values are stable UI-neutral codes. The browser localizes them;
	// backend policy text is never echoed into a user's locale.
	warnings := []string{"quota_cost", "read_only"}
	return Preview{RepositoryIdentity: request.RepositoryIdentity, RepositoryName: repo.Name, Window: request.Window, BranchScope: request.BranchScope, Branch: launchRequest.Branch, Command: preflight.Command, Argv: append([]string(nil), preflight.Command.Args...), Warnings: warnings, Token: token, ExpiresAt: expires}, nil
}

func (m *Manager) Start(ctx context.Context, token string) (StartResponse, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return StartResponse{}, errors.New("Insights confirmation token is required")
	}
	// Mark the token consumed before any fresh checks or process entry. This
	// makes concurrent double-confirm requests at-most-once, including when
	// the first request later encounters a drift or preflight error.
	m.mu.Lock()
	state, ok := m.tokens[token]
	if !ok {
		m.mu.Unlock()
		return StartResponse{}, errors.New("Insights confirmation token is expired or already used")
	}
	if !m.now().Before(state.expiresAt) {
		delete(m.tokens, token)
		m.mu.Unlock()
		return StartResponse{}, errors.New("Insights confirmation token is expired or already used")
	}
	// Delete atomically at claim time. The local state remains available for
	// fresh revalidation, while every concurrent or later claim fails closed;
	// ambiguity never restores the token.
	delete(m.tokens, token)
	m.mu.Unlock()

	path, err := m.registry.ResolveIdentity(state.identity)
	if err != nil || !sameRepositoryPath(path, state.path) {
		if err == nil {
			err = errors.New("repository canonical root changed")
		}
		return StartResponse{}, err
	}
	repo, err := m.registry.InspectIdentity(ctx, state.identity)
	if err != nil || !sameRepositoryPath(repo.Path, state.path) {
		if err == nil {
			err = errors.New("repository canonical root changed")
		}
		return StartResponse{}, err
	}
	if state.request.BranchScope == BranchCurrent {
		branch, ok := usableCurrentBranch(repo.Branch)
		if !ok || branch != state.request.Branch {
			return StartResponse{}, errors.New("current branch changed; prepare a new Insights preview")
		}
	}
	// Rebuild from fresh authoritative state before adapter preflight. The
	// browser never supplies a branch name or executable.
	freshRequest := state.request
	freshRequest.RepoPath = repo.Path
	preflight, err := m.adapter.InsightsPreflight(ctx, freshRequest)
	if err != nil {
		return StartResponse{}, err
	}
	if preflight.Binding != state.preflight.Binding || preflight.CLIVersion != state.preflight.CLIVersion || preflight.DaemonVersion != state.preflight.DaemonVersion {
		return StartResponse{}, errors.New("RoboRev binding changed; prepare a new Insights preview")
	}
	result, err := m.adapter.Insights(ctx, freshRequest, preflight)
	if err != nil {
		return StartResponse{}, err
	}
	if result.Unverified {
		if strings.TrimSpace(result.Message) == "" {
			result.Message = "Launch outcome could not be verified. A RoboRev Insights job may already have been queued."
		}
		return StartResponse{Status: "unverified", Message: result.Message}, nil
	}
	if result.Receipt == nil {
		return StartResponse{Status: "unverified", Message: "Launch outcome could not be verified. A RoboRev Insights job may already have been queued."}, nil
	}
	if result.Receipt.Skipped {
		return StartResponse{Status: "skipped", Skipped: true, Reason: result.Receipt.Reason, Since: result.Receipt.Since}, nil
	}
	return StartResponse{Status: "queued", JobID: result.Receipt.JobID, Agent: result.Receipt.Agent, Since: result.Receipt.Since}, nil
}

// TokenIdentity returns the still-valid repository binding without consuming
// the token. HTTP callers use it only to acquire the selected-repository read
// lease before Start reaches its atomic consume boundary.
func (m *Manager) TokenIdentity(token string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.tokens[strings.TrimSpace(token)]
	if !ok {
		return "", false
	}
	if !m.now().Before(state.expiresAt) {
		delete(m.tokens, strings.TrimSpace(token))
		return "", false
	}
	return state.identity, true
}

func randomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate Insights confirmation token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func sameRepositoryPath(left, right string) bool {
	return gitx.SameRepositoryPath(left, right)
}

func usableCurrentBranch(value string) (string, bool) {
	branch := strings.TrimSpace(value)
	if branch == "" || strings.EqualFold(branch, "HEAD") || strings.EqualFold(branch, "detached") {
		return "", false
	}
	return branch, true
}
