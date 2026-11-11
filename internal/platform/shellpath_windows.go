// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package platform

// LoginShellPathCommand reports ok=false: Windows has no login shell, and a GUI
// process inherits the full user PATH from the registry-backed environment.
func LoginShellPathCommand(getenv func(string) string) (LoginShellCommand, bool) {
	return LoginShellCommand{}, false
}
