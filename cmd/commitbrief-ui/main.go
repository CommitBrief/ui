// SPDX-License-Identifier: GPL-3.0-or-later

// Command commitbrief-ui is the composition root of CommitBrief UI. It wires
// the adapters to the application core and starts the desktop shell.
package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"path/filepath"
	"runtime"
	"time"

	ui "github.com/CommitBrief/ui"
	"github.com/CommitBrief/ui/internal/adapter/clicmd"
	"github.com/CommitBrief/ui/internal/adapter/desktop"
	"github.com/CommitBrief/ui/internal/adapter/github/gh"
	"github.com/CommitBrief/ui/internal/adapter/gitinfo"
	"github.com/CommitBrief/ui/internal/adapter/store/sqlite"
	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/app/jobkinds"
	"github.com/CommitBrief/ui/internal/domain/settings"
	"github.com/CommitBrief/ui/internal/platform"
)

const (
	// dbFileName is the UI database inside the OS config directory (UI-0006).
	dbFileName = "ui.db"
	// lockFileName guards the data directory: only its holder may open,
	// migrate or restore the database.
	lockFileName = "ui.lock"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	logger := slog.Default()

	dirs, err := platform.UserDirs(runtime.GOOS)
	if err != nil {
		return err
	}
	if err := dirs.Ensure(); err != nil {
		return err
	}

	// The database is opened by desktop.Run once the single-instance guard
	// has been passed (see desktop.Options.OpenState), never here.
	err = desktop.Run(desktop.Options{
		Assets: ui.Assets,
		OpenState: func() (desktop.State, error) {
			return openState(context.Background(), dirs, logger)
		},
		Logger: logger,
	})
	if errors.Is(err, platform.ErrLocked) {
		// The Wails guard normally catches a second launch first; this is
		// the backstop when the data directory is held by an instance it
		// did not see. Nothing was touched, so just stand down.
		logger.Info("another CommitBrief UI instance holds the data directory; exiting")
		return nil
	}
	return err
}

// openState takes the data-directory lock and only then opens the database
// (integrity check, migrations, recovery). The order is the point: whoever does
// not hold the lock must never reach sqlite.Open. State.Close closes the
// database and releases the lock.
func openState(ctx context.Context, dirs platform.Dirs, logger *slog.Logger) (desktop.State, error) {
	lock, err := platform.AcquireFileLock(filepath.Join(dirs.Data, lockFileName))
	if err != nil {
		return desktop.State{}, err
	}
	store, err := sqlite.Open(ctx, filepath.Join(dirs.Data, dbFileName), sqlite.WithLogger(logger))
	if err != nil {
		_ = lock.Release()
		return desktop.State{}, err
	}

	// The CLI adapter asks Settings for the user's CLI path override before
	// every call, and Settings asks the CLI adapter for config and providers:
	// the closure ties the knot.
	var settingsSvc *app.Settings
	engine := clicmd.New(clicmd.Config{
		DataDir:      dirs.Data,
		OverridePath: func() string { return settingsSvc.CLIPathOverride() },
	})
	settingsSvc = app.NewSettings(app.SettingsDeps{
		Engine: engine,
		Tx:     store,
		Store:  store.Settings(),
		Repos:  store.Repos(),
		GitHub: gh.New(gh.Config{}),
		Env:    platform.OSEnv{},
	})
	if err := settingsSvc.Load(ctx); err != nil {
		// Not fatal: the CLI is then discovered automatically.
		// The error's text is only its code; the cause is what says why.
		cause := err
		var se *settings.Error
		if errors.As(err, &se) && se.Err != nil {
			cause = se.Err
		}
		logger.Warn("reading the UI settings failed; using the defaults", "error", err, "cause", cause)
	}

	runs, err := newRuns(ctx, store, engine, dirs, logger)
	if err != nil {
		_ = store.Close()
		_ = lock.Release()
		return desktop.State{}, err
	}

	return desktop.State{
		Repos:    app.NewRepos(store.Repos(), store, gitinfo.New()),
		Health:   app.NewHealth(store),
		Settings: settingsSvc,
		Jobs:     runs,
		Close: func() error {
			// Stop the running reviews first (their CLI process trees die
			// with their context), then let go of the database.
			stop, cancel := context.WithTimeout(context.Background(), runsShutdownGrace)
			defer cancel()
			return errors.Join(runs.Close(stop), store.Close(), lock.Release())
		},
	}, nil
}

// runsShutdownGrace is how long quitting waits for running reviews to stop. The
// CLI adapter's own SIGTERM-to-SIGKILL window is 5 seconds.
const runsShutdownGrace = 15 * time.Second

// newRuns builds the manual-jobs service: the two job kinds on top of the CLI
// adapter, the stores, the Wails event emitter and the log directory. Start
// marks what an earlier process left unfinished as canceled and applies the log
// retention policy once.
func newRuns(ctx context.Context, store *sqlite.Store, engine *clicmd.Engine, dirs platform.Dirs, logger *slog.Logger) (*app.Runs, error) {
	kinds, err := app.NewKindRegistry(
		jobkinds.NewReviewLocal(store.Repos(), engine),
		jobkinds.NewReviewPR(store.Repos(), engine),
	)
	if err != nil {
		return nil, err
	}
	runs := app.NewRuns(app.RunsDeps{
		Kinds:    kinds,
		Jobs:     store.Jobs(),
		Runs:     store.Runs(),
		Tx:       store,
		Settings: store.Settings(),
		Events:   desktop.NewEventEmitter(),
		Logger:   logger,
		LogDir:   dirs.Log,
		Env:      platform.OSEnv{},
	})
	if err := runs.Start(ctx); err != nil {
		// Not fatal: unfinished rows from the last run stay as they are.
		logger.Warn("preparing the run service failed", "error", err)
	}
	return runs, nil
}
