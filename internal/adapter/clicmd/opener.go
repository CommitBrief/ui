// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/CommitBrief/ui/internal/domain/fspath"
	"github.com/CommitBrief/ui/internal/port"
)

// Opener opens a local file with the operating system's default application
// (port.FileOpener). It lives in this package because starting a process is
// allowed only here and in the gh adapter (UI-0003).
//
// The command is an argv array: the path is one argument, never part of a
// shell line, and there is no shell.
type Opener struct {
	goos string
	// start launches the command and returns once it is running; it does not
	// wait for it to finish. Replaced in tests so no real process starts.
	start func(name string, args []string) error
	// stat reports what is at a path; replaced in tests.
	stat func(path string) (os.FileInfo, error)
}

// NewOpener returns the opener for the running operating system.
func NewOpener() *Opener {
	return &Opener{goos: runtime.GOOS, start: startCommand, stat: os.Stat}
}

var _ port.FileOpener = (*Opener)(nil)

// openCommand is the command that opens path on goos. The path is absolute (the
// caller validated it), so it cannot be read as an option.
func openCommand(goos, path string) (string, []string) {
	switch goos {
	case "darwin":
		// -t: the default text editor, not whatever app claims .yml.
		return "open", []string{"-t", path}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", path}
	default:
		return "xdg-open", []string{path}
	}
}

// Open hands path to the system. A path that is not an absolute local path is
// refused before anything touches the file system (network and device paths on
// Windows), and a missing file is port.ErrFileMissing.
func (o *Opener) Open(ctx context.Context, path string) error {
	if err := fspath.ValidateLocalFor(o.goos, path); err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	info, err := o.stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return port.ErrFileMissing
		}
		return fmt.Errorf("open: %w", err)
	}
	if !info.Mode().IsRegular() {
		return port.ErrFileMissing
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	name, args := openCommand(o.goos, path)
	if err := o.start(name, args); err != nil {
		return fmt.Errorf("open: %w", err)
	}
	return nil
}

// startCommand launches the opener and lets go of it. Some openers (xdg-open
// on a desktop it does not know) stay in the foreground until the editor they
// started is closed, so waiting for them would report a file that is already
// open on screen as a failure. Only a failure to start is an error; the
// process is reaped in the background so it leaves no zombie.
func startCommand(name string, args []string) error {
	// No SysProcAttr: the opener is a GUI hand-over, and HideWindow on Windows
	// could carry over to the editor it starts.
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
