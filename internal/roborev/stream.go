package roborev

import (
	"context"
	"errors"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

var (
	ErrStreamUnsupported = errors.New("RoboRev stream capability is unsupported")
	ErrStreamOffline     = errors.New("RoboRev daemon is offline; stream is unavailable")
	ErrStreamUnhealthy   = errors.New("RoboRev daemon is unhealthy; stream is unavailable")
)

// StreamSource is the narrow optional service surface used by the SSE relay.
// It intentionally has no generic command or path input: callers must pass a
// path that was freshly authorized by the repository registry.
type StreamSource interface {
	OpenStream(context.Context, string) (proc.Stream, error)
}

// OpenStream is deliberately disabled. The v0.64 CLI stream command invokes
// ensureDaemon, which is a lifecycle mutation unsuitable for a dashboard
// request. Returning before any capability probe or runner call preserves the
// polling fallback and guarantees that no process is started.
func (c *Client) OpenStream(ctx context.Context, repoPath string) (proc.Stream, error) {
	return nil, ErrStreamUnsupported
}
