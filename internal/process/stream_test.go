package process

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestReadBoundedLineAcceptsJSONLAndRejectsOversized(t *testing.T) {
	line, err := readBoundedLine(bufio.NewReader(bytes.NewBufferString(`{"type":"review.completed"}`+"\n")), 64)
	if err != nil || string(line) != `{"type":"review.completed"}` {
		t.Fatalf("line=%q err=%v", line, err)
	}
	_, err = readBoundedLine(bufio.NewReader(bytes.NewBufferString("123456\n")), 5)
	if !errors.Is(err, errStreamLineTooLarge) {
		t.Fatalf("oversized err=%v", err)
	}
}

func TestReadBoundedLineHandlesMalformedAndEOFWithoutPanic(t *testing.T) {
	line, err := readBoundedLine(bufio.NewReader(bytes.NewBufferString("not-json")), 64)
	if !errors.Is(err, io.EOF) || string(line) != "not-json" {
		t.Fatalf("line=%q err=%v", line, err)
	}
}

func TestExecRunnerStartStreamUsesExactArgsAndBoundsStderr(t *testing.T) {
	t.Setenv("ROBOREV_STREAM_HELPER_MODE", "normal")
	stream, err := (ExecRunner{}).StartStream(context.Background(), StreamCommand{
		Executable:  os.Args[0],
		Args:        []string{"-test.run=^TestStreamHelperProcess$", "-test.count=1"},
		LineLimit:   1 << 20,
		StderrLimit: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	var lines [][]byte
	for line := range stream.Lines() {
		lines = append(lines, line)
	}
	if err := stream.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("stream returned no JSONL")
	}
	var payload struct {
		Args  []string `json:"args"`
		Stdin string   `json:"stdin"`
	}
	if err := json.Unmarshal(lines[0], &payload); err != nil {
		t.Fatalf("payload=%q err=%v", lines[0], err)
	}
	if len(payload.Args) != 2 || payload.Args[1] != "-test.count=1" {
		t.Fatalf("args=%#v", payload.Args)
	}
	if payload.Stdin != "eof" {
		t.Fatalf("stdin=%q, want eof", payload.Stdin)
	}
	if concrete, ok := stream.(*execStream); !ok || !concrete.stderrWasTruncated() {
		t.Fatal("stderr sink did not report bounded truncation")
	}
}

func TestExecRunnerStartStreamReportsExitAndCancelsPromptly(t *testing.T) {
	t.Setenv("ROBOREV_STREAM_HELPER_MODE", "exit")
	stream, err := (ExecRunner{}).StartStream(context.Background(), StreamCommand{Executable: os.Args[0], Args: []string{"-test.run=^TestStreamHelperProcess$"}})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Lines() {
	}
	if err := stream.Wait(); err == nil || !strings.Contains(err.Error(), "code 7") {
		t.Fatalf("exit err=%v", err)
	}

	t.Setenv("ROBOREV_STREAM_HELPER_MODE", "block")
	ctx, cancel := context.WithCancel(context.Background())
	blocked, err := (ExecRunner{}).StartStream(ctx, StreamCommand{Executable: os.Args[0], Args: []string{"-test.run=^TestStreamHelperProcess$"}, WaitDelay: 250 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked.Lines():
	case <-time.After(time.Second):
		t.Fatal("blocked helper did not start")
	}
	cancel()
	done := make(chan error, 1)
	go func() { done <- blocked.Wait() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled stream did not finish")
	}

	t.Setenv("ROBOREV_STREAM_HELPER_MODE", "oversized")
	oversized, err := (ExecRunner{}).StartStream(context.Background(), StreamCommand{Executable: os.Args[0], Args: []string{"-test.run=^TestStreamHelperProcess$"}, LineLimit: 32})
	if err != nil {
		t.Fatal(err)
	}
	for range oversized.Lines() {
	}
	if err := oversized.Wait(); err == nil || !strings.Contains(err.Error(), "bounded limit") {
		t.Fatalf("oversized err=%v", err)
	}
}

func TestExecRunnerStartStreamPreCanceledContextDoesNotSpawn(t *testing.T) {
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		stream, err := (ExecRunner{}).StartStream(ctx, StreamCommand{Executable: os.Args[0], Args: []string{"-test.run=^TestStreamHelperProcess$"}})
		if err == nil && stream != nil {
			for range stream.Lines() {
			}
			_ = stream.Wait()
			t.Fatalf("pre-canceled start returned a live stream on iteration %d", i)
		}
	}
}

func TestExecRunnerStartStreamPartialStdoutExitCleansWindowsDescendants(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows process-tree test")
	}
	temp := t.TempDir()
	marker := filepath.Join(temp, "partial-grandchild-survived.txt")
	t.Setenv("ROBOREV_STREAM_HELPER_MODE", "partial")
	t.Setenv("ROBOREV_STREAM_HELPER_MARKER", marker)
	stream, err := (ExecRunner{}).StartStream(context.Background(), StreamCommand{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestStreamHelperProcess$"},
		WaitDelay:  250 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-stream.Lines():
	case <-time.After(time.Second):
		t.Fatal("partial helper did not emit its line")
	}
	for range stream.Lines() {
	}
	if err := stream.Wait(); err != nil && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("partial stream wait err=%v", err)
	}
	time.Sleep(3 * time.Second)
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("partial stdout child/descendant survived: %v", statErr)
	}
}

func TestExecRunnerStartStreamInheritedDescriptorsExitIsBounded(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows inherited-handle process test")
	}
	temp := t.TempDir()
	marker := filepath.Join(temp, "inherited-grandchild-survived.txt")
	t.Setenv("ROBOREV_STREAM_HELPER_MODE", "inherited-parent")
	t.Setenv("ROBOREV_STREAM_HELPER_MARKER", marker)
	stream, err := (ExecRunner{}).StartStream(context.Background(), StreamCommand{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestStreamHelperProcess$"},
		WaitDelay:  250 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		for range stream.Lines() {
		}
		finished <- stream.Wait()
	}()
	select {
	case err := <-finished:
		if err != nil && !strings.Contains(err.Error(), "canceled") {
			t.Fatalf("inherited descriptor wait err=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("inherited descriptor stream did not finish bounded")
	}
	time.Sleep(3 * time.Second)
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("inherited descriptor descendant survived: %v", statErr)
	}
}

// TestStreamHelperProcess is a deterministic child executable implemented by
// the test binary itself; it never invokes RoboRev or a shell.
func TestStreamHelperProcess(t *testing.T) {
	mode := os.Getenv("ROBOREV_STREAM_HELPER_MODE")
	if mode == "" {
		return
	}
	switch mode {
	case "normal":
		stdin := "eof"
		var one [1]byte
		if n, err := os.Stdin.Read(one[:]); n > 0 || err == nil {
			stdin = "data"
		}
		payload, _ := json.Marshal(map[string]any{"args": os.Args[1:], "stdin": stdin})
		_, _ = fmt.Fprintln(os.Stdout, string(payload))
		_, _ = fmt.Fprintln(os.Stderr, strings.Repeat("stderr", 100))
		os.Exit(0)
	case "exit":
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"review.completed"}`)
		os.Exit(7)
	case "block":
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"review.started"}`)
		select {}
	case "oversized":
		_, _ = fmt.Fprintln(os.Stdout, strings.Repeat("x", 1024))
		os.Exit(0)
	case "partial":
		marker := os.Getenv("ROBOREV_STREAM_HELPER_MARKER")
		child := exec.Command(os.Args[0], "-test.run=^TestStreamHelperProcess$", "-test.count=1")
		child.Env = append(os.Environ(), "ROBOREV_STREAM_HELPER_MODE=grandchild", "ROBOREV_STREAM_HELPER_MARKER="+marker)
		child.Stdout = io.Discard
		child.Stderr = io.Discard
		_ = child.Start()
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"review.started"}`)
		_ = os.Stdout.Close()
		time.Sleep(2 * time.Second)
	case "inherited-parent":
		marker := os.Getenv("ROBOREV_STREAM_HELPER_MARKER")
		child := exec.Command(os.Args[0], "-test.run=^TestStreamHelperProcess$", "-test.count=1")
		child.Env = append(os.Environ(), "ROBOREV_STREAM_HELPER_MODE=grandchild", "ROBOREV_STREAM_HELPER_MARKER="+marker)
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		_ = child.Start()
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"review.completed"}`)
		os.Exit(0)
	case "grandchild":
		time.Sleep(1500 * time.Millisecond)
		marker := os.Getenv("ROBOREV_STREAM_HELPER_MARKER")
		if marker != "" {
			_ = os.WriteFile(marker, []byte("survived"), 0o600)
		}
		os.Exit(0)
	}
}
