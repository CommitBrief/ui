// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package gitinfo

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/repo"
)

// within fails the test if f does not return quickly: opening a FIFO for
// reading blocks until a writer shows up, so a missing regular-file check
// shows up as a hang.
func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s hung (a non-regular file was opened)", what)
	}
}

func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo not available: %v", err)
	}
}

func TestNonRegularGitFilesAreRefusedWithoutBlocking(t *testing.T) {
	ctx := context.Background()
	in := New()

	t.Run(".git is a FIFO", func(t *testing.T) {
		root := t.TempDir()
		mkfifo(t, filepath.Join(root, ".git"))
		within(t, "CheckRoot", func() { wantCode(t, in.CheckRoot(ctx, root), repo.CodeLocalPathNotGit) })
	})

	t.Run(".git is a symlink to a FIFO", func(t *testing.T) {
		root := t.TempDir()
		fifo := filepath.Join(t.TempDir(), "pipe")
		mkfifo(t, fifo)
		if err := os.Symlink(fifo, filepath.Join(root, ".git")); err != nil {
			t.Skip(err)
		}
		within(t, "CheckRoot", func() { wantCode(t, in.CheckRoot(ctx, root), repo.CodeLocalPathNotGit) })
	})

	t.Run("config is a FIFO", func(t *testing.T) {
		root := newCheckout(t, "")
		cfg := filepath.Join(root, ".git", "config")
		if err := os.Remove(cfg); err != nil {
			t.Fatal(err)
		}
		mkfifo(t, cfg)
		within(t, "Origin", func() {
			_, err := in.Origin(ctx, root)
			wantCode(t, err, repo.CodeNoRemote)
		})
	})

	t.Run("commondir is a FIFO", func(t *testing.T) {
		root := newCheckout(t, "[remote \"origin\"]\n\turl = git@github.com:a/b.git\n")
		mkfifo(t, filepath.Join(root, ".git", "commondir"))
		within(t, "Origin", func() {
			// An unreadable commondir is ignored: the git dir itself is used.
			if _, err := in.Origin(ctx, root); err != nil {
				t.Errorf("Origin = %v", err)
			}
		})
	})
}

func TestReadsHonorACanceledContext(t *testing.T) {
	root := newCheckout(t, "[remote \"origin\"]\n\turl = git@github.com:a/b.git\n")
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readLimited(cctx, filepath.Join(root, ".git", "config"), maxConfigBytes); err == nil {
		t.Error("readLimited ignored a canceled context")
	}
}
