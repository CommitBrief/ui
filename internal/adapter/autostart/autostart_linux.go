// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package autostart

import (
	"os"
	"path"

	"github.com/CommitBrief/ui/internal/port"
)

// New returns the Linux login item: $XDG_CONFIG_HOME/autostart/commitbrief-ui.desktop
// (default ~/.config). The desktop session starts it at the next login.
func New(cfg Config) port.AutoStart {
	return fileItem{
		path: desktopEntryPath(configOrEmpty(cfg)),
		render: func() ([]byte, error) {
			exe, err := cfg.linuxExecutable()
			if err != nil {
				return nil, err
			}
			return renderDesktopEntry(DisplayName, exe)
		},
	}
}

func desktopEntryPath(configDir string) string {
	return path.Join(configDir, "autostart", desktopFileName)
}

func configOrEmpty(cfg Config) string {
	d, err := cfg.configDir()
	if err != nil {
		return ""
	}
	return d
}

// desktopFileName is the XDG autostart file.
const desktopFileName = "commitbrief-ui.desktop"

func (c Config) configDir() (string, error) {
	f := c.ConfigDir
	if f == nil {
		f = os.UserConfigDir
	}
	return f()
}
