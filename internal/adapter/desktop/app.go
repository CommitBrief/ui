// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"errors"
	"io/fs"
	"log/slog"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	// appName is the product name shown in the window title and the tray.
	appName = "CommitBrief UI"
	// appID is the stable identifier used for single-instance detection.
	appID = "com.commitbrief.ui"

	mainWindowName   = "main"
	mainWindowWidth  = 1100
	mainWindowHeight = 720
)

// Options configures the desktop shell.
type Options struct {
	// Assets is the embedded frontend build (the directory that contains
	// index.html, or a tree that contains frontend/dist).
	Assets fs.FS
	// Logger receives shell diagnostics. Defaults to slog.Default().
	Logger *slog.Logger
}

// shell is the shared state of the desktop shell: the main window and the
// quit flag. It is touched from Wails callbacks that may run on different
// goroutines, hence the atomics.
type shell struct {
	log      *slog.Logger
	window   atomic.Pointer[application.WebviewWindow]
	quitting atomic.Bool
}

// Run builds the window, tray and single-instance guard and blocks until the
// application quits. Closing the window only hides it (UI-0002): the process
// keeps running in the tray until "Quit" is chosen.
func Run(opts Options) error {
	if opts.Assets == nil {
		return errors.New("desktop: Options.Assets is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &shell{log: logger}

	app := application.New(application.Options{
		Name:        appName,
		Description: "Desktop companion for the CommitBrief CLI",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(opts.Assets),
		},
		Mac: application.MacOptions{
			// The scheduler must outlive the window (UI-0002).
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: appID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				s.log.Info("second instance launched; focusing the main window")
				s.showMainWindow()
			},
		},
		ShouldQuit: func() bool {
			s.quitting.Store(true)
			return true
		},
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   mainWindowName,
		Title:  appName,
		Width:  mainWindowWidth,
		Height: mainWindowHeight,
		URL:    "/",
	})
	// Hide instead of destroying the window so the tray keeps a live target.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if s.quitting.Load() {
			return
		}
		window.Hide()
		e.Cancel()
	})
	s.window.Store(window)

	s.newTray(app)

	return app.Run()
}

// showMainWindow brings the main window to the front, restoring it from the
// hidden or minimised state first.
func (s *shell) showMainWindow() {
	w := s.window.Load()
	if w == nil {
		return
	}
	w.Show()
	if w.IsMinimised() {
		w.UnMinimise()
	}
	w.Focus()
}
