// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeShell(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoginShellPathCommand(t *testing.T) {
	zsh := fakeShell(t, "zsh")
	cmd, ok := LoginShellPathCommand(func(k string) string {
		if k == "SHELL" {
			return zsh
		}
		return ""
	})
	if !ok || cmd.Path != zsh {
		t.Fatalf("cmd = %+v, ok = %v", cmd, ok)
	}
	if len(cmd.Args) != 3 || cmd.Args[0] != "-l" || cmd.Args[1] != "-c" {
		t.Errorf("args = %v, want -l -c <script>", cmd.Args)
	}
	script := cmd.Args[2]
	if !strings.Contains(script, pathMarkerStart) || !strings.Contains(script, `"$PATH"`) {
		t.Errorf("script = %q", script)
	}
}

func TestLoginShellPathCommandFish(t *testing.T) {
	fish := fakeShell(t, "fish")
	cmd, ok := LoginShellPathCommand(func(string) string { return fish })
	if !ok {
		t.Fatal("fish was not accepted")
	}
	if !strings.Contains(cmd.Args[2], "string join : $PATH") {
		t.Errorf("fish needs its own list syntax, script = %q", cmd.Args[2])
	}
}

func TestLoginShellPathCommandFallsBack(t *testing.T) {
	for name, shell := range map[string]string{
		"unset":       "",
		"relative":    "zsh",
		"missing":     "/nonexistent/zsh",
		"a directory": t.TempDir(),
	} {
		cmd, ok := LoginShellPathCommand(func(string) string { return shell })
		if !ok {
			t.Skipf("%s: no fallback shell on this machine", name)
		}
		if cmd.Path == shell {
			t.Errorf("%s: the unusable $SHELL %q was used", name, shell)
		}
		if !filepath.IsAbs(cmd.Path) {
			t.Errorf("%s: fallback %q is not absolute", name, cmd.Path)
		}
	}

	nonExec := filepath.Join(t.TempDir(), "sh")
	if err := os.WriteFile(nonExec, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if usableShell(nonExec) {
		t.Error("a non-executable file counts as a shell")
	}
}

func TestProcessTreeSafetyGuards(t *testing.T) {
	// A pgid of 0 or 1 would signal our own group or every process.
	for _, pid := range []int{-5, 0, 1} {
		if _, err := AttachTree(pid); err == nil {
			t.Errorf("AttachTree(%d) = nil error", pid)
		}
	}
	tree, err := AttachTree(os.Getpid() + 1<<22) // a pid that does not exist
	if err != nil {
		t.Fatal(err)
	}
	if tree.Alive() {
		t.Error("a nonexistent group reports alive")
	}
	if err := tree.Terminate(); err != nil {
		t.Errorf("signalling a vanished group should be a no-op, got %v", err)
	}
	if err := tree.Kill(); err != nil {
		t.Errorf("killing a vanished group should be a no-op, got %v", err)
	}
	if err := tree.Close(); err != nil {
		t.Error(err)
	}
}
