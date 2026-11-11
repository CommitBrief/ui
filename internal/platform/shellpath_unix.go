// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// LoginShellPathCommand returns the command that prints the PATH of the user's
// login shell: `$SHELL -l -c 'printf ...'`. getenv reads the UI process's
// environment. When $SHELL is unset or unusable it falls back to /bin/zsh on
// macOS and /bin/bash elsewhere. ok is false when no shell is available.
func LoginShellPathCommand(getenv func(string) string) (LoginShellCommand, bool) {
	shell := getenv("SHELL")
	if !usableShell(shell) {
		shell = ""
		for _, c := range fallbackShells(runtime.GOOS) {
			if usableShell(c) {
				shell = c
				break
			}
		}
	}
	if shell == "" {
		return LoginShellCommand{}, false
	}
	script := `printf '%s%s%s' '` + pathMarkerStart + `' "$PATH" '` + pathMarkerEnd + `'`
	if filepath.Base(shell) == "fish" {
		// fish's $PATH is a list; quoting it would join with spaces.
		script = `printf '%s%s%s' '` + pathMarkerStart + `' (string join : $PATH) '` + pathMarkerEnd + `'`
	}
	return LoginShellCommand{Path: shell, Args: []string{"-l", "-c", script}}, true
}

func fallbackShells(goos string) []string {
	if goos == "darwin" {
		return []string{"/bin/zsh", "/bin/bash", "/bin/sh"}
	}
	return []string{"/bin/bash", "/bin/sh"}
}

// usableShell accepts only an absolute path to an executable regular file.
func usableShell(p string) bool {
	if p == "" || !filepath.IsAbs(p) {
		return false
	}
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}
