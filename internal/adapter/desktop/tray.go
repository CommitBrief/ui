// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/icons"
)

// Tray menu labels. They are native strings outside the frontend catalog;
// localisation of the tray is handled together with the rest of i18n.
const (
	trayShowLabel = "Show"
	trayQuitLabel = "Quit"
)

// newTray creates the system tray icon with a Show / Quit menu. The icons are
// the Wails defaults until the design system delivers the product icon.
func (s *shell) newTray(app *application.App) *application.SystemTray {
	tray := app.SystemTray.New()
	tray.SetTooltip(appName)

	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(icons.SystrayMacTemplate)
	} else {
		tray.SetIcon(icons.SystrayLight)
		tray.SetDarkModeIcon(icons.SystrayDark)
		// Windows and Linux: a left click restores the window; the menu stays
		// on the right click. On macOS the click opens the menu.
		tray.OnClick(s.showMainWindow)
	}

	menu := app.NewMenu()
	menu.Add(trayShowLabel).OnClick(func(*application.Context) {
		s.showMainWindow()
	})
	menu.AddSeparator()
	menu.Add(trayQuitLabel).OnClick(func(*application.Context) {
		s.quitting.Store(true)
		app.Quit()
	})
	tray.SetMenu(menu)

	return tray
}
