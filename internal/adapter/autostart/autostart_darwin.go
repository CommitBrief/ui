// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package autostart

import (
	"os"
	"path"

	"github.com/CommitBrief/ui/internal/port"
)

// New returns the macOS login item: ~/Library/LaunchAgents/com.commitbrief.ui.plist.
// launchd reads it at the next login; nothing is loaded now, so no process is
// started from here.
func New(cfg Config) port.AutoStart {
	return fileItem{
		path: launchAgentPath(homeOrEmpty(cfg)),
		render: func() ([]byte, error) {
			exe, err := cfg.executable()
			if err != nil {
				return nil, err
			}
			return renderLaunchAgent(Label, exe)
		},
	}
}

func launchAgentPath(home string) string {
	return path.Join(home, "Library", "LaunchAgents", Label+".plist")
}

func homeOrEmpty(cfg Config) string {
	h, err := cfg.homeDir()
	if err != nil {
		return ""
	}
	return h
}

func (c Config) homeDir() (string, error) {
	f := c.HomeDir
	if f == nil {
		f = os.UserHomeDir
	}
	return f()
}
