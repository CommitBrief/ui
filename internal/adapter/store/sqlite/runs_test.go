// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/port"
)

func newRun(t *testing.T, s *Store, at time.Time, status string) port.RunRecord {
	t.Helper()
	r, err := s.Runs().CreateRun(context.Background(), port.RunRecord{Trigger: "manual", StartedAt: at, Status: status})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func newTask(t *testing.T, s *Store, runID int64, status string, log string) port.TaskRecord {
	t.Helper()
	k, err := s.Runs().CreateTask(context.Background(), port.TaskRecord{
		RunID: runID, Kind: "review.local", Spec: json.RawMessage(`{"mode":"staged"}`),
		Status: status, QueuedAt: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC), LogPath: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestRunAndTaskRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	repo, _ := s.Repos().Create(ctx, newRepo("a", "one"))

	start := time.Date(2026, 11, 11, 10, 0, 0, 123_000_000, time.UTC)
	run := newRun(t, s, start, "queued")
	if run.ID == 0 || !run.StartedAt.Equal(start) {
		t.Fatalf("CreateRun = %+v", run)
	}

	task, err := s.Runs().CreateTask(ctx, port.TaskRecord{
		RunID: run.ID, Kind: "review.pr", Spec: json.RawMessage(`{"number":7,"mode":"preview"}`),
		RepositoryID: repo.ID, PRNumber: 7, Status: "queued", QueuedAt: start,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Runs().GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "review.pr" || got.RepositoryID != repo.ID || got.PRNumber != 7 || got.Status != "queued" ||
		string(got.Spec) != `{"number":7,"mode":"preview"}` || got.ExitCode != nil || got.StartedAt != nil || got.FindingCount != 0 {
		t.Errorf("fresh task = %+v", got)
	}

	// A running task gains everything it will report.
	exit := 0
	started, finished := start.Add(time.Second), start.Add(time.Minute)
	got.Status = "succeeded"
	got.ExitCode = &exit
	got.CLIVersion = "1.21.2"
	got.HeadOID = "abc123"
	got.Verdict = "comment"
	got.FindingsJSON = json.RawMessage(`[{"title":"a"},{"title":"b"}]`)
	got.ReportJSON = json.RawMessage(`{"content":"","findings":[{"title":"a"},{"title":"b"}]}`)
	got.LogPath = "/logs/task-1.log"
	got.CostUSD = 0.0632
	got.StartedAt, got.FinishedAt = &started, &finished
	if err := s.Runs().UpdateTask(ctx, got); err != nil {
		t.Fatal(err)
	}
	full, err := s.Runs().GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if full.Status != "succeeded" || full.ExitCode == nil || *full.ExitCode != 0 || full.CLIVersion != "1.21.2" ||
		full.HeadOID != "abc123" || full.Verdict != "comment" || full.FindingCount != 2 ||
		string(full.FindingsJSON) != `[{"title":"a"},{"title":"b"}]` || full.LogPath != "/logs/task-1.log" ||
		full.CostUSD != 0.0632 || full.StartedAt == nil || !full.StartedAt.Equal(started) ||
		full.FinishedAt == nil || !full.FinishedAt.Equal(finished) || len(full.ReportJSON) == 0 {
		t.Errorf("finished task = %+v", full)
	}

	// A listing without payload leaves the heavy columns out but keeps the count.
	light, err := s.Runs().ListTasks(ctx, run.ID, false)
	if err != nil || len(light) != 1 {
		t.Fatalf("ListTasks = %+v, %v", light, err)
	}
	if len(light[0].FindingsJSON) != 0 || len(light[0].ReportJSON) != 0 || light[0].FindingCount != 2 {
		t.Errorf("payload-less listing = %+v", light[0])
	}
	heavy, _ := s.Runs().ListTasks(ctx, run.ID, true)
	if len(heavy[0].FindingsJSON) == 0 || len(heavy[0].ReportJSON) == 0 {
		t.Errorf("listing with payload = %+v", heavy[0])
	}

	run.Status, run.CostUSD, run.ErrorCode = "failed", 0.5, "timeout"
	run.FinishedAt = &finished
	if err := s.Runs().UpdateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	gotRun, err := s.Runs().GetRun(ctx, run.ID)
	if err != nil || gotRun.Status != "failed" || gotRun.CostUSD != 0.5 || gotRun.ErrorCode != "timeout" ||
		gotRun.FinishedAt == nil || gotRun.JobID != 0 {
		t.Errorf("run after update = %+v, %v", gotRun, err)
	}

	if _, err := s.Runs().GetRun(ctx, 999); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("GetRun(999) = %v", err)
	}
	if _, err := s.Runs().GetTask(ctx, 999); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("GetTask(999) = %v", err)
	}
	if err := s.Runs().UpdateTask(ctx, port.TaskRecord{ID: 999, Status: "failed"}); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("UpdateTask(999) = %v", err)
	}
}

func TestListRunsNewestFirstAndBounded(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	base := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	j, _ := s.Jobs().CreateJob(ctx, port.JobRecord{Kind: "review.local", Name: "j", Spec: json.RawMessage(`{}`), Mode: "preview"})
	a := newRun(t, s, base, "succeeded")
	b, _ := s.Runs().CreateRun(ctx, port.RunRecord{JobID: j.ID, Trigger: "schedule", StartedAt: base.Add(time.Hour), Status: "failed"})
	c := newRun(t, s, base.Add(2*time.Hour), "queued")

	all, err := s.Runs().ListRuns(ctx, 0, 10)
	if err != nil || len(all) != 3 || all[0].ID != c.ID || all[1].ID != b.ID || all[2].ID != a.ID {
		t.Errorf("ListRuns = %+v, %v", all, err)
	}
	if got, _ := s.Runs().ListRuns(ctx, 0, 2); len(got) != 2 {
		t.Errorf("limit 2 returned %d", len(got))
	}
	if got, _ := s.Runs().ListRuns(ctx, j.ID, 10); len(got) != 1 || got[0].ID != b.ID || got[0].Trigger != "schedule" {
		t.Errorf("per-job listing = %+v", got)
	}
}

func TestAbortUnfinished(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	run := newRun(t, s, at, "running")
	queued := newTask(t, s, run.ID, "queued", "")
	running := newTask(t, s, run.ID, "running", "")
	done := newTask(t, s, run.ID, "succeeded", "")
	doneRun := newRun(t, s, at, "succeeded")

	n, err := s.Runs().AbortUnfinished(ctx, at.Add(time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("AbortUnfinished = %d, %v; want 2", n, err)
	}
	for _, id := range []int64{queued.ID, running.ID} {
		got, _ := s.Runs().GetTask(ctx, id)
		if got.Status != "canceled" || got.ErrorCode != "canceled" || got.FinishedAt == nil {
			t.Errorf("task %d = %+v", id, got)
		}
	}
	if got, _ := s.Runs().GetTask(ctx, done.ID); got.Status != "succeeded" {
		t.Errorf("a finished task was touched: %+v", got)
	}
	if got, _ := s.Runs().GetRun(ctx, run.ID); got.Status != "canceled" || got.FinishedAt == nil {
		t.Errorf("run = %+v", got)
	}
	if got, _ := s.Runs().GetRun(ctx, doneRun.ID); got.Status != "succeeded" {
		t.Errorf("a finished run was touched: %+v", got)
	}
}

func TestExpiredLogs(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	old := newRun(t, s, now.Add(-40*day), "succeeded")
	oldTask := newTask(t, s, old.ID, "succeeded", "/logs/old.log")
	mid := newRun(t, s, now.Add(-10*day), "succeeded")
	midTask := newTask(t, s, mid.ID, "succeeded", "/logs/mid.log")
	fresh := newRun(t, s, now.Add(-1*day), "succeeded")
	freshTask := newTask(t, s, fresh.ID, "succeeded", "/logs/fresh.log")
	running := newRun(t, s, now.Add(-50*day), "running")
	runningTask := newTask(t, s, running.ID, "running", "/logs/running.log")
	noLog := newTask(t, s, old.ID, "succeeded", "")

	ids := func(refs []port.TaskLogRef) map[int64]bool {
		out := map[int64]bool{}
		for _, r := range refs {
			out[r.TaskID] = true
		}
		return out
	}

	// By age: only what started before the cutoff, never a running task, never
	// a task without a log.
	byAge, err := s.Runs().ExpiredLogs(ctx, now.Add(-30*day), 500)
	if err != nil {
		t.Fatal(err)
	}
	got := ids(byAge)
	if len(got) != 1 || !got[oldTask.ID] {
		t.Errorf("by age: %v, want only the 40-day-old task", byAge)
	}
	if got[runningTask.ID] || got[noLog.ID] {
		t.Errorf("a running or log-less task was listed: %v", byAge)
	}

	// By count: with the 2 newest runs kept, everything older is expired.
	byCount, err := s.Runs().ExpiredLogs(ctx, now.Add(-365*day), 2)
	if err != nil {
		t.Fatal(err)
	}
	got = ids(byCount)
	// Newest by started_at: fresh (-1d), mid (-10d); old (-40d) and running (-50d) fall out.
	if len(got) != 1 || !got[oldTask.ID] || got[midTask.ID] || got[freshTask.ID] {
		t.Errorf("by count: %v", byCount)
	}

	if err := s.Runs().ClearLog(ctx, oldTask.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Runs().GetTask(ctx, oldTask.ID); got.LogPath != "" {
		t.Errorf("ClearLog left %q", got.LogPath)
	}
	if again, _ := s.Runs().ExpiredLogs(ctx, now.Add(-30*day), 500); len(again) != 0 {
		t.Errorf("a cleared log is listed again: %v", again)
	}
}

// Run, tasks and a job go in together through the transaction view.
func TestRunStoresShareATransaction(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	boom := errors.New("boom")
	err := s.InTx(ctx, func(tx port.Tx) error {
		r, err := tx.Runs().CreateRun(ctx, port.RunRecord{Trigger: "manual", StartedAt: s.now(), Status: "queued"})
		if err != nil {
			return err
		}
		if _, err := tx.Runs().CreateTask(ctx, port.TaskRecord{RunID: r.ID, Status: "queued", QueuedAt: s.now()}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx = %v", err)
	}
	var runs, tasks int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM runs").Scan(&runs)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&tasks)
	if runs != 0 || tasks != 0 {
		t.Errorf("a rolled back transaction left %d runs and %d tasks", runs, tasks)
	}
}

// Deleting a repository keeps the task history with the reference cleared.
func TestTaskSurvivesRepositoryDeletion(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	repo, _ := s.Repos().Create(ctx, newRepo("a", "one"))
	run := newRun(t, s, time.Now(), "succeeded")
	task, _ := s.Runs().CreateTask(ctx, port.TaskRecord{RunID: run.ID, RepositoryID: repo.ID, Status: "succeeded", QueuedAt: time.Now()})
	if err := s.Repos().Delete(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Runs().GetTask(ctx, task.ID)
	if err != nil || got.RepositoryID != 0 {
		t.Errorf("task after repository delete = %+v, %v", got, err)
	}
}

// The real migrations are reversible end to end, and 0002 adds the columns the
// task store reads.
func TestRealMigrationsAreReversible(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.migrateTo(ctx, ms, 1); err != nil {
		t.Fatalf("down to 1: %v", err)
	}
	rows, err := s.db.Query("SELECT kind FROM tasks")
	if err == nil {
		_ = rows.Close()
		t.Error("tasks.kind still exists after reversing 0002")
	}
	if err := s.migrateTo(ctx, ms, 0); err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	if err := s.migrateTo(ctx, ms, len(ms)); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if _, err := s.Runs().CreateRun(ctx, port.RunRecord{Trigger: "manual", StartedAt: time.Now(), Status: "queued"}); err != nil {
		t.Errorf("store unusable after the migration round trip: %v", err)
	}
}
