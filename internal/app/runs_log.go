// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/CommitBrief/ui/internal/domain/run"
)

// taskLog is the pair of log files of one task: the raw stderr of the CLI (one
// masked line at a time) and a structured slog JSON log of the task's lifecycle.
// All methods are safe for concurrent use, and every method is a no-op on a nil
// *taskLog, which is what a task whose files could not be created gets: it still
// runs, it just has no log.
type taskLog struct {
	path  string
	limit int64

	mu      sync.Mutex
	f       *os.File
	ef      *os.File
	events  *slog.Logger
	written int64
	capped  bool
	closed  bool
}

// openTaskLog creates the task's log files (owner-only). A failure is logged and
// reported as nil: a task is never refused for want of a log.
func (r *Runs) openTaskLog(t run.Task) *taskLog {
	path := r.logFile(t.ID)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		r.d.Logger.Warn("creating the task log failed; the task runs without one", "task", t.ID, "error", err)
		return nil
	}
	l := &taskLog{path: path, limit: r.d.MaxLogBytes, f: f}
	ef, err := os.OpenFile(r.eventFile(t.ID), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		r.d.Logger.Warn("creating the structured task log failed", "task", t.ID, "error", err)
		return l
	}
	l.ef = ef
	l.events = slog.New(slog.NewJSONHandler(ef, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return l
}

// logLine handles one stderr line of a running task: mask it, bound it, append
// it to the log file and send it to the screen.
func (r *Runs) logLine(w *work, tl *taskLog, raw string) {
	if tl != nil && tl.done() {
		return // full or closed: do not even mask the rest
	}
	line := boundLine(r.mask.Mask(strings.TrimRight(raw, "\r\n")))
	if tl != nil && !tl.write(line) {
		return // the log is full (or closed): the screen stops with it
	}
	if r.d.Events != nil {
		r.d.Events.Emit(EventTaskLog, TaskLogEvent{RunID: w.task.RunID, TaskID: w.task.ID, Line: line})
	}
}

// boundLine cuts a line to maxLogLineBytes on a character boundary.
func boundLine(s string) string {
	if len(s) <= maxLogLineBytes {
		return s
	}
	cut := maxLogLineBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// done reports whether the log stopped taking lines.
func (l *taskLog) done() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed || l.capped
}

// write appends one line. It returns false once the file reached its cap (one
// notice line is written at that point) or was closed.
func (l *taskLog) write(line string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.capped {
		return false
	}
	if l.written+int64(len(line))+1 > l.limit {
		l.capped = true
		_, _ = io.WriteString(l.f, "[log truncated: size limit reached]\n")
		return false
	}
	n, err := io.WriteString(l.f, line+"\n")
	l.written += int64(n)
	return err == nil
}

// event writes one structured record to the task's JSON log.
func (l *taskLog) event(level slog.Level, msg string, args ...any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.events == nil {
		return
	}
	l.events.Log(context.Background(), level, msg, args...)
}

// close flushes and closes both files.
func (l *taskLog) close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	_ = l.f.Close()
	if l.ef != nil {
		_ = l.ef.Close()
	}
}
