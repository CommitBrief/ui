// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package clicmd

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestKillEscalatesToSIGKILL: processes that ignore SIGTERM are killed after
// the grace period. Both the CLI and its grandchild ignore it here.
func TestKillEscalatesToSIGKILL(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	spec := spawnSpec(t, pidFile, "FAKECLI_IGNORE_TERM=1")
	spec.KillGrace = 400 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan runResult, 1)
	go func() {
		res, _ := runProcess(ctx, spec)
		done <- res
	}()
	gc := waitForPIDs(t, pidFile, 1)[0]

	start := time.Now()
	cancel()
	select {
	case res := <-done:
		if !res.Canceled {
			t.Errorf("Canceled = false: %+v", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runProcess did not return")
	}
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Errorf("returned after %v: the grace period was not honoured (SIGTERM is ignored, so only SIGKILL can end it)", d)
	}
	waitDead(t, gc, 5*time.Second)
}
