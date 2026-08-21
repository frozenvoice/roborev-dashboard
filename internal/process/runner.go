package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const DefaultOutputLimit = 4 << 20

// DefaultStdinLimit is the bound used by short-lived task commands.  The
// runner accepts stdin as opaque bytes represented by a string; it never
// interpolates those bytes into an executable or argument string.
const DefaultStdinLimit = 16 << 10

type Command struct {
	Executable string
	Args       []string
	Dir        string
	// Stdin is supplied directly to the child process. An empty value keeps
	// the historical nil-stdin behaviour for existing callers.
	Stdin string
	// StdinLimit overrides the default bounded stdin contract. Zero uses
	// DefaultStdinLimit; callers should only raise this for a separately
	// reviewed machine protocol, never for user task text.
	StdinLimit int
	// Env replaces the inherited environment when non-nil.
	Env     []string
	Timeout time.Duration
	// WaitDelay overrides the post-cancellation Wait bound; zero keeps the
	// existing two-second default.
	WaitDelay   time.Duration
	OutputLimit int
	// AllowDetachedChildren is reserved for lifecycle commands such as
	// `roborev daemon start` whose purpose is to leave a child process running.
	AllowDetachedChildren bool
}

type Result struct {
	Stdout          string        `json:"stdout"`
	Stderr          string        `json:"stderr"`
	Started         bool          `json:"started"`
	ExitCode        int           `json:"exitCode"`
	Duration        time.Duration `json:"duration"`
	StdoutTruncated bool          `json:"stdoutTruncated"`
	StderrTruncated bool          `json:"stderrTruncated"`
}

type Runner interface {
	Run(context.Context, Command) (Result, error)
}

type ExecRunner struct{}

type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	remaining int
	truncated bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	if limit <= 0 {
		limit = DefaultOutputLimit
	}
	return &limitedBuffer{remaining: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	original := len(p)
	if b.remaining == 0 {
		b.truncated = true
		return original, nil
	}
	write := p
	if len(write) > b.remaining {
		write = write[:b.remaining]
		b.truncated = true
	}
	_, _ = b.buf.Write(write)
	b.remaining -= len(write)
	return original, nil
}

func (b *limitedBuffer) snapshot() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String(), b.truncated
}

func (ExecRunner) Run(parent context.Context, command Command) (Result, error) {
	if strings.TrimSpace(command.Executable) == "" {
		return Result{}, errors.New("executable is required")
	}
	stdinLimit := command.StdinLimit
	if stdinLimit <= 0 {
		stdinLimit = DefaultStdinLimit
	}
	if len([]byte(command.Stdin)) > stdinLimit {
		return Result{}, fmt.Errorf("stdin exceeds %d bytes", stdinLimit)
	}
	ctx := parent
	cancel := func() {}
	if command.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, command.Timeout)
	}
	defer cancel()

	stdout := newLimitedBuffer(command.OutputLimit)
	stderr := newLimitedBuffer(command.OutputLimit)
	cmd := exec.CommandContext(ctx, command.Executable, command.Args...)
	cmd.Dir = command.Dir
	if command.Stdin != "" {
		cmd.Stdin = strings.NewReader(command.Stdin)
	}
	if command.Env != nil {
		cmd.Env = append([]string(nil), command.Env...)
	}
	prepareCommand(cmd, !command.AllowDetachedChildren)
	cmd.Stdout = io.Writer(stdout)
	cmd.Stderr = io.Writer(stderr)

	startedAt := time.Now()
	cmd.WaitDelay = command.WaitDelay
	if cmd.WaitDelay <= 0 {
		cmd.WaitDelay = 2 * time.Second
	}
	var treeMu sync.Mutex
	var tree processTree
	cmd.Cancel = func() error {
		treeMu.Lock()
		defer treeMu.Unlock()
		if tree != nil {
			return tree.Terminate()
		}
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	err := cmd.Start()
	processStarted := err == nil
	if err == nil && !command.AllowDetachedChildren {
		attached, attachErr := attachProcessTree(cmd)
		if attachErr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			err = fmt.Errorf("attach process tree: %w", attachErr)
		} else {
			treeMu.Lock()
			tree = attached
			if tree != nil {
				attachErr = tree.Resume()
			}
			treeMu.Unlock()
			if attachErr != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				err = fmt.Errorf("resume process tree: %w", attachErr)
			}
		}
	}
	if err == nil {
		err = cmd.Wait()
	}
	treeMu.Lock()
	if tree != nil {
		_ = tree.Close()
		tree = nil
	}
	treeMu.Unlock()
	result := Result{Duration: time.Since(startedAt), ExitCode: 0, Started: processStarted}
	result.Stdout, result.StdoutTruncated = stdout.snapshot()
	result.Stderr, result.StderrTruncated = stderr.snapshot()
	if err == nil {
		return result, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		result.ExitCode = -1
		return result, fmt.Errorf("process canceled: %w", ctxErr)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, fmt.Errorf("process exited with code %d", result.ExitCode)
	}
	result.ExitCode = -1
	return result, fmt.Errorf("start process: %w", err)
}

type processTree interface {
	Resume() error
	Terminate() error
	Close() error
}
