// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package autostart

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutostartDarwinWritesTheLaunchAgent(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "Applications", "CommitBrief UI.app", "Contents", "MacOS", "commitbrief-ui")
	a := New(Config{HomeDir: func() (string, error) { return home, nil }, Executable: func() (string, error) { return exe, nil }})
	ctx := context.Background()
	if err := a.Enable(ctx); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Library", "LaunchAgents", "com.commitbrief.ui.plist")
	b, err := os.ReadFile(want)
	if err != nil || !strings.Contains(string(b), exe) {
		t.Fatalf("plist at %s = %q, %v", want, b, err)
	}
	if on, _ := a.Enabled(ctx); !on {
		t.Error("not enabled")
	}
	if err := a.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(want); err == nil {
		t.Error("plist left behind")
	}
	if got := launchAgentPath("/Users/me"); got != "/Users/me/Library/LaunchAgents/com.commitbrief.ui.plist" {
		t.Errorf("path = %s", got)
	}
}
