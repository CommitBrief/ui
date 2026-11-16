// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	_ "embed"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Tray icons, drawn from build/appicon.svg by `make icons` (scripts/genicons.go).
// The macOS template and the Windows pair show the chevron of the CommitBrief
// mark without its square; the Linux icon is the full mark.
var (
	// trayMacTemplate is a template image: black plus alpha only, so macOS
	// recolours it for the light and dark menu bar. 44 px = 22 pt at @2x.
	//
	//go:embed trayicons/mac-template.png
	trayMacTemplate []byte

	// trayLight is shown when the system theme is light: ink chevron.
	//
	//go:embed trayicons/light.png
	trayLight []byte

	// trayDark is shown when the system theme is dark: brand green chevron.
	//
	//go:embed trayicons/dark.png
	trayDark []byte

	// trayLinux is the full mark (green square, ink chevron). Wails' Linux
	// tray implements SetDarkModeIcon as a plain SetIcon, so a light/dark pair
	// cannot work there: the dark icon would overwrite the light one on every
	// panel. One icon that reads on light and dark panels is used instead.
	//
	//go:embed trayicons/linux.png
	trayLinux []byte
)

// Tray menu labels. They are native strings outside the frontend catalog;
// localisation of the tray is handled together with the rest of i18n.
const (
	trayShowLabel = "Show"
	trayQuitLabel = "Quit"
)

// newTray creates the system tray icon with a Show / bot status / pause-resume /
// Quit menu (the bot items only when bot is not nil). The icon is the
// CommitBrief mark: a chevron template image on macOS, a light and a dark chevron
// variant on Windows, and a single full-mark icon on Linux.
//
// The returned stop function ends the menu's refresh timer; call it when the
// application has quit.
func (s *shell) newTray(app *application.App, bot BotControl) (tray *application.SystemTray, stop func()) {
	tray = app.SystemTray.New()
	tray.SetTooltip(appName)

	switch runtime.GOOS {
	case "darwin":
		tray.SetTemplateIcon(trayMacTemplate)
	case "linux":
		// SetDarkModeIcon is deliberately not called: on Linux it replaces
		// the icon set by SetIcon regardless of the theme.
		tray.SetIcon(trayLinux)
	default:
		tray.SetIcon(trayLight)
		tray.SetDarkModeIcon(trayDark)
	}
	if runtime.GOOS != "darwin" {
		// Windows and Linux: a left click restores the window; the menu stays
		// on the right click. On macOS the click opens the menu.
		tray.OnClick(s.showMainWindow)
	}

	menu := app.NewMenu()
	stop = buildTrayMenu(wailsMenu{menu}, trayActions{
		show: s.showMainWindow,
		quit: func() {
			s.quitting.Store(true)
			app.Quit()
		},
		// The click handler runs on the main thread; the pause writes to the
		// database, so it does not run there.
		async: func(f func()) { go f() },
	}, bot, s.log)
	tray.SetMenu(menu)

	return tray, stop
}
