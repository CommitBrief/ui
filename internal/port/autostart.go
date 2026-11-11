// SPDX-License-Identifier: GPL-3.0-or-later

package port

import (
	"context"
	"errors"
)

// ErrAutostartUnsupported means this operating system has no login item the UI
// can manage.
var ErrAutostartUnsupported = errors.New("autostart: not supported on this operating system")

// AutoStart registers the application to start when the user logs in (UI-0002
// section 2): a LaunchAgent on macOS, an XDG autostart entry on Linux and a
// HKCU Run value on Windows. It is off until the user turns it on.
type AutoStart interface {
	// Enabled reports whether the login item exists.
	Enabled(ctx context.Context) (bool, error)
	// Enable creates (or rewrites, for the running executable) the login item.
	Enable(ctx context.Context) error
	// Disable removes it; removing a missing item is not an error.
	Disable(ctx context.Context) error
}
