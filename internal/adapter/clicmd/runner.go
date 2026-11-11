// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/CommitBrief/ui/internal/platform"
)

const (
	// maxStdout caps what is read from the CLI's stdout. The only thing the UI
	// reads from stdout is one JSON document.
	maxStdout = 32 << 20 // 32 MiB
	// stderrTailBytes is how much trailing stderr is kept for classification.
	stderrTailBytes = 16 << 10
	// maxStderrLine splits a pathological line without a newline.
	maxStderrLine = 8 << 10
	// defaultKillGrace is the SIGTERM -> SIGKILL window (UI-0003 section 4).
	defaultKillGrace = 5 * time.Second
	// pipeWaitDelay bounds how long Wait lingers on open pipes held by a
	// descendant after the CLI itself has exited.
	pipeWaitDelay = 2 * time.Second
)

// runSpec describes one child process.
type runSpec struct {
	Bin  string
	Args []string
	Dir  string
	Env  []string
	// Timeout is a hard backstop for the whole run; zero means none. The CLI's
	// own --timeout is set a little lower so the CLI reports it first.
	Timeout time.Duration
	// Stdin is fed to the process and then closed; nil means the null device.
	Stdin []byte
	// OnStderrLine receives stderr line by line while the process runs.
	OnStderrLine func(string)
	// StdoutLimit caps stdout; zero means maxStdout.
	StdoutLimit int64
	// KillGrace is the time between SIGTERM and SIGKILL; zero means 5 s.
	KillGrace time.Duration
}

// runResult is what a finished process left behind.
type runResult struct {
	Stdout     []byte
	StderrTail string
	// ExitCode is the process exit code; -1 when it was killed by a signal.
	ExitCode int
	// Canceled is set when the caller's context was canceled.
	Canceled bool
	// TimedOut is set when the caller's deadline or the Timeout backstop expired.
	TimedOut bool
	// StdoutOverflow is set when stdout exceeded the cap; the process was
	// stopped and Stdout holds the part read before that.
	StdoutOverflow bool
}

// startError means the process could not be started at all (binary missing,
// not executable, process-tree setup failed).
type startError struct{ err error }

func (e *startError) Error() string { return "start process: " + e.err.Error() }
func (e *startError) Unwrap() error { return e.err }

// runProcess starts spec's process in its own process tree, streams its stderr,
// and enforces the timeout, cancellation and output cap. On cancel or timeout
// the whole tree gets SIGTERM, then SIGKILL after the grace period (Unix), or
// is terminated through its Job Object (Windows). stdin is the null device.
//
// It returns an error only when the process could not be started or waiting on
// it failed in an unexpected way; a non-zero exit is reported through
// runResult.ExitCode.
func runProcess(parent context.Context, spec runSpec) (runResult, error) {
	var res runResult

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	runCtx := ctx
	if spec.Timeout > 0 {
		var tcancel context.CancelFunc
		runCtx, tcancel = context.WithTimeout(ctx, spec.Timeout)
		defer tcancel()
	}
	if runCtx.Err() != nil {
		res.Canceled = errors.Is(parent.Err(), context.Canceled)
		res.TimedOut = !res.Canceled
		res.ExitCode = -1
		return res, nil
	}

	limit := spec.StdoutLimit
	if limit <= 0 {
		limit = maxStdout
	}
	grace := spec.KillGrace
	if grace <= 0 {
		grace = defaultKillGrace
	}

	stdout := &limitedBuffer{limit: limit, onOverflow: cancel}
	stderr := &lineWriter{fn: spec.OnStderrLine}

	cmd := exec.Command(spec.Bin, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	if spec.Stdin != nil {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	} else {
		cmd.Stdin = nil // the null device: a prompt reads EOF as "no"
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = platform.NewSysProcAttr()
	cmd.WaitDelay = pipeWaitDelay

	if err := cmd.Start(); err != nil {
		return res, &startError{err: err}
	}
	tree, err := platform.AttachTree(cmd.Process.Pid)
	if err != nil {
		// Without a handle on the tree a cancel could leave descendants behind;
		// refuse to run rather than run unsupervised.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return res, &startError{err: err}
	}
	defer func() { _ = tree.Close() }()

	done := make(chan struct{})
	escalated := make(chan struct{})
	go func() {
		defer close(escalated)
		select {
		case <-runCtx.Done():
		case <-done:
			return
		}
		_ = tree.Terminate()
		deadline := time.Now().Add(grace)
		for tree.Alive() {
			if time.Now().After(deadline) {
				_ = tree.Kill()
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	waitErr := cmd.Wait()
	close(done)
	<-escalated
	stderr.flush()

	// Nothing may outlive a finished CLI run, whether or not a descendant held
	// the pipes: a helper that detached its stdio (a CLI-backed provider's
	// background process, say) would otherwise pile up on Unix on every run,
	// while Windows already kills it when the Job Object closes. An empty group
	// answers ESRCH, which Kill swallows.
	_ = tree.Kill()
	if errors.Is(waitErr, exec.ErrWaitDelay) {
		waitErr = nil // the CLI itself exited cleanly; only a pipe was held open
	}

	res.Stdout = stdout.Bytes()
	res.StderrTail = stderr.tailString()
	res.StdoutOverflow = stdout.overflowed()
	res.ExitCode = -1
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}

	if parentErr := parent.Err(); parentErr != nil {
		res.Canceled = errors.Is(parentErr, context.Canceled)
		res.TimedOut = errors.Is(parentErr, context.DeadlineExceeded)
	} else if runCtx.Err() != nil && !res.StdoutOverflow {
		res.TimedOut = true // the Timeout backstop
	}

	var ee *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &ee) {
		return res, fmt.Errorf("wait for process: %w", waitErr)
	}
	return res, nil
}

// limitedBuffer collects up to limit bytes and then discards the rest, calling
// onOverflow once so the run is stopped instead of blocking on a full pipe.
type limitedBuffer struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	limit      int64
	over       bool
	onOverflow func()
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.over {
		return len(p), nil
	}
	if int64(b.buf.Len())+int64(len(p)) > b.limit {
		b.over = true
		if b.onOverflow != nil {
			b.onOverflow()
		}
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

func (b *limitedBuffer) overflowed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.over
}

// lineWriter splits a stream into lines for a callback and keeps the last
// stderrTailBytes bytes for classification. Lines are sanitized to valid UTF-8
// and stripped of a trailing carriage return.
type lineWriter struct {
	fn      func(string)
	mu      sync.Mutex
	partial []byte
	tail    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.tail = append(w.tail, p...)
	if over := len(w.tail) - stderrTailBytes; over > 0 {
		w.tail = append([]byte(nil), w.tail[over:]...)
	}

	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		w.emit(w.partial[:i])
		w.partial = w.partial[i+1:]
	}
	for len(w.partial) > maxStderrLine {
		w.emit(w.partial[:maxStderrLine])
		w.partial = w.partial[maxStderrLine:]
	}
	if len(w.partial) == 0 {
		w.partial = nil
	}
	return len(p), nil
}

func (w *lineWriter) emit(line []byte) {
	if w.fn == nil {
		return
	}
	s := strings.ToValidUTF8(strings.TrimRight(string(line), "\r"), "�")
	w.fn(s)
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.partial) > 0 {
		w.emit(w.partial)
		w.partial = nil
	}
}

func (w *lineWriter) tailString() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.ToValidUTF8(string(w.tail), "�")
}
