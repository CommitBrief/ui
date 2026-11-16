// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/port"
)

// recordingMenu is a trayMenuBuilder that records what buildTrayMenu adds, in
// order.
type recordingMenu struct {
	items []*fakeItem
	seps  []int // positions (index into items) a separator was added before
}

func (m *recordingMenu) Add(label string) trayEntry {
	it := &fakeItem{label: label, enabled: true}
	m.items = append(m.items, it)
	return it
}
func (m *recordingMenu) AddSeparator() { m.seps = append(m.seps, len(m.items)) }

// menuSettings, menuJobs and menuRuns are the smallest stores the real scheduler
// and schedules use cases need for the pause and the status.
type menuSettings struct {
	port.SettingsStore
	mu sync.Mutex
	kv map[string]string
}

func (s *menuSettings) Get(_ context.Context, k string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.kv[k]
	return v, ok, nil
}

func (s *menuSettings) Set(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kv[k] = v
	return nil
}

func (s *menuSettings) Delete(_ context.Context, k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.kv, k)
	return nil
}

type menuJobs struct {
	port.JobStore
	recs []port.JobRecord
}

func (j *menuJobs) ListJobs(context.Context) ([]port.JobRecord, error) { return j.recs, nil }

type menuRuns struct {
	port.RunStore
	byJob map[int64]port.RunRecord
}

func (r *menuRuns) ListRuns(_ context.Context, jobID int64, _ int) ([]port.RunRecord, error) {
	if rec, ok := r.byJob[jobID]; ok {
		return []port.RunRecord{rec}, nil
	}
	return nil, nil
}

type menuClock struct{ now time.Time }

func (c menuClock) Now() time.Time { return c.now }

type menuEvents struct {
	mu  sync.Mutex
	got []app.SchedulerStatus
}

func (e *menuEvents) Emit(name string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if name == app.EventSchedulerStatus {
		e.got = append(e.got, payload.(app.SchedulerStatus))
	}
}

// The real wiring, end to end except for the window system: buildTrayMenu on a
// recording menu, the real Scheduler (pause stored in a settings fake) and the
// real Schedules use cases. A click on the menu items must pause and resume the
// scheduler and update the labels; a pause made elsewhere must reach the menu.
func TestTrayMenuClicksReachTheSchedulerAndLabelsFollow(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	settings := &menuSettings{kv: map[string]string{}}
	sched := app.NewScheduler(app.SchedulerDeps{Settings: settings, Clock: menuClock{testNow}, Logger: log})
	started := testNow.Add(-2 * time.Hour)
	jobs := &menuJobs{recs: []port.JobRecord{
		{ID: 1, Kind: string(job.KindBotReviewRequested), Name: "bot", Enabled: true},
		{ID: 2, Kind: string(job.KindBotReviewRequested), Name: "stuck", Enabled: true, AttentionCode: "gh_not_authenticated"},
	}}
	runs := &menuRuns{byJob: map[int64]port.RunRecord{1: {ID: 9, JobID: 1, Status: "succeeded", StartedAt: started}}}
	ev := &menuEvents{}
	schedules := app.NewSchedules(app.SchedulesDeps{
		Jobs: jobs, Runs: runs, Scheduler: sched, Events: ev, Clock: menuClock{testNow}, Logger: log,
	})

	var shown, quit int
	menu := &recordingMenu{}
	stop := buildTrayMenu(menu, trayActions{
		show:  func() { shown++ },
		quit:  func() { quit++ },
		async: func(f func()) { f() },
		now:   func() time.Time { return testNow },
	}, schedules, log)
	defer stop()

	// Layout: Show | status, last run, attention, toggle | Quit.
	if len(menu.items) != 6 || len(menu.seps) != 2 || menu.seps[0] != 1 || menu.seps[1] != 5 {
		t.Fatalf("menu = %d items, separators before %v", len(menu.items), menu.seps)
	}
	show, status, lastRun, attention, toggle, quitItem := menu.items[0], menu.items[1], menu.items[2], menu.items[3], menu.items[4], menu.items[5]
	if show.text() != "Show" || quitItem.text() != "Quit" {
		t.Errorf("plain items = %q, %q", show.text(), quitItem.text())
	}
	if status.enabled || lastRun.enabled {
		t.Error("the status and last-run lines must be disabled")
	}
	// Outside every window (no windows on the fake jobs), not paused, one job waiting.
	if status.text() != "Bot: outside its time windows" || toggle.text() != "Pause bot" {
		t.Errorf("at start = %q / %q", status.text(), toggle.text())
	}
	if lastRun.text() != "Last run: succeeded, 13:00" || attention.text() != "1 job needs your attention" || attention.isHidden() {
		t.Errorf("summary = %q / %q hidden=%v", lastRun.text(), attention.text(), attention.isHidden())
	}

	// Click "Pause bot".
	toggle.click()
	if !sched.Paused() {
		t.Fatal("the click did not pause the scheduler")
	}
	if v, ok, _ := settings.Get(ctx, app.KeyPaused); !ok || v != "true" {
		t.Errorf("the pause was not stored: %q, %v", v, ok)
	}
	if status.text() != "Bot: paused" || toggle.text() != "Resume bot" {
		t.Errorf("after the pause click = %q / %q", status.text(), toggle.text())
	}
	if len(ev.got) != 1 || !ev.got[0].Paused {
		t.Errorf("events after the pause = %+v", ev.got)
	}

	// Click "Resume bot".
	toggle.click()
	if sched.Paused() {
		t.Fatal("the second click did not resume the scheduler")
	}
	if _, ok, _ := settings.Get(ctx, app.KeyPaused); ok {
		t.Error("the stored pause was not removed")
	}
	if status.text() != "Bot: outside its time windows" || toggle.text() != "Pause bot" {
		t.Errorf("after the resume click = %q / %q", status.text(), toggle.text())
	}

	// The screen pauses (the service call the frontend makes): the tray follows
	// without a click, and the next click resumes.
	if _, err := schedules.SetSchedulerPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	if status.text() != "Bot: paused" || toggle.text() != "Resume bot" {
		t.Errorf("after a pause from the screen = %q / %q", status.text(), toggle.text())
	}
	toggle.click()
	if sched.Paused() {
		t.Error("the click after a pause from the screen did not resume")
	}

	// The plain items.
	attention.click()
	show.click()
	quitItem.click()
	if shown != 2 || quit != 1 {
		t.Errorf("show ran %d times (want 2: Show and the attention line), quit %d times (want 1)", shown, quit)
	}
}

// Without a bot the menu is the plain Show / Quit one.
func TestTrayMenuWithoutABotIsShowAndQuit(t *testing.T) {
	menu := &recordingMenu{}
	stop := buildTrayMenu(menu, trayActions{show: func() {}, quit: func() {}, async: func(f func()) { f() }}, nil, nil)
	stop()
	if len(menu.items) != 2 || menu.items[0].text() != "Show" || menu.items[1].text() != "Quit" || len(menu.seps) != 1 {
		t.Errorf("menu = %+v", menu.items)
	}
}

// A pause that cannot be stored leaves the menu where it was.
type failingMenuSettings struct{ *menuSettings }

func (failingMenuSettings) Set(context.Context, string, string) error { return errors.New("disk full") }

func TestTrayMenuClickThatFailsLeavesTheLabels(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sched := app.NewScheduler(app.SchedulerDeps{
		Settings: failingMenuSettings{&menuSettings{kv: map[string]string{}}}, Clock: menuClock{testNow}, Logger: log,
	})
	schedules := app.NewSchedules(app.SchedulesDeps{Jobs: &menuJobs{}, Scheduler: sched, Clock: menuClock{testNow}, Logger: log})
	menu := &recordingMenu{}
	stop := buildTrayMenu(menu, trayActions{show: func() {}, quit: func() {}, async: func(f func()) { f() }}, schedules, log)
	defer stop()
	toggle := menu.items[4]
	toggle.click()
	if sched.Paused() || menu.items[1].text() == "Bot: paused" || toggle.text() != "Pause bot" {
		t.Errorf("a failed pause changed things: paused=%v, %q / %q", sched.Paused(), menu.items[1].text(), toggle.text())
	}
	var re *run.Error
	if _, err := schedules.SetSchedulerPaused(context.Background(), true); !errors.As(err, &re) {
		t.Errorf("err = %v, want a *run.Error", err)
	}
}
