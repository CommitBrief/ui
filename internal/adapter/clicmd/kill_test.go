// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
)

// readPIDs returns the pids a fake grandchild appended to file.
func readPIDs(t *testing.T, file string) []int {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var pids []int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if n, err := strconv.Atoi(strings.TrimSpace(sc.Text())); err == nil {
			pids = append(pids, n)
		}
	}
	return pids
}

// waitForPIDs waits until the fake grandchild has registered itself.
func waitForPIDs(t *testing.T, file string, n int) []int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if pids := readPIDs(t, file); len(pids) >= n {
			return pids
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the grandchild never registered in %s", file)
	return nil
}

// waitDead fails the test if pid is still alive after the deadline.
func waitDead(t *testing.T, pid int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Errorf("process %d is still alive %v after the cancel", pid, within)
}

// spawnSpec runs the fake CLI directly through the runner with a grandchild.
func spawnSpec(t *testing.T, pidFile string, extraEnv ...string) runSpec {
	t.Helper()
	env := append(os.Environ(),
		"FAKECLI_SPAWN_GRANDCHILD=1",
		"FAKECLI_SLEEP_MS=60000",
		"FAKECLI_PIDFILE="+pidFile,
	)
	env = append(env, extraEnv...)
	return runSpec{Bin: fakeBin, Args: []string{"review"}, Env: env, KillGrace: 300 * time.Millisecond}
}

// TestKillGrandchildOnCancel: canceling the context stops the CLI *and* the
// process the CLI started (UI-0003 section 4: commitbrief's own `gh` or
// `claude` children must die too).
func TestKillGrandchildOnCancel(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	ctx, cancel := context.WithCancel(context.Background())

	type outcome struct {
		res runResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := runProcess(ctx, spawnSpec(t, pidFile))
		done <- outcome{res, err}
	}()

	gc := waitForPIDs(t, pidFile, 1)[0]
	if !processAlive(gc) {
		t.Fatalf("grandchild %d is not running before the cancel", gc)
	}

	start := time.Now()
	cancel()
	select {
	case o := <-done:
		if o.err != nil {
			t.Fatal(o.err)
		}
		if !o.res.Canceled {
			t.Errorf("Canceled = false: %+v", o.res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runProcess did not return after the cancel")
	}
	t.Logf("run returned %v after the cancel", time.Since(start))
	waitDead(t, gc, 5*time.Second)
}

// TestKillGrandchildOnTimeout: the same, when the deadline expires instead.
func TestKillGrandchildOnTimeout(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	spec := spawnSpec(t, pidFile)
	spec.Timeout = 700 * time.Millisecond

	res, err := runProcess(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.Canceled {
		t.Errorf("TimedOut=%v Canceled=%v, want a timeout", res.TimedOut, res.Canceled)
	}
	gcs := waitForPIDs(t, pidFile, 1)
	waitDead(t, gcs[0], 5*time.Second)
}

// TestKillThroughTheEngine: the same scenario one level up, so the engine's
// cancel path is covered too.
func TestKillThroughTheEngine(t *testing.T) {
	h := newHarness(t, true)
	pidFile := filepath.Join(t.TempDir(), "pids")
	h.set("FAKECLI_SPAWN_GRANDCHILD", "1", "FAKECLI_SLEEP_MS", "60000", "FAKECLI_PIDFILE", pidFile)

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := h.engine.ReviewLocal(ctx, localStaged(t))
		errc <- err
	}()
	gc := waitForPIDs(t, pidFile, 1)[0]
	cancel()
	select {
	case err := <-errc:
		if !errcode.Is(err, errcode.Canceled) {
			t.Errorf("err = %v, want canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ReviewLocal did not return after the cancel")
	}
	waitDead(t, gc, 5*time.Second)
}

// TestKillStdoutOverflowStopsTheProcess: output past the cap ends the run.
func TestKillStdoutOverflowStopsTheProcess(t *testing.T) {
	spec := runSpec{
		Bin:         fakeBin,
		Args:        []string{"review"},
		Env:         append(os.Environ(), "FAKECLI_BIG_STDOUT=4194304", "FAKECLI_SLEEP_MS=60000"),
		StdoutLimit: 1 << 20,
		KillGrace:   300 * time.Millisecond,
	}
	start := time.Now()
	res, err := runProcess(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !res.StdoutOverflow {
		t.Error("StdoutOverflow = false")
	}
	if len(res.Stdout) > 1<<20 {
		t.Errorf("kept %d bytes, cap is %d", len(res.Stdout), 1<<20)
	}
	if res.Canceled || res.TimedOut {
		t.Errorf("an overflow is neither a cancel nor a timeout: %+v", res)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("the run lingered %v after the overflow", d)
	}
}

func TestKillAlreadyCanceledContextStartsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := runProcess(ctx, runSpec{Bin: fakeBin, Args: []string{"x"}, Env: os.Environ()})
	if err != nil || !res.Canceled {
		t.Errorf("res=%+v err=%v, want Canceled", res, err)
	}
}
