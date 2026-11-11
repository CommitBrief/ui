// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/fspath"
	"github.com/CommitBrief/ui/internal/platform"
	"github.com/CommitBrief/ui/internal/port"
)

// Source says where the CLI binary was found (UI-0003 section 1).
type Source string

const (
	// SourceOverride is the path the user set in Settings.
	SourceOverride Source = "override"
	// SourcePath is a directory of the login shell's PATH or of the UI
	// process's own PATH.
	SourcePath Source = "path"
	// SourceKnown is one of the well-known install locations.
	SourceKnown Source = "known_location"
	// SourceManaged is the copy the UI downloaded into its data directory.
	SourceManaged Source = "managed"
)

const (
	cliName    = "commitbrief"
	probeLimit = 1 << 20
	probeWait  = 5 * time.Second
)

// Discovery is the outcome of locating the CLI.
type Discovery struct {
	// Binary is the absolute path of the commitbrief executable.
	Binary string
	Source Source
	// PathDirs are the directories handed to every child as PATH: the login
	// shell's PATH, the process PATH and the known install locations that
	// exist. commitbrief finds `gh` and the CLI-backed providers through it.
	PathDirs []string

	goos string
}

// Env returns base with PATH replaced by the discovered PATH and the variables
// every call sets: NO_COLOR=1 and GIT_TERMINAL_PROMPT=0 (no credential prompt
// on a terminal that does not exist). Existing values of those names are
// dropped; on Windows the name match is case-insensitive.
func (d Discovery) Env(base []string) []string {
	drop := map[string]bool{"PATH": true, "NO_COLOR": true, "GIT_TERMINAL_PROMPT": true}
	out := make([]string, 0, len(base)+3)
	for _, kv := range base {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if d.goos == "windows" {
			name = strings.ToUpper(name)
		}
		if drop[name] {
			continue
		}
		out = append(out, kv)
	}
	pathKey := "PATH"
	if d.goos == "windows" {
		pathKey = "Path"
	}
	return append(out,
		pathKey+"="+strings.Join(d.PathDirs, string(os.PathListSeparator)),
		"NO_COLOR=1",
		"GIT_TERMINAL_PROMPT=0",
	)
}

// FindTool locates another executable (for example `gh`) in the discovered
// PATH. It performs a file lookup only; nothing is executed.
func (d Discovery) FindTool(name string) (string, bool) {
	return platform.LookupInDirs(d.PathDirs, name, d.goos)
}

// Discoverer locates the CLI. The login-shell probe runs at most once per
// Discoverer (until Refresh), because it starts a shell.
type Discoverer struct {
	Env port.ProcessEnv
	// GOOS defaults to runtime.GOOS; tests set it.
	GOOS string
	// Override returns the user's override path from Settings; may be nil.
	Override func() string
	// DataDir is the application data directory holding the managed copy.
	DataDir string
	// Probe returns the login shell's PATH. nil uses the real probe.
	Probe func(ctx context.Context) (string, error)
	// KnownDirs replaces the well-known install locations; nil computes them
	// for GOOS. A test seam: it keeps tests from finding the developer's own
	// commitbrief or gh.
	KnownDirs func() []string

	mu       sync.Mutex
	probed   bool
	probedAs string
}

func (d *Discoverer) goos() string {
	if d.GOOS != "" {
		return d.GOOS
	}
	return runtime.GOOS
}

// Refresh forgets the cached login-shell PATH, so the next Discover probes
// again (after the user installed the CLI, say).
func (d *Discoverer) Refresh() {
	d.mu.Lock()
	d.probed = false
	d.probedAs = ""
	d.mu.Unlock()
}

// Discover walks the order from UI-0003 section 1: user override, then the
// login-shell/process PATH, then known locations, then the managed copy. A
// configured override that is unusable is an error, not a reason to silently
// use a different binary.
func (d *Discoverer) Discover(ctx context.Context) (Discovery, error) {
	goos := d.goos()
	shellPath := d.loginShellPath(ctx)
	processDirs := platform.SplitPathList(d.getenv("PATH"))
	probeDirs := platform.SplitPathList(shellPath)
	knownCandidates := knownDirs(goos, d.Env)
	if d.KnownDirs != nil {
		knownCandidates = d.KnownDirs()
	}
	known := platform.ExistingDirs(knownCandidates)
	managedBin := ManagedBinaryPath(d.DataDir, goos)

	pathDirs := platform.UniqueDirs(probeDirs, processDirs)
	newDisc := func(bin string, src Source, extra ...string) Discovery {
		all := platform.UniqueDirs(extra, pathDirs, known)
		return Discovery{Binary: bin, Source: src, PathDirs: all, goos: goos}
	}

	if d.Override != nil {
		if o := strings.TrimSpace(d.Override()); o != "" {
			// Defence in depth: app.Settings validates the path too, but this
			// adapter must not stat or exec a network/device path (a UNC stat
			// on Windows triggers NTLM authentication) whatever feeds Override.
			if err := fspath.ValidateLocalFor(goos, o); err != nil {
				return Discovery{}, errcode.Wrap(errcode.CLINotFound, "the configured commitbrief path is not a local absolute path", err)
			}
			if err := platform.CheckExecutable(o, goos); err != nil {
				return Discovery{}, errcode.Wrap(errcode.CLINotFound, "the configured commitbrief path is not usable", err)
			}
			return newDisc(o, SourceOverride), nil
		}
	}
	if bin, ok := platform.LookupInDirs(pathDirs, cliName, goos); ok {
		return newDisc(bin, SourcePath), nil
	}
	if bin, ok := platform.LookupInDirs(known, cliName, goos); ok {
		return newDisc(bin, SourceKnown), nil
	}
	if d.DataDir != "" && platform.CheckExecutable(managedBin, goos) == nil {
		// The managed directory goes first so the CLI's own "which commitbrief"
		// check after an upgrade resolves to the copy it just replaced.
		return newDisc(managedBin, SourceManaged, filepath.Dir(managedBin)), nil
	}
	return Discovery{}, errcode.New(errcode.CLINotFound, "no commitbrief binary found")
}

func (d *Discoverer) getenv(key string) string {
	if d.Env == nil {
		return ""
	}
	v, _ := d.Env.LookupEnv(key)
	return v
}

func (d *Discoverer) loginShellPath(ctx context.Context) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.probed {
		return d.probedAs
	}
	probe := d.Probe
	if probe == nil {
		probe = d.runLoginShellProbe
	}
	p, err := probe(ctx)
	if err != nil {
		p = "" // best effort: the process PATH and known locations remain
	}
	d.probed = true
	d.probedAs = p
	return p
}

// runLoginShellProbe asks the user's login shell for its PATH. The command is
// described by internal/platform and executed here, the one place allowed to
// start processes (UI-0003 section 4).
func (d *Discoverer) runLoginShellProbe(ctx context.Context) (string, error) {
	cmd, ok := platform.LoginShellPathCommand(d.getenv)
	if !ok {
		return "", nil
	}
	var base []string
	if d.Env != nil {
		base = d.Env.Environ()
	}
	res, err := runProcess(ctx, runSpec{
		Bin:         cmd.Path,
		Args:        cmd.Args,
		Env:         base,
		Timeout:     probeWait,
		StdoutLimit: probeLimit,
		KillGrace:   time.Second,
	})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("login shell exited with code %d", res.ExitCode)
	}
	p, ok := platform.ParseLoginShellPath(string(res.Stdout))
	if !ok {
		return "", fmt.Errorf("login shell printed no PATH")
	}
	return p, nil
}

// knownDirs are the well-known install locations for the CLI and for `gh`.
func knownDirs(goos string, env port.ProcessEnv) []string {
	get := func(k string) string {
		if env == nil {
			return ""
		}
		v, _ := env.LookupEnv(k)
		return v
	}
	home := get("HOME")
	if env != nil {
		if h, err := env.HomeDir(); err == nil && h != "" {
			home = h
		}
	}
	join := func(base string, elem ...string) string {
		if base == "" {
			return ""
		}
		return filepath.Join(append([]string{base}, elem...)...)
	}
	var dirs []string
	switch goos {
	case "windows":
		local := get("LOCALAPPDATA")
		scoop := get("SCOOP")
		if scoop == "" {
			scoop = join(home, "scoop")
		}
		dirs = []string{
			join(local, "Programs", "commitbrief"),
			join(local, "commitbrief"),
			join(scoop, "shims"),
			join(home, "go", "bin"),
			join(get("ProgramFiles"), "GitHub CLI"),
		}
	case "darwin":
		dirs = []string{"/opt/homebrew/bin", "/usr/local/bin", join(home, "go", "bin")}
	default:
		dirs = []string{"/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin", join(home, "go", "bin"), join(home, ".local", "bin")}
	}
	if gobin := get("GOBIN"); gobin != "" {
		dirs = append(dirs, gobin)
	}
	return dirs
}
