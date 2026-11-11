// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package gitinfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/port"
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

// Symbolic links are followed only while they stay local. A link whose target
// is a network or device path is refused with repo_local_path_network without
// being followed (filepath.EvalSymlinks would have stat-ed the target on the way
// to noticing it). The hosts here do not exist, so "network" rather than
// "missing" / "not a repository" shows the target was screened as a string.
func TestSymlinksToNetworkPathsAreRefused(t *testing.T) {
	// On Unix "//attacker/share" is an ordinary path; the Windows rules decide
	// what a link target may be, so they are switched on for this test (a Windows
	// machine always uses them).
	asWindowsLinks(t)
	ctx := context.Background()
	in := New()
	const unc = "//attacker/share"

	symlink := func(t *testing.T, target, link string) {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}

	t.Run("the checkout path itself is a link", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "repo")
		symlink(t, unc+"/repo", link)
		wantCode(t, in.CheckRoot(ctx, link), repo.CodeLocalPathNetwork)
	})

	t.Run("a directory on the way is a link", func(t *testing.T) {
		base := t.TempDir()
		symlink(t, unc, filepath.Join(base, "mnt"))
		wantCode(t, in.CheckRoot(ctx, filepath.Join(base, "mnt", "repo")), repo.CodeLocalPathNetwork)
	})

	t.Run(".git is a link", func(t *testing.T) {
		root := t.TempDir()
		symlink(t, unc+"/.git", filepath.Join(root, ".git"))
		wantCode(t, in.CheckRoot(ctx, root), repo.CodeLocalPathNetwork)
	})

	t.Run("the config file is a link", func(t *testing.T) {
		root := newCheckout(t, "")
		cfg := filepath.Join(root, ".git", "config")
		if err := os.Remove(cfg); err != nil {
			t.Fatal(err)
		}
		symlink(t, unc+"/config", cfg)
		_, err := in.Origin(ctx, root)
		wantCode(t, err, repo.CodeLocalPathNetwork)
	})

	t.Run("a link chain ends at the network", func(t *testing.T) {
		base := t.TempDir()
		symlink(t, unc, filepath.Join(base, "b"))
		symlink(t, filepath.Join(base, "b"), filepath.Join(base, "a"))
		wantCode(t, in.CheckRoot(ctx, filepath.Join(base, "a", "repo")), repo.CodeLocalPathNetwork)
	})

	t.Run("a link to a device or NT namespace path", func(t *testing.T) {
		for i, target := range []string{`\\attacker\share`, `\\?\UNC\attacker\share`, `\\.\pipe\x`, `\??\UNC\attacker\share`} {
			root := t.TempDir()
			symlink(t, target, filepath.Join(root, ".git"))
			if err := in.CheckRoot(ctx, root); !isNetwork(err) {
				t.Errorf("target %d (%q): %v, want repo_local_path_network", i, target, err)
			}
		}
	})

	t.Run("the gitdir target is a link to the network", func(t *testing.T) {
		base := t.TempDir()
		symlink(t, unc, filepath.Join(base, "real"))
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".git"), "gitdir: "+filepath.Join(base, "real", "x")+"\n")
		wantCode(t, in.CheckRoot(ctx, root), repo.CodeLocalPathNetwork)
	})
}

// Links that stay local keep working (relative, absolute, chained), and a link
// loop ends in an error instead of hanging.
func TestLocalSymlinksStillResolve(t *testing.T) {
	ctx := context.Background()
	in := New()
	want := port.RemoteRef{Host: "github.com", Owner: "CommitBrief", Name: "ui"}
	real := newCheckout(t, "[remote \"origin\"]\n\turl = git@github.com:CommitBrief/ui.git\n")

	base := t.TempDir()
	abs := filepath.Join(base, "abs")
	rel := filepath.Join(base, "rel")
	chain := filepath.Join(base, "chain")
	for link, target := range map[string]string{abs: real, rel: mustRel(t, base, real), chain: abs} {
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	for _, p := range []string{abs, rel, chain} {
		if err := in.CheckRoot(ctx, p); err != nil {
			t.Errorf("CheckRoot(%s): %v", filepath.Base(p), err)
		}
		if got, err := in.Origin(ctx, p); err != nil || got != want {
			t.Errorf("Origin(%s) = %+v, %v", filepath.Base(p), got, err)
		}
	}

	loopA, loopB := filepath.Join(base, "loopa"), filepath.Join(base, "loopb")
	if err := os.Symlink(loopB, loopA); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(loopA, loopB); err != nil {
		t.Skip(err)
	}
	within(t, "CheckRoot on a symlink loop", func() {
		err := in.CheckRoot(ctx, loopA)
		if err == nil {
			t.Error("a symlink loop was accepted")
		}
	})
}

func mustRel(t *testing.T, base, target string) string {
	t.Helper()
	r, err := filepath.Rel(base, target)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func isNetwork(err error) bool {
	var e *repo.Error
	return errors.As(err, &e) && e.Code == repo.CodeLocalPathNetwork
}

// On Unix a backslash is an ordinary file name character, not a separator: a
// checkout directory called `a\b` is that directory, not "b" inside "a", and a
// link target or "gitdir:" value containing one names the file with that
// literal name.
func TestBackslashIsAFileNameCharacterOnUnix(t *testing.T) {
	ctx := context.Background()
	in := New()
	base := t.TempDir()

	const cfg = "[remote \"origin\"]\n\turl = git@github.com:%s/%s.git\n"
	writeCheckout := func(root, owner string) {
		writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
		writeFile(t, filepath.Join(root, ".git", "config"), fmt.Sprintf(cfg, owner, "ui"))
	}
	odd := filepath.Join(base, `a\b`)
	decoy := filepath.Join(base, "a", "b")
	writeCheckout(odd, "real")
	writeCheckout(decoy, "decoy")

	if err := in.CheckRoot(ctx, odd); err != nil {
		t.Errorf("CheckRoot on a directory with a backslash in its name: %v", err)
	}
	got, err := in.Origin(ctx, odd)
	if err != nil || got.Owner != "real" {
		t.Errorf("Origin = %+v, %v; want the checkout called `a\\b`, not the decoy", got, err)
	}

	// A link target with a backslash names a literal file name too.
	if err := os.Symlink(`a\b`, filepath.Join(base, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, err := in.Origin(ctx, filepath.Join(base, "link")); err != nil || got.Owner != "real" {
		t.Errorf("Origin via link = %+v, %v; want the checkout called `a\\b`", got, err)
	}

	// And so does a relative "gitdir:" value.
	wt := t.TempDir()
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+`g\d`+"\n")
	writeFile(t, filepath.Join(wt, `g\d`, "HEAD"), "ref: refs/heads/main\n")
	if err := in.CheckRoot(ctx, wt); err != nil {
		t.Errorf("CheckRoot with gitdir naming a backslash directory: %v", err)
	}
}
