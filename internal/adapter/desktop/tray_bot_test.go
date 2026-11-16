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
)

type fakeItem struct {
	mu      sync.Mutex
	label   string
	enabled bool
	hidden  bool
	click   func()
}

func (f *fakeItem) SetLabel(l string) {
	f.mu.Lock()
	f.label = l
	f.mu.Unlock()
}

func (f *fakeItem) SetEnabled(e bool) {
	f.mu.Lock()
	f.enabled = e
	f.mu.Unlock()
}

func (f *fakeItem) SetHidden(h bool) {
	f.mu.Lock()
	f.hidden = h
	f.mu.Unlock()
}

func (f *fakeItem) OnClick(fn func()) { f.click = fn }

func (f *fakeItem) isHidden() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hidden
}

func (f *fakeItem) text() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.label
}

// fakeBot is a BotControl whose state the test sets.
type fakeBot struct {
	mu     sync.Mutex
	st     app.SchedulerStatus
	err    error
	subs   []func(app.SchedulerStatus)
	setTo  []bool
	reads  int
	failOn bool // SetSchedulerPaused fails
}

func (b *fakeBot) SchedulerStatus(context.Context) (app.SchedulerStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reads++
	return b.st, b.err
}

func (b *fakeBot) SetSchedulerPaused(_ context.Context, paused bool) (app.SchedulerStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setTo = append(b.setTo, paused)
	if b.failOn {
		return app.SchedulerStatus{}, errors.New("boom")
	}
	b.st.Paused = paused
	b.st.State = app.SchedulerRunning
	if paused {
		b.st.State = app.SchedulerPaused
	}
	return b.st, nil
}

func (b *fakeBot) SubscribeScheduler(f func(app.SchedulerStatus)) {
	b.mu.Lock()
	b.subs = append(b.subs, f)
	b.mu.Unlock()
}

func (b *fakeBot) announce(st app.SchedulerStatus) {
	b.mu.Lock()
	b.st = st
	subs := append([]func(app.SchedulerStatus){}, b.subs...)
	b.mu.Unlock()
	for _, f := range subs {
		f(st)
	}
}

var testNow = time.Date(2026, time.November, 16, 15, 0, 0, 0, time.UTC)

type testItems struct{ status, lastRun, attention, toggle *fakeItem }

func newTestBotMenu(bot *fakeBot) (*botMenu, testItems) {
	it := testItems{&fakeItem{}, &fakeItem{}, &fakeItem{}, &fakeItem{}}
	m := newBotMenu(bot, botItems{status: it.status, lastRun: it.lastRun, attention: it.attention, toggle: it.toggle},
		func() time.Time { return testNow }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return m, it
}

func TestBotMenuLabelsFollowTheState(t *testing.T) {
	for _, c := range []struct {
		name   string
		st     app.SchedulerStatus
		status string
		toggle string
	}{
		{"running", app.SchedulerStatus{State: app.SchedulerRunning}, "Bot: running", "Pause bot"},
		{"outside window", app.SchedulerStatus{State: app.SchedulerOutsideWindow}, "Bot: outside its time windows", "Pause bot"},
		{"paused", app.SchedulerStatus{Paused: true, State: app.SchedulerPaused}, "Bot: paused", "Resume bot"},
		// A pause wins over the state word.
		{"paused flag alone", app.SchedulerStatus{Paused: true}, "Bot: paused", "Resume bot"},
	} {
		l := botMenuLabels(c.st, testNow)
		if l.status != c.status || l.toggle != c.toggle {
			t.Errorf("%s: labels = %q, %q; want %q, %q", c.name, l.status, l.toggle, c.status, c.toggle)
		}
	}
}

func TestBotMenuLabelsLastRunAndAttention(t *testing.T) {
	today := testNow.Add(-30 * time.Minute) // 14:30
	older := testNow.Add(-48 * time.Hour)   // Nov 14
	for _, c := range []struct {
		name      string
		st        app.SchedulerStatus
		lastRun   string
		attention string
		visible   bool
	}{
		{"nothing yet", app.SchedulerStatus{}, "Last run: none yet", "", false},
		{"run today", app.SchedulerStatus{LastRun: &app.LastRunSummary{Status: "succeeded", StartedAt: today}}, "Last run: succeeded, 14:30", "", false},
		{"older run", app.SchedulerStatus{LastRun: &app.LastRunSummary{Status: "failed", StartedAt: older}}, "Last run: failed, Nov 14 15:00", "", false},
		{"one job", app.SchedulerStatus{AttentionCount: 1}, "Last run: none yet", "1 job needs your attention", true},
		{"two jobs", app.SchedulerStatus{AttentionCount: 2}, "Last run: none yet", "2 jobs need your attention", true},
	} {
		l := botMenuLabels(c.st, testNow)
		if l.lastRun != c.lastRun || l.attention != c.attention || l.attentionVisible != c.visible {
			t.Errorf("%s: %q / %q / %v; want %q / %q / %v", c.name, l.lastRun, l.attention, l.attentionVisible, c.lastRun, c.attention, c.visible)
		}
	}
}

func TestBotMenuShowsTheStateAtStartAndFollowsChanges(t *testing.T) {
	bot := &fakeBot{st: app.SchedulerStatus{Paused: true, State: app.SchedulerPaused, Version: 3}}
	_, it := newTestBotMenu(bot)
	if it.status.text() != "Bot: paused" || it.toggle.text() != "Resume bot" {
		t.Fatalf("at start = %q / %q (a launch while paused must show it)", it.status.text(), it.toggle.text())
	}

	// The screen resumes the bot: the tray follows without a click.
	bot.announce(app.SchedulerStatus{State: app.SchedulerRunning, Version: 4, AttentionCount: 2,
		LastRun: &app.LastRunSummary{Status: "succeeded", StartedAt: testNow.Add(-time.Hour)}})
	if it.status.text() != "Bot: running" || it.toggle.text() != "Pause bot" {
		t.Errorf("after the screen resumed = %q / %q", it.status.text(), it.toggle.text())
	}
	if it.lastRun.text() != "Last run: succeeded, 14:00" || it.attention.text() != "2 jobs need your attention" || it.attention.isHidden() {
		t.Errorf("summary lines = %q / %q hidden=%v", it.lastRun.text(), it.attention.text(), it.attention.isHidden())
	}
	bot.announce(app.SchedulerStatus{State: app.SchedulerRunning, Version: 5})
	if !it.attention.isHidden() {
		t.Error("the attention line stays visible with no job waiting")
	}
}

func TestBotMenuIgnoresAStatusOlderThanTheOneShown(t *testing.T) {
	bot := &fakeBot{st: app.SchedulerStatus{State: app.SchedulerRunning, Version: 1}}
	m, it := newTestBotMenu(bot)

	// The user pauses (version 2) while a timer read that began earlier, as of
	// version 1, is still on its way.
	bot.announce(app.SchedulerStatus{Paused: true, State: app.SchedulerPaused, Version: 2})
	m.apply(app.SchedulerStatus{State: app.SchedulerRunning, Version: 1}) // the slow read arrives
	if it.status.text() != "Bot: paused" || it.toggle.text() != "Resume bot" {
		t.Errorf("a stale read undid the pause: %q / %q", it.status.text(), it.toggle.text())
	}
	// ... and a click after that still offers to resume.
	m.clickToggle(context.Background())
	if len(bot.setTo) != 1 || bot.setTo[0] {
		t.Errorf("the click asked for %v, want resume", bot.setTo)
	}
}

func TestBotMenuToggleFlipsThePause(t *testing.T) {
	bot := &fakeBot{st: app.SchedulerStatus{State: app.SchedulerRunning}}
	m, it := newTestBotMenu(bot)
	ctx := context.Background()

	m.clickToggle(ctx) // "Pause bot"
	if len(bot.setTo) != 1 || !bot.setTo[0] {
		t.Fatalf("first click asked for %v, want pause", bot.setTo)
	}
	if it.status.text() != "Bot: paused" || it.toggle.text() != "Resume bot" {
		t.Errorf("after pausing = %q / %q", it.status.text(), it.toggle.text())
	}

	m.clickToggle(ctx) // "Resume bot"
	if len(bot.setTo) != 2 || bot.setTo[1] {
		t.Fatalf("second click asked for %v, want resume", bot.setTo)
	}
	if it.status.text() != "Bot: running" || it.toggle.text() != "Pause bot" {
		t.Errorf("after resuming = %q / %q", it.status.text(), it.toggle.text())
	}
}

func TestBotMenuFailedToggleShowsWhatIsReallyTheCase(t *testing.T) {
	bot := &fakeBot{st: app.SchedulerStatus{State: app.SchedulerRunning}, failOn: true}
	m, it := newTestBotMenu(bot)
	m.clickToggle(context.Background())
	if it.status.text() != "Bot: running" || it.toggle.text() != "Pause bot" {
		t.Errorf("after a failed pause = %q / %q, want unchanged", it.status.text(), it.toggle.text())
	}

	// An unreadable status leaves the menu alone.
	bot.mu.Lock()
	bot.err = errors.New("db closed")
	bot.mu.Unlock()
	bot.announce(app.SchedulerStatus{Paused: true, State: app.SchedulerPaused, Version: 1}) // shown
	bot.mu.Lock()
	bot.st = app.SchedulerStatus{State: app.SchedulerRunning, Version: 2}
	bot.mu.Unlock()
	m.refresh(context.Background())
	if it.status.text() != "Bot: paused" {
		t.Errorf("a failed read changed the menu to %q", it.status.text())
	}
}

func TestBotMenuRefreshEveryFollowsTheClockUntilStopped(t *testing.T) {
	bot := &fakeBot{st: app.SchedulerStatus{State: app.SchedulerRunning}}
	m, it := newTestBotMenu(bot)
	bot.mu.Lock()
	bot.st = app.SchedulerStatus{State: app.SchedulerOutsideWindow} // a window closed
	bot.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.refreshEvery(ctx, 5*time.Millisecond); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for it.status.text() != "Bot: outside its time windows" {
		if time.Now().After(deadline) {
			t.Fatal("the timer never refreshed the menu")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("refreshEvery did not stop with its context")
	}
}
