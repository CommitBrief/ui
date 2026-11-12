// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/port"
)

func setWorkers(db *memDB, n string) { _ = db.Set(context.Background(), "worker.count", n) }

func TestManualRunSucceedsAndStoresTheOutcome(t *testing.T) {
	kind := &fakeKind{exec: func(_ context.Context, _ run.Task, progress func(string)) (run.Outcome, error) {
		progress("reviewing 3 files")
		progress("done")
		return okOutcome(), nil
	}}
	r, db, rec := newRuns(t, kind)
	started := local(t, r, 1)
	if started.Status != run.StatusQueued || len(started.Tasks) != 1 || started.Trigger != run.TriggerManual || started.JobID != 0 {
		t.Fatalf("StartLocalReview returned %+v", started)
	}
	taskID := started.Tasks[0].ID

	waitFor(t, "the run to succeed", func() bool { return db.run(t, started.ID).Status == "succeeded" })

	got := db.task(t, taskID)
	if got.Status != "succeeded" || got.CLIVersion != "1.21.2" || got.HeadOID != "abc" || got.Verdict != "comment" ||
		got.ExitCode == nil || *got.ExitCode != 0 || got.CostUSD != 0.25 || got.FindingCount != 2 ||
		got.StartedAt == nil || got.FinishedAt == nil || got.LogPath == "" || got.Kind != "review.local" {
		t.Errorf("stored task = %+v", got)
	}
	if rr := db.run(t, started.ID); rr.CostUSD != 0.25 || rr.FinishedAt == nil || rr.ErrorCode != "" {
		t.Errorf("stored run = %+v", rr)
	}

	// Status events follow the state machine.
	waitFor(t, "the final task event", func() bool {
		s := rec.taskStatuses(taskID)
		return len(s) > 0 && s[len(s)-1] == run.StatusSucceeded
	})
	if s := rec.taskStatuses(taskID); len(s) != 2 || s[0] != run.StatusRunning || s[1] != run.StatusSucceeded {
		t.Errorf("task events = %v, want [running succeeded]", s)
	}
	if lines := rec.logLines(taskID); len(lines) != 2 || lines[0] != "reviewing 3 files" {
		t.Errorf("log events = %v", lines)
	}

	// The log reads back, and the structured log has the lifecycle.
	view, err := r.TaskLog(context.Background(), taskID)
	if err != nil || view.Text != "reviewing 3 files\ndone\n" || view.Truncated {
		t.Errorf("TaskLog = %+v, %v", view, err)
	}
	events, err := os.ReadFile(r.eventFile(taskID))
	if err != nil || !strings.Contains(string(events), `"msg":"task started"`) || !strings.Contains(string(events), `"msg":"task finished"`) {
		t.Errorf("structured log = %q, %v", events, err)
	}

	// GetRun carries the payload, ListRuns does not.
	full, err := r.GetRun(context.Background(), started.ID)
	if err != nil || len(full.Tasks) != 1 || len(full.Tasks[0].Findings) == 0 || full.Tasks[0].FindingCount != 2 {
		t.Errorf("GetRun = %+v, %v", full, err)
	}
	list, err := r.ListRuns(context.Background(), 0)
	if err != nil || len(list) != 1 || len(list[0].Tasks) != 1 || len(list[0].Tasks[0].Findings) != 0 || list[0].Tasks[0].FindingCount != 2 {
		t.Errorf("ListRuns = %+v, %v", list, err)
	}
}

func TestSecondTaskForTheSamePRWaitsOnTheLock(t *testing.T) {
	gate := make(chan struct{})
	var running, maxRunning atomic.Int32
	started := make(chan int64, 4)
	kind := &fakeKind{
		plan: func(_ context.Context, j job.Job) ([]run.Task, error) {
			// Both jobs name the same pull request, whatever repository ID.
			return []run.Task{{Kind: job.KindReviewLocal, Spec: json.RawMessage(`{}`),
				LockKeys: []string{run.LockKeyForPR("Acme/App", 7)}}}, nil
		},
		exec: func(ctx context.Context, tk run.Task, _ func(string)) (run.Outcome, error) {
			n := running.Add(1)
			if n > maxRunning.Load() {
				maxRunning.Store(n)
			}
			started <- tk.ID
			defer running.Add(-1)
			select {
			case <-gate:
				return okOutcome(), nil
			case <-ctx.Done():
				return run.Outcome{}, errcode.New(errcode.Canceled, "")
			}
		},
	}
	r, db, _ := newRuns(t, kind)
	setWorkers(db, "2") // a free worker exists: only the lock holds the second back

	first := local(t, r, 1)
	second := local(t, r, 2)
	t1, t2 := first.Tasks[0].ID, second.Tasks[0].ID

	if got := <-started; got != t1 {
		t.Fatalf("first started task = %d, want %d", got, t1)
	}
	// Give a wrongly released second task every chance to start.
	time.Sleep(100 * time.Millisecond)
	if s := db.task(t, t2).Status; s != "queued" {
		t.Fatalf("the second task for the same pull request is %q while the first runs, want queued", s)
	}
	if s := db.task(t, t1).Status; s != "running" {
		t.Fatalf("first task = %q, want running", s)
	}

	gate <- struct{}{} // finish the first
	if got := <-started; got != t2 {
		t.Fatalf("second started task = %d, want %d", got, t2)
	}
	gate <- struct{}{}
	waitFor(t, "both runs to succeed", func() bool {
		return db.run(t, first.ID).Status == "succeeded" && db.run(t, second.ID).Status == "succeeded"
	})
	if maxRunning.Load() != 1 {
		t.Errorf("%d tasks held the same lock at once", maxRunning.Load())
	}
}

func TestDifferentLocksRunInParallelUpToTheWorkerLimit(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan int64, 4)
	kind := &fakeKind{exec: func(ctx context.Context, tk run.Task, _ func(string)) (run.Outcome, error) {
		started <- tk.ID
		select {
		case <-gate:
			return okOutcome(), nil
		case <-ctx.Done():
			return run.Outcome{}, errcode.New(errcode.Canceled, "")
		}
	}}
	r, db, _ := newRuns(t, kind)

	// One worker (the default): the second task waits for a worker, not a lock.
	a, b := local(t, r, 1), local(t, r, 2)
	<-started
	time.Sleep(100 * time.Millisecond)
	if s := db.task(t, b.Tasks[0].ID).Status; s != "queued" {
		t.Fatalf("with one worker the second task is %q, want queued", s)
	}

	// Raising the setting takes effect for the next dispatch: a third trigger
	// starts the scan, and with two workers the second task goes too.
	setWorkers(db, "2")
	c := local(t, r, 3)
	<-started
	waitFor(t, "second task running", func() bool { return db.task(t, b.Tasks[0].ID).Status == "running" })
	if s := db.task(t, c.Tasks[0].ID).Status; s != "queued" {
		t.Errorf("third task = %q, want queued behind two workers", s)
	}
	close(gate)
	waitFor(t, "all runs to succeed", func() bool {
		return db.run(t, a.ID).Status == "succeeded" && db.run(t, b.ID).Status == "succeeded" && db.run(t, c.ID).Status == "succeeded"
	})
}

func TestCancelRunningTaskEndsCanceled(t *testing.T) {
	started := make(chan struct{})
	kind := &fakeKind{exec: func(ctx context.Context, _ run.Task, _ func(string)) (run.Outcome, error) {
		close(started)
		<-ctx.Done()
		// The adapter reports a stopped process however it likes; here as a
		// generic failure, to prove the service decides on the context.
		return run.Outcome{}, errcode.New(errcode.Unknown, "the process died")
	}}
	r, db, rec := newRuns(t, kind)
	st := local(t, r, 1)
	<-started

	if err := r.CancelTask(context.Background(), st.Tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to end", func() bool { return db.run(t, st.ID).Status == "canceled" })
	got := db.task(t, st.Tasks[0].ID)
	if got.Status != "canceled" || got.ErrorCode != "canceled" || got.FinishedAt == nil {
		t.Errorf("task = %+v", got)
	}
	if rr := db.run(t, st.ID); rr.ErrorCode != "canceled" {
		t.Errorf("run = %+v", rr)
	}
	waitFor(t, "the canceled event", func() bool {
		s := rec.taskStatuses(st.Tasks[0].ID)
		return len(s) == 2 && s[1] == run.StatusCanceled
	})

	// Canceling again, or canceling something that finished, is fine; an unknown
	// ID is not.
	if err := r.CancelTask(context.Background(), st.Tasks[0].ID); err != nil {
		t.Errorf("second cancel = %v", err)
	}
	if err := r.CancelTask(context.Background(), 999); !errors.Is(err, run.NewError(run.CodeTaskNotFound, "")) {
		t.Errorf("cancel of an unknown task = %v", err)
	}
	if err := r.CancelRun(context.Background(), 999); !errors.Is(err, run.NewError(run.CodeRunNotFound, "")) {
		t.Errorf("cancel of an unknown run = %v", err)
	}
}

func TestCancelQueuedTaskNeverRuns(t *testing.T) {
	gate := make(chan struct{})
	var executed atomic.Int32
	started := make(chan int64, 2)
	kind := &fakeKind{exec: func(ctx context.Context, tk run.Task, _ func(string)) (run.Outcome, error) {
		executed.Add(1)
		started <- tk.ID
		select {
		case <-gate:
			return okOutcome(), nil
		case <-ctx.Done():
			return run.Outcome{}, errcode.New(errcode.Canceled, "")
		}
	}}
	r, db, _ := newRuns(t, kind)
	first := local(t, r, 1)
	queued := local(t, r, 2) // one worker: it waits
	<-started

	if err := r.CancelRun(context.Background(), queued.ID); err != nil {
		t.Fatal(err)
	}
	if got := db.task(t, queued.Tasks[0].ID); got.Status != "canceled" || got.StartedAt != nil || got.FinishedAt == nil {
		t.Errorf("queued task after cancel = %+v", got)
	}
	if rr := db.run(t, queued.ID); rr.Status != "canceled" {
		t.Errorf("queued run after cancel = %+v", rr)
	}
	close(gate)
	waitFor(t, "the first run to succeed", func() bool { return db.run(t, first.ID).Status == "succeeded" })
	if executed.Load() != 1 {
		t.Errorf("Execute ran %d times, want 1 (the canceled task must never start)", executed.Load())
	}
}

func TestFailuresAreClassified(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		out        run.Outcome
		wantStatus string
		wantCode   string
	}{
		{"cli too old needs attention", errcode.New(errcode.CLITooOld, ""), run.Outcome{}, "attention", "cli_too_old"},
		{"gh not authenticated needs attention", errcode.New(errcode.GHNotAuthenticated, ""), run.Outcome{}, "attention", "gh_not_authenticated"},
		{"provider error fails", errcode.New(errcode.ProviderError, ""), run.Outcome{}, "failed", "provider_error"},
		{"timeout fails", errcode.New(errcode.Timeout, ""), run.Outcome{}, "failed", "timeout"},
		{"a plain error is unknown", errors.New("boom"), run.Outcome{}, "failed", "unknown"},
		{"a degraded review keeps its report", errcode.New(errcode.Degraded, ""),
			run.Outcome{Report: json.RawMessage(`{"content":"raw model text"}`), Findings: json.RawMessage(`[]`)}, "failed", "degraded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind := &fakeKind{exec: func(context.Context, run.Task, func(string)) (run.Outcome, error) { return tc.out, tc.err }}
			r, db, _ := newRuns(t, kind)
			st := local(t, r, 1)
			waitFor(t, "the run to end", func() bool { return run.Status(db.run(t, st.ID).Status).Terminal() })
			got := db.task(t, st.Tasks[0].ID)
			if got.Status != tc.wantStatus || got.ErrorCode != tc.wantCode {
				t.Errorf("task = %q / %q, want %q / %q", got.Status, got.ErrorCode, tc.wantStatus, tc.wantCode)
			}
			if rr := db.run(t, st.ID); rr.Status != tc.wantStatus || rr.ErrorCode != tc.wantCode {
				t.Errorf("run = %q / %q, want %q / %q", rr.Status, rr.ErrorCode, tc.wantStatus, tc.wantCode)
			}
			if len(tc.out.Report) > 0 && len(got.ReportJSON) == 0 {
				t.Error("the report of a degraded review was not stored")
			}
		})
	}
}

func TestAPanickingKindFailsTheTaskNotTheApp(t *testing.T) {
	kind := &fakeKind{exec: func(context.Context, run.Task, func(string)) (run.Outcome, error) { panic("kaboom") }}
	r, db, _ := newRuns(t, kind)
	st := local(t, r, 1)
	waitFor(t, "the run to end", func() bool { return db.run(t, st.ID).Status == "failed" })
	if got := db.task(t, st.Tasks[0].ID); got.ErrorCode != "unknown" {
		t.Errorf("task = %+v", got)
	}
	// The service still works.
	kind.exec = func(context.Context, run.Task, func(string)) (run.Outcome, error) { return okOutcome(), nil }
	again := local(t, r, 1)
	waitFor(t, "a later run to succeed", func() bool { return db.run(t, again.ID).Status == "succeeded" })
}

func TestLogsAreMasked(t *testing.T) {
	kind := &fakeKind{exec: func(_ context.Context, _ run.Task, progress func(string)) (run.Outcome, error) {
		progress("GH_TOKEN=ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789")
		progress("using key sk-ant-" + "api03-abcdefghijklmnopqrstuvwxyz")
		progress("Authorization: Bearer abcdefgh12345678")
		progress("input_tokens: 4231")
		return okOutcome(), nil
	}}
	r, db, rec := newRuns(t, kind)
	st := local(t, r, 1)
	waitFor(t, "the run to succeed", func() bool { return db.run(t, st.ID).Status == "succeeded" })
	id := st.Tasks[0].ID

	raw, err := os.ReadFile(r.logFile(id))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"ghp_" + "abcdef", "sk-ant-" + "api03", "abcdefgh12345678"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the log file contains %q:\n%s", secret, raw)
		}
		for _, line := range rec.logLines(id) {
			if strings.Contains(line, secret) {
				t.Errorf("an event carries %q: %q", secret, line)
			}
		}
	}
	if !strings.Contains(string(raw), "input_tokens: 4231") {
		t.Errorf("an ordinary line was masked:\n%s", raw)
	}
	if fi, err := os.Stat(r.logFile(id)); err != nil || fi.Mode().Perm()&0o077 != 0 && !onWindows() {
		t.Errorf("log file mode = %v, %v; want owner-only", fi.Mode().Perm(), err)
	}
}

func onWindows() bool { return os.PathSeparator == '\\' }

func TestLogFileIsCapped(t *testing.T) {
	const limit = 100 << 10
	line := strings.Repeat("x", 1000)
	kind := &fakeKind{exec: func(_ context.Context, _ run.Task, progress func(string)) (run.Outcome, error) {
		for i := 0; i < limit/len(line)+50; i++ {
			progress(line)
		}
		return okOutcome(), nil
	}}
	r, db, rec := newRuns(t, kind, func(d *RunsDeps) { d.MaxLogBytes = limit })
	st := local(t, r, 1)
	waitFor(t, "the run to succeed", func() bool { return db.run(t, st.ID).Status == "succeeded" })
	fi, err := os.Stat(r.logFile(st.Tasks[0].ID))
	if err != nil || fi.Size() > limit+200 {
		t.Errorf("log size = %v, %v; want at most the cap", fi, err)
	}
	view, err := r.TaskLog(context.Background(), st.Tasks[0].ID)
	if err != nil || len(view.Text) > maxLogReadBytes {
		t.Errorf("TaskLog = %d bytes, %v", len(view.Text), err)
	}
	if !strings.HasSuffix(strings.TrimSpace(view.Text), "[log truncated: size limit reached]") {
		t.Errorf("the notice is missing from the tail: %q", view.Text[len(view.Text)-80:])
	}
	if n := len(rec.logLines(st.Tasks[0].ID)); n > limit/len(line)+1 {
		t.Errorf("%d log events after the cap", n)
	}
}

func TestRetentionRemovesOldLogsKeepsResults(t *testing.T) {
	kind := &fakeKind{exec: func(_ context.Context, _ run.Task, progress func(string)) (run.Outcome, error) {
		progress("line")
		return okOutcome(), nil
	}}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	clock := &now
	r, db, _ := newRuns(t, kind, func(d *RunsDeps) {
		d.Now = func() time.Time { return *clock }
		d.KeepRuns = 3
	})
	_ = db.Set(context.Background(), "log.retention_days", "30")

	var ids []int64
	var runs []run.Run
	for i := 0; i < 2; i++ {
		st := local(t, r, 1)
		waitFor(t, "run to succeed", func() bool { return db.run(t, st.ID).Status == "succeeded" })
		ids, runs = append(ids, st.Tasks[0].ID), append(runs, st)
	}
	*clock = now.Add(40 * 24 * time.Hour) // both runs are now 40 days old
	fresh := local(t, r, 1)
	waitFor(t, "fresh run to succeed", func() bool { return db.run(t, fresh.ID).Status == "succeeded" })
	freshTask := fresh.Tasks[0].ID

	// Finishing the fresh run swept already; the old logs are gone, the fresh
	// one stays, and every result is intact.
	for _, id := range ids {
		if _, err := os.Stat(r.logFile(id)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("log of task %d survived retention: %v", id, err)
		}
		if _, err := os.Stat(r.eventFile(id)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("structured log of task %d survived retention: %v", id, err)
		}
		got := db.task(t, id)
		if got.LogPath != "" || got.Status != "succeeded" || got.FindingCount != 2 {
			t.Errorf("task %d after retention = %+v", id, got)
		}
		if _, err := r.TaskLog(context.Background(), id); !errors.Is(err, run.NewError(run.CodeLogUnavailable, "")) {
			t.Errorf("TaskLog of a pruned log = %v", err)
		}
	}
	if _, err := os.Stat(r.logFile(freshTask)); err != nil {
		t.Errorf("the fresh log was removed: %v", err)
	}
	_ = runs

	// By count: with KeepRuns=3 and recent runs, a fourth recent run pushes the
	// oldest out even though nothing is older than 30 days.
	for i := 0; i < 3; i++ {
		*clock = clock.Add(time.Hour)
		st := local(t, r, 1)
		waitFor(t, "run to succeed", func() bool { return db.run(t, st.ID).Status == "succeeded" })
	}
	if _, err := os.Stat(r.logFile(freshTask)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the log beyond the newest %d runs survived: %v", 3, err)
	}
}

func TestSweepNeverTouchesARunningTask(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{})
	kind := &fakeKind{exec: func(ctx context.Context, _ run.Task, progress func(string)) (run.Outcome, error) {
		progress("still going")
		close(started)
		<-gate
		return okOutcome(), nil
	}}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	clock := &now
	r, db, _ := newRuns(t, kind, func(d *RunsDeps) { d.Now = func() time.Time { return *clock } })
	st := local(t, r, 1)
	<-started
	*clock = now.Add(365 * 24 * time.Hour)
	r.Sweep(context.Background())
	if _, err := os.Stat(r.logFile(st.Tasks[0].ID)); err != nil {
		t.Errorf("a running task's log was removed: %v", err)
	}
	close(gate)
	waitFor(t, "the run to succeed", func() bool { return db.run(t, st.ID).Status == "succeeded" })
}

func TestStartMarksLeftoversCanceled(t *testing.T) {
	kind := &fakeKind{exec: func(context.Context, run.Task, func(string)) (run.Outcome, error) { return okOutcome(), nil }}
	r, db, _ := newRuns(t, kind)
	ctx := context.Background()
	leftover, _ := db.CreateRun(ctx, port.RunRecord{Trigger: "manual", StartedAt: time.Now(), Status: "running"})
	task, _ := db.CreateTask(ctx, port.TaskRecord{RunID: leftover.ID, Status: "running", QueuedAt: time.Now()})

	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if got := db.task(t, task.ID); got.Status != "canceled" || got.ErrorCode != "canceled" {
		t.Errorf("leftover task = %+v", got)
	}
	if got := db.run(t, leftover.ID); got.Status != "canceled" {
		t.Errorf("leftover run = %+v", got)
	}
}

func TestTriggerErrors(t *testing.T) {
	kind := &fakeKind{
		plan: func(_ context.Context, j job.Job) ([]run.Task, error) {
			if j.Targets[0] == 404 {
				return nil, run.NewError(run.CodeRepositoryNotFound, "targets")
			}
			return []run.Task{{Kind: job.KindReviewLocal, Spec: json.RawMessage(`{}`)}}, nil
		},
		exec: func(context.Context, run.Task, func(string)) (run.Outcome, error) { return okOutcome(), nil },
	}
	r, _, _ := newRuns(t, kind)
	ctx := context.Background()

	is := func(err error, code run.Code, field string) {
		t.Helper()
		var re *run.Error
		if !errors.As(err, &re) || re.Code != code || re.Field != field {
			t.Errorf("error = %v, want %s (%s)", err, code, field)
		}
	}

	_, err := r.StartLocalReview(ctx, LocalReviewInput{RepositoryID: 404, Mode: "staged"})
	is(err, run.CodeRepositoryNotFound, "targets")

	// A bad definition is job_invalid with the field.
	_, err = r.Trigger(ctx, job.Job{Kind: job.KindReviewLocal, Name: "", Spec: json.RawMessage(`{}`), Mode: job.ModePreview, Targets: []int64{1}}, run.TriggerManual)
	is(err, run.CodeJobInvalid, "name")
	_, err = r.Trigger(ctx, job.Job{Kind: job.KindReviewLocal, Name: "x", Spec: json.RawMessage(`{}`), Mode: job.ModePost, Targets: []int64{1}}, run.TriggerManual)
	is(err, run.CodeJobInvalid, "mode")
	_, err = r.Trigger(ctx, job.Job{Kind: job.KindReviewLocal, Name: "x", Spec: json.RawMessage(`{}`), Mode: job.ModePreview, Targets: []int64{1, 2}}, run.TriggerManual)
	is(err, run.CodeJobInvalid, "targets")

	// A kind nobody implemented.
	_, err = r.Trigger(ctx, job.Job{Kind: job.KindReviewPR, Name: "x", Spec: json.RawMessage(`{}`), Mode: job.ModePreview, Targets: []int64{1}}, run.TriggerManual)
	is(err, run.CodeJobKindUnknown, "kind")

	// A missing stored job.
	_, err = r.RunJob(ctx, 77)
	is(err, run.CodeJobNotFound, "")
}

func TestRunJobRunsAStoredJob(t *testing.T) {
	kind := &fakeKind{exec: func(context.Context, run.Task, func(string)) (run.Outcome, error) { return okOutcome(), nil }}
	r, db, _ := newRuns(t, kind)
	ctx := context.Background()
	jr, _ := db.CreateJob(ctx, port.JobRecord{
		Kind: "review.local", Name: "nightly", Spec: json.RawMessage(`{"mode":"staged"}`), SpecVersion: 1,
		Mode: "preview", Enabled: false, RepositoryIDs: []int64{5},
	})
	st, err := r.RunJob(ctx, jr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.JobID != jr.ID || st.Trigger != run.TriggerManual {
		t.Errorf("run = %+v", st)
	}
	waitFor(t, "the run to succeed", func() bool { return db.run(t, st.ID).Status == "succeeded" })
	if db.run(t, st.ID).JobID != jr.ID {
		t.Error("the run lost its job")
	}
}

func TestQueueIsBounded(t *testing.T) {
	gate := make(chan struct{})
	kind := &fakeKind{exec: func(ctx context.Context, _ run.Task, _ func(string)) (run.Outcome, error) {
		<-gate
		return okOutcome(), nil
	}}
	r, _, _ := newRuns(t, kind)
	defer close(gate)
	var full error
	for i := 0; i < maxQueuedTasks+5 && full == nil; i++ {
		_, full = r.StartLocalReview(context.Background(), LocalReviewInput{RepositoryID: 1, Mode: "staged"})
	}
	var re *run.Error
	if !errors.As(full, &re) || re.Code != run.CodeQueueFull {
		t.Errorf("an overfull queue answered %v, want run_queue_full", full)
	}
}

func TestCloseCancelsEverythingAndRefusesNewWork(t *testing.T) {
	started := make(chan struct{})
	kind := &fakeKind{exec: func(ctx context.Context, _ run.Task, _ func(string)) (run.Outcome, error) {
		close(started)
		<-ctx.Done()
		return run.Outcome{}, errcode.New(errcode.Canceled, "")
	}}
	r, db, _ := newRuns(t, kind)
	running := local(t, r, 1)
	queued := local(t, r, 2)
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for _, st := range []run.Run{running, queued} {
		if got := db.run(t, st.ID).Status; got != "canceled" {
			t.Errorf("run %d after Close = %q, want canceled", st.ID, got)
		}
	}
	_, err := r.StartLocalReview(context.Background(), LocalReviewInput{RepositoryID: 1, Mode: "staged"})
	var re *run.Error
	if !errors.As(err, &re) || re.Code != run.CodeShuttingDown {
		t.Errorf("Trigger after Close = %v", err)
	}
	if err := r.Close(ctx); err != nil {
		t.Errorf("second Close = %v", err)
	}
}

func TestTaskLogReadsTheTail(t *testing.T) {
	kind := &fakeKind{exec: func(context.Context, run.Task, func(string)) (run.Outcome, error) { return okOutcome(), nil }}
	r, db, _ := newRuns(t, kind)
	st := local(t, r, 1)
	waitFor(t, "the run to succeed", func() bool { return db.run(t, st.ID).Status == "succeeded" })
	id := st.Tasks[0].ID

	big := strings.Repeat("0123456789\n", maxLogReadBytes/11+1000)
	if err := os.WriteFile(r.logFile(id), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	view, err := r.TaskLog(context.Background(), id)
	if err != nil || !view.Truncated || len(view.Text) > maxLogReadBytes || !strings.HasPrefix(view.Text, "0123456789\n") {
		t.Errorf("TaskLog = truncated %v, %d bytes (%v)", view.Truncated, len(view.Text), err)
	}

	if _, err := r.TaskLog(context.Background(), 999); !errors.Is(err, run.NewError(run.CodeTaskNotFound, "")) {
		t.Errorf("TaskLog of an unknown task = %v", err)
	}
	// The path comes from the ID, not from the row: a row pointing elsewhere
	// reads the same file.
	rec := db.task(t, id)
	rec.LogPath = filepath.Join(t.TempDir(), "elsewhere.log")
	_ = db.UpdateTask(context.Background(), rec)
	if v, err := r.TaskLog(context.Background(), id); err != nil || v.Text != view.Text {
		t.Errorf("TaskLog followed the stored path: %v", err)
	}
}

// Raising the worker count while tasks wait starts them without any new trigger:
// the periodic rescan notices.
func TestRaisedWorkerCountStartsWaitingTasks(t *testing.T) {
	gate := make(chan struct{})
	kind := &fakeKind{exec: func(ctx context.Context, _ run.Task, _ func(string)) (run.Outcome, error) {
		select {
		case <-gate:
			return okOutcome(), nil
		case <-ctx.Done():
			return run.Outcome{}, errcode.New(errcode.Canceled, "")
		}
	}}
	r, db, _ := newRuns(t, kind, func(d *RunsDeps) { d.RescanEvery = 20 * time.Millisecond })
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, second := local(t, r, 1), local(t, r, 2)
	waitFor(t, "first task running", func() bool { return db.task(t, first.Tasks[0].ID).Status == "running" })
	time.Sleep(60 * time.Millisecond)
	if s := db.task(t, second.Tasks[0].ID).Status; s != "queued" {
		t.Fatalf("second task = %q with one worker", s)
	}
	setWorkers(db, "2")
	waitFor(t, "second task running after the setting changed", func() bool { return db.task(t, second.Tasks[0].ID).Status == "running" })
	close(gate)
}

// A job that plans nothing (a bot with no new pull requests) is a finished run
// with no tasks, and a task whose kind forgot to name itself is stored under the
// job's kind.
func TestEmptyPlanIsAFinishedRun(t *testing.T) {
	kind := &fakeKind{
		plan: func(_ context.Context, j job.Job) ([]run.Task, error) {
			if j.Targets[0] == 1 {
				return nil, nil
			}
			return []run.Task{{RepositoryID: j.Targets[0], Spec: json.RawMessage(`{}`)}}, nil // no Kind set
		},
		exec: func(context.Context, run.Task, func(string)) (run.Outcome, error) { return okOutcome(), nil },
	}
	r, db, _ := newRuns(t, kind)

	empty := local(t, r, 1)
	if empty.Status != run.StatusSucceeded || empty.FinishedAt == nil || len(empty.Tasks) != 0 {
		t.Errorf("empty run = %+v", empty)
	}
	if got := db.run(t, empty.ID); got.Status != "succeeded" || got.FinishedAt == nil {
		t.Errorf("stored empty run = %+v", got)
	}

	named := local(t, r, 2)
	waitFor(t, "the run to succeed", func() bool { return db.run(t, named.ID).Status == "succeeded" })
	if got := db.task(t, named.Tasks[0].ID); got.Kind != "review.local" {
		t.Errorf("stored kind = %q, want the job's kind", got.Kind)
	}
}
