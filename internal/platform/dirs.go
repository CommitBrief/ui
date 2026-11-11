// SPDX-License-Identifier: GPL-3.0-or-later

package platform

import (
	"fmt"
	"os"
	"path/filepath"
)

// OSEnv is the real process environment. It satisfies port.ProcessEnv.
type OSEnv struct{}

// Environ returns os.Environ().
func (OSEnv) Environ() []string { return os.Environ() }

// LookupEnv wraps os.LookupEnv.
func (OSEnv) LookupEnv(key string) (string, bool) { return os.LookupEnv(key) }

// HomeDir wraps os.UserHomeDir.
func (OSEnv) HomeDir() (string, error) { return os.UserHomeDir() }

// Dirs are the application's per-user directories (UI-0006).
//
// Config and Data are the same directory on purpose: the UI keeps one SQLite
// database and the managed CLI copy next to each other in the OS config
// directory (on Linux this knowingly uses the config directory rather than
// XDG_STATE_HOME, UI-0006 section 2).
type Dirs struct {
	Config string
	Data   string
	Log    string
}

// appDirName is the per-OS application directory name.
func appDirName(goos string) string {
	if goos == "linux" {
		return "commitbrief-ui"
	}
	return "CommitBrief UI"
}

// ResolveDirs computes the directories from the OS user config and cache
// directories (os.UserConfigDir / os.UserCacheDir). It performs no I/O.
//
//	macOS   <config>/CommitBrief UI    logs in <cache>/CommitBrief UI/logs
//	Linux   <config>/commitbrief-ui    logs in <cache>/commitbrief-ui/logs
//	Windows <config>\CommitBrief UI    logs in <cache>\CommitBrief UI\logs
func ResolveDirs(goos, userConfigDir, userCacheDir string) (Dirs, error) {
	if !filepath.IsAbs(userConfigDir) || !filepath.IsAbs(userCacheDir) {
		return Dirs{}, fmt.Errorf("platform: user config/cache directories must be absolute (got %q, %q)", userConfigDir, userCacheDir)
	}
	name := appDirName(goos)
	cfg := filepath.Join(userConfigDir, name)
	return Dirs{
		Config: cfg,
		Data:   cfg,
		Log:    filepath.Join(userCacheDir, name, "logs"),
	}, nil
}

// UserDirs resolves the directories for the running OS and user.
func UserDirs(goos string) (Dirs, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return Dirs{}, fmt.Errorf("platform: user config dir: %w", err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return Dirs{}, fmt.Errorf("platform: user cache dir: %w", err)
	}
	return ResolveDirs(goos, cfg, cache)
}

// Ensure creates the directories with mode 0700 (the OS ignores the mode on
// Windows, where the profile ACL applies).
func (d Dirs) Ensure() error {
	for _, p := range []string{d.Config, d.Data, d.Log} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return fmt.Errorf("platform: create %s: %w", p, err)
		}
	}
	return nil
}
