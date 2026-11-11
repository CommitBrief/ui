// SPDX-License-Identifier: GPL-3.0-or-later

package platform

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func writeTool(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSplitPathListKeepsOnlyAbsoluteCleanEntries(t *testing.T) {
	abs := t.TempDir()
	sep := string(os.PathListSeparator)
	got := SplitPathList(strings.Join([]string{abs, "", ".", "rel/dir", `"` + abs + `"`, abs + string(filepath.Separator)}, sep))
	if want := []string{abs, abs, abs}; !reflect.DeepEqual(got, want) {
		t.Errorf("SplitPathList = %v, want %v", got, want)
	}
	if SplitPathList("") != nil {
		t.Error("an empty list has no entries")
	}
	if got := UniqueDirs(got, []string{"relative"}, []string{""}); !reflect.DeepEqual(got, []string{abs}) {
		t.Errorf("UniqueDirs = %v", got)
	}
}

func TestExistingDirsAndLookup(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	missing := filepath.Join(a, "missing")
	if got := ExistingDirs([]string{missing, a, b, a}); !reflect.DeepEqual(got, []string{a, b}) {
		t.Errorf("ExistingDirs = %v", got)
	}

	want := writeTool(t, b, "tool", 0o755)
	if runtime.GOOS != "windows" {
		// A file without the executable bit earlier in the list is skipped.
		writeTool(t, a, "tool", 0o644)
	}
	got, ok := LookupInDirs([]string{missing, a, b}, "tool", runtime.GOOS)
	if !ok || got != want {
		t.Errorf("LookupInDirs = %q, %v; want %q", got, ok, want)
	}
	if _, ok := LookupInDirs([]string{a}, "absent", runtime.GOOS); ok {
		t.Error("found a tool that is not there")
	}
}

func TestCheckExecutableRules(t *testing.T) {
	dir := t.TempDir()
	if err := CheckExecutable("relative/tool", runtime.GOOS); err == nil {
		t.Error("a relative path must be refused")
	}
	if err := CheckExecutable(dir, runtime.GOOS); err == nil {
		t.Error("a directory is not an executable")
	}
	good := writeTool(t, dir, "ok", 0o755)
	if err := CheckExecutable(good, runtime.GOOS); err != nil {
		t.Error(err)
	}
	// Windows rules are pure string/extension checks, testable on any host.
	if runtime.GOOS != "windows" {
		txt := filepath.Join(dir, "tool.txt")
		_ = os.WriteFile(txt, []byte("x"), 0o755)
		if err := CheckExecutable(txt, "windows"); err == nil {
			t.Error("on Windows only .exe counts")
		}
		if got := ExecutableNames("gh", "windows"); !reflect.DeepEqual(got, []string{"gh.exe"}) {
			t.Errorf("ExecutableNames = %v", got)
		}
	}
}
