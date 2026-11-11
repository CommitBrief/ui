// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/platform"
)

// fakeExecutable creates an empty executable for the running OS in dir.
func fakeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

type discoveryFixture struct {
	root    string
	pathDir string // in the process PATH
	probeDr string // only the login shell knows it
	known   string // a well-known location
	dataDir string
}

func newDiscoveryFixture(t *testing.T) discoveryFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return discoveryFixture{
		root:    root,
		pathDir: filepath.Join(root, "pathdir"),
		probeDr: filepath.Join(root, "probedir"),
		known:   filepath.Join(root, "known"),
		dataDir: filepath.Join(root, "data"),
	}
}

func (f discoveryFixture) discoverer(processPATH, probePATH string, override string) *Discoverer {
	return &Discoverer{
		Env:       testEnv{vars: map[string]string{"PATH": processPATH}, home: f.root},
		Override:  func() string { return override },
		DataDir:   f.dataDir,
		Probe:     func(context.Context) (string, error) { return probePATH, nil },
		KnownDirs: func() []string { return []string{f.known} },
	}
}

func TestDiscoveryOrder(t *testing.T) {
	f := newDiscoveryFixture(t)
	override := fakeExecutable(t, filepath.Join(f.root, "custom"), "my-commitbrief")
	inPath := fakeExecutable(t, f.pathDir, "commitbrief")
	inProbe := fakeExecutable(t, f.probeDr, "commitbrief")
	inKnown := fakeExecutable(t, f.known, "commitbrief")
	managed := fakeExecutable(t, filepath.Dir(ManagedBinaryPath(f.dataDir, runtime.GOOS)), "commitbrief")
	ctx := context.Background()
	sep := string(os.PathListSeparator)

	steps := []struct {
		name     string
		d        *Discoverer
		want     string
		wantFrom Source
	}{
		{"override beats everything", f.discoverer(f.pathDir, f.probeDr, override), override, SourceOverride},
		{"the login shell's PATH comes before the process PATH", f.discoverer(f.pathDir, f.probeDr, ""), inProbe, SourcePath},
		{"the process PATH is searched too", f.discoverer(f.pathDir, "", ""), inPath, SourcePath},
		{"relative PATH entries are ignored, known locations follow", f.discoverer("relative/dir"+sep+"", "", ""), inKnown, SourceKnown},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			got, err := s.d.Discover(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got.Binary != s.want || got.Source != s.wantFrom {
				t.Errorf("got %s (%s), want %s (%s)", got.Binary, got.Source, s.want, s.wantFrom)
			}
		})
	}

	// Remove the earlier candidates one by one: the managed copy is last.
	if err := os.Remove(inKnown); err != nil {
		t.Fatal(err)
	}
	got, err := f.discoverer("", "", "").Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Binary != managed || got.Source != SourceManaged {
		t.Errorf("got %s (%s), want the managed copy %s", got.Binary, got.Source, managed)
	}
	if got.PathDirs[0] != filepath.Dir(managed) {
		t.Errorf("the managed directory should lead PATH, got %v", got.PathDirs)
	}

	if err := os.Remove(managed); err != nil {
		t.Fatal(err)
	}
	_, err = f.discoverer("", "", "").Discover(ctx)
	if !errcode.Is(err, errcode.CLINotFound) {
		t.Errorf("nothing installed: err = %v, want cli_not_found", err)
	}
}

func TestDiscoveryBrokenOverrideIsAnErrorNotAFallthrough(t *testing.T) {
	f := newDiscoveryFixture(t)
	fakeExecutable(t, f.pathDir, "commitbrief")
	for name, override := range map[string]string{
		"missing":     filepath.Join(f.root, "nope"),
		"relative":    "bin/commitbrief",
		"a directory": f.pathDir,
	} {
		_, err := f.discoverer(f.pathDir, "", override).Discover(context.Background())
		if !errcode.Is(err, errcode.CLINotFound) {
			t.Errorf("%s: err = %v, want cli_not_found (a working commitbrief is on PATH, but the user chose this path)", name, err)
		}
	}
	if runtime.GOOS != "windows" {
		noExec := filepath.Join(f.root, "noexec")
		if err := os.WriteFile(noExec, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := f.discoverer(f.pathDir, "", noExec).Discover(context.Background()); !errcode.Is(err, errcode.CLINotFound) {
			t.Errorf("non-executable override: err = %v", err)
		}
	}
}

// The override is validated as a local absolute path before any file system
// access. Each row names a path CheckExecutable alone could accept or probe
// (a real .exe under a POSIX root, a UNC share), so a pass proves the
// fspath gate ran first.
func TestDiscoveryOverrideRejectsNonLocalPathsOnWindows(t *testing.T) {
	f := newDiscoveryFixture(t)
	fakeExecutable(t, f.pathDir, "commitbrief")
	cases := map[string]string{
		"UNC share":         `\\host\share\commitbrief.exe`,
		"forward-slash UNC": `//host/share/commitbrief.exe`,
		"extended UNC":      `\\?\UNC\host\share\commitbrief.exe`,
		"extended drive":    `\\?\C:\bin\commitbrief.exe`,
		"device namespace":  `\\.\pipe\commitbrief.exe`,
		"NT object path":    `\??\C:\bin\commitbrief.exe`,
		"drive-relative":    `C:bin\commitbrief.exe`,
		"rooted, no drive":  `\bin\commitbrief.exe`,
		"mixed separators":  `C:\bin/commitbrief.exe`,
		"alternate stream":  `C:\bin\commitbrief.exe:stream`,
		"reserved name":     `C:\bin\NUL`,
		"NUL byte":          "C:\\bin\\commitbrief.exe\x00",
		"relative":          `bin\commitbrief.exe`,
		"POSIX path":        `/usr/local/bin/commitbrief.exe`,
	}
	if runtime.GOOS != "windows" {
		// A real, executable-looking file: only the fspath gate refuses it.
		cases["existing POSIX .exe"] = fakeExecutableAs(t, f.known, "commitbrief.exe")
	}
	for name, override := range cases {
		d := f.discoverer(f.pathDir, "", override)
		d.GOOS = "windows"
		_, err := d.Discover(context.Background())
		if !errcode.Is(err, errcode.CLINotFound) {
			t.Errorf("%s: err = %v, want cli_not_found", name, err)
		}
	}
}

func fakeExecutableAs(t *testing.T, dir, file string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, file)
	if err := os.WriteFile(p, []byte("MZ"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDiscoveryProbeRunsOncePerRefresh(t *testing.T) {
	f := newDiscoveryFixture(t)
	fakeExecutable(t, f.probeDr, "commitbrief")
	var calls atomic.Int32
	d := f.discoverer("", "", "")
	d.Probe = func(context.Context) (string, error) {
		calls.Add(1)
		return f.probeDr, nil
	}
	for i := 0; i < 3; i++ {
		if _, err := d.Discover(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("probe ran %d times, want 1", calls.Load())
	}
	d.Refresh()
	if _, err := d.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("probe ran %d times after Refresh, want 2", calls.Load())
	}
}

func TestDiscoveryFindsGhThroughTheSamePath(t *testing.T) {
	f := newDiscoveryFixture(t)
	fakeExecutable(t, f.pathDir, "commitbrief")
	gh := fakeExecutable(t, f.probeDr, "gh") // Homebrew-style: only in the login shell's PATH
	got, err := f.discoverer(f.pathDir, f.probeDr, "").Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := got.FindTool("gh"); !ok || p != gh {
		t.Errorf("FindTool(gh) = %q, %v, want %q", p, ok, gh)
	}
	if _, ok := got.FindTool("definitely-not-installed"); ok {
		t.Error("FindTool found a tool that does not exist")
	}
}

func TestDiscoveryEnv(t *testing.T) {
	d := Discovery{PathDirs: []string{"/a", "/b"}, goos: "linux"}
	env := d.Env([]string{"PATH=/old", "NO_COLOR=0", "GIT_TERMINAL_PROMPT=1", "KEEP=me", "HOME=/h"})
	want := map[string]string{
		"PATH":                "/a" + string(os.PathListSeparator) + "/b",
		"NO_COLOR":            "1",
		"GIT_TERMINAL_PROMPT": "0",
		"KEEP":                "me",
		"HOME":                "/h",
	}
	got := map[string]string{}
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if _, dup := got[kv[:i]]; dup {
			t.Errorf("duplicate %s in %v", kv[:i], env)
		}
		got[kv[:i]] = kv[i+1:]
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}

	w := Discovery{PathDirs: []string{`C:\a`}, goos: "windows"}
	env = w.Env([]string{`Path=C:\old`, "no_color=0", "SystemRoot=C:\\Windows"})
	n := 0
	for _, kv := range env {
		name := strings.ToUpper(kv[:strings.IndexByte(kv, '=')])
		if name == "PATH" || name == "NO_COLOR" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("Windows env must hold exactly one PATH and one NO_COLOR whatever the case of the old ones: %v", env)
	}
}

func TestDiscoveryKnownLocations(t *testing.T) {
	env := testEnv{vars: map[string]string{
		"LOCALAPPDATA": `/Users/u/AppData/Local`,
		"ProgramFiles": `/Program Files`,
	}, home: "/Users/u"}

	darwin := knownDirs("darwin", env)
	for _, want := range []string{"/opt/homebrew/bin", "/usr/local/bin", filepath.Join("/Users/u", "go", "bin")} {
		if !contains(darwin, want) {
			t.Errorf("darwin known dirs %v lack %s", darwin, want)
		}
	}
	linux := knownDirs("linux", env)
	if !contains(linux, "/usr/local/bin") || !contains(linux, filepath.Join("/Users/u", "go", "bin")) {
		t.Errorf("linux known dirs = %v", linux)
	}
	win := knownDirs("windows", env)
	for _, want := range []string{
		filepath.Join("/Users/u/AppData/Local", "commitbrief"),
		filepath.Join("/Users/u", "scoop", "shims"),
		filepath.Join("/Program Files", "GitHub CLI"),
	} {
		if !contains(win, want) {
			t.Errorf("windows known dirs %v lack %s", win, want)
		}
	}
	scoop := knownDirs("windows", testEnv{vars: map[string]string{"SCOOP": "/custom/scoop"}, home: "/Users/u"})
	if !contains(scoop, filepath.Join("/custom/scoop", "shims")) {
		t.Errorf("SCOOP override ignored: %v", scoop)
	}
}

func TestDiscoverySplitPathList(t *testing.T) {
	sep := string(os.PathListSeparator)
	abs := filepath.Join(string(filepath.Separator), "usr", "bin")
	if runtime.GOOS == "windows" {
		abs = `C:\bin`
	}
	got := platform.SplitPathList(strings.Join([]string{abs, "", ".", "rel/dir", abs + string(filepath.Separator)}, sep))
	if len(got) != 2 || got[0] != abs || got[1] != abs {
		t.Errorf("splitPathList = %v, want only the absolute entries", got)
	}
	if got := platform.UniqueDirs(got); len(got) != 1 {
		t.Errorf("uniqueDirs = %v", got)
	}
}

// TestDiscoveryLoginShellProbe runs the real probe against /bin/sh, which is
// available everywhere except Windows.
func TestDiscoveryLoginShellProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no login shell on Windows")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	d := &Discoverer{Env: testEnv{vars: map[string]string{"SHELL": "/bin/sh", "PATH": "/usr/bin:/bin"}}}
	got, err := d.runLoginShellProbe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "/bin") {
		t.Errorf("probe PATH = %q", got)
	}
}

// TestDiscoveryLiveFinderLaunch is the manual check from the phase brief,
// runnable on demand: it hands discovery the minimal PATH a Finder-launched app
// gets and expects the login shell to recover commitbrief and gh. Run it with
//
//	CBUI_LIVE_DISCOVERY=1 go test ./internal/adapter/clicmd -run LiveFinderLaunch -v
func TestDiscoveryLiveFinderLaunch(t *testing.T) {
	if os.Getenv("CBUI_LIVE_DISCOVERY") != "1" {
		t.Skip("set CBUI_LIVE_DISCOVERY=1 to run against the real login shell")
	}
	home, _ := os.UserHomeDir()
	env := testEnv{home: home, vars: map[string]string{
		"PATH":  "/usr/bin:/bin:/usr/sbin:/sbin", // what launchd gives a Finder-launched app
		"SHELL": os.Getenv("SHELL"),
		"HOME":  home,
	}}
	d := &Discoverer{Env: env}
	got, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	t.Logf("commitbrief: %s (source %s)", got.Binary, got.Source)
	t.Logf("PATH handed to children: %v", got.PathDirs)
	if gh, ok := got.FindTool("gh"); ok {
		t.Logf("gh: %s", gh)
	} else {
		t.Errorf("gh not found on the discovered PATH")
	}
}
