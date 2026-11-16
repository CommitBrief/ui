// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/CommitBrief/ui/internal/app"
)

// Tray menu labels. Like Show and Quit they are native English constants until
// the tray is localised with the rest of i18n.
const (
	trayStatusRunning       = "Bot: running"
	trayStatusPaused        = "Bot: paused"
	trayStatusOutsideWindow = "Bot: outside its time windows"
	trayPauseLabel          = "Pause bot"
	trayResumeLabel         = "Resume bot"
	trayLastRunNone         = "Last run: none yet"
)

// trayRefreshEvery is how often the tray re-reads the scheduler: "running" and
// "outside window" change with the clock, not with a user action.
const trayRefreshEvery = 30 * time.Second

// BotControl is what the tray needs from the scheduler use cases.
// *app.Schedules implements it.
type BotControl interface {
	SchedulerStatus(ctx context.Context) (app.SchedulerStatus, error)
	SetSchedulerPaused(ctx context.Context, paused bool) (app.SchedulerStatus, error)
	SubscribeScheduler(f func(app.SchedulerStatus))
}

// menuItem is the part of a native menu item the bot section changes. It is the
// seam that lets the section be tested without a window system.
type menuItem interface {
	SetLabel(label string)
	SetEnabled(enabled bool)
	SetHidden(hidden bool)
}

// trayEntry is a menu item that can also be clicked.
type trayEntry interface {
	menuItem
	OnClick(f func())
}

// trayMenuBuilder is the menu the tray is built on: the Wails menu in the
// application, a recording fake in the tests.
type trayMenuBuilder interface {
	Add(label string) trayEntry
	AddSeparator()
}

// trayActions are what the menu's plain items do.
type trayActions struct {
	show func()
	quit func()
	// async runs a click handler that must not block the thread the click
	// arrived on (the pause writes to the database). The application passes a
	// goroutine starter, tests run it inline.
	async func(f func())
	// now is the clock the summary lines are told in; nil means time.Now.
	now func() time.Time
}

// wailsMenu adapts the Wails menu.
type wailsMenu struct{ menu *application.Menu }

func (w wailsMenu) Add(label string) trayEntry { return wailsEntry{w.menu.Add(label)} }
func (w wailsMenu) AddSeparator()              { w.menu.AddSeparator() }

// wailsEntry adapts a Wails menu item. Wails applies a change to the native item
// on the calling goroutine, so changes are moved to the main thread (before the
// application runs they wait in Wails' early queue).
type wailsEntry struct{ item *application.MenuItem }

func (w wailsEntry) SetLabel(label string) {
	application.InvokeAsync(func() { w.item.SetLabel(label) })
}

func (w wailsEntry) SetEnabled(enabled bool) {
	application.InvokeAsync(func() { w.item.SetEnabled(enabled) })
}

func (w wailsEntry) SetHidden(hidden bool) {
	application.InvokeAsync(func() { w.item.SetHidden(hidden) })
}

func (w wailsEntry) OnClick(f func()) { w.item.OnClick(func(*application.Context) { f() }) }

// buildTrayMenu fills the tray menu: Show, then (when bot is not nil) the bot
// section, then Quit. The bot section is the status line, the last bot run, a
// line that says how many jobs wait for the user (hidden at zero; clicking it
// opens the window) and one pause/resume toggle. The returned function ends the
// section's refresh timer.
func buildTrayMenu(b trayMenuBuilder, a trayActions, bot BotControl, log *slog.Logger) (stop func()) {
	stop = func() {}
	b.Add(trayShowLabel).OnClick(a.show)
	b.AddSeparator()
	if bot != nil {
		status := b.Add(trayStatusRunning)
		status.SetEnabled(false) // a status line, not a command
		lastRun := b.Add(trayLastRunNone)
		lastRun.SetEnabled(false)
		attention := b.Add("")
		attention.SetHidden(true)
		attention.OnClick(a.show) // the jobs that need the user are on the window
		toggle := b.Add(trayPauseLabel)
		now := a.now
		if now == nil {
			now = time.Now
		}
		bm := newBotMenu(bot, botItems{status: status, lastRun: lastRun, attention: attention, toggle: toggle}, now, log)
		toggle.OnClick(func() { a.async(func() { bm.clickToggle(context.Background()) }) })
		ctx, cancel := context.WithCancel(context.Background())
		go bm.refreshEvery(ctx, trayRefreshEvery)
		stop = cancel
		b.AddSeparator()
	}
	b.Add(trayQuitLabel).OnClick(a.quit)
	return stop
}

// trayLabels is what the bot items say for a scheduler status.
type trayLabels struct {
	status, lastRun, attention, toggle string
	attentionVisible                   bool
}

// botMenuLabels computes the bot items' text. now is only used to tell a run of
// today (shown as a clock time) from an older one (shown with its date).
func botMenuLabels(st app.SchedulerStatus, now time.Time) trayLabels {
	var l trayLabels
	switch {
	case st.Paused:
		l.status, l.toggle = trayStatusPaused, trayResumeLabel
	case st.State == app.SchedulerOutsideWindow:
		l.status, l.toggle = trayStatusOutsideWindow, trayPauseLabel
	default:
		l.status, l.toggle = trayStatusRunning, trayPauseLabel
	}
	l.lastRun = trayLastRunNone
	if r := st.LastRun; r != nil {
		at := r.StartedAt.In(now.Location())
		layout := "Jan 2 15:04"
		if y, m, d := at.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
			layout = "15:04"
		}
		l.lastRun = fmt.Sprintf("Last run: %s, %s", r.Status, at.Format(layout))
	}
	switch n := st.AttentionCount; {
	case n == 1:
		l.attention, l.attentionVisible = "1 job needs your attention", true
	case n > 1:
		l.attention, l.attentionVisible = fmt.Sprintf("%d jobs need your attention", n), true
	}
	return l
}

// botItems are the four items of the bot section.
type botItems struct {
	status, lastRun, attention, toggle menuItem
}

// botMenu keeps the bot section of the tray menu in step with the scheduler.
type botMenu struct {
	ctrl  BotControl
	items botItems
	now   func() time.Time
	log   *slog.Logger

	mu      sync.Mutex
	paused  bool   // what the toggle currently offers to undo
	version uint64 // the newest status shown; an older one is ignored
}

// newBotMenu wires the items to the scheduler: it shows the current state now and
// follows every pause or resume made elsewhere (the screen). Call refreshEvery to
// follow the clock too.
func newBotMenu(ctrl BotControl, items botItems, now func() time.Time, log *slog.Logger) *botMenu {
	if log == nil {
		log = slog.Default()
	}
	m := &botMenu{ctrl: ctrl, items: items, now: now, log: log}
	ctrl.SubscribeScheduler(m.apply)
	m.refresh(context.Background())
	return m
}

// apply shows a status unless a newer one is already shown (a slow read that
// returns after a pause must not undo it).
func (m *botMenu) apply(st app.SchedulerStatus) {
	l := botMenuLabels(st, m.now())
	m.mu.Lock()
	defer m.mu.Unlock()
	if st.Version < m.version {
		return
	}
	m.version, m.paused = st.Version, st.Paused
	m.items.status.SetLabel(l.status)
	m.items.toggle.SetLabel(l.toggle)
	m.items.lastRun.SetLabel(l.lastRun)
	m.items.attention.SetLabel(l.attention)
	m.items.attention.SetHidden(!l.attentionVisible)
}

// refresh reads the scheduler and shows it. A failed read leaves the menu as it
// is.
func (m *botMenu) refresh(ctx context.Context) {
	st, err := m.ctrl.SchedulerStatus(ctx)
	if err != nil {
		m.log.Warn("reading the scheduler status for the tray failed", "error", err)
		return
	}
	m.apply(st)
}

// clickToggle is the pause/resume item: it flips the pause the menu shows.
func (m *botMenu) clickToggle(ctx context.Context) {
	m.mu.Lock()
	want := !m.paused
	m.mu.Unlock()
	st, err := m.ctrl.SetSchedulerPaused(ctx, want)
	if err != nil {
		m.log.Error("changing the bot pause from the tray failed", "paused", want, "error", err)
		m.refresh(ctx) // show what is really the case
		return
	}
	m.apply(st)
}

// refreshEvery re-reads the scheduler on a timer until ctx ends.
func (m *botMenu) refreshEvery(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refresh(ctx)
		}
	}
}
