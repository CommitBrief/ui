// SPDX-License-Identifier: GPL-3.0-or-later

// Command commitbrief-ui is the composition root of CommitBrief UI. It wires
// the adapters to the application core and starts the desktop shell.
package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	ui "github.com/CommitBrief/ui"
	"github.com/CommitBrief/ui/internal/adapter/autostart"
	"github.com/CommitBrief/ui/internal/adapter/clicmd"
	"github.com/CommitBrief/ui/internal/adapter/desktop"
	"github.com/CommitBrief/ui/internal/adapter/github/gh"
	"github.com/CommitBrief/ui/internal/adapter/gitinfo"
	"github.com/CommitBrief/ui/internal/adapter/notify"
	"github.com/CommitBrief/ui/internal/adapter/store/sqlite"
	"github.com/CommitBrief/ui/internal/adapter/updater"
	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/app/jobkinds"
	"github.com/CommitBrief/ui/internal/domain/settings"
	"github.com/CommitBrief/ui/internal/domain/update"
	"github.com/CommitBrief/ui/internal/platform"
	"github.com/CommitBrief/ui/internal/port"
)

// version is the release version of this build, stamped at build time
// (-ldflags "-X main.version=1.2.3", see the release workflow). A build without
// the stamp reports update.DevVersion and never updates itself.
var version = update.DevVersion

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
	// One gh client: Settings asks it who is logged in, the bot asks it for the
	// pull requests.
	ghClient := gh.New(gh.Config{})
	settingsSvc = app.NewSettings(app.SettingsDeps{
		Engine: engine,
		Tx:     store,
		Store:  store.Settings(),
		Repos:  store.Repos(),
		GitHub: ghClient,
		Env:    platform.OSEnv{},
		Opener: clicmd.NewOpener(),
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

	clock := platform.SystemClock{}
	budget := app.NewBudget(app.BudgetDeps{Settings: store.Settings(), Costs: store.Costs(), Clock: clock})
	runs, err := newRuns(ctx, store, engine, ghClient, budget, clock, dirs, logger)
	if err != nil {
		_ = store.Close()
		_ = lock.Release()
		return desktop.State{}, err
	}

	// The scheduler lives as long as the process (UI-0002): it starts after the
	// run service, has its first look at once (the catch-up after a launch) and
	// stops before the reviews it started are stopped.
	// Notifications are queued and shown from the notifier's own goroutine: the
	// first one can sit behind an operating system permission dialog for minutes.
	notifier := notify.New(notify.NewLazy(desktop.NewNotificationSender().Open), logger)
	scheduler := app.NewScheduler(app.SchedulerDeps{
		Jobs:     store.Jobs(),
		Runs:     store.Runs(),
		Settings: store.Settings(),
		Trigger:  runs,
		Budget:   budget,
		Notifier: notifier,
		Clock:    clock,
		Logger:   logger,
	})
	scheduler.Start(context.Background())

	// The self-update checks once a day (when the user leaves it on) and starts
	// after the launch has settled; like the scheduler it stops before the state
	// closes.
	updates := newUpdates(store, notifier, clock, dirs, logger)
	updates.Start(context.Background())

	return desktop.State{
		Repos:    app.NewRepos(store.Repos(), store, gitinfo.New()),
		Health:   app.NewHealth(store),
		Settings: settingsSvc,
		Jobs:     runs,
		Updates:  updates,
		Schedules: app.NewSchedules(app.SchedulesDeps{
			Jobs:      store.Jobs(),
			Runner:    runs,
			Budget:    budget,
			AutoStart: autostart.New(autostart.Config{}),
			Clock:     clock,
			Logger:    logger,
			Scheduler: scheduler,
			Events:    desktop.NewEventEmitter(),
			Repos:     store.Repos(),
			Tracked:   store.Tracked(),
			Runs:      store.Runs(),
		}),
		Close: func() error {
			// Stop scheduling first, then the running reviews (their CLI
			// process trees die with their context), then let go of the
			// database.
			updates.Stop()
			scheduler.Stop()
			// A notification still waiting behind a permission dialog is let go
			// after a moment; quitting never waits for the dialog.
			flush, flushCancel := context.WithTimeout(context.Background(), notifierShutdownGrace)
			notifier.Close(flush)
			flushCancel()
			stop, cancel := context.WithTimeout(context.Background(), runsShutdownGrace)
			defer cancel()
			return errors.Join(runs.Close(stop), store.Close(), lock.Release())
		},
	}, nil
}

// newUpdates builds the self-update: the GitHub release feed, the Wails
// swap-and-restart backend and the use cases on top. Where the application cannot
// replace itself (a package-manager install, a development build, a read-only
// location) the updater is delegated: it checks nothing and installs nothing.
func newUpdates(store *sqlite.Store, notifier port.Notifier, clock platform.SystemClock, dirs platform.Dirs, logger *slog.Logger) *app.Updates {
	info := platform.InstallInfo{Delegation: update.DelegationUnsupported}
	if exe, err := os.Executable(); err == nil {
		info = platform.DetectInstall(runtime.GOOS, exe, os.Getenv, platform.DirWritable)
	} else {
		logger.Warn("locating the running executable failed; self-update is off", "error", err)
	}
	if info.Delegation == update.DelegationNone && !platform.ShipsReleaseFor(runtime.GOOS, runtime.GOARCH) {
		// No release artefact is built for this system, so there is nothing to
		// update to.
		info = platform.InstallInfo{Delegation: update.DelegationUnsupported}
	}

	engine := updater.New(updater.Config{
		Feed: updater.NewSource(updater.SourceConfig{
			GOOS:    runtime.GOOS,
			GOARCH:  runtime.GOARCH,
			Suffix:  platform.ArtifactSuffix(info.Kind),
			Version: version,
		}),
		Backend:    desktop.NewUpdateBackend(version, logger),
		Current:    version,
		Delegation: info.Delegation,
		StagingDir: filepath.Join(dirs.Data, updatesDirName),
		Observer: func(st update.Status) {
			desktop.NewEventEmitter().Emit(app.EventUpdateStatus, st)
		},
		Logger: logger,
	})
	if err := engine.CleanStaging(); err != nil {
		logger.Warn("removing an interrupted update download failed", "error", err)
	}
	return app.NewUpdates(app.UpdatesDeps{
		Updater:  engine,
		Settings: store.Settings(),
		Clock:    clock,
		Notifier: notifier,
		Logger:   logger,
	})
}

// updatesDirName is the staging directory of update downloads inside the data
// directory.
const updatesDirName = "updates"

// runsShutdownGrace is how long quitting waits for running reviews to stop. The
// CLI adapter's own SIGTERM-to-SIGKILL window is 5 seconds.
const runsShutdownGrace = 15 * time.Second

// notifierShutdownGrace is how long quitting lets queued notifications reach the
// screen before it gives up on them.
const notifierShutdownGrace = time.Second

// newRuns builds the run service: the job kinds on top of the CLI adapter (and,
// for the bot, gh), the stores, the Wails event emitter and the log directory.
// Start marks what an earlier process left unfinished as canceled and applies
// the log retention policy once.
func newRuns(ctx context.Context, store *sqlite.Store, engine *clicmd.Engine, ghClient *gh.Client, budget *app.Budget, clock platform.SystemClock, dirs platform.Dirs, logger *slog.Logger) (*app.Runs, error) {
	kinds, err := app.NewKindRegistry(
		jobkinds.NewReviewLocal(store.Repos(), engine),
		jobkinds.NewReviewPR(store.Repos(), engine),
		jobkinds.NewBotReviewRequested(jobkinds.BotDeps{
			Repos:   store.Repos(),
			Tracked: store.Tracked(),
			Jobs:    store.Jobs(),
			Source:  ghClient,
			Engine:  engine,
			Budget:  budget,
			Clock:   clock,
			Logger:  logger,
		}),
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
