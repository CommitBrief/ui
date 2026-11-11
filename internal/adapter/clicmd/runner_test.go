// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunnerStreamsStderrLineByLine(t *testing.T) {
	var lines []string
	res, err := runProcess(context.Background(), runSpec{
		Bin:          fakeBin,
		Args:         []string{"review"},
		Env:          append(os.Environ(), "FAKECLI_STDERR_LINES=5", "FAKECLI_EXIT=1"),
		OnStderrLine: func(l string) { lines = append(lines, l) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", res.ExitCode)
	}
	if len(lines) != 5 || lines[0] != "progress line 1" || lines[4] != "progress line 5" {
		t.Errorf("lines = %q", lines)
	}
	if !strings.Contains(res.StderrTail, "progress line 5") {
		t.Errorf("StderrTail = %q", res.StderrTail)
	}
}

func TestRunnerLineWriter(t *testing.T) {
	var got []string
	w := &lineWriter{fn: func(l string) { got = append(got, l) }}
	for _, chunk := range []string{"par", "tial\nsecond\r\nthi", "rd", "\n\nlast without newline"} {
		_, _ = w.Write([]byte(chunk))
	}
	w.flush()
	want := []string{"partial", "second", "third", "", "last without newline"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("lines = %q, want %q", got, want)
	}

	got = nil
	w = &lineWriter{fn: func(l string) { got = append(got, l) }}
	_, _ = w.Write([]byte(strings.Repeat("x", maxStderrLine*2+10)))
	w.flush()
	if len(got) != 3 || len(got[0]) != maxStderrLine {
		t.Errorf("a line without newline should be split at %d bytes, got %d pieces", maxStderrLine, len(got))
	}

	got = nil
	w = &lineWriter{fn: func(l string) { got = append(got, l) }}
	_, _ = w.Write([]byte("bad \xff\xfe bytes\n"))
	if len(got) != 1 || !strings.Contains(got[0], "�") {
		t.Errorf("invalid UTF-8 should be replaced, got %q", got)
	}

	w = &lineWriter{}
	_, _ = w.Write([]byte(strings.Repeat("y", stderrTailBytes*2)))
	if n := len(w.tailString()); n != stderrTailBytes {
		t.Errorf("tail = %d bytes, want %d", n, stderrTailBytes)
	}
}

func TestRunnerRefusesAMissingBinary(t *testing.T) {
	_, err := runProcess(context.Background(), runSpec{Bin: "/nonexistent/commitbrief", Env: os.Environ()})
	var se *startError
	if err == nil || !asStartError(err, &se) {
		t.Fatalf("err = %v, want a startError", err)
	}
}

func asStartError(err error, target **startError) bool {
	se, ok := err.(*startError)
	if ok {
		*target = se
	}
	return ok
}

func TestRunnerReportsTheExitCodeWithoutAnError(t *testing.T) {
	res, err := runProcess(context.Background(), runSpec{
		Bin: fakeBin, Args: []string{"x"}, Env: append(os.Environ(), "FAKECLI_EXIT=3", "FAKECLI_STDERR=boom"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.Canceled || res.TimedOut || !strings.Contains(res.StderrTail, "boom") {
		t.Errorf("res = %+v", res)
	}
}

// TestRunnerNothingOutlivesAFinishedRun: a grandchild that keeps the pipes open
// after the CLI exited must not hang Wait, and must not survive.
func TestRunnerNothingOutlivesAFinishedRun(t *testing.T) {
	pidFile := t.TempDir() + "/pids"
	start := time.Now()
	res, err := runProcess(context.Background(), runSpec{
		Bin:  fakeBin,
		Args: []string{"review"},
		Env:  append(os.Environ(), "FAKECLI_SPAWN_GRANDCHILD=1", "FAKECLI_PIDFILE="+pidFile),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d", res.ExitCode)
	}
	if d := time.Since(start); d > 8*time.Second {
		t.Errorf("Wait hung on the grandchild's pipes for %v", d)
	}
	for _, pid := range waitForPIDs(t, pidFile, 1) {
		waitDead(t, pid, 5*time.Second)
	}
}

// TestRunnerKillsADetachedHelperAfterANormalExit: a descendant that took the
// null device for stdio holds no pipe, yet must not survive the run (it would
// otherwise pile up on Unix on every scheduled review).
func TestRunnerKillsADetachedHelperAfterANormalExit(t *testing.T) {
	pidFile := t.TempDir() + "/pids"
	res, err := runProcess(context.Background(), runSpec{
		Bin:  fakeBin,
		Args: []string{"review"},
		Env:  append(os.Environ(), "FAKECLI_SPAWN_GRANDCHILD=1", "FAKECLI_DETACH_GRANDCHILD=1", "FAKECLI_PIDFILE="+pidFile),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.Canceled || res.TimedOut {
		t.Errorf("res = %+v", res)
	}
	pids := waitForPIDs(t, pidFile, 1)
	for _, pid := range pids {
		waitDead(t, pid, 5*time.Second)
	}
}
