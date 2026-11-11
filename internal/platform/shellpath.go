// SPDX-License-Identifier: GPL-3.0-or-later

package platform

import "strings"

// The login-shell PATH probe (UI-0003 section 1). macOS hands an application
// launched from Finder or the Dock the minimal PATH /usr/bin:/bin:/usr/sbin:/sbin,
// so Homebrew's /opt/homebrew/bin is invisible. Asking the user's login shell
// for its PATH recovers it.
//
// This package only *describes* the probe command; it never starts it. Spawning
// processes is confined to internal/adapter/clicmd (UI-0003 section 4), which
// runs LoginShellPathCommand's result through its own runner.

const (
	pathMarkerStart = "__CBUI_PATH_START__"
	pathMarkerEnd   = "__CBUI_PATH_END__"
)

// LoginShellCommand is an argv to run, without a shell around it.
type LoginShellCommand struct {
	Path string
	Args []string
}

// ParseLoginShellPath extracts the PATH value from the probe's stdout. Login
// shells may print banners or rc-file noise around it, hence the markers.
func ParseLoginShellPath(stdout string) (string, bool) {
	start := strings.LastIndex(stdout, pathMarkerStart)
	if start < 0 {
		return "", false
	}
	rest := stdout[start+len(pathMarkerStart):]
	end := strings.Index(rest, pathMarkerEnd)
	if end < 0 {
		return "", false
	}
	p := strings.TrimSpace(rest[:end])
	if p == "" {
		return "", false
	}
	return p, true
}
