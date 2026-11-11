// SPDX-License-Identifier: GPL-3.0-or-later

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Executable lookup shared by the adapters that run external tools (the
// commitbrief CLI and gh). It starts nothing: it only reads the file system,
// so one copy serves both adapters and a fix to the PATH handling reaches both.

// SplitPathList splits a PATH value into absolute, clean directories. Relative
// entries (including the empty entry, which means the current directory on
// Unix) are dropped: resolving a binary relative to the working directory would
// let a repository plant one.
func SplitPathList(list string) []string {
	if list == "" {
		return nil
	}
	var out []string
	for _, e := range strings.Split(list, string(os.PathListSeparator)) {
		e = strings.Trim(strings.TrimSpace(e), `"`)
		if e == "" || !filepath.IsAbs(e) {
			continue
		}
		out = append(out, filepath.Clean(e))
	}
	return out
}

// UniqueDirs concatenates directory lists, cleaned, absolute only and without
// duplicates, keeping the first occurrence.
func UniqueDirs(lists ...[]string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, l := range lists {
		for _, d := range l {
			if d == "" || !filepath.IsAbs(d) {
				continue
			}
			d = filepath.Clean(d)
			if seen[d] {
				continue
			}
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// ExistingDirs keeps the directories of dirs that exist.
func ExistingDirs(dirs []string) []string {
	var out []string
	for _, d := range UniqueDirs(dirs) {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

// ExecutableNames are the file names tried for a tool. Only .exe on Windows: a
// .cmd/.bat shim would be run through cmd.exe, which re-parses the argument
// string.
func ExecutableNames(name, goos string) []string {
	if goos == "windows" {
		return []string{name + ".exe"}
	}
	return []string{name}
}

// LookupInDirs finds the first usable executable called name in dirs.
func LookupInDirs(dirs []string, name, goos string) (string, bool) {
	for _, d := range dirs {
		for _, n := range ExecutableNames(name, goos) {
			p := filepath.Join(d, n)
			if CheckExecutable(p, goos) == nil {
				return p, true
			}
		}
	}
	return "", false
}

// CheckExecutable accepts an absolute path to a regular, executable file (an
// .exe on Windows).
func CheckExecutable(p, goos string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%q is not an absolute path", p)
	}
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", p)
	}
	if goos == "windows" {
		if !strings.EqualFold(filepath.Ext(p), ".exe") {
			return fmt.Errorf("%q is not an .exe file", p)
		}
		return nil
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%q is not executable", p)
	}
	return nil
}
