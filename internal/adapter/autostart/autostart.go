// SPDX-License-Identifier: GPL-3.0-or-later

// Package autostart registers the application to start when the user logs in
// (UI-0002 section 2): a LaunchAgent plist on macOS, an XDG autostart .desktop
// entry on Linux and a value under HKCU\Software\Microsoft\Windows\CurrentVersion\Run
// on Windows. It implements port.AutoStart. Everything is per user; nothing needs
// administrator rights, and nothing is registered until the user asks.
//
// This file holds what is the same everywhere: the item's name, the pure
// renderers of the three formats (testable on any machine) and the file-backed
// item the two Unix-like systems share.
package autostart

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/CommitBrief/ui/internal/domain/fspath"
	"github.com/CommitBrief/ui/internal/port"
)

const (
	// Label is the identifier of the login item (the single-instance id too).
	Label = "com.commitbrief.ui"
	// DisplayName is the name shown in the system's login-items list.
	DisplayName = "CommitBrief UI"
)

// Config wires the adapter. The zero value is the real system.
type Config struct {
	// Executable returns the path of the running program; nil means
	// os.Executable. The login item points at it.
	Executable func() (string, error)
	// HomeDir returns the user's home directory; nil means os.UserHomeDir.
	HomeDir func() (string, error)
	// ConfigDir returns the XDG config directory ($XDG_CONFIG_HOME, else
	// ~/.config); nil means os.UserConfigDir. Only Linux reads it.
	ConfigDir func() (string, error)
	// AppImage returns the path of the AppImage file the program runs from, empty
	// when it is not one; nil means $APPIMAGE. Only Linux reads it.
	AppImage func() string
}

func (c Config) executable() (string, error) {
	f := c.Executable
	if f == nil {
		f = os.Executable
	}
	return f()
}

// linuxExecutable is what the Linux login item runs. Inside an AppImage
// os.Executable is a path under a temporary mount (/tmp/.mount_XXXX/...) that
// disappears when the program exits, and an entry pointing there would never
// start; the AppImage runtime exports the file itself as $APPIMAGE, and that is
// what a login must start. The path goes through the same checks as any other
// (checkExecutable): a malformed $APPIMAGE is an error, never a silent fallback
// to the mount path.
func (c Config) linuxExecutable() (string, error) {
	f := c.AppImage
	if f == nil {
		f = func() string { return os.Getenv("APPIMAGE") }
	}
	if p := f(); p != "" {
		return p, nil
	}
	return c.executable()
}

// errBadExecutable marks a program path the login item must not point at.
var errBadExecutable = errors.New("autostart: the executable path is not acceptable")

// checkExecutable applies the project's local-path allowlist (fspath) for the
// given operating system, and refuses control characters on top: the path goes
// into a plist, a .desktop file or a registry value.
func checkExecutable(goos, exe string) error {
	if err := fspath.ValidateLocalFor(goos, exe); err != nil {
		return fmt.Errorf("%w: %v", errBadExecutable, err)
	}
	for _, r := range exe {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: control character", errBadExecutable)
		}
	}
	return nil
}

// renderLaunchAgent is the macOS LaunchAgent plist: start the program at login,
// once (no KeepAlive: choosing Quit must stay quit).
func renderLaunchAgent(label, exe string) ([]byte, error) {
	if err := checkExecutable("darwin", exe); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	b.WriteString("\t<key>Label</key>\n\t<string>" + xmlEscape(label) + "</string>\n")
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n\t\t<string>" + xmlEscape(exe) + "</string>\n\t</array>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>ProcessType</key>\n\t<string>Interactive</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes(), nil
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s)) // a bytes.Buffer does not fail
	return b.String()
}

// renderDesktopEntry is the XDG autostart entry (Desktop Entry specification
// 1.5). The command is one quoted argument, so a path with spaces or shell
// metacharacters stays one word.
func renderDesktopEntry(name, exe string) ([]byte, error) {
	if err := checkExecutable("linux", exe); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("[Desktop Entry]\n")
	b.WriteString("Type=Application\n")
	b.WriteString("Name=" + name + "\n")
	b.WriteString("Comment=Desktop companion for the CommitBrief CLI\n")
	b.WriteString("Exec=" + desktopExec(exe) + "\n")
	b.WriteString("Terminal=false\n")
	b.WriteString("X-GNOME-Autostart-enabled=true\n")
	return b.Bytes(), nil
}

// desktopExec quotes a program path for the Exec key. Inside double quotes the
// Exec rules escape ", `, $ and \ with a backslash and a literal percent sign
// is %%; the file's own string escaping then doubles every backslash.
func desktopExec(path string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range path {
		switch r {
		case '"', '`', '$':
			b.WriteString(`\\`)
			b.WriteRune(r)
		case '\\':
			b.WriteString(`\\\\`)
		case '%':
			b.WriteString("%%")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// windowsRunValue is the data of the Run value: the quoted program path.
func windowsRunValue(exe string) (string, error) {
	if err := checkExecutable("windows", exe); err != nil {
		return "", err
	}
	if strings.ContainsRune(exe, '"') {
		return "", fmt.Errorf("%w: quote", errBadExecutable)
	}
	return `"` + exe + `"`, nil
}

// fileItem is a login item that is one small file: the macOS plist and the
// Linux .desktop entry.
type fileItem struct {
	path   string
	render func() ([]byte, error)
}

var _ port.AutoStart = fileItem{}

// check refuses a target that is not an acceptable absolute local path (an empty
// home directory would otherwise make it relative to the working directory).
func (f fileItem) check() error {
	if err := fspath.ValidateLocal(f.path); err != nil {
		return fmt.Errorf("autostart: the login item path is not acceptable: %w", err)
	}
	return nil
}

// Enabled reports whether the file exists.
func (f fileItem) Enabled(context.Context) (bool, error) {
	if err := f.check(); err != nil {
		return false, err
	}
	info, err := os.Stat(f.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("autostart: read %s: %w", filepath.Base(f.path), err)
	}
	return info.Mode().IsRegular(), nil
}

// Enable writes the file for the running program, replacing an older one (the
// program may have moved since the item was created). The directory is created
// with mode 0755 and the file 0644 (launchd refuses a group-writable plist).
func (f fileItem) Enable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.check(); err != nil {
		return err
	}
	data, err := f.render()
	if err != nil {
		return err
	}
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("autostart: create %s: %w", filepath.Base(dir), err)
	}
	// Write beside the target and rename, so a crash never leaves half a file
	// for the login manager to read.
	tmp, err := os.CreateTemp(dir, ".commitbrief-ui-*")
	if err != nil {
		return fmt.Errorf("autostart: write %s: %w", filepath.Base(f.path), err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("autostart: write %s: %w", filepath.Base(f.path), err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("autostart: write %s: %w", filepath.Base(f.path), err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("autostart: write %s: %w", filepath.Base(f.path), err)
	}
	if err := os.Rename(tmpName, f.path); err != nil {
		cleanup()
		return fmt.Errorf("autostart: write %s: %w", filepath.Base(f.path), err)
	}
	return nil
}

// Disable removes the file; a missing file is fine.
func (f fileItem) Disable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.check(); err != nil {
		return err
	}
	if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("autostart: remove %s: %w", filepath.Base(f.path), err)
	}
	return nil
}
