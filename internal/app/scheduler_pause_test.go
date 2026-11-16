// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/port"
)

func TestSchedulerPausedTickStartsNothingAndResumeStartsAgain(t *testing.T) {
	ctx := context.Background()
	ist := istanbul(t)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), nil)
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 10, 0, 15))
	s.tick() // 08:59:40

	if err := s.sched.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if !s.sched.Paused() {
		t.Fatal("Paused() = false after Pause")
	}
	s.step(30 * time.Second) // 09:00:10: the 09:00 slot comes due while paused
	if got := s.runsOf(id); len(got) != 0 {
		t.Fatalf("a paused scheduler started %+v", got)
	}

	// The pause does not leave a debt: after Resume the slot that fell inside it
	// is not replayed or caught up, only the next one runs.
	if err := s.sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if s.sched.Paused() {
		t.Fatal("Paused() = true after Resume")
	}
	s.step(30 * time.Second) // 09:00:40
	if got := s.runsOf(id); len(got) != 0 {
		t.Fatalf("Resume replayed a slot of the pause: %+v", got)
	}
	s.clock.Set(time.Date(2026, time.November, 11, 9, 14, 40, 0, ist))
	s.tick()
	s.step(30 * time.Second) // 09:15:10: the 09:15 slot
	if got := s.runsOf(id); len(got) != 1 || got[0].Trigger != "schedule" {
		t.Fatalf("runs after Resume = %+v, want one scheduled run", got)
	}
}

func TestSchedulerPauseDoesNotStopTheManualRunOrTheSettlingOfRuns(t *testing.T) {
	ctx := context.Background()
	ist := istanbul(t)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 9, 0, 10, 0, ist), nil)
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 10, 0, 15))
	if err := s.sched.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	// Starting a job by hand goes through the run service, not the scheduler.
	if _, err := s.runs.RunJob(ctx, id); err != nil {
		t.Fatalf("manual run while paused: %v", err)
	}
	s.waitIdle()
	if got := s.runsOf(id); len(got) != 1 || got[0].Trigger != "manual" {
		t.Fatalf("runs = %+v, want the manual run", got)
	}
}

func TestSchedulerPauseSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	ist := istanbul(t)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 59, 40, 0, ist), nil)
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 10, 0, 15))
	if err := s.sched.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.db.Get(ctx, KeyPaused); !ok || v != "true" {
		t.Fatalf("stored pause = %q, %v", v, ok)
	}

	// A new process: a new scheduler over the same database.
	again := s.newScheduler()
	if again.Paused() {
		t.Fatal("a scheduler that has not started knows nothing yet")
	}
	s.sched = again
	again.d.Tick = time.Hour // only the first look of Start matters
	s.clock.Set(time.Date(2026, time.November, 11, 9, 0, 10, 0, ist))
	again.Start(ctx)
	defer again.Stop()
	if !again.Paused() {
		t.Fatal("the pause was not read at start")
	}
	waitFor(t, "the first look", func() bool { return again.Status().LastTick != nil })
	if got := s.runsOf(id); len(got) != 0 {
		t.Fatalf("a scheduler that started paused ran %+v", got)
	}

	// Resume removes the row; the next process starts running.
	if err := again.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.db.Get(ctx, KeyPaused); ok {
		t.Error("Resume left the stored pause behind")
	}
	if third := s.newScheduler(); third.loadPaused(ctx) {
		t.Error("a scheduler started after Resume is paused")
	}
}

// failingSettings fails the reads or the writes of a pause.
type failingSettings struct {
	port.SettingsStore
	mu     sync.Mutex
	getErr error
	setErr error
}

func (f *failingSettings) Get(ctx context.Context, key string) (string, bool, error) {
	f.mu.Lock()
	err := f.getErr
	f.mu.Unlock()
	if err != nil && key == KeyPaused {
		return "", false, err
	}
	return f.SettingsStore.Get(ctx, key)
}

func (f *failingSettings) Set(ctx context.Context, key, value string) error {
	if key == KeyPaused && f.setErr != nil {
		return f.setErr
	}
	return f.SettingsStore.Set(ctx, key, value)
}

func TestSchedulerPauseFailsClosed(t *testing.T) {
	ctx := context.Background()
	ist := istanbul(t)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 9, 0, 10, 0, ist), nil)
	id := s.addJob(weekdayWindow("Europe/Istanbul", 9, 0, 10, 0, 15))
	fs := &failingSettings{SettingsStore: s.db, setErr: errors.New("disk full")}
	s.sched.d.Settings = fs

	// A pause that could not be stored did not happen.
	if err := s.sched.Pause(ctx); err == nil {
		t.Fatal("Pause succeeded although it could not be stored")
	}
	if s.sched.Paused() {
		t.Error("Paused() = true after a failed Pause")
	}

	// A pause that cannot be read holds the scheduler back until it can.
	fs.mu.Lock()
	fs.getErr = errors.New("locked")
	fs.mu.Unlock()
	s.tick()
	if got := s.runsOf(id); len(got) != 0 {
		t.Fatalf("started with an unreadable pause: %+v", got)
	}
	fs.mu.Lock()
	fs.getErr = nil
	fs.mu.Unlock()
	s.step(30 * time.Second)
	s.clock.Set(time.Date(2026, time.November, 11, 9, 14, 40, 0, ist))
	s.tick()
	s.step(30 * time.Second)
	if got := s.runsOf(id); len(got) != 1 {
		t.Fatalf("runs once the pause was readable = %d, want 1", len(got))
	}
}

func TestSchedulerStatusFields(t *testing.T) {
	ctx := context.Background()
	ist := istanbul(t)
	start := time.Date(2026, time.November, 11, 8, 0, 0, 0, ist)
	s := newSim(t, ist, start, nil)
	s.sched.d.Tick = 30 * time.Second

	if st := s.sched.Status(); st.Paused || st.LastTick != nil || st.NextTick != nil {
		t.Fatalf("before the first look = %+v", st)
	}
	s.tick()
	st := s.sched.Status()
	if st.LastTick == nil || !st.LastTick.Equal(start) {
		t.Fatalf("LastTick = %v, want %v", st.LastTick, start)
	}
	if st.NextTick != nil {
		t.Errorf("NextTick = %v for a loop that is not running", st.NextTick)
	}

	// A running loop reports the next look; a paused one does not.
	s.sched.d.Tick = time.Hour
	s.sched.Start(ctx)
	defer s.sched.Stop()
	waitFor(t, "the first look of Start", func() bool { return s.sched.Status().NextTick != nil })
	st = s.sched.Status()
	if st.Paused || !st.NextTick.Equal(st.LastTick.Add(time.Hour)) {
		t.Errorf("running status = %+v", st)
	}
	if err := s.sched.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if st := s.sched.Status(); !st.Paused || st.NextTick != nil || st.LastTick == nil {
		t.Errorf("paused status = %+v", st)
	}
}

// --- Schedules: status, pause events and the tracked pull request list ---

type controlFake struct {
	paused bool
	err    error
}

func (c *controlFake) Pause(context.Context) error {
	if c.err == nil {
		c.paused = true
	}
	return c.err
}

func (c *controlFake) Resume(context.Context) error {
	if c.err == nil {
		c.paused = false
	}
	return c.err
}
func (c *controlFake) Paused() bool { return c.paused }
func (c *controlFake) Status() SchedulerSnapshot {
	return SchedulerSnapshot{Paused: c.paused}
}

type eventsFake struct {
	mu   sync.Mutex
	got  []string
	data []any
}

func (e *eventsFake) Emit(name string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got, e.data = append(e.got, name), append(e.data, payload)
}

func TestSchedulesSchedulerStatusState(t *testing.T) {
	ctx := context.Background()
	s, db, clock, _ := newSchedules(t) // Wednesday 08:00 Istanbul
	ctl := &controlFake{}
	s.d.Scheduler = ctl

	state := func() SchedulerState {
		t.Helper()
		st, err := s.SchedulerStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return st.State
	}
	if got := state(); got != SchedulerOutsideWindow {
		t.Errorf("with no job = %q, want outside_window", got)
	}
	saved, err := s.SaveBotJob(ctx, goodInput()) // Mon-Fri 09:30-17:45
	if err != nil {
		t.Fatal(err)
	}
	if got := state(); got != SchedulerOutsideWindow {
		t.Errorf("08:00 = %q, want outside_window", got)
	}
	clock.Set(time.Date(2026, time.November, 11, 9, 30, 0, 0, clock.loc))
	if got := state(); got != SchedulerRunning {
		t.Errorf("09:30 = %q, want running", got)
	}
	clock.Set(time.Date(2026, time.November, 11, 17, 45, 0, 0, clock.loc)) // End is exclusive
	if got := state(); got != SchedulerOutsideWindow {
		t.Errorf("17:45 = %q, want outside_window", got)
	}
	clock.Set(time.Date(2026, time.November, 14, 12, 0, 0, 0, clock.loc)) // Saturday
	if got := state(); got != SchedulerOutsideWindow {
		t.Errorf("Saturday = %q, want outside_window", got)
	}

	// A job that needs attention or is switched off does not count.
	clock.Set(time.Date(2026, time.November, 11, 10, 0, 0, 0, clock.loc))
	if _, err := s.SetJobEnabled(ctx, saved.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != SchedulerOutsideWindow {
		t.Errorf("disabled job = %q, want outside_window", got)
	}
	if _, err := s.SetJobEnabled(ctx, saved.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != SchedulerRunning {
		t.Errorf("re-enabled = %q, want running", got)
	}
	if err := db.SetAttention(ctx, saved.ID, "gh_not_authenticated", clock.Now()); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != SchedulerOutsideWindow {
		t.Errorf("job needing attention = %q, want outside_window", got)
	}

	// Paused wins over everything.
	_ = db.ClearAttention(ctx, saved.ID)
	ctl.paused = true
	if st, _ := s.SchedulerStatus(ctx); !st.Paused || st.State != SchedulerPaused {
		t.Errorf("paused = %+v", st)
	}
}

func TestSchedulesSetSchedulerPausedEmitsAndNotifies(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newSchedules(t)
	ctl, ev := &controlFake{}, &eventsFake{}
	s.d.Scheduler, s.d.Events = ctl, ev
	var seen []SchedulerStatus
	s.SubscribeScheduler(func(st SchedulerStatus) { seen = append(seen, st) })

	st, err := s.SetSchedulerPaused(ctx, true)
	if err != nil || !st.Paused || st.State != SchedulerPaused || !ctl.paused {
		t.Fatalf("pause = %+v, %v", st, err)
	}
	st, err = s.SetSchedulerPaused(ctx, false)
	if err != nil || st.Paused || ctl.paused {
		t.Fatalf("resume = %+v, %v", st, err)
	}
	if len(ev.got) != 2 || ev.got[0] != EventSchedulerStatus || ev.got[1] != EventSchedulerStatus {
		t.Errorf("events = %v", ev.got)
	}
	if p, ok := ev.data[0].(SchedulerStatus); !ok || !p.Paused {
		t.Errorf("first payload = %#v", ev.data[0])
	}
	if len(seen) != 2 || !seen[0].Paused || seen[1].Paused {
		t.Errorf("observers saw %+v", seen)
	}

	// A failure changes nothing and tells nobody.
	ctl.err = errors.New("boom")
	if _, err := s.SetSchedulerPaused(ctx, true); err == nil {
		t.Fatal("no error")
	} else if c, _ := runCode(err); c != run.CodeInternal {
		t.Errorf("code = %q, want internal", c)
	}
	if len(ev.got) != 2 || len(seen) != 2 {
		t.Error("a failed pause was announced")
	}

	// Without a scheduler the calls fail as internal rather than panic.
	s.d.Scheduler = nil
	if _, err := s.SchedulerStatus(ctx); err == nil {
		t.Error("SchedulerStatus without a scheduler succeeded")
	}
	if _, err := s.SetSchedulerPaused(ctx, true); err == nil {
		t.Error("SetSchedulerPaused without a scheduler succeeded")
	}
}

// trackedFake is a TrackedPRStore with a fixed set of open rows. That closed and
// merged pull requests are not returned is the store's job (the SQL filter,
// proved by TestScheduleTrackedPullRequests and TestTrackedPRsOverTheRealStore in
// the sqlite package); this fake only lets the service test check the mapping:
// one row per job and pull request, names, mode, the job's own reviewed head.
type trackedFake struct {
	port.TrackedPRStore
	rows  []trackedRow
	calls []string
}

type trackedRow struct {
	pr       port.TrackedPR
	jobID    int64
	reviewed string
}

func (f *trackedFake) ListOpen(_ context.Context, jobID int64, mode string, repoIDs []int64) ([]port.TrackedPR, error) {
	f.calls = append(f.calls, mode)
	in := map[int64]bool{}
	for _, id := range repoIDs {
		in[id] = true
	}
	out := []port.TrackedPR{}
	for _, r := range f.rows {
		if !in[r.pr.RepositoryID] { // the store's target filter; the open filter is the store's too
			continue
		}
		pr := r.pr
		pr.LastHeadOID, pr.LastRunAt = "", nil
		if r.jobID == jobID {
			pr.LastHeadOID = r.reviewed
			if r.reviewed != "" {
				at := time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC)
				pr.LastRunAt = &at
			}
		}
		out = append(out, pr)
	}
	return out, nil
}

func TestSchedulesTrackedPRsListsOpenPullRequestsPerJob(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newSchedules(t)
	repos := newMemStore()
	r1, _ := repos.Create(ctx, mustRepo("octo", "alpha"))
	r2, _ := repos.Create(ctx, mustRepo("octo", "beta"))
	s.d.Repos = repos
	first := time.Date(2026, time.November, 9, 8, 0, 0, 0, time.UTC)
	tr := &trackedFake{}
	s.d.Tracked = tr

	// No job: nothing, and the store is not asked.
	if got, err := s.TrackedPRs(ctx); err != nil || len(got) != 0 || got == nil {
		t.Fatalf("no job = %+v, %v", got, err)
	}

	in := goodInput()
	in.Targets = []int64{r1.ID}
	j1, err := s.SaveBotJob(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	in2 := goodInput()
	in2.Name, in2.Targets, in2.Enabled = "Beta watcher", []int64{r2.ID}, false
	j2, err := s.SaveBotJob(ctx, in2)
	if err != nil {
		t.Fatal(err)
	}
	tr.rows = []trackedRow{
		{pr: port.TrackedPR{RepositoryID: r1.ID, Number: 7, FirstSeenAt: first, State: "open"}, jobID: j1.ID, reviewed: "abc123"},
		{pr: port.TrackedPR{RepositoryID: r1.ID, Number: 8, FirstSeenAt: first, State: "open"}}, // never reviewed
		{pr: port.TrackedPR{RepositoryID: r2.ID, Number: 3, FirstSeenAt: first, State: "open"}},
	}

	got, err := s.TrackedPRs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(got), got)
	}
	a := got[0]
	if a.Repository != "octo/alpha" || a.Number != 7 || a.JobID != j1.ID || a.JobName != "Review requests" || a.Mode != job.ModePreview ||
		!a.JobEnabled || a.State != "open" || a.LastHeadOID != "abc123" || a.LastRunAt == nil || !a.FirstSeenAt.Equal(first) {
		t.Errorf("row 0 = %+v", a)
	}
	if b := got[1]; b.Number != 8 || b.LastHeadOID != "" || b.LastRunAt != nil {
		t.Errorf("row 1 (never reviewed) = %+v", b)
	}
	if c := got[2]; c.Repository != "octo/beta" || c.Number != 3 || c.JobID != j2.ID || c.JobEnabled || c.LastHeadOID != "" {
		t.Errorf("row 2 = %+v", c)
	}
	// A job's own review is not shown on another job's rows.
	if len(tr.calls) != 2 {
		t.Errorf("ListOpen called %d times, want once per job", len(tr.calls))
	}

	// A repository that is gone from the store is skipped, not an error.
	_ = repos.Delete(ctx, r2.ID)
	if got, err := s.TrackedPRs(ctx); err != nil || len(got) != 2 {
		t.Errorf("after deleting a repository = %d rows, %v", len(got), err)
	}
}

func mustRepo(owner, name string) repo.Repository { return repo.Repository{Owner: owner, Name: name} }

// gatedSettings holds the first write of the pause after it reached the store,
// so a test can place another call exactly between the write and the scheduler's
// own bookkeeping.
type gatedSettings struct {
	port.SettingsStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedSettings) Set(ctx context.Context, key, value string) error {
	err := g.SettingsStore.Set(ctx, key, value)
	if key == KeyPaused {
		g.once.Do(func() {
			close(g.entered)
			<-g.release
		})
	}
	return err
}

// A pause from the tray and a resume from the screen can arrive together. With
// the resume landing between the pause's store write and its bookkeeping, the
// scheduler's memory and the stored pause must still agree at the end (otherwise
// a restart would run a bot the user paused).
func TestSchedulerConcurrentPauseAndResumeKeepMemoryAndStoreTogether(t *testing.T) {
	ctx := context.Background()
	ist := istanbul(t)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 0, 0, 0, ist), nil)
	g := &gatedSettings{SettingsStore: s.db, entered: make(chan struct{}), release: make(chan struct{})}
	s.sched.d.Settings = g

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = s.sched.Pause(ctx) }()
	<-g.entered // the pause is in the store, not yet in memory
	go func() { defer wg.Done(); _ = s.sched.Resume(ctx) }()
	time.Sleep(50 * time.Millisecond) // the resume runs now, or waits for the pause
	close(g.release)
	wg.Wait()

	_, stored, _ := s.db.Get(ctx, KeyPaused)
	if s.sched.Paused() != stored {
		t.Fatalf("memory paused=%v, store has the pause=%v", s.sched.Paused(), stored)
	}
}

func TestSchedulerStatusVersionGrowsWithEveryChange(t *testing.T) {
	ctx := context.Background()
	ist := istanbul(t)
	s := newSim(t, ist, time.Date(2026, time.November, 11, 8, 0, 0, 0, ist), nil)
	v0 := s.sched.Status().Version
	_ = s.sched.Pause(ctx)
	v1 := s.sched.Status().Version
	_ = s.sched.Resume(ctx)
	v2 := s.sched.Status().Version
	if v0 >= v1 || v1 >= v2 {
		t.Errorf("versions = %d, %d, %d; want strictly growing", v0, v1, v2)
	}
	// A failed write is not a change.
	s.sched.d.Settings = &failingSettings{SettingsStore: s.db, setErr: errors.New("x")}
	_ = s.sched.Pause(ctx)
	if got := s.sched.Status().Version; got != v2 {
		t.Errorf("a failed pause moved the version to %d", got)
	}
}

// ListJobs fails after the pause was stored: the change still succeeds and is
// announced, with what the scheduler itself knows.
type listFailJobs struct {
	port.JobStore
	fail bool
}

func (l *listFailJobs) ListJobs(ctx context.Context) ([]port.JobRecord, error) {
	if l.fail {
		return nil, errors.New("database is locked")
	}
	return l.JobStore.ListJobs(ctx)
}

func TestSchedulesPauseIsAnnouncedEvenWhenTheStatusCannotBeRead(t *testing.T) {
	ctx := context.Background()
	s, db, _, _ := newSchedules(t)
	jobs := &listFailJobs{JobStore: db, fail: true}
	s.d.Jobs = jobs
	ctl, ev := &controlFake{}, &eventsFake{}
	s.d.Scheduler, s.d.Events = ctl, ev
	var seen []SchedulerStatus
	s.SubscribeScheduler(func(st SchedulerStatus) { seen = append(seen, st) })

	st, err := s.SetSchedulerPaused(ctx, true)
	if err != nil {
		t.Fatalf("a pause that was saved reported %v", err)
	}
	if !st.Paused || st.State != SchedulerPaused || !ctl.paused {
		t.Errorf("status = %+v", st)
	}
	if len(ev.got) != 1 || len(seen) != 1 || !seen[0].Paused {
		t.Errorf("announced events=%v observers=%+v", ev.got, seen)
	}
	st, err = s.SetSchedulerPaused(ctx, false)
	if err != nil || st.Paused || st.State == SchedulerPaused {
		t.Errorf("resume = %+v, %v", st, err)
	}
	if len(ev.got) != 2 {
		t.Errorf("the resume was not announced: %v", ev.got)
	}
}

func TestSchedulesStatusCarriesAttentionCountAndTheNewestBotRun(t *testing.T) {
	ctx := context.Background()
	s, db, clock, _ := newSchedules(t)
	s.d.Scheduler = &controlFake{}
	s.d.Runs = db

	if st, err := s.SchedulerStatus(ctx); err != nil || st.AttentionCount != 0 || st.LastRun != nil {
		t.Fatalf("empty = %+v, %v", st, err)
	}
	j1, err := s.SaveBotJob(ctx, goodInput())
	if err != nil {
		t.Fatal(err)
	}
	in := goodInput()
	in.Name = "second"
	j2, err := s.SaveBotJob(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	// A run of another kind of job is not the bot's last run.
	other, _ := db.CreateJob(ctx, port.JobRecord{Kind: "review.pr", Name: "x", Mode: "preview"})
	t0 := clock.Now()
	for _, r := range []port.RunRecord{
		{JobID: j1.ID, Trigger: "schedule", StartedAt: t0.Add(-3 * time.Hour), Status: "succeeded"},
		{JobID: j2.ID, Trigger: "schedule", StartedAt: t0.Add(-1 * time.Hour), Status: "failed"},
		{JobID: other.ID, Trigger: "manual", StartedAt: t0.Add(-5 * time.Minute), Status: "succeeded"},
	} {
		if _, err := db.CreateRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetAttention(ctx, j1.ID, "gh_not_authenticated", t0); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAttention(ctx, j2.ID, "cli_too_old", t0); err != nil {
		t.Fatal(err)
	}

	st, err := s.SchedulerStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.AttentionCount != 2 {
		t.Errorf("AttentionCount = %d, want 2", st.AttentionCount)
	}
	if st.LastRun == nil || st.LastRun.JobID != j2.ID || st.LastRun.Status != "failed" || !st.LastRun.StartedAt.Equal(t0.Add(-time.Hour)) {
		t.Errorf("LastRun = %+v, want the failed run of the second bot job", st.LastRun)
	}
}
