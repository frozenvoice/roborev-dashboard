package process

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// StreamCommand is the deliberately small process contract for long-lived
// JSONL commands.  It is separate from Command because a stream must never
// accumulate unbounded stdout in memory.
type StreamCommand struct {
	Executable string
	Args       []string
	// Dir is intentionally supplied by the adapter only.  RoboRev stream has
	// no generic working-directory or environment API.
	Dir         string
	LineLimit   int
	StderrLimit int
	WaitDelay   time.Duration
}

const (
	DefaultStreamLineLimit   = 1 << 20
	DefaultStreamStderrLimit = 64 << 10
	DefaultStreamWaitDelay   = 2 * time.Second
)

// StreamRunner starts a bounded JSONL child process.
type StreamRunner interface {
	StartStream(context.Context, StreamCommand) (Stream, error)
}

// Stream exposes only sanitized-by-consumer stdout lines.  Stderr is consumed
// and bounded internally and is never part of this interface.
type Stream interface {
	Lines() <-chan []byte
	Wait() error
	Close() error
}

// ExecRunner implements both Runner and StreamRunner.  Keeping the
// implementation on the existing runner allows Windows descendant cleanup to
// reuse prepareCommand and attachProcessTree.
func (ExecRunner) StartStream(parent context.Context, command StreamCommand) (Stream, error) {
	if strings.TrimSpace(command.Executable) == "" {
		return nil, errors.New("executable is required")
	}
	if command.LineLimit <= 0 {
		command.LineLimit = DefaultStreamLineLimit
	}
	if command.StderrLimit <= 0 {
		command.StderrLimit = DefaultStreamStderrLimit
	}
	if command.WaitDelay <= 0 {
		command.WaitDelay = DefaultStreamWaitDelay
	}
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, command.Executable, command.Args...)
	cmd.Dir = command.Dir
	cmd.Stdin = nil
	cmd.WaitDelay = command.WaitDelay
	prepareCommand(cmd, true)
	var treeMu sync.Mutex
	var tree processTree
	// Install cancellation before Start. CommandContext may observe a
	// pre-canceled/immediately-canceled context while Start/attach is in
	// flight; the nil-tree fallback kills the process in that window.
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

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create stream stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create stream stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start stream process: %w", err)
	}

	attached, attachErr := attachProcessTree(cmd)
	if attachErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cancel()
		return nil, fmt.Errorf("attach stream process tree: %w", attachErr)
	}
	treeMu.Lock()
	if ctx.Err() != nil {
		treeMu.Unlock()
		if attached != nil {
			_ = attached.Terminate()
			_ = attached.Close()
		}
		_ = cmd.Wait()
		cancel()
		return nil, ctx.Err()
	}
	tree = attached
	var resumeErr error
	if tree != nil {
		resumeErr = tree.Resume()
	}
	treeMu.Unlock()
	if resumeErr != nil {
		treeMu.Lock()
		failedTree := tree
		tree = nil
		treeMu.Unlock()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if failedTree != nil {
			_ = failedTree.Close()
		}
		cancel()
		return nil, fmt.Errorf("resume stream process tree: %w", resumeErr)
	}
	s := &execStream{ctx: ctx, cancel: cancel, cmd: cmd, stdout: make(chan []byte, 32), done: make(chan struct{}), processDone: make(chan error, 1), waitDelay: command.WaitDelay}
	var stderrDone sync.WaitGroup
	stderrDone.Add(1)
	go func() {
		defer stderrDone.Done()
		// The stream never exposes this buffer.  Draining stderr prevents a
		// noisy child from blocking on its pipe while retaining a hard bound.
		buf := newLimitedBuffer(command.StderrLimit)
		_, _ = io.Copy(buf, stderr)
		_, truncated := buf.snapshot()
		s.stderrMu.Lock()
		s.stderrTruncated = truncated
		s.stderrMu.Unlock()
	}()
	go func() {
		s.readLoop(stdout, &stderrDone, &treeMu, &tree, command.LineLimit)
	}()
	// Start Wait exactly once after both pipe readers are live. Wait observes
	// main-child exit and closes StdoutPipe/StderrPipe even when a descendant
	// inherited the underlying handles, which unblocks both readers.
	go func() {
		s.processDone <- cmd.Wait()
	}()
	return s, nil
}

type execStream struct {
	ctx             context.Context
	cancel          context.CancelFunc
	cmd             *exec.Cmd
	stdout          chan []byte
	done            chan struct{}
	processDone     chan error
	waitDelay       time.Duration
	stderrMu        sync.Mutex
	stderrTruncated bool

	once sync.Once
	err  error
}

func (s *execStream) Lines() <-chan []byte { return s.stdout }

func (s *execStream) readLoop(stdout io.ReadCloser, stderrDone *sync.WaitGroup, treeMu *sync.Mutex, tree *processTree, lineLimit int) {
	defer close(s.stdout)
	defer close(s.done)
	reader := bufio.NewReaderSize(stdout, 32<<10)
	for {
		line, err := readBoundedLine(reader, lineLimit)
		if len(line) > 0 {
			copyLine := append([]byte(nil), line...)
			select {
			case s.stdout <- copyLine:
			case <-s.ctx.Done():
				_ = s.Close()
				_ = stdout.Close()
				goto wait
			}
		}
		if err != nil {
			if errors.Is(err, errStreamLineTooLarge) {
				s.setErr(err)
				_ = s.Close()
			} else if !errors.Is(err, io.EOF) {
				_ = s.Close()
			} else {
				// Let a normally exiting child finish naturally first; the
				// bounded waitAfterRead grace handles a child that only closed
				// stdout while retaining stderr or descendants.
			}
			break
		}
	}
wait:
	_ = stdout.Close()
	var err error
	if terminalErr := s.awaitProcess(); terminalErr != nil {
		err = terminalErr
	}
	// The processDone goroutine has already run cmd.Wait exactly once. Waiting
	// for the bounded stderr drainer now is safe because Wait closed inherited
	// pipe handles; close the Job Object/tree only after the drain completes.
	stderrDone.Wait()
	treeMu.Lock()
	if *tree != nil {
		_ = (*tree).Close()
		*tree = nil
	}
	treeMu.Unlock()
	if err != nil && s.ctx.Err() == nil && s.err == nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			s.setErr(fmt.Errorf("stream process exited with code %d", exitErr.ExitCode()))
		} else {
			s.setErr(errors.New("stream process failed"))
		}
	}
}

// awaitProcess gives a normally-terminating child a bounded grace period so
// its real exit code is retained. If stdout closed while the child (or an
// inherited descendant pipe) remains alive, cancellation then drives the
// existing WaitDelay cleanup path. The processDone channel is consumed once.
func (s *execStream) awaitProcess() error {
	timer := time.NewTimer(s.waitDelay)
	defer timer.Stop()
	select {
	case err := <-s.processDone:
		return err
	case <-timer.C:
		_ = s.Close()
		return <-s.processDone
	}
}

var errStreamLineTooLarge = errors.New("stream JSONL line exceeded bounded limit")

func readBoundedLine(reader *bufio.Reader, limit int) ([]byte, error) {
	line := make([]byte, 0, min(limit, 32<<10))
	for {
		part, err := reader.ReadSlice('\n')
		if len(part) > 0 {
			if len(line)+len(part) > limit {
				return nil, errStreamLineTooLarge
			}
			line = append(line, part...)
		}
		if err == nil {
			return trimLineEnding(line), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			if len(line) == 0 {
				return nil, io.EOF
			}
			return trimLineEnding(line), io.EOF
		}
		return trimLineEnding(line), err
	}
}

func trimLineEnding(line []byte) []byte {
	line = bytes.TrimSuffix(line, []byte("\n"))
	line = bytes.TrimSuffix(line, []byte("\r"))
	return line
}

func (s *execStream) setErr(err error) {
	s.once.Do(func() { s.err = err })
}

func (s *execStream) Wait() error {
	<-s.done
	return s.err
}

func (s *execStream) Close() error {
	s.cancel()
	// cmd.Cancel invokes the same process-tree cleanup used by context
	// cancellation.  It is deliberately best effort during shutdown.
	if s.cmd.Cancel != nil {
		if err := s.cmd.Cancel(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
	}
	return nil
}

// stderrWasTruncated is package-private so diagnostics never expose stderr
// bytes while tests can assert that the sink bound was applied.
func (s *execStream) stderrWasTruncated() bool {
	s.stderrMu.Lock()
	defer s.stderrMu.Unlock()
	return s.stderrTruncated
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
