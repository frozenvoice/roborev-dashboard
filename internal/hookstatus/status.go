// Package hookstatus provides the read-only, repository-scoped Git hook
// status contract. It deliberately has no install, repair, uninstall, init,
// or daemon operations.
package hookstatus

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	PostCommitVersionMarker  = "post-commit hook v4"
	PostRewriteVersionMarker = "post-rewrite hook v2"
	MaxHookBytes             = 256 << 10
)

type Status string

const (
	Current      Status = "current"
	Outdated     Status = "outdated"
	NotInstalled Status = "not-installed"
	Missing      Status = "missing"
	Unknown      Status = "unknown"
)

type SummaryStatus string

const (
	Ready               SummaryStatus = "ready"
	NeedsAttention      SummaryStatus = "needs-attention"
	NotInstalledSummary SummaryStatus = "not-installed"
	// Unavailable is represented as the typed `unknown` wire state. The UI
	// translates that state to its localized “Unavailable” label.
	Unavailable SummaryStatus = "unknown"
)

// Response is the complete browser DTO. It contains only typed states and
// the opaque Dashboard identity; no hook content, path, binary, or config is
// exposed.
type Response struct {
	RepositoryIdentity string        `json:"repositoryIdentity"`
	Status             SummaryStatus `json:"status"`
	PostCommit         HookResult    `json:"postCommit"`
	PostRewrite        HookResult    `json:"postRewrite"`
}

type HookResult struct {
	Status Status `json:"status"`
}

// VersionProbe is intentionally narrower than roborev.Installation. Hook
// status needs only the typed CLI version probe and never health or daemon
// state.
type VersionProbe interface {
	ProbeVersion(context.Context) (string, error)
}

type HooksPathResolver interface {
	HooksPath(context.Context, string) (string, error)
}

// FileSystem is injectable for deterministic tests. Production uses the
// osFileSystem implementation below.
type FileSystem interface {
	Lstat(string) (os.FileInfo, error)
	Open(string) (File, error)
}

type File interface {
	io.ReadCloser
	Stat() (os.FileInfo, error)
}

type osFileSystem struct{}

func (osFileSystem) Lstat(path string) (os.FileInfo, error) { return os.Lstat(path) }
func (osFileSystem) Open(path string) (File, error)         { return os.Open(path) }

type Service struct {
	Version VersionProbe
	Hooks   HooksPathResolver
	FS      FileSystem
}

func New(version VersionProbe, hooks HooksPathResolver, fs FileSystem) *Service {
	if fs == nil {
		fs = osFileSystem{}
	}
	return &Service{Version: version, Hooks: hooks, FS: fs}
}

// Inspect performs only the version gate, hook-path resolution, and bounded
// reads of the two fixed hook names. Ordinary inspection failures become
// typed unknown states; context cancellation/deadline remains an error so an
// HTTP caller can report the lifecycle failure accurately.
func (s *Service) Inspect(ctx context.Context, identity, repoPath string) (Response, error) {
	response := Response{
		RepositoryIdentity: identity,
		Status:             Unavailable,
		PostCommit:         HookResult{Status: Unknown},
		PostRewrite:        HookResult{Status: Unknown},
	}
	if err := ctx.Err(); err != nil {
		return response, err
	}
	if s == nil || s.Version == nil || s.Hooks == nil || strings.TrimSpace(repoPath) == "" {
		return response, nil
	}
	version, err := s.Version.ProbeVersion(ctx)
	if err != nil {
		if isContextError(err) {
			return response, err
		}
		return response, nil
	}
	if !roborevcompat.IsSupportedVersion(version) {
		return response, nil
	}
	hooksDir, err := s.Hooks.HooksPath(ctx, repoPath)
	if err != nil {
		if isContextError(err) {
			return response, err
		}
		return response, nil
	}
	postCommit, postCommitRead := s.readHook(filepath.Join(hooksDir, "post-commit"), PostCommitVersionMarker, NotInstalled)
	postRewrite, rewriteRead := s.readHook(filepath.Join(hooksDir, "post-rewrite"), PostRewriteVersionMarker, NotInstalled)
	if postCommitRead == readUnknown {
		postCommit = Unknown
	}
	if rewriteRead == readUnknown {
		postRewrite = Unknown
	}
	// The two files are classified independently first. Only the meaning of
	// an absent/unrelated post-rewrite is contextual: with a RoboRev
	// post-commit it is Missing, while a completely uninstalled hook set keeps
	// the less alarming NotInstalled state. If post-commit is unreadable, the
	// rewrite's positive C/O fact remains useful, but an absent rewrite cannot
	// be promoted to a claim about installation.
	if postCommit == Current || postCommit == Outdated {
		if postRewrite == NotInstalled {
			postRewrite = Missing
		}
	} else if postCommit == Unknown && postRewrite == NotInstalled {
		postRewrite = Unknown
	}
	response.PostCommit.Status = postCommit
	response.PostRewrite.Status = postRewrite
	response.Status = summaryFor(postCommit, postRewrite)
	return response, nil
}

type readState uint8

const (
	readKnown readState = iota
	readMissing
	readUnknown
)

func (s *Service) readHook(path, marker string, absent Status) (Status, readState) {
	content, state := s.readBounded(path)
	switch state {
	case readMissing:
		return absent, readKnown
	case readUnknown:
		return Unknown, readUnknown
	}
	lower := strings.ToLower(string(content))
	if !strings.Contains(lower, "roborev") {
		return absent, readKnown
	}
	if strings.Contains(string(content), marker) {
		return Current, readKnown
	}
	return Outdated, readKnown
}

func (s *Service) readBounded(path string) ([]byte, readState) {
	fs := s.FS
	if fs == nil {
		fs = osFileSystem{}
	}
	info, err := fs.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, readMissing
		}
		return nil, readUnknown
	}
	if !safeRegular(info) || info.Size() < 0 || info.Size() > MaxHookBytes {
		return nil, readUnknown
	}
	file, err := fs.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, readMissing
		}
		return nil, readUnknown
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !safeRegular(openedInfo) || !sameFile(info, openedInfo) {
		return nil, readUnknown
	}
	content, err := io.ReadAll(io.LimitReader(file, MaxHookBytes+1))
	if err != nil || len(content) > MaxHookBytes {
		return nil, readUnknown
	}
	return content, readKnown
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func sameFile(before, after os.FileInfo) bool {
	if os.SameFile(before, after) {
		return true
	}
	// Injected filesystems may not expose platform file identities. Retain a
	// conservative metadata fallback for those adapters while still requiring
	// the opened handle to remain a regular file of the same name and size.
	if before == nil || after == nil || before.Sys() != nil || after.Sys() != nil {
		return false
	}
	return before.Name() == after.Name() && before.Size() == after.Size() && before.Mode() == after.Mode() && before.ModTime() == after.ModTime()
}

func summaryFor(postCommit, postRewrite Status) SummaryStatus {
	if postCommit == Unknown || postRewrite == Unknown {
		return Unavailable
	}
	if postCommit == NotInstalled {
		return NotInstalledSummary
	}
	if postCommit == Current && postRewrite == Current {
		return Ready
	}
	return NeedsAttention
}
