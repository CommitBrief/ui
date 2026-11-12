// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/CommitBrief/ui/internal/domain/update"
	"github.com/CommitBrief/ui/internal/port"
)

// EventUpdateStatus is the event the updater's status changes are emitted as
// (through port.EventEmitter); its payload is an update.Status.
const EventUpdateStatus = "updates:status"

// Timings of the periodic check.
const (
	// DefaultUpdatesTick is how often the loop looks at the clock. The check
	// itself runs once a day (update.CheckInterval); looking hourly makes the
	// loop notice a due check after a sleep or a changed setting.
	DefaultUpdatesTick = time.Hour
	// DefaultUpdatesStartDelay keeps the first look away from the launch.
	DefaultUpdatesStartDelay = 30 * time.Second
	// KeyUpdateNotified remembers the version the user was last told about, so
	// a restart does not announce it again.
	KeyUpdateNotified = "updates.notified"
)

// UpdatesDeps are what the update use cases need.
type UpdatesDeps struct {
	Updater  port.Updater
	Settings port.SettingsStore
	Clock    port.Clock
	// Notifier tells the user that an update was found by the periodic check.
	Notifier port.Notifier
	Logger   *slog.Logger
	// Tick and StartDelay default to DefaultUpdatesTick and
	// DefaultUpdatesStartDelay.
	Tick       time.Duration
	StartDelay time.Duration
}

// Updates is the use-case layer of the self-update: the preferences (follow
// stable or pre-releases, check daily or not), a manual check, the install the
// user confirms, and the periodic check. Everything else (verification,
// download, swap) is the port.Updater's.
type Updates struct {
	d UpdatesDeps

	mu sync.Mutex // serializes Tick against itself

	// A failed periodic check is retried with a growing pause (retryBackoff), not
	// at every tick. In memory only: a restart starts over, and the first look
	// after a launch is due anyway when the last success is a day old.
	retry    sync.Mutex
	failures int
	retryAt  time.Time

	loop   sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewUpdates wires the use cases.
func NewUpdates(d UpdatesDeps) *Updates {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Tick <= 0 {
		d.Tick = DefaultUpdatesTick
	}
	if d.StartDelay <= 0 {
		d.StartDelay = DefaultUpdatesStartDelay
	}
	return &Updates{d: d}
}

// UpdatesView is what the Updates screen shows: the updater's state and the
// user's preferences.
type UpdatesView struct {
	Status update.Status `json:"status"`
	// Channel is "stable" or "prerelease".
	Channel string `json:"channel"`
	// AutoCheck is whether the application looks for updates once a day.
	AutoCheck bool `json:"autoCheck"`
	// LastCheck is when the last successful check finished; null before the
	// first one.
	LastCheck *time.Time `json:"lastCheck"`
}

// prefs is the stored preferences with their defaults applied.
type updatePrefs struct {
	channel   update.Channel
	autoCheck bool
	lastCheck time.Time
	notified  string
}

func readUpdatePrefs(rows map[string]string) updatePrefs {
	p := updatePrefs{channel: update.DefaultChannel, autoCheck: update.DefaultAutoCheck, notified: rows[KeyUpdateNotified]}
	if c, ok := update.ParseChannel(rows[update.KeyChannel]); ok {
		p.channel = c
	}
	if b, err := strconv.ParseBool(rows[update.KeyAutoCheck]); err == nil {
		p.autoCheck = b
	}
	if t, err := time.Parse(time.RFC3339, rows[update.KeyLastCheck]); err == nil {
		p.lastCheck = t
	}
	return p
}

func (u *Updates) prefs(ctx context.Context) (updatePrefs, error) {
	rows, err := u.d.Settings.All(ctx)
	if err != nil {
		return updatePrefs{}, update.Wrap(update.CodeInternal, err)
	}
	return readUpdatePrefs(rows), nil
}

func (u *Updates) view(ctx context.Context) (UpdatesView, error) {
	p, err := u.prefs(ctx)
	if err != nil {
		return UpdatesView{}, err
	}
	v := UpdatesView{Status: u.d.Updater.Status(), Channel: string(p.channel), AutoCheck: p.autoCheck}
	if !p.lastCheck.IsZero() {
		t := p.lastCheck
		v.LastCheck = &t
	}
	return v, nil
}

// View returns the state and the preferences.
func (u *Updates) View(ctx context.Context) (UpdatesView, error) { return u.view(ctx) }

// SetPreferences stores the channel and the daily-check switch and returns the
// new view. An unknown channel is update.CodeInvalid (field "channel"). Changing
// the channel drops the pending update: one found on the old channel (a
// pre-release, say) must not be installable after the user chose another; the
// next check looks again.
func (u *Updates) SetPreferences(ctx context.Context, channel string, autoCheck bool) (UpdatesView, error) {
	ch, ok := update.ParseChannel(channel)
	if !ok {
		return UpdatesView{}, update.New(update.CodeInvalid, "channel")
	}
	before, err := u.prefs(ctx)
	if err != nil {
		return UpdatesView{}, err
	}
	if err := u.d.Settings.Set(ctx, update.KeyChannel, string(ch)); err != nil {
		return UpdatesView{}, update.Wrap(update.CodeInternal, err)
	}
	if err := u.d.Settings.Set(ctx, update.KeyAutoCheck, strconv.FormatBool(autoCheck)); err != nil {
		return UpdatesView{}, update.Wrap(update.CodeInternal, err)
	}
	if ch != before.channel {
		u.d.Updater.Forget()
		u.resetRetry()
	}
	return u.view(ctx)
}

// Check looks for an update on the stored channel, now. A successful look
// (whether it found something or not) is remembered as the last check.
func (u *Updates) Check(ctx context.Context) (UpdatesView, error) {
	p, err := u.prefs(ctx)
	if err != nil {
		return UpdatesView{}, err
	}
	if _, err := u.d.Updater.Check(ctx, p.channel); err != nil {
		return UpdatesView{}, err
	}
	u.rememberCheck(ctx)
	u.resetRetry()
	return u.view(ctx)
}

// Install installs the update the last check found. The user has confirmed it
// by asking; the application restarts into the new version when it succeeds.
// Install refuses an update the stored channel does not accept (a second line of
// defence behind SetPreferences, which drops it when the channel changes).
func (u *Updates) Install(ctx context.Context) error {
	p, err := u.prefs(ctx)
	if err != nil {
		return err
	}
	if rel := u.d.Updater.Status().Available; rel != nil {
		if v, err := update.ParseVersion(rel.Version); err != nil || !p.channel.Accepts(v) {
			u.d.Updater.Forget()
			return update.New(update.CodeNothingToInstall, "")
		}
	}
	return u.d.Updater.Install(ctx)
}

// retryBackoff is the pause before the periodic check tries again after the
// n-th failure in a row: 2h, 4h, 8h, 16h, then the daily interval.
func retryBackoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	if failures >= 5 {
		return update.CheckInterval
	}
	return time.Hour << failures
}

func (u *Updates) resetRetry() {
	u.retry.Lock()
	u.failures, u.retryAt = 0, time.Time{}
	u.retry.Unlock()
}

// retryPending reports whether a failed periodic check is still backing off.
func (u *Updates) retryPending(now time.Time) bool {
	u.retry.Lock()
	defer u.retry.Unlock()
	return !u.retryAt.IsZero() && now.Before(u.retryAt) && u.retryAt.Sub(now) <= update.CheckInterval
}

func (u *Updates) noteFailure(now time.Time) {
	u.retry.Lock()
	u.failures++
	u.retryAt = now.Add(retryBackoff(u.failures))
	u.retry.Unlock()
}

func (u *Updates) rememberCheck(ctx context.Context) {
	now := u.d.Clock.Now().UTC().Format(time.RFC3339)
	if err := u.d.Settings.Set(ctx, update.KeyLastCheck, now); err != nil {
		u.d.Logger.Warn("updates: remembering the last check failed", "error", err)
	}
}

// Start runs the periodic check in the background until Stop. The first look
// comes after StartDelay (so it never competes with the launch), the next ones
// every Tick; Tick decides whether a check is actually due.
func (u *Updates) Start(ctx context.Context) {
	u.loop.Lock()
	defer u.loop.Unlock()
	if u.cancel != nil {
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	u.cancel = cancel
	u.done = make(chan struct{})
	go func(done chan struct{}) {
		defer close(done)
		wait := time.NewTimer(u.d.StartDelay)
		defer wait.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-wait.C:
				u.Tick(loopCtx)
				wait.Reset(u.d.Tick)
			}
		}
	}(u.done)
}

// Stop ends the loop and waits for the look in progress to return. Safe to call
// more than once.
func (u *Updates) Stop() {
	u.loop.Lock()
	cancel, done := u.cancel, u.done
	u.cancel, u.done = nil, nil
	u.loop.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// Tick is one look at the clock: when the daily check is switched on, this
// installation updates itself and a check is due, it checks and tells the user
// once about a new version. Exported so tests drive it with a fake clock.
func (u *Updates) Tick(ctx context.Context) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if ctx.Err() != nil || u.d.Updater.Delegation() != update.DelegationNone {
		return
	}
	p, err := u.prefs(ctx)
	if err != nil {
		u.d.Logger.Warn("updates: reading the preferences failed", "error", err)
		return
	}
	now := u.d.Clock.Now()
	// A remembered time in the future (the clock moved back) counts as due.
	if !p.autoCheck || (!p.lastCheck.IsZero() && !p.lastCheck.After(now) && now.Sub(p.lastCheck) < update.CheckInterval) {
		return
	}

	if u.retryPending(now) {
		return
	}

	rel, err := u.d.Updater.Check(ctx, p.channel)
	if err != nil {
		// Busy means the user is checking or installing right now, and a canceled
		// call is the shutdown: neither counts as a failed look. Anything else is
		// logged by the updater and retried after a growing pause, not every tick.
		var ue *update.Error
		if !errors.As(err, &ue) || (ue.Code != update.CodeBusy && ue.Code != update.CodeCanceled) {
			u.d.Logger.Info("updates: the periodic check did not succeed", "error", err)
			u.noteFailure(now)
		}
		return
	}
	u.resetRetry()
	u.rememberCheck(ctx)
	if rel == nil || rel.Version == p.notified {
		return
	}
	if u.d.Notifier != nil {
		u.d.Notifier.Notify(ctx, port.Notification{Kind: port.NotifyUpdateAvailable, Version: rel.Version})
	}
	if err := u.d.Settings.Set(ctx, KeyUpdateNotified, rel.Version); err != nil {
		u.d.Logger.Warn("updates: remembering the announced version failed", "error", err)
	}
}
