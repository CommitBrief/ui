// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/domain/settings"
	"github.com/CommitBrief/ui/internal/port"
)

// Limits of the run service.
const (
	// maxQueuedTasks bounds the tasks that wait for a worker or a lock (the
	// architecture's backpressure example). It is checked before a run is
	// created; two triggers racing may overshoot it by one run.
	maxQueuedTasks = 50
	// DefaultKeepRuns is how many of the newest runs keep their log files when
	// the retention sweep runs (the "500 runs" half of the policy).
	DefaultKeepRuns = 500
	// maxLogLineBytes cuts a single stderr line; maxLogFileBytes caps a task's
	// log file, after which one notice is written and the rest is dropped.
	maxLogLineBytes = 4096
	maxLogFileBytes = 8 << 20
	// DefaultRescanEvery is the period of the waiting-task rescan.
	DefaultRescanEvery = 5 * time.Second
	// maxLogReadBytes is how much of a log the screen can read back (the tail).
	maxLogReadBytes = 256 << 10
	// dbTimeout bounds one store call made from a worker, which must not use
	// the (possibly canceled) task context.
	dbTimeout = 15 * time.Second
	// DefaultListLimit and maxListLimit bound ListRuns.
	DefaultListLimit = 100
	maxListLimit     = 500
)

// RunsDeps are what the run service needs.
type RunsDeps struct {
	// Kinds are the registered job kinds.
	Kinds *KindRegistry
	// Jobs and Runs are the stores; Tx writes a run and its tasks together.
	Jobs port.JobStore
	Runs port.RunStore
	Tx   port.TxRunner
	// Settings supplies the worker count and the log retention days; they are
	// read when needed, so a change in the Settings screen applies at once.
	Settings port.SettingsStore
	// Events may be nil: nothing is then sent to the frontend.
	Events port.EventEmitter
	// Logger receives the structured lifecycle log; nil means slog.Default().
	Logger *slog.Logger
	// LogDir holds the per-task log files. It must exist (platform.Dirs.Ensure).
	LogDir string
	// Env is the process environment, only used to learn which secret values
	// to mask in logs; may be nil.
	Env port.ProcessEnv
	// Now replaces time.Now (tests).
	Now func() time.Time
	// KeepRuns overrides DefaultKeepRuns (tests).
	KeepRuns int
	// MaxLogBytes overrides the per-task log file cap (tests).
	MaxLogBytes int64
	// RescanEvery is how often waiting tasks are looked at again even when
	// nothing finished, so a raised worker count takes effect without waiting
	// for the next run; zero means DefaultRescanEvery.
	RescanEvery time.Duration
}

// Runs is the use-case service behind manual jobs: it plans a job into tasks,
// queues them, runs them on a bounded worker pool under exclusion locks,
// supports cancellation, streams the logs and keeps the results.
//
// Every error it returns is a *run.Error.
type Runs struct {
	d    RunsDeps
	mask *Masker

	rootCtx    context.Context
	rootCancel context.CancelFunc

	sweeping sync.Mutex // held while a retention sweep runs

	mu     sync.Mutex
	queue  []*work             // waiting for a worker or a lock, oldest first
	active map[int64]*work     // running, by task ID
	locks  map[string]int64    // exclusion key -> task ID holding it
	runs   map[int64]*runState // runs that still have unfinished tasks
	closed bool
	wg     sync.WaitGroup // running executions
}

// work is a task on its way through the service.
type work struct {
	task   run.Task
	kind   port.JobKind
	ctx    context.Context
	cancel context.CancelFunc
}

// runState is the in-memory bookkeeping of a run with unfinished tasks.
type runState struct {
	rec     port.RunRecord
	pending int
	started bool
}

// NewRuns wires the service. It does no I/O; call Start once after the store is
// open.
func NewRuns(d RunsDeps) *Runs {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.KeepRuns <= 0 {
		d.KeepRuns = DefaultKeepRuns
	}
	if d.MaxLogBytes <= 0 {
		d.MaxLogBytes = maxLogFileBytes
	}
	if d.RescanEvery <= 0 {
		d.RescanEvery = DefaultRescanEvery
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runs{
		d:          d,
		mask:       NewMasker(d.Env),
		rootCtx:    ctx,
		rootCancel: cancel,
		active:     map[int64]*work{},
		locks:      map[string]int64{},
		runs:       map[int64]*runState{},
	}
}

// Start prepares the service after a launch: whatever the previous process left
// queued or running ended with it, so those tasks and runs are marked canceled,
// and the retention policy is applied once.
func (r *Runs) Start(ctx context.Context) error {
	n, err := r.d.Runs.AbortUnfinished(ctx, r.d.Now())
	if err != nil {
		return r.fail("abort unfinished tasks", err)
	}
	if n > 0 {
		r.d.Logger.Warn("tasks left unfinished by the previous run of the application were marked canceled", "tasks", n)
	}
	r.Sweep(ctx)
	go r.rescanLoop()
	return nil
}

// rescanLoop looks at the waiting tasks now and then: the worker count is a
// setting that can be raised while tasks wait, and nothing else would notice.
func (r *Runs) rescanLoop() {
	t := time.NewTicker(r.d.RescanEvery)
	defer t.Stop()
	for {
		select {
		case <-r.rootCtx.Done():
			return
		case <-t.C:
			if r.queueDepth() > 0 {
				r.dispatch()
			}
		}
	}
}

// Close stops the service for good: no new work is accepted, queued tasks are
// canceled, running ones get their context canceled (the CLI adapter then stops
// the process tree) and are waited for until ctx ends. What does not finish in
// time is marked canceled by the next Start.
func (r *Runs) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	queued := r.queue
	r.queue = nil
	r.mu.Unlock()

	r.rootCancel()
	for _, w := range queued {
		r.settle(w, taskResult{status: run.StatusCanceled, code: errcode.Canceled}, nil)
	}
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("app: tasks still running at shutdown: %w", ctx.Err())
	}
}

// ----- starting work -----

// LocalReviewInput asks for a review of the changes in a repository's checkout.
type LocalReviewInput struct {
	RepositoryID int64 `json:"repositoryId"`
	// Mode is "staged", "unstaged" or "range".
	Mode string `json:"mode"`
	// Range holds the `git diff` revision arguments of a "range" review.
	Range []string `json:"range"`
}

// PRPreviewInput asks for a preview review of one pull request.
type PRPreviewInput struct {
	RepositoryID int64 `json:"repositoryId"`
	Number       int   `json:"number"`
}

// StartLocalReview queues a local review (manual trigger).
func (r *Runs) StartLocalReview(ctx context.Context, in LocalReviewInput) (run.Run, error) {
	spec := job.LocalReviewSpec{Mode: in.Mode, Range: in.Range}
	return r.Trigger(ctx, job.Job{
		Kind:        job.KindReviewLocal,
		Name:        "Local review",
		Spec:        spec.Marshal(),
		SpecVersion: job.CurrentSpecV1,
		Mode:        job.ModePreview,
		Enabled:     true,
		Targets:     []int64{in.RepositoryID},
	}, run.TriggerManual)
}

// StartPRPreview queues a pull request preview review (manual trigger): the
// review runs with --no-post and its result stays in the UI.
func (r *Runs) StartPRPreview(ctx context.Context, in PRPreviewInput) (run.Run, error) {
	spec := job.PRReviewSpec{Number: in.Number}
	return r.Trigger(ctx, job.Job{
		Kind:        job.KindReviewPR,
		Name:        "Pull request preview",
		Spec:        spec.Marshal(),
		SpecVersion: job.CurrentSpecV1,
		Mode:        job.ModePreview,
		Enabled:     true,
		Targets:     []int64{in.RepositoryID},
	}, run.TriggerManual)
}

// RunJob queues a run of a stored job (manual trigger), whether or not the job
// is enabled: "enabled" governs the scheduler, not the user's own click.
func (r *Runs) RunJob(ctx context.Context, jobID int64) (run.Run, error) {
	rec, err := r.d.Jobs.GetJob(ctx, jobID)
	switch {
	case errors.Is(err, port.ErrNotFound):
		return run.Run{}, run.NewError(run.CodeJobNotFound, "")
	case err != nil:
		return run.Run{}, r.fail("read job", err)
	}
	return r.Trigger(ctx, jobFromRecord(rec), run.TriggerManual)
}

// Trigger validates a job, plans it into tasks, stores the run with its tasks
// and queues them. A job with ID 0 is a one-off (no stored job). The returned
// run is the stored one, its tasks queued.
func (r *Runs) Trigger(ctx context.Context, j job.Job, trigger run.Trigger) (run.Run, error) {
	if !trigger.Valid() {
		return run.Run{}, r.fail("trigger", fmt.Errorf("unknown trigger %q", trigger))
	}
	if r.isClosed() {
		return run.Run{}, run.NewError(run.CodeShuttingDown, "")
	}
	kind, ok := r.d.Kinds.Get(j.Kind)
	if !ok {
		return run.Run{}, run.NewError(run.CodeJobKindUnknown, "kind")
	}
	if err := j.Validate(); err != nil {
		return run.Run{}, r.fail("validate job", err)
	}
	if err := kind.Validate(j.Spec); err != nil {
		return run.Run{}, r.fail("validate job spec", err)
	}
	planned, err := kind.Plan(ctx, j)
	if err != nil {
		return run.Run{}, r.fail("plan job", err)
	}
	if r.queueDepth()+len(planned) > maxQueuedTasks {
		return run.Run{}, run.NewError(run.CodeQueueFull, "")
	}

	now := r.d.Now().UTC().Truncate(time.Millisecond)
	runRec := port.RunRecord{JobID: j.ID, Trigger: string(trigger), StartedAt: now, Status: string(run.StatusQueued)}
	if len(planned) == 0 {
		// Nothing to do (a bot with no new pull requests): the run is recorded
		// as done at once.
		runRec.Status, runRec.FinishedAt = string(run.StatusSucceeded), &now
	}
	var taskRecs []port.TaskRecord
	err = r.d.Tx.InTx(ctx, func(tx port.Tx) error {
		created, err := tx.Runs().CreateRun(ctx, runRec)
		if err != nil {
			return err
		}
		runRec = created
		taskRecs = taskRecs[:0]
		for i := range planned {
			if planned[i].Kind == "" {
				planned[i].Kind = j.Kind
			}
			rec := taskRecordOf(planned[i])
			rec.RunID, rec.Status, rec.QueuedAt = runRec.ID, string(run.StatusQueued), now
			rec, err := tx.Runs().CreateTask(ctx, rec)
			if err != nil {
				return err
			}
			taskRecs = append(taskRecs, rec)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return run.Run{}, run.NewError(run.CodeRepositoryNotFound, "targets")
		}
		return run.Run{}, r.fail("store run", err)
	}

	out := runFromRecord(runRec)
	works := make([]*work, 0, len(planned))
	for i, t := range planned {
		t = taskFromRecord(taskRecs[i], t.LockKeys)
		out.Tasks = append(out.Tasks, t)
		works = append(works, r.newWork(t, kind))
	}
	r.d.Logger.Info("run queued", "run", runRec.ID, "job", j.ID, "kind", j.Kind, "trigger", trigger, "tasks", len(planned))
	if len(works) > 0 {
		if !r.enqueue(runRec, works) {
			// The service closed between the checks and now: the rows exist,
			// so settle them instead of leaving them queued.
			for _, w := range works {
				r.settle(w, taskResult{status: run.StatusCanceled, code: errcode.Canceled}, nil)
			}
			r.finalizeRun(runRec.ID) // the run was never registered, so settle could not
			return run.Run{}, run.NewError(run.CodeShuttingDown, "")
		}
		r.emitRun(runRec.ID, run.StatusQueued, "")
		r.dispatch()
	}
	return out, nil
}

func (r *Runs) newWork(t run.Task, kind port.JobKind) *work {
	ctx, cancel := context.WithCancel(r.rootCtx)
	return &work{task: t, kind: kind, ctx: ctx, cancel: cancel}
}

func (r *Runs) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *Runs) queueDepth() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.queue)
}

// enqueue registers the run and appends its tasks, unless the service closed.
func (r *Runs) enqueue(rec port.RunRecord, works []*work) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.runs[rec.ID] = &runState{rec: rec, pending: len(works)}
	r.queue = append(r.queue, works...)
	return true
}

// ----- scheduling -----

// workerLimit is how many tasks may run at once: the setting, read now.
func (r *Runs) workerLimit() int {
	ctx, cancel := r.dbCtx()
	defer cancel()
	rows, err := r.d.Settings.All(ctx)
	if err != nil {
		r.d.Logger.Warn("reading the worker count failed; using the default", "error", err)
		return settings.DefaultWorkers
	}
	return settings.FromStored(rows).Workers
}

// dispatch starts every queued task that has a free worker and free locks.
func (r *Runs) dispatch() {
	if r.isClosed() {
		return
	}
	limit := r.workerLimit() // store access happens before the lock is taken
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	for i := 0; i < len(r.queue) && len(r.active) < limit; {
		w := r.queue[i]
		if !r.locksFree(w) {
			i++ // waits for its lock; later tasks may overtake it
			continue
		}
		r.queue = append(r.queue[:i], r.queue[i+1:]...)
		for _, k := range w.task.LockKeys {
			r.locks[k] = w.task.ID
		}
		r.active[w.task.ID] = w
		r.wg.Add(1)
		go r.execute(w)
	}
}

func (r *Runs) locksFree(w *work) bool {
	for _, k := range w.task.LockKeys {
		if _, held := r.locks[k]; held {
			return false
		}
	}
	return true
}

// ----- executing -----

// taskResult is how a task ended.
type taskResult struct {
	status  run.Status
	code    errcode.Code
	outcome run.Outcome
}

func (r *Runs) execute(w *work) {
	defer r.wg.Done()
	tl := r.openTaskLog(w.task)
	r.markRunning(w, tl)

	progress := func(line string) { r.logLine(w, tl, line) }
	out, err := r.runKind(w, progress)

	status, code := run.Classify(err)
	if err != nil && status != run.StatusCanceled && w.ctx.Err() != nil {
		// Stopped from outside: whatever the adapter made of the dying process,
		// the task was canceled.
		status, code = run.StatusCanceled, errcode.Canceled
	}
	if err != nil {
		tl.event(slog.LevelWarn, "task failed", "error", r.mask.Mask(err.Error()))
	}
	r.settle(w, taskResult{status: status, code: code, outcome: out}, tl)
}

// runKind calls Execute and turns a panic into an unknown failure: a bug in one
// kind must not take the application down with it.
func (r *Runs) runKind(w *work, progress func(string)) (out run.Outcome, err error) {
	defer func() {
		if p := recover(); p != nil {
			r.d.Logger.Error("a job kind panicked", "kind", w.task.Kind, "task", w.task.ID, "panic", r.mask.Mask(fmt.Sprint(p)))
			out, err = run.Outcome{}, errcode.New(errcode.Unknown, "the job kind panicked")
		}
	}()
	return w.kind.Execute(w.ctx, w.task, progress)
}

// markRunning records that the task started and, for the first task of a run,
// that the run did.
func (r *Runs) markRunning(w *work, tl *taskLog) {
	now := r.d.Now().UTC().Truncate(time.Millisecond)
	w.task.Status = run.StatusRunning
	w.task.StartedAt = &now
	if tl != nil {
		w.task.LogPath = tl.path
		w.task.HasLog = true
	}
	r.persistTask(w.task)

	r.mu.Lock()
	rs := r.runs[w.task.RunID]
	first := rs != nil && !rs.started
	var runRec port.RunRecord
	if first {
		rs.started = true
		rs.rec.Status = string(run.StatusRunning)
		runRec = rs.rec
	}
	r.mu.Unlock()

	if first {
		r.persistRun(runRec)
		r.emitRun(w.task.RunID, run.StatusRunning, "")
	}
	r.emitTask(w.task, run.StatusRunning, "")
	tl.event(slog.LevelInfo, "task started", "run", w.task.RunID, "task", w.task.ID, "kind", w.task.Kind,
		"repository", w.task.RepositoryID, "pr", w.task.PRNumber)
	r.d.Logger.Info("task started", "run", w.task.RunID, "task", w.task.ID, "kind", w.task.Kind)
}

// settle ends a task: it stores the result, frees its locks, finishes the run
// when it was the last one, and lets the next queued task start. tl is nil for a
// task that never started.
func (r *Runs) settle(w *work, res taskResult, tl *taskLog) {
	now := r.d.Now().UTC().Truncate(time.Millisecond)
	t := &w.task
	t.Status, t.ErrorCode = res.status, res.code
	t.ExitCode = res.outcome.ExitCode
	t.CLIVersion = res.outcome.CLIVersion
	t.HeadOID = res.outcome.HeadOID
	t.Verdict = res.outcome.Verdict
	t.Findings = res.outcome.Findings
	t.Report = res.outcome.Report
	t.CostUSD = res.outcome.CostUSD
	t.FinishedAt = &now
	r.persistTask(*t)

	tl.event(slog.LevelInfo, "task finished", "run", t.RunID, "task", t.ID, "status", res.status,
		"error_code", res.code, "cost_usd", res.outcome.CostUSD, "cli_version", res.outcome.CLIVersion)
	tl.close()
	r.d.Logger.Info("task finished", "run", t.RunID, "task", t.ID, "status", res.status, "error_code", res.code)

	r.mu.Lock()
	delete(r.active, t.ID)
	for _, k := range t.LockKeys {
		if r.locks[k] == t.ID {
			delete(r.locks, k)
		}
	}
	rs := r.runs[t.RunID]
	runDone := false
	if rs != nil {
		rs.pending--
		if rs.pending <= 0 {
			runDone = true
			delete(r.runs, t.RunID)
		}
	}
	r.mu.Unlock()

	r.emitTask(*t, res.status, res.code)
	if runDone {
		r.finalizeRun(t.RunID)
	}
	r.dispatch()
	if runDone && r.rootCtx.Err() == nil {
		r.Sweep(r.rootCtx)
	}
}

// finalizeRun derives the run's end state from its tasks and stores it.
func (r *Runs) finalizeRun(runID int64) {
	ctx, cancel := r.dbCtx()
	defer cancel()
	recs, err := r.d.Runs.ListTasks(ctx, runID, false)
	if err != nil {
		r.d.Logger.Error("reading the tasks of a finished run failed", "run", runID, "error", err)
		return
	}
	tasks := make([]run.Task, 0, len(recs))
	statuses := make([]run.Status, 0, len(recs))
	for _, rec := range recs {
		t := taskFromRecord(rec, nil)
		tasks = append(tasks, t)
		statuses = append(statuses, t.Status)
	}
	rec, err := r.d.Runs.GetRun(ctx, runID)
	if err != nil {
		r.d.Logger.Error("reading a finished run failed", "run", runID, "error", err)
		return
	}
	now := r.d.Now().UTC().Truncate(time.Millisecond)
	status := run.Aggregate(statuses)
	code := run.RunErrorCode(tasks)
	rec.Status, rec.ErrorCode, rec.CostUSD, rec.FinishedAt = string(status), string(code), run.TotalCost(tasks), &now
	if err := r.d.Runs.UpdateRun(ctx, rec); err != nil {
		r.d.Logger.Error("storing a finished run failed", "run", runID, "error", err)
		return
	}
	r.emitRun(runID, status, code)
	r.d.Logger.Info("run finished", "run", runID, "status", status, "error_code", code, "cost_usd", rec.CostUSD)
}

// ----- cancelling -----

// CancelTask cancels one task. A queued task is canceled at once; a running one
// has its context canceled, which makes the CLI adapter stop the whole process
// tree, and ends canceled when it has stopped. Canceling a task that already
// finished is not an error.
func (r *Runs) CancelTask(ctx context.Context, taskID int64) error {
	if r.cancelWhere(func(w *work) bool { return w.task.ID == taskID }) {
		return nil
	}
	if _, err := r.d.Runs.GetTask(ctx, taskID); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return run.NewError(run.CodeTaskNotFound, "")
		}
		return r.fail("read task", err)
	}
	return nil // known, and not in flight: already finished
}

// CancelRun cancels every unfinished task of a run. Canceling a finished run is
// not an error.
func (r *Runs) CancelRun(ctx context.Context, runID int64) error {
	if r.cancelWhere(func(w *work) bool { return w.task.RunID == runID }) {
		return nil
	}
	if _, err := r.d.Runs.GetRun(ctx, runID); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return run.NewError(run.CodeRunNotFound, "")
		}
		return r.fail("read run", err)
	}
	return nil
}

// cancelWhere cancels the queued and running tasks that match and reports
// whether there were any.
func (r *Runs) cancelWhere(match func(*work) bool) bool {
	r.mu.Lock()
	var queued []*work
	kept := r.queue[:0:0]
	for _, w := range r.queue {
		if match(w) {
			queued = append(queued, w)
		} else {
			kept = append(kept, w)
		}
	}
	r.queue = kept
	var running []*work
	for _, w := range r.active {
		if match(w) {
			running = append(running, w)
		}
	}
	r.mu.Unlock()

	for _, w := range running {
		w.cancel() // execute settles it as canceled once the process is gone
	}
	for _, w := range queued {
		w.cancel()
		r.settle(w, taskResult{status: run.StatusCanceled, code: errcode.Canceled}, nil)
	}
	return len(queued)+len(running) > 0
}

// ----- reading -----

// ListRuns returns the newest runs first, each with its tasks but without the
// findings and report payloads (those come with GetRun).
func (r *Runs) ListRuns(ctx context.Context, limit int) ([]run.Run, error) {
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	recs, err := r.d.Runs.ListRuns(ctx, 0, limit)
	if err != nil {
		return nil, r.fail("list runs", err)
	}
	out := make([]run.Run, 0, len(recs))
	for _, rec := range recs {
		v, err := r.loadRun(ctx, rec, false)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// GetRun returns one run with its tasks, findings and report included.
func (r *Runs) GetRun(ctx context.Context, id int64) (run.Run, error) {
	rec, err := r.d.Runs.GetRun(ctx, id)
	switch {
	case errors.Is(err, port.ErrNotFound):
		return run.Run{}, run.NewError(run.CodeRunNotFound, "")
	case err != nil:
		return run.Run{}, r.fail("read run", err)
	}
	return r.loadRun(ctx, rec, true)
}

func (r *Runs) loadRun(ctx context.Context, rec port.RunRecord, payload bool) (run.Run, error) {
	tasks, err := r.d.Runs.ListTasks(ctx, rec.ID, payload)
	if err != nil {
		return run.Run{}, r.fail("list tasks", err)
	}
	out := runFromRecord(rec)
	for _, t := range tasks {
		out.Tasks = append(out.Tasks, taskFromRecord(t, nil))
	}
	return out, nil
}

// TaskLogView is the tail of a task's log.
type TaskLogView struct {
	// Text is the masked stderr of the task, the last 256 KiB at most.
	Text string `json:"text"`
	// Truncated is true when earlier lines were left out.
	Truncated bool `json:"truncated"`
}

// TaskLog reads back the log of a task. The file name is derived from the task
// ID, never taken from the database, so a damaged row cannot point it at
// another file.
func (r *Runs) TaskLog(ctx context.Context, taskID int64) (TaskLogView, error) {
	if _, err := r.d.Runs.GetTask(ctx, taskID); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return TaskLogView{}, run.NewError(run.CodeTaskNotFound, "")
		}
		return TaskLogView{}, r.fail("read task", err)
	}
	f, err := os.Open(r.logFile(taskID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TaskLogView{}, run.NewError(run.CodeLogUnavailable, "")
		}
		return TaskLogView{}, r.fail("open task log", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return TaskLogView{}, r.fail("stat task log", err)
	}
	view := TaskLogView{}
	if size := info.Size(); size > maxLogReadBytes {
		if _, err := f.Seek(size-maxLogReadBytes, io.SeekStart); err != nil {
			return TaskLogView{}, r.fail("seek task log", err)
		}
		view.Truncated = true
	}
	b, err := io.ReadAll(io.LimitReader(f, maxLogReadBytes))
	if err != nil {
		return TaskLogView{}, r.fail("read task log", err)
	}
	text := string(b)
	if view.Truncated {
		// The cut may fall inside a line: drop the partial first one.
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	view.Text = text
	return view, nil
}

// ----- retention -----

// Sweep applies the log retention policy: the log files of finished tasks whose
// run is older than the retention days (a UI setting), or is not among the
// newest KeepRuns runs, are deleted and forgotten. The results (findings,
// report, cost) stay in the database; only the raw logs are pruned. Failures
// are logged, never returned: a sweep is housekeeping.
func (r *Runs) Sweep(ctx context.Context) {
	if !r.sweeping.TryLock() {
		return
	}
	defer r.sweeping.Unlock()

	rows, err := r.d.Settings.All(ctx)
	if err != nil {
		r.d.Logger.Warn("log retention: reading the settings failed", "error", err)
		return
	}
	days := settings.FromStored(rows).LogRetentionDays
	cutoff := r.d.Now().Add(-time.Duration(days) * 24 * time.Hour)
	refs, err := r.d.Runs.ExpiredLogs(ctx, cutoff, r.d.KeepRuns)
	if err != nil {
		r.d.Logger.Warn("log retention: listing expired logs failed", "error", err)
		return
	}
	removed := 0
	for _, ref := range refs {
		ok := true
		for _, p := range []string{r.logFile(ref.TaskID), r.eventFile(ref.TaskID)} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				r.d.Logger.Warn("log retention: removing a log file failed", "task", ref.TaskID, "error", err)
				ok = false
			}
		}
		if !ok {
			continue // keep the row so the next sweep tries again
		}
		if err := r.d.Runs.ClearLog(ctx, ref.TaskID); err != nil && !errors.Is(err, port.ErrNotFound) {
			r.d.Logger.Warn("log retention: forgetting a log failed", "task", ref.TaskID, "error", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		r.d.Logger.Info("log retention removed expired logs", "tasks", removed, "retention_days", days, "keep_runs", r.d.KeepRuns)
	}
}

// ----- persistence helpers -----

func (r *Runs) dbCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), dbTimeout)
}

func (r *Runs) persistTask(t run.Task) {
	ctx, cancel := r.dbCtx()
	defer cancel()
	if err := r.d.Runs.UpdateTask(ctx, taskRecordOf(t)); err != nil {
		r.d.Logger.Error("storing a task failed", "task", t.ID, "status", t.Status, "error", err)
	}
}

func (r *Runs) persistRun(rec port.RunRecord) {
	ctx, cancel := r.dbCtx()
	defer cancel()
	if err := r.d.Runs.UpdateRun(ctx, rec); err != nil {
		r.d.Logger.Error("storing a run failed", "run", rec.ID, "status", rec.Status, "error", err)
	}
}

// ----- events -----

func (r *Runs) emitTask(t run.Task, status run.Status, code errcode.Code) {
	if r.d.Events != nil {
		r.d.Events.Emit(EventTaskStatus, TaskStatusEvent{RunID: t.RunID, TaskID: t.ID, Status: status, ErrorCode: code})
	}
}

func (r *Runs) emitRun(runID int64, status run.Status, code errcode.Code) {
	if r.d.Events != nil {
		r.d.Events.Emit(EventRunStatus, RunStatusEvent{RunID: runID, Status: status, ErrorCode: code})
	}
}

// ----- errors -----

// fail turns an internal error into the *run.Error the frontend sees. Domain
// errors pass through (a *job.Error becomes job_invalid with its field); the
// cause of anything unexpected goes to the log only.
func (r *Runs) fail(op string, err error) error {
	var re *run.Error
	if errors.As(err, &re) {
		return re
	}
	var je *job.Error
	if errors.As(err, &je) {
		return run.WrapError(run.CodeJobInvalid, je.Field, je)
	}
	if errors.Is(err, context.Canceled) {
		return run.WrapError(run.CodeCanceled, "", err)
	}
	r.d.Logger.Error("run service call failed", "op", op, "error", err)
	return run.WrapError(run.CodeInternal, "", err)
}

// logFile and eventFile name a task's raw stderr log and its structured
// (slog JSON) log inside the log directory.
func (r *Runs) logFile(taskID int64) string {
	return filepath.Join(r.d.LogDir, fmt.Sprintf("task-%d.log", taskID))
}

func (r *Runs) eventFile(taskID int64) string {
	return filepath.Join(r.d.LogDir, fmt.Sprintf("task-%d.jsonl", taskID))
}

// ----- record conversion -----

func jobFromRecord(rec port.JobRecord) job.Job {
	return job.Job{
		ID: rec.ID, Kind: job.Kind(rec.Kind), Name: rec.Name, Spec: rec.Spec, SpecVersion: rec.SpecVersion,
		Mode: job.Mode(rec.Mode), Enabled: rec.Enabled, Targets: rec.RepositoryIDs, CreatedAt: rec.CreatedAt,
	}
}

func runFromRecord(rec port.RunRecord) run.Run {
	return run.Run{
		ID: rec.ID, JobID: rec.JobID, Trigger: run.Trigger(rec.Trigger), Status: run.Status(rec.Status),
		ErrorCode: errcode.Code(rec.ErrorCode), CostUSD: rec.CostUSD, StartedAt: rec.StartedAt,
		FinishedAt: rec.FinishedAt, Tasks: []run.Task{},
	}
}

func taskFromRecord(rec port.TaskRecord, lockKeys []string) run.Task {
	return run.Task{
		ID: rec.ID, RunID: rec.RunID, Kind: job.Kind(rec.Kind), RepositoryID: rec.RepositoryID,
		PRNumber: rec.PRNumber, Spec: rec.Spec, Status: run.Status(rec.Status), ErrorCode: errcode.Code(rec.ErrorCode),
		ExitCode: rec.ExitCode, CLIVersion: rec.CLIVersion, HeadOID: rec.HeadOID, Verdict: rec.Verdict,
		Findings: rec.FindingsJSON, Report: rec.ReportJSON, FindingCount: rec.FindingCount,
		LogPath: rec.LogPath, HasLog: rec.LogPath != "", CostUSD: rec.CostUSD,
		QueuedAt: rec.QueuedAt, StartedAt: rec.StartedAt, FinishedAt: rec.FinishedAt, LockKeys: lockKeys,
	}
}

func taskRecordOf(t run.Task) port.TaskRecord {
	return port.TaskRecord{
		ID: t.ID, RunID: t.RunID, Kind: string(t.Kind), Spec: t.Spec, RepositoryID: t.RepositoryID,
		PRNumber: t.PRNumber, Status: string(t.Status), ErrorCode: string(t.ErrorCode), ExitCode: t.ExitCode,
		CLIVersion: t.CLIVersion, HeadOID: t.HeadOID, Verdict: t.Verdict, FindingsJSON: t.Findings,
		ReportJSON: t.Report, LogPath: t.LogPath, CostUSD: t.CostUSD, QueuedAt: t.QueuedAt,
		StartedAt: t.StartedAt, FinishedAt: t.FinishedAt,
	}
}
