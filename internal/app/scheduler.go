// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/domain/schedule"
	"github.com/CommitBrief/ui/internal/port"
)

// Scheduler constants.
const (
	// DefaultSchedulerTick is how often the scheduler looks at the clock.
	DefaultSchedulerTick = 30 * time.Second
	// catchupTicks: a gap of more than this many ticks between two looks means
	// the machine slept (or the application was closed), so what came due in the
	// gap is one catch-up run, not a normal slot.
	catchupTicks = 2
	// triggerTimeout bounds planning and queueing of one job: for the bot,
	// planning is the GitHub discovery.
	triggerTimeout = 3 * time.Minute
	// KeyLastTick is the settings row that remembers when the scheduler last
	// looked, across restarts (the catch-up after the application was closed).
	KeyLastTick = "scheduler.last_tick"
	// KeyPaused is the settings row that remembers the user's pause across
	// restarts: "true" while paused, absent otherwise. The pause is the user's
	// decision, not a session state, so a launch honours it.
	KeyPaused = "scheduler.paused"
)

// jobAttentionCodes are the failure classes that stop a scheduled job until the
// user fixes something (architecture "Yeniden deneme"): retrying cannot help and
// every retry costs money. timeout, too_volatile and the rest are not here: they
// are left to the next slot.
var jobAttentionCodes = map[errcode.Code]bool{
	errcode.ProviderNotAPI:     true,
	errcode.SelfPR:             true,
	errcode.CLITooOld:          true,
	errcode.CLINotFound:        true,
	errcode.GHNotFound:         true,
	errcode.GHNotAuthenticated: true,
	errcode.CapabilityMissing:  true,
}

// RunTrigger starts a run of a stored job. *Runs implements it.
type RunTrigger interface {
	Trigger(ctx context.Context, j job.Job, trigger run.Trigger) (run.Run, error)
}

// SchedulerDeps are what the scheduler needs.
type SchedulerDeps struct {
	Jobs     port.JobStore
	Runs     port.RunStore
	Settings port.SettingsStore
	// Trigger plans and queues a run (the run service).
	Trigger RunTrigger
	// Budget gates new runs on the daily cost; nil means no limit.
	Budget *Budget
	// Notifier tells the user about new findings, failures and the budget; nil
	// means silent.
	Notifier port.Notifier
	Clock    port.Clock
	// Logger receives the lifecycle log; nil means slog.Default().
	Logger *slog.Logger
	// Tick overrides DefaultSchedulerTick (tests).
	Tick time.Duration
}

// Scheduler starts the runs of jobs that have time windows (UI-0002). It looks
// at the wall clock every tick; a job is due when a slot of one of its windows
// fell in (last look, now]. It never replays missed slots: after a sleep or a
// restart a single catch-up run covers the window that is open now, and slots of
// closed windows are dropped. A job that is still running is skipped (no
// overlap), so is everything while the daily budget is spent, and a job that
// needs the user's attention stays quiet until they clear it.
//
// The scheduler plans and queues through the run service; the kinds decide what a
// run does. It follows each run it started until it ends, to notify the user and
// to put the job into the attention state.
type Scheduler struct {
	d SchedulerDeps

	mu          sync.Mutex // serializes ticks
	last        time.Time
	haveLast    bool
	inflight    map[int64]int64        // job ID -> run ID started by the scheduler
	lastFailure map[int64]errcode.Code // job ID -> failure class already notified
	budgetDay   string                 // local day the budget notice was sent for

	// ctl serializes Pause and Resume: the store write and the in-memory pause
	// move together, so two callers (the tray and the screen) cannot leave the
	// memory saying paused while the store says not.
	ctl sync.Mutex

	loop   sync.Mutex // guards cancel and done
	cancel context.CancelFunc
	done   chan struct{}

	// st guards the pause and the status. It is separate from mu because a tick
	// can hold mu for minutes (planning a run) and the tray must still answer.
	st           sync.Mutex
	version      uint64 // counts the changes of the pause; the newer status wins
	paused       bool
	pausedLoaded bool      // the pause was read from the store (or written to it)
	lastLook     time.Time // when the last tick finished; zero before the first
	running      bool      // Start was called and Stop was not
}

// SchedulerSnapshot is what the scheduler can say about itself without doing
// I/O.
type SchedulerSnapshot struct {
	// Paused is the user's pause: ticks start nothing until Resume.
	Paused bool
	// LastTick is when the scheduler last looked at the clock in this process;
	// nil before the first look.
	LastTick *time.Time
	// NextTick is when the next look is due; nil when the loop is not running
	// or is paused.
	NextTick *time.Time
	// Version grows with every change of the pause. A reader that holds two
	// snapshots keeps the one with the higher version, so a slow read that
	// finishes late cannot undo a newer pause or resume.
	Version uint64
}

// NewScheduler wires the scheduler. It does no I/O; call Start.
func NewScheduler(d SchedulerDeps) *Scheduler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Tick <= 0 {
		d.Tick = DefaultSchedulerTick
	}
	return &Scheduler{
		d:           d,
		inflight:    map[int64]int64{},
		lastFailure: map[int64]errcode.Code{},
	}
}

// Start runs the scheduler in the background: one look now (the catch-up after a
// launch) and then one per tick, until ctx ends or Stop is called. Starting a
// running scheduler does nothing.
func (s *Scheduler) Start(ctx context.Context) {
	s.loop.Lock()
	defer s.loop.Unlock()
	if s.cancel != nil {
		return
	}
	// The pause is read before the first look, so a launch while paused starts
	// nothing.
	s.loadPaused(ctx)
	loopCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	s.st.Lock()
	s.running = true
	s.st.Unlock()
	go func(done chan struct{}) {
		defer close(done)
		s.Tick(loopCtx)
		t := time.NewTicker(s.d.Tick)
		defer t.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-t.C:
				s.Tick(loopCtx)
			}
		}
	}(s.done)
}

// Stop ends the loop and waits for the look in progress to return. It is safe to
// call more than once.
func (s *Scheduler) Stop() {
	s.loop.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.loop.Unlock()
	if cancel == nil {
		return
	}
	s.st.Lock()
	s.running = false
	s.st.Unlock()
	cancel()
	<-done
}

// Tick is one look at the clock: it settles the runs that ended, then starts what
// came due. It is exported so a simulation can drive it with a fake clock.
func (s *Scheduler) Tick(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	now := s.d.Clock.Now()
	last := s.lastTick(ctx, now)

	s.settleRuns(ctx, now)
	// A paused look still happens: runs that ended are settled and the time moves
	// on, so Resume does not replay (or catch up on) what the pause skipped.
	if s.loadPaused(ctx) {
		s.d.Logger.Debug("scheduler: paused, starting nothing")
	} else {
		s.startDue(ctx, last, now)
	}

	if ctx.Err() == nil {
		s.last = now
		s.st.Lock()
		s.lastLook = now
		s.st.Unlock()
		if err := s.d.Settings.Set(ctx, KeyLastTick, now.UTC().Format(time.RFC3339Nano)); err != nil {
			s.d.Logger.Warn("scheduler: remembering the last look failed", "error", err)
		}
	}
}

// lastTick is when the scheduler last looked: from memory, else from the
// database (the previous process), else now (the first ever look has nothing to
// catch up on). A remembered time in the future (the clock moved back) is now.
func (s *Scheduler) lastTick(ctx context.Context, now time.Time) time.Time {
	if !s.haveLast {
		s.haveLast = true
		s.last = now
		if v, ok, err := s.d.Settings.Get(ctx, KeyLastTick); err != nil {
			s.d.Logger.Warn("scheduler: reading the last look failed; assuming now", "error", err)
		} else if ok {
			if t, perr := time.Parse(time.RFC3339Nano, v); perr == nil {
				s.last = t
			}
		}
	}
	if s.last.After(now) {
		return now
	}
	return s.last
}

// startDue starts the runs of the jobs that came due in (last, now].
func (s *Scheduler) startDue(ctx context.Context, last, now time.Time) {
	jobs, err := s.d.Jobs.ListJobs(ctx)
	if err != nil {
		s.d.Logger.Error("scheduler: listing the jobs failed", "error", err)
		return
	}
	trigger := run.TriggerSchedule
	if now.Sub(last) > catchupTicks*s.d.Tick {
		trigger = run.TriggerCatchup
	}

	var budget BudgetStatus
	if s.d.Budget != nil {
		if budget, err = s.d.Budget.Status(ctx); err != nil {
			// Not knowing the spend must not silence the scheduler, but it must be
			// visible.
			s.d.Logger.Warn("scheduler: reading the daily budget failed; running without it", "error", err)
			budget = BudgetStatus{}
		}
	}

	heldBack := 0
	for _, rec := range jobs {
		if ctx.Err() != nil {
			return
		}
		if s.Paused() {
			return // paused while this look was running
		}
		if !rec.Enabled || len(rec.Windows) == 0 || rec.AttentionCode != "" {
			continue
		}
		slots := schedule.MissedOf(rec.Windows, last, now)
		if len(slots) == 0 {
			continue
		}
		if s.overlaps(ctx, rec.ID) {
			s.d.Logger.Info("scheduler: skipping a slot, the previous run of the job is still going", "job", rec.ID)
			continue
		}
		if budget.Exceeded {
			heldBack++
			continue
		}
		s.start(ctx, rec, trigger, len(slots))
	}
	if heldBack > 0 {
		s.noteBudget(ctx, budget, now)
	}
}

// overlaps reports whether the job has a run that has not ended: one the
// scheduler follows, or one that is queued or running in the database (a manual
// run, a run of an earlier tick).
func (s *Scheduler) overlaps(ctx context.Context, jobID int64) bool {
	if _, ok := s.inflight[jobID]; ok {
		return true
	}
	recs, err := s.d.Runs.ListRuns(ctx, jobID, 1)
	if err != nil {
		s.d.Logger.Warn("scheduler: reading the last run of a job failed", "job", jobID, "error", err)
		return false
	}
	return len(recs) > 0 && !run.Status(recs[0].Status).Terminal()
}

// start triggers one run of the job and remembers it.
func (s *Scheduler) start(ctx context.Context, rec port.JobRecord, trigger run.Trigger, slots int) {
	tctx, cancel := context.WithTimeout(ctx, triggerTimeout)
	defer cancel()
	started, err := s.d.Trigger.Trigger(tctx, jobFromRecord(rec), trigger)
	if err != nil {
		var re *run.Error
		switch {
		case errors.As(err, &re) && re.Code == run.CodeQueueFull:
			// Backpressure: the queue is full, the slot is dropped and the next
			// one tries again.
			s.d.Logger.Warn("scheduler: the queue is full, skipping the slot", "job", rec.ID)
		case ctx.Err() != nil:
			// Shutting down.
		default:
			s.d.Logger.Error("scheduler: starting a run failed", "job", rec.ID, "error", err)
		}
		return
	}
	s.d.Logger.Info("scheduler: run started", "job", rec.ID, "run", started.ID, "trigger", trigger, "slots", slots)
	s.inflight[rec.ID] = started.ID
	if started.Status.Terminal() {
		// A run with nothing to do ends at once.
		s.settleOne(ctx, rec.ID, started.ID, s.d.Clock.Now())
	}
}

// settleRuns looks at the runs the scheduler started and handles the ones that
// ended.
func (s *Scheduler) settleRuns(ctx context.Context, now time.Time) {
	ids := make([]int64, 0, len(s.inflight))
	for id := range s.inflight {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, jobID := range ids {
		if ctx.Err() != nil {
			return
		}
		s.settleOne(ctx, jobID, s.inflight[jobID], now)
	}
}

// settleOne handles one followed run if it has ended: notifications, and the
// attention state of the job.
func (s *Scheduler) settleOne(ctx context.Context, jobID, runID int64, now time.Time) {
	rec, err := s.d.Runs.GetRun(ctx, runID)
	if errors.Is(err, port.ErrNotFound) {
		delete(s.inflight, jobID)
		return
	}
	if err != nil {
		s.d.Logger.Warn("scheduler: reading a run failed", "run", runID, "error", err)
		return
	}
	status := run.Status(rec.Status)
	if !status.Terminal() {
		return
	}
	delete(s.inflight, jobID)
	if status == run.StatusCanceled {
		return // the user (or a shutdown) stopped it; nothing to report
	}

	tasks, err := s.d.Runs.ListTasks(ctx, runID, false)
	if err != nil {
		s.d.Logger.Warn("scheduler: reading the tasks of a run failed", "run", runID, "error", err)
		return
	}
	jr, err := s.d.Jobs.GetJob(ctx, jobID)
	if err != nil {
		if !errors.Is(err, port.ErrNotFound) {
			s.d.Logger.Warn("scheduler: reading a job failed", "job", jobID, "error", err)
		}
		return
	}

	var findings, reviewed int
	var attention, failure errcode.Code
	for _, t := range tasks {
		switch run.Status(t.Status) {
		case run.StatusSucceeded:
			findings += t.FindingCount
			if t.PRNumber > 0 {
				reviewed++
			}
		case run.StatusFailed, run.StatusAttention:
			code := errcode.Code(t.ErrorCode)
			if failure == "" {
				failure = code
			}
			if attention == "" && jobAttentionCodes[code] {
				attention = code
			}
		}
	}

	switch {
	case attention != "":
		if err := s.d.Jobs.SetAttention(ctx, jobID, string(attention), now); err != nil {
			s.d.Logger.Error("scheduler: marking a job as needing attention failed", "job", jobID, "error", err)
		}
		s.d.Logger.Warn("scheduler: the job needs attention and will not run until it is resumed", "job", jobID, "error_code", attention)
		s.notifyFailure(ctx, jr, rec.ID, attention, true)
	case failure != "":
		s.d.Logger.Warn("scheduler: a run failed", "job", jobID, "run", runID, "error_code", failure)
		s.notifyFailure(ctx, jr, rec.ID, failure, false)
	default:
		delete(s.lastFailure, jobID)
	}
	if findings > 0 {
		s.notify(ctx, port.Notification{
			Kind: port.NotifyNewFindings, JobID: jobID, JobName: jr.Name, RunID: runID,
			Findings: findings, PullRequests: reviewed,
		})
	}
}

// notifyFailure tells the user about a failed run, once per failure class: the
// same failure on every slot would be a notification every few minutes.
func (s *Scheduler) notifyFailure(ctx context.Context, jr port.JobRecord, runID int64, code errcode.Code, needsAttention bool) {
	if prev, seen := s.lastFailure[jr.ID]; seen && prev == code && !needsAttention {
		return
	}
	s.lastFailure[jr.ID] = code
	s.notify(ctx, port.Notification{
		Kind: port.NotifyRunFailed, JobID: jr.ID, JobName: jr.Name, RunID: runID,
		ErrorCode: code, NeedsAttention: needsAttention,
	})
}

// noteBudget tells the user, once per day, that the budget stopped the
// scheduler.
func (s *Scheduler) noteBudget(ctx context.Context, st BudgetStatus, now time.Time) {
	day := strconv.Itoa(now.Year()*10000 + int(now.Month())*100 + now.Day())
	if s.budgetDay == day {
		return
	}
	s.budgetDay = day
	s.d.Logger.Warn("scheduler: the daily budget is spent; no new run starts today", "budget_usd", st.LimitUSD, "spent_usd", st.SpentUSD)
	s.notify(ctx, port.Notification{Kind: port.NotifyBudgetExceeded, BudgetUSD: st.LimitUSD, SpentUSD: st.SpentUSD})
}

func (s *Scheduler) notify(ctx context.Context, n port.Notification) {
	if s.d.Notifier != nil {
		s.d.Notifier.Notify(ctx, n)
	}
}

// Pause stops the scheduler from starting runs until Resume. The pause is stored
// first, so it survives a restart; if storing fails nothing changes and the
// error is returned. A run that is already going is not interrupted, and a run
// the user starts by hand is not affected.
func (s *Scheduler) Pause(ctx context.Context) error {
	s.ctl.Lock()
	defer s.ctl.Unlock()
	if err := s.d.Settings.Set(ctx, KeyPaused, "true"); err != nil {
		return err
	}
	s.setPaused(true)
	return nil
}

// Resume lets the scheduler start runs again. Slots that came due during the
// pause are dropped, not replayed.
func (s *Scheduler) Resume(ctx context.Context) error {
	s.ctl.Lock()
	defer s.ctl.Unlock()
	if err := s.d.Settings.Delete(ctx, KeyPaused); err != nil {
		return err
	}
	s.setPaused(false)
	return nil
}

// Paused reports the user's pause as the scheduler knows it now.
func (s *Scheduler) Paused() bool {
	s.st.Lock()
	defer s.st.Unlock()
	return s.paused
}

// Status is the scheduler's own state: the pause, the last look and the next
// one.
func (s *Scheduler) Status() SchedulerSnapshot {
	s.st.Lock()
	defer s.st.Unlock()
	out := SchedulerSnapshot{Paused: s.paused, Version: s.version}
	if !s.lastLook.IsZero() {
		t := s.lastLook
		out.LastTick = &t
		if s.running && !s.paused {
			next := t.Add(s.d.Tick)
			out.NextTick = &next
		}
	}
	return out
}

func (s *Scheduler) setPaused(v bool) {
	s.st.Lock()
	s.paused, s.pausedLoaded = v, true
	s.version++
	s.st.Unlock()
}

// loadPaused returns whether starting is held back. The pause is read from the
// store once; when that fails the scheduler holds back (a pause the user chose
// must not be lost to a read error) and reads again at the next look.
func (s *Scheduler) loadPaused(ctx context.Context) bool {
	s.st.Lock()
	loaded, paused := s.pausedLoaded, s.paused
	s.st.Unlock()
	if loaded {
		return paused
	}
	v, ok, err := s.d.Settings.Get(ctx, KeyPaused)
	if err != nil {
		s.d.Logger.Warn("scheduler: reading the pause failed; holding back until it can be read", "error", err)
		return true
	}
	s.st.Lock()
	defer s.st.Unlock()
	if !s.pausedLoaded { // a Pause or Resume may have settled it meanwhile
		s.paused, s.pausedLoaded = ok && v == "true", true
	}
	return s.paused
}
