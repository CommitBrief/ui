// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"errors"
	"io/fs"
	"log/slog"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/CommitBrief/ui/internal/app"
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

// State is the opened persistent state: the use-case services bound to the
// webview and the function that closes the state again.
type State struct {
	Repos    *app.Repos
	Health   *app.Health
	Settings *app.Settings
	Jobs     *app.Runs
	// Schedules serves the scheduled-bot screen. Its scheduler is started and
	// stopped by the composition root, not by the shell.
	Schedules *app.Schedules
	// Updates serves the Updates screen. Its periodic check is started and
	// stopped by the composition root, not by the shell.
	Updates *app.Updates
	// Close releases the database and the data-directory lock.
	Close func() error
}

// Options configures the desktop shell.
type Options struct {
	// Assets is the embedded frontend build (the directory that contains
	// index.html, or a tree that contains frontend/dist).
	Assets fs.FS
	// OpenState opens the persistent state and returns the services the
	// frontend may call. Required.
	//
	// Run calls it only after the single-instance guard has been passed: a
	// second launch exits (after waking the first instance) before OpenState
	// runs, so it never migrates, restores or otherwise touches the database
	// of the instance that is already running. Do not move this call above
	// application.New (TestRunOpensStateAfterSingleInstanceGuard).
	OpenState func() (State, error)
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
	if opts.OpenState == nil {
		return errors.New("desktop: Options.OpenState is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &shell{log: logger}

	app := application.New(application.Options{
		Name: appName,
		// Wails logs every bound call with its JSON arguments at Debug level
		// ("Binding call complete", args and result). SetConfig's arguments
		// carry an API key, so the system logger stays at Info or above;
		// TestWailsLogLevelStaysAboveDebug pins it.
		LogLevel:    slog.LevelInfo,
		Description: "Desktop companion for the CommitBrief CLI",
		Assets: application.AssetOptions{
			Handler:    application.AssetFileServerFS(opts.Assets),
			Middleware: securityMiddleware(responseCSP(logger), logger),
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

	// application.New above holds the single-instance guard: a second launch
	// has already notified the first instance and exited. Only now may the
	// database be opened.
	state, err := opts.OpenState()
	if err != nil {
		return err
	}
	defer func() {
		if err := state.Close(); err != nil {
			logger.Error("closing the persistent state", "error", err)
		}
	}()
	// Only registered services are reachable from the webview. Add a service
	// here, never widen the runtime allowlist (security.go).
	app.RegisterService(application.NewService(NewReposService(state.Repos, logger)))
	app.RegisterService(application.NewService(NewHealthService(state.Health, logger)))
	app.RegisterService(application.NewService(NewSettingsService(state.Settings, logger)))
	app.RegisterService(application.NewService(NewJobsService(state.Jobs, logger)))
	app.RegisterService(application.NewService(NewSchedulesService(state.Schedules, logger)))
	app.RegisterService(application.NewService(NewUpdatesService(state.Updates, logger)))
	app.RegisterService(application.NewService(NewDialogsService(s.pickFolder(app), s.saveFile(app), state.Repos, logger)))

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

	var bot BotControl // stays a nil interface when there are no schedules
	if state.Schedules != nil {
		bot = state.Schedules
	}
	_, stopTray := s.newTray(app, bot)
	defer stopTray()

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
