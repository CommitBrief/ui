// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/update"
)

// fakeUpdater is a port.Updater a test scripts.
type fakeUpdater struct {
	mu         sync.Mutex
	delegation update.Delegation
	found      *update.Release
	checkErr   error
	installErr error
	status     update.Status
	checks     []update.Channel
	installs   int
	forgets    int
}

func (f *fakeUpdater) Current() string               { return "0.1.0" }
func (f *fakeUpdater) Delegation() update.Delegation { return f.delegation }
func (f *fakeUpdater) Status() update.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.status
	st.Current = "0.1.0"
	return st
}

func (f *fakeUpdater) Check(_ context.Context, ch update.Channel) (*update.Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks = append(f.checks, ch)
	if f.checkErr != nil {
		return nil, f.checkErr
	}
	if f.found != nil {
		r := *f.found
		f.status.State, f.status.Available = update.StateAvailable, &r
		return &r, nil
	}
	f.status.State, f.status.Available = update.StateUpToDate, nil
	return nil, nil
}

func (f *fakeUpdater) Forget() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgets++
	f.status.State, f.status.Available = update.StateIdle, nil
}

func (f *fakeUpdater) Install(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installs++
	return f.installErr
}

func (f *fakeUpdater) checkCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.checks)
}

type updatesRig struct {
	up    *fakeUpdater
	db    *memDB
	clock *fakeClock
	notes *notes
	svc   *Updates
}

func newUpdatesRig(t *testing.T) *updatesRig {
	t.Helper()
	r := &updatesRig{
		up:    &fakeUpdater{},
		db:    newMemDB(),
		clock: newFakeClock(time.UTC, 2026, time.November, 12, 9, 0, 0),
		notes: &notes{},
	}
	r.svc = NewUpdates(UpdatesDeps{Updater: r.up, Settings: r.db, Clock: r.clock, Notifier: r.notes, Logger: quietLogger})
	return r
}

func updateCode(err error) update.Code {
	var ue *update.Error
	if errors.As(err, &ue) {
		return ue.Code
	}
	return ""
}

func TestUpdatesDefaultsAndPreferences(t *testing.T) {
	r := newUpdatesRig(t)
	ctx := context.Background()

	v, err := r.svc.View(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Channel != "stable" || !v.AutoCheck || v.LastCheck != nil || v.Status.Current != "0.1.0" {
		t.Errorf("defaults = %+v", v)
	}

	v, err = r.svc.SetPreferences(ctx, "prerelease", false)
	if err != nil || v.Channel != "prerelease" || v.AutoCheck {
		t.Fatalf("SetPreferences = %+v, %v", v, err)
	}
	// It is stored, not just returned.
	if v2, _ := r.svc.View(ctx); v2.Channel != "prerelease" || v2.AutoCheck {
		t.Errorf("not persisted: %+v", v2)
	}

	for _, bad := range []string{"", "nightly", "STABLE", "stable "} {
		_, err := r.svc.SetPreferences(ctx, bad, true)
		var ue *update.Error
		if !errors.As(err, &ue) || ue.Code != update.CodeInvalid || ue.Field != "channel" {
			t.Errorf("SetPreferences(%q) = %v, want update_invalid on channel", bad, err)
		}
	}
	// A rejected input changes nothing.
	if v3, _ := r.svc.View(ctx); v3.Channel != "prerelease" || v3.AutoCheck {
		t.Errorf("a rejected input changed the preferences: %+v", v3)
	}

	// A hand-edited database falls back to the defaults.
	r.db.settings[update.KeyChannel] = "garbage"
	r.db.settings[update.KeyAutoCheck] = "maybe"
	r.db.settings[update.KeyLastCheck] = "yesterday"
	if v4, _ := r.svc.View(ctx); v4.Channel != "stable" || !v4.AutoCheck || v4.LastCheck != nil {
		t.Errorf("damaged rows: %+v", v4)
	}
}

func TestUpdatesManualCheckUsesTheStoredChannelAndRemembersIt(t *testing.T) {
	r := newUpdatesRig(t)
	ctx := context.Background()
	r.up.found = &update.Release{Version: "0.2.0"}
	if _, err := r.svc.SetPreferences(ctx, "prerelease", true); err != nil {
		t.Fatal(err)
	}

	v, err := r.svc.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.up.checks) != 1 || r.up.checks[0] != update.ChannelPrerelease {
		t.Errorf("channels checked = %v", r.up.checks)
	}
	if v.Status.State != update.StateAvailable || v.Status.Available == nil || v.Status.Available.Version != "0.2.0" {
		t.Errorf("view = %+v", v)
	}
	if v.LastCheck == nil || !v.LastCheck.Equal(r.clock.Now()) {
		t.Errorf("last check = %v", v.LastCheck)
	}
	// A manual check never raises a notification: the user is looking at it.
	if n := len(r.notes.kinds("update_available")); n != 0 {
		t.Errorf("%d notifications for a manual check", n)
	}
}

func TestUpdatesFailedCheckIsNotRemembered(t *testing.T) {
	r := newUpdatesRig(t)
	r.up.checkErr = update.New(update.CodeSignatureInvalid, "")
	_, err := r.svc.Check(context.Background())
	if updateCode(err) != update.CodeSignatureInvalid {
		t.Fatalf("Check = %v", err)
	}
	if _, ok := r.db.settings[update.KeyLastCheck]; ok {
		t.Error("a failed check was remembered as done")
	}
}

func TestUpdatesInstallGoesToTheUpdater(t *testing.T) {
	r := newUpdatesRig(t)
	if err := r.svc.Install(context.Background()); err != nil || r.up.installs != 1 {
		t.Errorf("Install = %v, installs = %d", err, r.up.installs)
	}
	r.up.installErr = update.New(update.CodeInstallFailed, "")
	if err := r.svc.Install(context.Background()); updateCode(err) != update.CodeInstallFailed {
		t.Errorf("Install = %v", err)
	}
}

func TestUpdatesPeriodicCheckRunsOncePerDay(t *testing.T) {
	r := newUpdatesRig(t)
	ctx := context.Background()

	r.svc.Tick(ctx) // never checked: due
	if r.up.checkCount() != 1 {
		t.Fatalf("checks = %d after the first tick", r.up.checkCount())
	}
	r.clock.Add(time.Hour)
	r.svc.Tick(ctx)
	r.clock.Add(22 * time.Hour)
	r.svc.Tick(ctx) // 23h since the check
	if r.up.checkCount() != 1 {
		t.Errorf("checks = %d before a day passed", r.up.checkCount())
	}
	r.clock.Add(time.Hour + time.Minute)
	r.svc.Tick(ctx) // 24h01
	if r.up.checkCount() != 2 {
		t.Errorf("checks = %d after a day", r.up.checkCount())
	}

	// The clock went back: the remembered time is in the future, so a check is due.
	r.clock.Add(-72 * time.Hour)
	r.svc.Tick(ctx)
	if r.up.checkCount() != 3 {
		t.Errorf("checks = %d after the clock moved back", r.up.checkCount())
	}
}

func TestUpdatesPeriodicCheckCanBeSwitchedOff(t *testing.T) {
	r := newUpdatesRig(t)
	ctx := context.Background()
	if _, err := r.svc.SetPreferences(ctx, "stable", false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		r.svc.Tick(ctx)
		r.clock.Add(48 * time.Hour)
	}
	if r.up.checkCount() != 0 {
		t.Errorf("%d checks although the daily check is off", r.up.checkCount())
	}
	// A manual check still works.
	if _, err := r.svc.Check(ctx); err != nil || r.up.checkCount() != 1 {
		t.Errorf("manual check = %v, count %d", err, r.up.checkCount())
	}
}

func TestUpdatesPeriodicCheckSkipsDelegatedInstallations(t *testing.T) {
	r := newUpdatesRig(t)
	r.up.delegation = update.DelegationPackageManager
	r.svc.Tick(context.Background())
	if r.up.checkCount() != 0 {
		t.Error("a delegated installation checked for updates")
	}
}

func TestUpdatesPeriodicCheckAnnouncesAVersionOnce(t *testing.T) {
	r := newUpdatesRig(t)
	ctx := context.Background()
	r.up.found = &update.Release{Version: "0.2.0"}

	r.svc.Tick(ctx)
	got := r.notes.kinds("update_available")
	if len(got) != 1 || got[0].Version != "0.2.0" {
		t.Fatalf("notifications = %+v", got)
	}
	// A day later the same version is still on offer: no second notice, not even
	// from a new process (the announced version is stored).
	r.clock.Add(25 * time.Hour)
	r.svc.Tick(ctx)
	fresh := NewUpdates(UpdatesDeps{Updater: r.up, Settings: r.db, Clock: r.clock, Notifier: r.notes, Logger: quietLogger})
	r.clock.Add(25 * time.Hour)
	fresh.Tick(ctx)
	if n := len(r.notes.kinds("update_available")); n != 1 {
		t.Errorf("%d notifications for one version", n)
	}
	// A newer version is announced.
	r.up.found = &update.Release{Version: "0.3.0"}
	r.clock.Add(25 * time.Hour)
	r.svc.Tick(ctx)
	if n := len(r.notes.kinds("update_available")); n != 2 {
		t.Errorf("%d notifications after a newer version", n)
	}
}

func TestUpdatesPeriodicCheckFailureBacksOff(t *testing.T) {
	r := newUpdatesRig(t)
	ctx := context.Background()
	r.up.checkErr = update.New(update.CodeCheckFailed, "")

	// Not once an hour (every tick) but after a growing pause: 2h, 4h, 8h, 16h,
	// then the daily interval.
	pauses := []time.Duration{2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour}
	r.svc.Tick(ctx)
	want := 1
	if r.up.checkCount() != want {
		t.Fatalf("checks = %d, want %d", r.up.checkCount(), want)
	}
	for i, pause := range pauses {
		// Every tick inside the pause is a no-op ...
		for e := time.Hour; e < pause; e += time.Hour {
			r.clock.Add(time.Hour)
			r.svc.Tick(ctx)
			if r.up.checkCount() != want {
				t.Fatalf("failure %d: checked again after %v of a %v pause", i+1, e, pause)
			}
		}
		// ... and the tick that ends it tries again.
		r.clock.Add(time.Hour)
		r.svc.Tick(ctx)
		want++
		if r.up.checkCount() != want {
			t.Fatalf("failure %d: no retry after the %v pause (checks = %d, want %d)", i+1, pause, r.up.checkCount(), want)
		}
	}
	if _, ok := r.db.settings[update.KeyLastCheck]; ok {
		t.Error("a failed look was remembered")
	}
	if len(r.notes.kinds("update_available")) != 0 {
		t.Error("a failed look raised a notification")
	}

	// A look that succeeds resets the pause: the next failure waits 2h again.
	r.up.checkErr = nil
	r.clock.Add(24 * time.Hour)
	r.svc.Tick(ctx)
	r.up.checkErr = update.New(update.CodeCheckFailed, "")
	r.clock.Add(24 * time.Hour)
	r.svc.Tick(ctx)
	n := r.up.checkCount()
	r.clock.Add(time.Hour)
	r.svc.Tick(ctx)
	if r.up.checkCount() != n {
		t.Error("a failure after a success did not start the backoff over")
	}
	r.clock.Add(time.Hour)
	r.svc.Tick(ctx)
	if r.up.checkCount() != n+1 {
		t.Errorf("checks = %d, want %d after the first pause", r.up.checkCount(), n+1)
	}
}

func TestUpdatesBusyAndCanceledAreNotFailedLooks(t *testing.T) {
	r := newUpdatesRig(t)
	ctx := context.Background()
	for _, code := range []update.Code{update.CodeBusy, update.CodeCanceled} {
		r.up.checkErr = update.New(code, "")
		before := r.up.checkCount()
		r.svc.Tick(ctx)
		r.clock.Add(time.Hour)
		r.svc.Tick(ctx)
		if got := r.up.checkCount() - before; got != 2 {
			t.Errorf("%s: %d checks in two ticks, want 2 (no backoff)", code, got)
		}
	}
}

func TestUpdatesManualCheckEndsABackoff(t *testing.T) {
	r := newUpdatesRig(t)
	ctx := context.Background()
	r.up.checkErr = update.New(update.CodeCheckFailed, "")
	r.svc.Tick(ctx) // fails: backing off for 2h
	r.up.checkErr = nil
	if _, err := r.svc.Check(ctx); err != nil {
		t.Fatal(err)
	}
	r.clock.Add(time.Hour)
	r.svc.Tick(ctx)
	// Remembered as checked just now, so nothing is due: no extra check, and no
	// stale backoff either.
	if r.up.checkCount() != 2 {
		t.Errorf("checks = %d, want 2", r.up.checkCount())
	}
}

func TestUpdatesChannelChangeDropsThePendingUpdate(t *testing.T) {
	r := newUpdatesRig(t)
	ctx := context.Background()
	r.up.found = &update.Release{Version: "0.2.0-rc.1", Prerelease: true}
	if _, err := r.svc.SetPreferences(ctx, "prerelease", true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.svc.View(ctx); v.Status.Available == nil {
		t.Fatal("the pre-release was not found")
	}

	// Saving the same channel again keeps what was found.
	before := r.up.forgets
	if v, _ := r.svc.SetPreferences(ctx, "prerelease", false); v.Status.Available == nil || r.up.forgets != before {
		t.Errorf("an unchanged channel dropped the update: %+v", v.Status)
	}

	v, err := r.svc.SetPreferences(ctx, "stable", false)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status.Available != nil || v.Status.State != update.StateIdle || r.up.forgets != before+1 {
		t.Errorf("view after the channel change = %+v (forgets %d)", v.Status, r.up.forgets)
	}
}

// Even when the stored channel changed behind SetPreferences' back, Install
// refuses a release the channel does not accept.
func TestUpdatesInstallRefusesAReleaseOfAnotherChannel(t *testing.T) {
	r := newUpdatesRig(t)
	ctx := context.Background()
	r.up.status.State = update.StateAvailable
	r.up.status.Available = &update.Release{Version: "0.2.0-rc.1", Prerelease: true}
	// The stored channel is the default, stable.
	err := r.svc.Install(ctx)
	if updateCode(err) != update.CodeNothingToInstall || r.up.installs != 0 || r.up.forgets != 1 {
		t.Errorf("Install = %v, installs = %d, forgets = %d", err, r.up.installs, r.up.forgets)
	}

	// A release the channel accepts installs.
	r.up.status.Available = &update.Release{Version: "0.2.0"}
	if err := r.svc.Install(ctx); err != nil || r.up.installs != 1 {
		t.Errorf("Install = %v, installs = %d", err, r.up.installs)
	}
}

func TestUpdatesLoopStartsAfterTheDelayAndStops(t *testing.T) {
	r := newUpdatesRig(t)
	svc := NewUpdates(UpdatesDeps{Updater: r.up, Settings: r.db, Clock: r.clock, Notifier: r.notes, Logger: quietLogger,
		Tick: 20 * time.Millisecond, StartDelay: 20 * time.Millisecond})
	svc.Start(context.Background())
	svc.Start(context.Background()) // a second Start is a no-op
	deadline := time.Now().Add(5 * time.Second)
	for r.up.checkCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.up.checkCount() == 0 {
		t.Fatal("the loop never checked")
	}
	svc.Stop()
	svc.Stop() // idempotent
	n := r.up.checkCount()
	r.clock.Add(48 * time.Hour)
	time.Sleep(100 * time.Millisecond)
	if r.up.checkCount() != n {
		t.Error("the loop kept running after Stop")
	}
}
