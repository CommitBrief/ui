// SPDX-License-Identifier: GPL-3.0-or-later

package gitinfo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/port"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newCheckout lays out a plain checkout with the given .git/config text.
func newCheckout(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(root, ".git", "config"), config)
	return root
}

func wantCode(t *testing.T, err error, code repo.Code) {
	t.Helper()
	var e *repo.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error = %v, want code %q", err, code)
	}
}

func TestCheckRoot(t *testing.T) {
	ctx := context.Background()
	in := New()

	root := newCheckout(t, "")
	if err := in.CheckRoot(ctx, root); err != nil {
		t.Errorf("plain checkout: %v", err)
	}

	wantCode(t, in.CheckRoot(ctx, "relative/path"), repo.CodeLocalPathInvalid)
	wantCode(t, in.CheckRoot(ctx, ""), repo.CodeLocalPathInvalid)
	wantCode(t, in.CheckRoot(ctx, filepath.Join(root, "missing")), repo.CodeLocalPathMissing)
	wantCode(t, in.CheckRoot(ctx, t.TempDir()), repo.CodeLocalPathNotGit)

	// A subdirectory of a work tree is not its root.
	sub := filepath.Join(root, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wantCode(t, in.CheckRoot(ctx, sub), repo.CodeLocalPathNotGit)

	// A regular file is not a directory.
	f := filepath.Join(t.TempDir(), "file.txt")
	writeFile(t, f, "x")
	wantCode(t, in.CheckRoot(ctx, f), repo.CodeLocalPathInvalid)

	// An empty .git directory (no HEAD) is not a repository.
	bare := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bare, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantCode(t, in.CheckRoot(ctx, bare), repo.CodeLocalPathNotGit)

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := in.CheckRoot(cctx, root); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled context: %v", err)
	}
}

func TestOrigin(t *testing.T) {
	ctx := context.Background()
	in := New()
	want := port.RemoteRef{Host: "github.com", Owner: "CommitBrief", Name: "ui"}

	configs := map[string]string{
		"ssh":                    "[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = git@github.com:CommitBrief/ui.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n[branch \"main\"]\n\tremote = origin\n",
		"https":                  "[remote \"origin\"]\n\turl = https://github.com/CommitBrief/ui.git\n",
		"https with credentials": "[remote \"origin\"]\n\turl = https://user:tok@github.com/CommitBrief/ui\n",
		"legacy header":          "[remote.origin]\n\turl = git@github.com:CommitBrief/ui.git\n",
		"url on header line":     "[remote \"origin\"] url = git@github.com:CommitBrief/ui.git\n",
		"quoted with comment":    "[remote \"origin\"]\n\turl = \"git@github.com:CommitBrief/ui.git\" # the main remote\n",
		"other remote first":     "[remote \"upstream\"]\n\turl = git@github.com:other/thing.git\n[remote \"origin\"]\n\turl = git@github.com:CommitBrief/ui.git\n",
		"last url wins":          "[remote \"origin\"]\n\turl = git@github.com:old/name.git\n\turl = git@github.com:CommitBrief/ui.git\n",
		"crlf":                   "[remote \"origin\"]\r\n\turl = git@github.com:CommitBrief/ui.git\r\n",
		"mixed case key":         "[Remote \"origin\"]\n\tURL = git@github.com:CommitBrief/ui.git\n",
	}
	for name, cfg := range configs {
		t.Run(name, func(t *testing.T) {
			got, err := in.Origin(ctx, newCheckout(t, cfg))
			if err != nil {
				t.Fatalf("Origin: %v", err)
			}
			if got != want {
				t.Errorf("Origin = %+v, want %+v", got, want)
			}
		})
	}

	noRemote := map[string]string{
		"empty":                "",
		"only other remote":    "[remote \"upstream\"]\n\turl = git@github.com:a/b.git\n",
		"origin without url":   "[remote \"origin\"]\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n",
		"url in other section": "[core]\n\turl = git@github.com:a/b.git\n[remote \"origin\"]\n",
		"commented out":        "[remote \"origin\"]\n#\turl = git@github.com:a/b.git\n",
		"case differs":         "[remote \"Origin\"]\n\turl = git@github.com:a/b.git\n",
	}
	for name, cfg := range noRemote {
		t.Run("no remote/"+name, func(t *testing.T) {
			_, err := in.Origin(ctx, newCheckout(t, cfg))
			wantCode(t, err, repo.CodeNoRemote)
		})
	}

	t.Run("unsupported origin", func(t *testing.T) {
		_, err := in.Origin(ctx, newCheckout(t, "[remote \"origin\"]\n\turl = /srv/git/repo.git\n"))
		wantCode(t, err, repo.CodeRemoteUnsupported)
	})
	t.Run("missing config file", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
		_, err := in.Origin(ctx, root)
		wantCode(t, err, repo.CodeNoRemote)
	})
	t.Run("not a repository", func(t *testing.T) {
		_, err := in.Origin(ctx, t.TempDir())
		wantCode(t, err, repo.CodeLocalPathNotGit)
	})
	t.Run("oversized config", func(t *testing.T) {
		big := make([]byte, maxConfigBytes+10)
		for i := range big {
			big[i] = '#'
		}
		_, err := in.Origin(ctx, newCheckout(t, string(big)))
		wantCode(t, err, repo.CodeNoRemote)
	})
}

func TestLinkedWorktreeAndSubmodule(t *testing.T) {
	ctx := context.Background()
	in := New()
	want := port.RemoteRef{Host: "github.com", Owner: "CommitBrief", Name: "ui"}

	// Main repo with a linked worktree: the worktree's .git file points at
	// <main>/.git/worktrees/wt, whose commondir leads back to <main>/.git.
	main := newCheckout(t, "[remote \"origin\"]\n\turl = git@github.com:CommitBrief/ui.git\n")
	wtGit := filepath.Join(main, ".git", "worktrees", "wt")
	writeFile(t, filepath.Join(wtGit, "HEAD"), "ref: refs/heads/feature\n")
	writeFile(t, filepath.Join(wtGit, "commondir"), "../..\n")
	wt := t.TempDir()
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+wtGit+"\n")
	if err := in.CheckRoot(ctx, wt); err != nil {
		t.Fatalf("worktree CheckRoot: %v", err)
	}
	got, err := in.Origin(ctx, wt)
	if err != nil || got != want {
		t.Errorf("worktree Origin = %+v, %v; want %+v", got, err, want)
	}

	// Submodule: a relative gitdir with its own config and no commondir.
	super := t.TempDir()
	modGit := filepath.Join(super, ".git", "modules", "sub")
	writeFile(t, filepath.Join(modGit, "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(modGit, "config"), "[remote \"origin\"]\n\turl = https://github.com/CommitBrief/ui.git\n")
	sub := filepath.Join(super, "sub")
	writeFile(t, filepath.Join(sub, ".git"), "gitdir: ../.git/modules/sub\n")
	got, err = in.Origin(ctx, sub)
	if err != nil || got != want {
		t.Errorf("submodule Origin = %+v, %v; want %+v", got, err, want)
	}

	// A .git file that is not a pointer is not a repository.
	bad := t.TempDir()
	writeFile(t, filepath.Join(bad, ".git"), "garbage\n")
	wantCode(t, in.CheckRoot(ctx, bad), repo.CodeLocalPathNotGit)
	dangling := t.TempDir()
	writeFile(t, filepath.Join(dangling, ".git"), "gitdir: "+filepath.Join(dangling, "nowhere")+"\n")
	wantCode(t, in.CheckRoot(ctx, dangling), repo.CodeLocalPathNotGit)
}

// A network or device path must be refused by string inspection before the file
// system is touched: on Windows even stat-ing a UNC path makes the machine
// authenticate (NTLM) to the named host. The hosts below do not exist, so a
// refusal code (rather than "missing" / "not a repository") proves the string
// check ran first. Windows accepts only X:\... drive paths and reports the rest
// as network paths; on Unix the backslash spellings are plain relative names.
func TestNetworkPathsAreRefusedBeforeTheFileSystemIsTouched(t *testing.T) {
	ctx := context.Background()
	in := New()

	paths := []string{
		`\\attacker\share\repo`,
		`\\?\UNC\attacker\share\repo`,
		`\\.\pipe\x`,
		`\??\UNC\attacker\share\repo`,
	}
	want := repo.CodeLocalPathInvalid
	if runtime.GOOS == "windows" {
		want = repo.CodeLocalPathNetwork
		paths = append(paths, `//attacker/share/repo`, `\\?\C:\Users\me`)
	}
	for _, p := range paths {
		wantCode(t, in.CheckRoot(ctx, p), want)
		_, err := in.Origin(ctx, p)
		wantCode(t, err, want)
	}
}

// asWindowsLinks makes the strings found inside a checkout (link targets,
// "gitdir:" and "commondir" values) follow the Windows rules for the duration
// of a test, so that the refusal of network targets is exercised on every OS.
func asWindowsLinks(t *testing.T) {
	t.Helper()
	old := linkTargetOS
	linkTargetOS = "windows"
	t.Cleanup(func() { linkTargetOS = old })
}

// The contents of a checkout are attacker controlled too: a ".git" pointer file
// or a "commondir" file that names a network location must not be followed.
func TestNetworkPointersInsideACheckoutAreRefused(t *testing.T) {
	asWindowsLinks(t)
	ctx := context.Background()
	in := New()

	for _, target := range []string{
		`\\attacker\share\gitdir`,
		`//attacker/share/gitdir`,
		`\\?\UNC\attacker\share\gitdir`,
		`\\.\pipe\gitdir`,
		`\??\UNC\attacker\share\gitdir`,
	} {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".git"), "gitdir: "+target+"\n")
		wantCode(t, in.CheckRoot(ctx, root), repo.CodeLocalPathNetwork)
		_, err := in.Origin(ctx, root)
		wantCode(t, err, repo.CodeLocalPathNetwork)

		// commondir of an otherwise ordinary checkout.
		plain := newCheckout(t, "[remote \"origin\"]\n\turl = git@github.com:a/b.git\n")
		writeFile(t, filepath.Join(plain, ".git", "commondir"), target+"\n")
		wantCode(t, in.CheckRoot(ctx, plain), repo.CodeLocalPathNetwork)
		_, err = in.Origin(ctx, plain)
		wantCode(t, err, repo.CodeLocalPathNetwork)
	}
}
