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

	ui "github.com/CommitBrief/ui"
	"github.com/CommitBrief/ui/internal/adapter/clicmd"
	"github.com/CommitBrief/ui/internal/adapter/desktop"
	"github.com/CommitBrief/ui/internal/adapter/github/gh"
	"github.com/CommitBrief/ui/internal/adapter/gitinfo"
	"github.com/CommitBrief/ui/internal/adapter/store/sqlite"
	"github.com/CommitBrief/ui/internal/app"
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

	return desktop.State{
		Repos:    app.NewRepos(store.Repos(), store, gitinfo.New()),
		Health:   app.NewHealth(store),
		Settings: settingsSvc,
		Close:    func() error { return errors.Join(store.Close(), lock.Release()) },
	}, nil
}
