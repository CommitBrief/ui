// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package autostart

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutostartLinuxWritesTheDesktopEntry(t *testing.T) {
	cfgDir := t.TempDir()
	exe := filepath.Join(cfgDir, "opt", "commitbrief-ui")
	a := New(Config{ConfigDir: func() (string, error) { return cfgDir, nil }, Executable: func() (string, error) { return exe, nil }})
	ctx := context.Background()
	if err := a.Enable(ctx); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cfgDir, "autostart", "commitbrief-ui.desktop")
	b, err := os.ReadFile(want)
	if err != nil || !strings.Contains(string(b), `Exec="`+exe+`"`) {
		t.Fatalf("entry at %s = %q, %v", want, b, err)
	}
	if on, _ := a.Enabled(ctx); !on {
		t.Error("not enabled")
	}
	if err := a.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if on, _ := a.Enabled(ctx); on {
		t.Error("still enabled")
	}
	if got := desktopEntryPath("/home/me/.config"); got != "/home/me/.config/autostart/commitbrief-ui.desktop" {
		t.Errorf("path = %s", got)
	}
}
