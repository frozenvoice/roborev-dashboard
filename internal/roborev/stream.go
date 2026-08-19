package roborev

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// OpenStream performs all capability and daemon gates before spawning a child.
// The command uses an immutable binary snapshot and an exact typed argv array;
// no shell, stdin, environment, or working directory is involved.
func (c *Client) OpenStream(ctx context.Context, repoPath string) (proc.Stream, error) {
	if c == nil {
		return nil, ErrStreamUnsupported
	}
	if ctx == nil {
		ctx = context.Background()
	}
	args, err := StreamArgs(repoPath)
	if err != nil {
		return nil, err
	}
	binary, _, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return nil, ErrStreamUnsupported
	}
	installation := c.Installation(ctx)
	if !installation.Installed || !installation.Capabilities.StreamEvents || installation.Version != "v0.64.0" {
		return nil, fmt.Errorf("%w: %s", ErrStreamUnsupported, installation.Version)
	}
	running, healthErr := c.DaemonHealth(ctx)
	if healthErr != nil {
		if errors.Is(healthErr, ErrDaemonUnhealthy) {
			return nil, ErrStreamUnhealthy
		}
		return nil, healthErr
	}
	if !running {
		// Crucially, do not invoke daemon start or stream when offline.
		return nil, ErrStreamOffline
	}
	binaryNow, _, generationNow := c.snapshot()
	if generationNow != generation || binaryNow != binary {
		return nil, ErrBindingChanged
	}
	runner := c.StreamRunner
	if runner == nil {
		runner = proc.ExecRunner{}
	}
	return runner.StartStream(ctx, proc.StreamCommand{
		Executable:  binary,
		Args:        append([]string(nil), args...),
		Dir:         "",
		LineLimit:   proc.DefaultStreamLineLimit,
		StderrLimit: proc.DefaultStreamStderrLimit,
		WaitDelay:   proc.DefaultStreamWaitDelay,
	})
}
