// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/domain/schedule"
	"github.com/CommitBrief/ui/internal/domain/settings"
	"github.com/CommitBrief/ui/internal/port"
)

// notes collects the notifications.
type notes struct {
	mu  sync.Mutex
	all []port.Notification
}

func (n *notes) Notify(_ context.Context, x port.Notification) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.all = append(n.all, x)
}

func (n *notes) kinds(k port.NotificationKind) []port.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []port.Notification
	for _, x := range n.all {
		if x.Kind == k {
			out = append(out, x)
		}
	}
	return out
}

// sim is a scheduler running against the in-memory store, the real run service
// and a fake kind, on a fake clock.
type sim struct {
	t     *testing.T
	db    *memDB
	runs  *Runs
	sched *Scheduler
	clock *fakeClock
	notes *notes
	kind  *fakeKind
	loc   *time.Location
}

func newSim(t *testing.T, loc *time.Location, start time.Time, exec func(context.Context, run.Task, func(string)) (run.Outcome, error)) *sim {
	t.Helper()
	kind := &fakeKind{exec: exec}
	if exec == nil {
		kind.exec = func(context.Context, run.Task, func(string)) (run.Outcome, error) { return run.Outcome{}, nil }
	}
	clock := &fakeClock{t: start, loc: loc}
	// The run service stamps runs with the fake clock too.
	runs, db, _ := newRuns(t, kind, func(d *RunsDeps) { d.Now = clock.Now })
	n := &notes{}
	s := &sim{t: t, db: db, runs: runs, clock: clock, notes: n, kind: kind, loc: loc}
	s.sched = s.newScheduler()
	return s
}

func (s *sim) newScheduler() *Scheduler {
	return NewScheduler(SchedulerDeps{
		Jobs: s.db, Runs: s.db, Settings: s.db, Trigger: s.runs,
		Budget:   NewBudget(BudgetDeps{Settings: s.db, Costs: s.db, Clock: s.clock}),
		Notifier: s.notes, Clock: s.clock, Logger: quietLogger,
	})
}

func (s *sim) addJob(ws ...schedule.Window) int64 {
	s.t.Helper()
	rec, err := s.db.CreateJob(context.Background(), port.JobRecord{
		Kind: string(job.KindReviewLocal), Name: "scheduled", Spec: json.RawMessage(`{"mode":"staged"}`),
		SpecVersion: 1, Mode: string(job.ModePreview), Enabled: true, RepositoryIDs: []int64{1}, Windows: ws,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return rec.ID
}

// tick runs one look and waits until every run it started has ended, so the next
// look sees a settled world.
func (s *sim) tick() {
	s.t.Helper()
	s.sched.Tick(context.Background())
	s.waitIdle()
}

func (s *sim) waitIdle() {
	s.t.Helper()
	waitFor(s.t, "every run to end", func() bool {
		s.db.mu.Lock()
		defer s.db.mu.Unlock()
		for _, r := range s.db.runs {
			if !run.Status(r.Status).Terminal() {
				return false
			}
		}
		return true
	})
}

// step advances the clock and looks, as the 30-second ticker would.
func (s *sim) step(d time.Duration) {
	s.clock.Add(d)
	s.tick()
}

func (s *sim) runsOf(jobID int64) []port.RunRecord {
	s.t.Helper()
	recs, err := s.db.ListRuns(context.Background(), jobID, 0)
	if err != nil {
		s.t.Fatal(err)
	}
	return recs
}

func weekdayWindow(tz string, startH, startM, endH, endM, every int) schedule.Window {
	return schedule.Window{Days: schedule.Weekdays, Start: startH*60 + startM, End: endH*60 + endM, Every: every, TZ: tz}
}

func istanbul(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestSchedulerRunsAJobWhenASlotComesDue(t *testing.T) {
	ist := istanbul(t)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), nil) // a Wednesday
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 10, 0, 15))

	s.tick() // 08:59:40, before the window: nothing
	if got := s.runsOf(id); len(got) != 0 {
		t.Fatalf("a run before the window: %+v", got)
	}

	s.step(30 * time.Second) // 09:00:10: the 09:00 slot
	got := s.runsOf(id)
	if len(got) != 1 || got[0].Trigger != "schedule" || got[0].Status != "succeeded" {
		t.Fatalf("runs = %+v, want one scheduled, succeeded run", got)
	}

	// The same slot is never run twice, however many looks fall inside it.
	s.step(30 * time.Second)
	s.step(30 * time.Second)
	if got := s.runsOf(id); len(got) != 1 {
		t.Fatalf("a slot ran twice: %+v", got)
	}

	// 09:15:10: the next slot.
	s.clock.Set(time.Date(2026, time.November, 11, 9, 14, 40, 0, ist))
	s.tick()
	s.step(30 * time.Second)
	if got := s.runsOf(id); len(got) != 2 {
		t.Fatalf("runs = %d, want the 09:15 slot as well", len(got))
	}
}

func TestSchedulerSkipsWhatShouldNotRun(t *testing.T) {
	ist := istanbul(t)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), nil)
	w := weekdayWindow("Europe/Istanbul", 9, 0, 10, 0, 15)
	disabled := s.addJob(w)
	noWindows := s.addJob()
	attention := s.addJob(w)
	running := s.addJob(w)
	s.db.mu.Lock()
	d := s.db.jobs[disabled]
	d.Enabled = false
	s.db.jobs[disabled] = d
	s.db.mu.Unlock()
	_ = s.db.SetAttention(context.Background(), attention, "gh_not_authenticated", s.clock.Now())

	s.tick()
	s.step(30 * time.Second) // 09:00:10
	for name, id := range map[string]int64{"disabled": disabled, "no windows": noWindows, "attention": attention} {
		if got := s.runsOf(id); len(got) != 0 {
			t.Errorf("%s job ran: %+v", name, got)
		}
	}
	if got := s.runsOf(running); len(got) != 1 {
		t.Errorf("the healthy job did not run: %+v", got)
	}

	// A weekend slot does not exist.
	s.clock.Set(time.Date(2026, time.November, 14, 8, 59, 40, 0, ist)) // Saturday
	s.tick()
	s.step(30 * time.Second)
	if got := s.runsOf(running); len(got) != 1 {
		t.Errorf("ran on a Saturday: %+v", got)
	}
}

// After a sleep (or a closed application) one catch-up run covers everything
// that came due in the open window; slots of closed windows are dropped.
func TestSchedulerCatchesUpOnceAfterASleep(t *testing.T) {
	ist := istanbul(t)
	ctx := context.Background()
	w := weekdayWindow("Europe/Istanbul", 9, 0, 17, 0, 15)

	t.Run("six slots missed, one catch-up run", func(t *testing.T) {
		s := newSim(t, ist, time.Date(2026, time.November, 11, 11, 20, 0, 0, ist), nil)
		id := s.addJob(w)
		// The previous look was at 09:50; the machine slept until 11:20.
		_ = s.db.Set(ctx, KeyLastTick, time.Date(2026, time.November, 11, 9, 50, 0, 0, ist).UTC().Format(time.RFC3339Nano))

		s.tick()
		got := s.runsOf(id)
		if len(got) != 1 || got[0].Trigger != "catchup" {
			t.Fatalf("runs = %+v, want exactly one catch-up run for the 10:00..11:15 slots", got)
		}
		s.step(30 * time.Second) // the next look is a normal one: nothing more due
		if got := s.runsOf(id); len(got) != 1 {
			t.Errorf("runs = %d after the catch-up, want 1", len(got))
		}
	})

	t.Run("a closed window is not caught up", func(t *testing.T) {
		s := newSim(t, ist, time.Date(2026, time.November, 16, 8, 0, 0, 0, ist), nil) // Monday 08:00
		id := s.addJob(w)
		// Last look was Friday 16:50, in the previous window.
		_ = s.db.Set(ctx, KeyLastTick, time.Date(2026, time.November, 13, 16, 50, 0, 0, ist).UTC().Format(time.RFC3339Nano))
		s.tick()
		if got := s.runsOf(id); len(got) != 0 {
			t.Errorf("Friday's slots were replayed: %+v", got)
		}
	})

	t.Run("woken inside today's window: one run for today's first slot", func(t *testing.T) {
		s := newSim(t, ist, time.Date(2026, time.November, 16, 9, 10, 0, 0, ist), nil)
		id := s.addJob(w)
		_ = s.db.Set(ctx, KeyLastTick, time.Date(2026, time.November, 13, 16, 50, 0, 0, ist).UTC().Format(time.RFC3339Nano))
		s.tick()
		got := s.runsOf(id)
		if len(got) != 1 || got[0].Trigger != "catchup" {
			t.Errorf("runs = %+v, want one catch-up run", got)
		}
	})

	t.Run("woken after the window closed: nothing", func(t *testing.T) {
		s := newSim(t, ist, time.Date(2026, time.November, 11, 17, 30, 0, 0, ist), nil)
		id := s.addJob(w)
		_ = s.db.Set(ctx, KeyLastTick, time.Date(2026, time.November, 11, 12, 0, 0, 0, ist).UTC().Format(time.RFC3339Nano))
		s.tick()
		if got := s.runsOf(id); len(got) != 0 {
			t.Errorf("runs = %+v, want none: 17:30 is outside 09:00-17:00", got)
		}
	})

	t.Run("a restart is the same as a sleep", func(t *testing.T) {
		s := newSim(t, ist, time.Date(2026, time.November, 11, 9, 30, 0, 0, ist), nil)
		id := s.addJob(w)
		s.tick() // first ever look: nothing to catch up on, remembers 09:30
		if got := s.runsOf(id); len(got) != 0 {
			t.Fatalf("the first look ran something: %+v", got)
		}
		// The application is closed for 40 minutes; a new scheduler (a new
		// process) starts from the remembered look.
		s.clock.Set(time.Date(2026, time.November, 11, 10, 10, 0, 0, ist))
		second := s.newScheduler()
		second.Tick(ctx)
		s.waitIdle()
		got := s.runsOf(id)
		if len(got) != 1 || got[0].Trigger != "catchup" {
			t.Errorf("runs = %+v, want one catch-up run", got)
		}
	})
}

func TestSchedulerSkipsASlotWhileTheJobIsStillRunning(t *testing.T) {
	ist := istanbul(t)
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), func(ctx context.Context, _ run.Task, _ func(string)) (run.Outcome, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return run.Outcome{}, nil
	})
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 10, 0, 15))

	s.tick()
	s.clock.Add(30 * time.Second) // 09:00:10
	s.sched.Tick(context.Background())
	<-started // the first run is executing

	// 09:15:10: the 09:15 slot comes due while the 09:00 run has not ended.
	s.clock.Set(time.Date(2026, time.November, 11, 9, 14, 40, 0, ist))
	s.sched.Tick(context.Background())
	s.clock.Add(30 * time.Second)
	s.sched.Tick(context.Background())
	if got := s.runsOf(id); len(got) != 1 {
		t.Fatalf("runs = %d, want the 09:15 slot skipped (no overlap)", len(got))
	}

	close(release)
	s.waitIdle()
	// 09:30: the job is free again.
	s.clock.Set(time.Date(2026, time.November, 11, 9, 29, 40, 0, ist))
	s.tick()
	s.step(30 * time.Second)
	if got := s.runsOf(id); len(got) != 2 {
		t.Errorf("runs = %d, want the 09:30 slot to run", len(got))
	}
}

func TestSchedulerDoesNotOverlapAManualRun(t *testing.T) {
	ist := istanbul(t)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), nil)
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 10, 0, 15))
	// A run the user started that has not ended (written straight to the store, as
	// the run service would).
	_, _ = s.db.CreateRun(context.Background(), port.RunRecord{JobID: id, Trigger: "manual", StartedAt: s.clock.Now(), Status: "running"})

	s.sched.Tick(context.Background())
	s.clock.Add(30 * time.Second)
	s.sched.Tick(context.Background())
	if got := s.runsOf(id); len(got) != 1 {
		t.Errorf("runs = %+v, want only the manual one", got)
	}
}

func TestSchedulerBudgetStopsNewRunsAndNotifiesOncePerDay(t *testing.T) {
	ist := istanbul(t)
	ctx := context.Background()
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), nil)
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 17, 0, 15))
	_ = s.db.Set(ctx, settings.KeyDailyBudgetUSD, "5")
	finishedTask(s.db, 5, time.Date(2026, time.November, 11, 8, 0, 0, 0, ist)) // today's spend equals the budget

	s.tick()
	s.step(30 * time.Second) // 09:00 slot
	if got := s.runsOf(id); len(got) != 0 {
		t.Fatalf("a run started with the budget spent: %+v", got)
	}
	over := s.notes.kinds(port.NotifyBudgetExceeded)
	if len(over) != 1 || over[0].BudgetUSD != 5 || over[0].SpentUSD != 5 {
		t.Fatalf("budget notifications = %+v", over)
	}

	// The next slot the same day: still held back, no second notification.
	s.clock.Set(time.Date(2026, time.November, 11, 9, 14, 40, 0, ist))
	s.tick()
	s.step(30 * time.Second)
	if got := s.runsOf(id); len(got) != 0 || len(s.notes.kinds(port.NotifyBudgetExceeded)) != 1 {
		t.Errorf("runs = %d, notifications = %d", len(got), len(s.notes.kinds(port.NotifyBudgetExceeded)))
	}

	// Raising the budget in Settings applies at once.
	_ = s.db.Set(ctx, settings.KeyDailyBudgetUSD, "50")
	s.clock.Set(time.Date(2026, time.November, 11, 9, 29, 40, 0, ist))
	s.tick()
	s.step(30 * time.Second)
	if got := s.runsOf(id); len(got) != 1 {
		t.Fatalf("runs = %d, want the run to start once there is budget", len(got))
	}

	// Tomorrow the spend starts from zero, and a new overrun is notified again.
	_ = s.db.Set(ctx, settings.KeyDailyBudgetUSD, "5")
	finishedTask(s.db, 5, time.Date(2026, time.November, 12, 8, 0, 0, 0, ist))
	s.clock.Set(time.Date(2026, time.November, 12, 8, 59, 40, 0, ist))
	s.tick()
	s.step(30 * time.Second)
	if len(s.notes.kinds(port.NotifyBudgetExceeded)) != 2 {
		t.Errorf("budget notifications = %d, want one per day", len(s.notes.kinds(port.NotifyBudgetExceeded)))
	}
}

func TestSchedulerNeedsAttentionStopsTheJobUntilItIsResumed(t *testing.T) {
	ist := istanbul(t)
	ctx := context.Background()
	fail := true
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), func(context.Context, run.Task, func(string)) (run.Outcome, error) {
		if fail {
			return run.Outcome{}, errcode.New(errcode.GHNotAuthenticated, "gh is not logged in")
		}
		return run.Outcome{}, nil
	})
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 17, 0, 15))

	s.tick()
	s.step(30 * time.Second) // 09:00:10: the run starts and ends in attention
	if got := s.runsOf(id); len(got) != 1 || got[0].Status != "attention" || got[0].ErrorCode != "gh_not_authenticated" {
		t.Fatalf("runs = %+v", got)
	}
	s.step(30 * time.Second) // the scheduler settles the run it followed
	rec, _ := s.db.GetJob(ctx, id)
	if rec.AttentionCode != "gh_not_authenticated" || rec.AttentionAt == nil {
		t.Fatalf("job attention = %q", rec.AttentionCode)
	}
	if n := s.notes.kinds(port.NotifyRunFailed); len(n) != 1 || !n[0].NeedsAttention || n[0].ErrorCode != errcode.GHNotAuthenticated || n[0].JobID != id {
		t.Fatalf("failure notifications = %+v", n)
	}

	// Later slots do not run it.
	s.clock.Set(time.Date(2026, time.November, 11, 9, 29, 40, 0, ist))
	s.tick()
	s.step(30 * time.Second)
	if got := s.runsOf(id); len(got) != 1 {
		t.Fatalf("runs = %d; a job that needs attention must stay quiet", len(got))
	}

	// The user fixes gh and resumes the job: the next slot runs.
	fail = false
	if err := s.db.ClearAttention(ctx, id); err != nil {
		t.Fatal(err)
	}
	s.clock.Set(time.Date(2026, time.November, 11, 9, 44, 40, 0, ist))
	s.tick()
	s.step(30 * time.Second)
	if got := s.runsOf(id); len(got) != 2 || got[0].Status != "succeeded" {
		t.Errorf("runs = %+v, want a second, successful run", got)
	}
}

// Every attention class of the brief stops the job; the rest are left to the
// next slot.
func TestSchedulerAttentionClasses(t *testing.T) {
	ist := istanbul(t)
	cases := []struct {
		code      errcode.Code
		attention bool
	}{
		{errcode.ProviderNotAPI, true},
		{errcode.SelfPR, true},
		{errcode.CLITooOld, true},
		{errcode.GHNotAuthenticated, true},
		{errcode.CapabilityMissing, true},
		{errcode.Timeout, false},
		{errcode.TooVolatile, false},
		{errcode.ProviderError, false},
		{errcode.Degraded, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.code), func(t *testing.T) {
			s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), func(context.Context, run.Task, func(string)) (run.Outcome, error) {
				return run.Outcome{}, errcode.New(tc.code, "boom")
			})
			id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 17, 0, 15))
			s.tick()
			s.step(30 * time.Second)
			s.step(30 * time.Second)
			rec, _ := s.db.GetJob(context.Background(), id)
			if (rec.AttentionCode != "") != tc.attention {
				t.Errorf("attention = %q, want %v", rec.AttentionCode, tc.attention)
			}
			if got := s.notes.kinds(port.NotifyRunFailed); len(got) != 1 || got[0].NeedsAttention != tc.attention {
				t.Errorf("notifications = %+v", got)
			}
		})
	}
}

// A timeout is retried at the next slot, not at once, and the same failure is not
// announced again and again.
func TestSchedulerBacksOffToTheNextSlotAfterATimeout(t *testing.T) {
	ist := istanbul(t)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), func(context.Context, run.Task, func(string)) (run.Outcome, error) {
		return run.Outcome{}, errcode.New(errcode.Timeout, "slow")
	})
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 17, 0, 15))
	s.tick()
	s.step(30 * time.Second)  // 09:00:10: fails
	for i := 0; i < 20; i++ { // ... and nothing more for the rest of the slot
		s.step(30 * time.Second)
	}
	if got := s.runsOf(id); len(got) != 1 { // 09:10:10 now
		t.Fatalf("runs = %d, want no retry before the next slot", len(got))
	}
	s.clock.Set(time.Date(2026, time.November, 11, 9, 14, 40, 0, ist))
	s.tick()
	s.step(30 * time.Second)
	s.clock.Set(time.Date(2026, time.November, 11, 9, 29, 40, 0, ist))
	s.tick()
	s.step(30 * time.Second)
	if got := s.runsOf(id); len(got) != 3 {
		t.Fatalf("runs = %d, want one run per slot", len(got))
	}
	if got := s.notes.kinds(port.NotifyRunFailed); len(got) != 1 {
		t.Errorf("failure notifications = %d, want the same failure announced once", len(got))
	}
}

func TestSchedulerNotifiesNewFindings(t *testing.T) {
	ist := istanbul(t)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), func(context.Context, run.Task, func(string)) (run.Outcome, error) {
		return okOutcome(), nil // two findings
	})
	s.kind.plan = func(_ context.Context, j job.Job) ([]run.Task, error) {
		return []run.Task{
			{Kind: s.kind.name, RepositoryID: j.Targets[0], PRNumber: 7, Spec: json.RawMessage(`{}`), LockKeys: []string{"pr:7"}},
			{Kind: s.kind.name, RepositoryID: j.Targets[0], PRNumber: 8, Spec: json.RawMessage(`{}`), LockKeys: []string{"pr:8"}},
		}, nil
	}
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 17, 0, 15))
	s.tick()
	s.step(30 * time.Second)
	s.step(30 * time.Second)

	got := s.notes.kinds(port.NotifyNewFindings)
	if len(got) != 1 || got[0].Findings != 4 || got[0].PullRequests != 2 || got[0].JobID != id || got[0].JobName != "scheduled" {
		t.Fatalf("notifications = %+v", got)
	}
	if len(s.notes.kinds(port.NotifyRunFailed)) != 0 {
		t.Error("a successful run must not announce a failure")
	}
}

func TestSchedulerStaysSilentWhenThereIsNothingNew(t *testing.T) {
	ist := istanbul(t)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), nil)
	s.kind.plan = func(context.Context, job.Job) ([]run.Task, error) { return nil, nil } // no new pull requests
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 17, 0, 15))
	s.tick()
	s.step(30 * time.Second)
	got := s.runsOf(id)
	if len(got) != 1 || got[0].Status != "succeeded" {
		t.Fatalf("runs = %+v, want an empty, succeeded run", got)
	}
	s.step(30 * time.Second)
	if len(s.notes.all) != 0 {
		t.Errorf("notifications = %+v, want none", s.notes.all)
	}
}

// queueFullTrigger answers like a run service whose queue is at its limit.
type queueFullTrigger struct{ calls int }

func (q *queueFullTrigger) Trigger(context.Context, job.Job, run.Trigger) (run.Run, error) {
	q.calls++
	return run.Run{}, run.NewError(run.CodeQueueFull, "")
}

func TestSchedulerDropsASlotWhenTheQueueIsFull(t *testing.T) {
	ist := istanbul(t)
	ctx := context.Background()
	db := newMemDB()
	clock := &fakeClock{t: time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), loc: ist}
	rec, _ := db.CreateJob(ctx, port.JobRecord{
		Kind: string(job.KindReviewLocal), Name: "j", Spec: json.RawMessage(`{"mode":"staged"}`), Mode: "preview", Enabled: true,
		RepositoryIDs: []int64{1}, Windows: []schedule.Window{weekdayWindow("Europe/Istanbul", 9, 0, 17, 0, 15)},
	})
	q := &queueFullTrigger{}
	n := &notes{}
	s := NewScheduler(SchedulerDeps{Jobs: db, Runs: db, Settings: db, Trigger: q, Notifier: n, Clock: clock, Logger: quietLogger})

	s.Tick(ctx)
	clock.Add(30 * time.Second)
	s.Tick(ctx)
	if q.calls != 1 {
		t.Fatalf("Trigger calls = %d", q.calls)
	}
	// Not an attention state, no notification, and the slot is not retried at
	// the very next look: it is dropped and the next slot tries again.
	clock.Add(30 * time.Second)
	s.Tick(ctx)
	if q.calls != 1 {
		t.Errorf("a dropped slot was retried: %d calls", q.calls)
	}
	cur, _ := db.GetJob(ctx, rec.ID)
	if cur.AttentionCode != "" || len(n.all) != 0 {
		t.Errorf("queue-full must be silent: attention=%q notes=%+v", cur.AttentionCode, n.all)
	}
	clock.Set(time.Date(2026, time.November, 11, 9, 14, 40, 0, ist))
	s.Tick(ctx)
	clock.Add(30 * time.Second)
	s.Tick(ctx)
	if q.calls != 2 {
		t.Errorf("calls = %d, want the next slot to try again", q.calls)
	}
}

// The real run service refuses a run that would take the queue past 50 tasks;
// the scheduler turns that into a dropped slot, not an error state.
func TestSchedulerQueueCapComesFromTheRunService(t *testing.T) {
	ist := istanbul(t)
	block := make(chan struct{})
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), func(ctx context.Context, _ run.Task, _ func(string)) (run.Outcome, error) {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return run.Outcome{}, nil
	})
	defer close(block)
	s.kind.plan = func(_ context.Context, j job.Job) ([]run.Task, error) {
		tasks := make([]run.Task, 0, 60)
		for i := 1; i <= 60; i++ {
			tasks = append(tasks, run.Task{Kind: s.kind.name, RepositoryID: j.Targets[0], PRNumber: i, Spec: json.RawMessage(`{}`), LockKeys: []string{"pr:" + strconv.Itoa(i)}})
		}
		return tasks, nil
	}
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 17, 0, 15))
	s.sched.Tick(context.Background())
	s.clock.Add(30 * time.Second)
	s.sched.Tick(context.Background())
	if got := s.runsOf(id); len(got) != 0 {
		t.Errorf("a run of 60 tasks was queued past the cap of %d: %+v", maxQueuedTasks, got)
	}
	if rec, _ := s.db.GetJob(context.Background(), id); rec.AttentionCode != "" {
		t.Errorf("attention = %q", rec.AttentionCode)
	}
}

func TestSchedulerStartAndStop(t *testing.T) {
	ist := istanbul(t)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 9, 0, 10, 0, ist), nil)
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 17, 0, 15))
	_ = s.db.Set(context.Background(), KeyLastTick, time.Date(2026, time.November, 11, 8, 50, 0, 0, ist).UTC().Format(time.RFC3339Nano))
	s.sched.d.Tick = 10 * time.Millisecond

	s.sched.Start(context.Background())
	s.sched.Start(context.Background()) // a second start does nothing
	waitFor(t, "the first look to run the catch-up", func() bool { return len(s.runsOf(id)) == 1 })
	s.sched.Stop()
	s.sched.Stop() // and a second stop is harmless
	before := len(s.runsOf(id))
	time.Sleep(50 * time.Millisecond)
	if len(s.runsOf(id)) != before {
		t.Error("the scheduler kept running after Stop")
	}
	if v, ok, _ := s.db.Get(context.Background(), KeyLastTick); !ok || v == "" {
		t.Error("the last look was not remembered")
	}
}

// countRuns steps the clock in 30-second ticks from start to end and returns how
// many runs the job got and the local wall-clock time of each.
func countRuns(t *testing.T, loc *time.Location, start, end time.Time, w schedule.Window) []string {
	t.Helper()
	s := newSim(t, loc, start, nil)
	id := s.addJob(w)
	s.tick()
	for s.clock.Now().Before(end) {
		s.step(30 * time.Second)
	}
	var at []string
	recs := s.runsOf(id)
	for i := len(recs) - 1; i >= 0; i-- {
		at = append(at, recs[i].StartedAt.In(loc).Format("15:04"))
	}
	return at
}

// The scheduler follows the wall clock across a daylight-saving change: a slot
// runs once, whatever the clock does to the hour around it.
func TestSchedulerDSTNewYork(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	w := schedule.Window{Days: schedule.EveryDay, Start: 1 * 60, End: 4 * 60, Every: 30, TZ: "America/New_York"}

	t.Run("spring forward", func(t *testing.T) {
		// 2026-03-08: 02:00 EST becomes 03:00 EDT. The wall slots 02:00 and 02:30
		// do not exist and collapse onto 03:00 and 03:30.
		start := time.Date(2026, time.March, 8, 0, 59, 40, 0, ny)
		got := countRuns(t, ny, start, time.Date(2026, time.March, 8, 4, 30, 0, 0, ny), w)
		if want := []string{"01:00", "01:30", "03:00", "03:30"}; !equalStr(got, want) {
			t.Errorf("runs at %v, want %v", got, want)
		}
	})

	t.Run("fall back", func(t *testing.T) {
		// 2026-11-01: 01:00-01:59 happens twice; each wall slot still runs once.
		start := time.Date(2026, time.November, 1, 0, 59, 40, 0, ny)
		got := countRuns(t, ny, start, time.Date(2026, time.November, 1, 4, 30, 0, 0, ny).Add(time.Hour), w)
		if want := []string{"01:00", "01:30", "02:00", "02:30", "03:00", "03:30"}; !equalStr(got, want) {
			t.Errorf("runs at %v, want %v: the repeated hour must not run a slot twice", got, want)
		}
	})
}

func TestSchedulerDSTIstanbul(t *testing.T) {
	ist := istanbul(t)
	// Istanbul's last change was 2016-03-27: 03:00 became 04:00.
	w := schedule.Window{Days: schedule.EveryDay, Start: 2 * 60, End: 5 * 60, Every: 30, TZ: "Europe/Istanbul"}
	start := time.Date(2016, time.March, 27, 1, 59, 40, 0, ist)
	got := countRuns(t, ist, start, time.Date(2016, time.March, 27, 5, 30, 0, 0, ist), w)
	if want := []string{"02:00", "02:30", "04:00", "04:30"}; !equalStr(got, want) {
		t.Errorf("runs at %v, want %v", got, want)
	}

	// An ordinary day for comparison: all six slots.
	got = countRuns(t, ist, time.Date(2026, time.March, 29, 1, 59, 40, 0, ist), time.Date(2026, time.March, 29, 5, 30, 0, 0, ist), w)
	if len(got) != 6 {
		t.Errorf("an ordinary day ran at %v, want six slots", got)
	}
}

func equalStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
