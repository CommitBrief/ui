// SPDX-License-Identifier: GPL-3.0-or-later

package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveDirs(t *testing.T) {
	// Absolute on every host OS: filepath.IsAbs is checked against the running
	// OS, so build the roots from the temp directory.
	cfg := filepath.Join(t.TempDir(), "config")
	cache := filepath.Join(t.TempDir(), "cache")

	tests := []struct {
		goos    string
		appName string
	}{
		{"darwin", "CommitBrief UI"},
		{"windows", "CommitBrief UI"},
		{"linux", "commitbrief-ui"},
	}
	for _, tc := range tests {
		t.Run(tc.goos, func(t *testing.T) {
			d, err := ResolveDirs(tc.goos, cfg, cache)
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(cfg, tc.appName); d.Config != want || d.Data != want {
				t.Errorf("Config/Data = %q / %q, want %q", d.Config, d.Data, want)
			}
			if want := filepath.Join(cache, tc.appName, "logs"); d.Log != want {
				t.Errorf("Log = %q, want %q", d.Log, want)
			}
		})
	}

	if _, err := ResolveDirs("linux", "relative", cache); err == nil {
		t.Error("a relative config directory was accepted")
	}
	if _, err := ResolveDirs("linux", cfg, ""); err == nil {
		t.Error("an empty cache directory was accepted")
	}
}

func TestDirsEnsureCreatesPrivateDirectories(t *testing.T) {
	root := t.TempDir()
	d, err := ResolveDirs("linux", filepath.Join(root, "c"), filepath.Join(root, "k"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(); err != nil {
		t.Fatalf("Ensure is not idempotent: %v", err)
	}
	for _, p := range []string{d.Config, d.Log} {
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			t.Fatalf("%s: %v", p, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %v, want 0700", p, info.Mode().Perm())
		}
	}
}

func TestOSEnv(t *testing.T) {
	t.Setenv("CBUI_TEST_VAR", "yes")
	e := OSEnv{}
	if v, ok := e.LookupEnv("CBUI_TEST_VAR"); !ok || v != "yes" {
		t.Errorf("LookupEnv = %q, %v", v, ok)
	}
	if _, ok := e.LookupEnv("CBUI_TEST_UNSET_VAR"); ok {
		t.Error("LookupEnv reported an unset variable")
	}
	found := false
	for _, kv := range e.Environ() {
		if kv == "CBUI_TEST_VAR=yes" {
			found = true
		}
	}
	if !found {
		t.Error("Environ misses the variable")
	}
	if h, err := e.HomeDir(); err != nil || h == "" {
		t.Errorf("HomeDir = %q, %v", h, err)
	}
}
