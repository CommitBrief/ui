// SPDX-License-Identifier: GPL-3.0-or-later

// Package desktop is the Wails adapter: it owns the application window, the
// system tray and single-instance handling. Together with cmd/ it is the only
// place allowed to import Wails (UI-0001, enforced by depguard).
package desktop
