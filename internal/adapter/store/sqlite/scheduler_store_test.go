// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/schedule"
	"github.com/CommitBrief/ui/internal/port"
)

func TestScheduleWindowsTravelWithTheJob(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	r, _ := s.Repos().Create(ctx, newRepo("a", "one"))
	w1 := schedule.Window{Days: schedule.Weekdays, Start: 9*60 + 30, End: 17*60 + 45, Every: 15, TZ: "Europe/Istanbul"}
	w2 := schedule.Window{Days: schedule.Saturday | schedule.Sunday, Start: 600, End: 720, Every: 60, TZ: "America/New_York"}

	j, err := s.Jobs().CreateJob(ctx, port.JobRecord{
		Kind: "bot.review_requested", Name: "bot", Spec: json.RawMessage(`{}`), Mode: "preview", Enabled: true,
		RepositoryIDs: []int64{r.ID}, Windows: []schedule.Window{w1, w2},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Jobs().GetJob(ctx, j.ID)
	if err != nil || !reflect.DeepEqual(got.Windows, []schedule.Window{w1, w2}) {
		t.Fatalf("GetJob windows = %+v, %v", got.Windows, err)
	}
	list, _ := s.Jobs().ListJobs(ctx)
	if len(list) != 1 || !reflect.DeepEqual(list[0].Windows, []schedule.Window{w1, w2}) {
		t.Errorf("ListJobs windows = %+v", list)
	}

	// Update replaces the set; an empty set removes every window.
	got.Windows = []schedule.Window{w2}
	if err := s.Jobs().UpdateJob(ctx, got); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Jobs().GetJob(ctx, j.ID)
	if !reflect.DeepEqual(after.Windows, []schedule.Window{w2}) {
		t.Errorf("after update = %+v", after.Windows)
	}
	after.Windows = nil
	if err := s.Jobs().UpdateJob(ctx, after); err != nil {
		t.Fatal(err)
	}
	none, _ := s.Jobs().GetJob(ctx, j.ID)
	if none.Windows == nil || len(none.Windows) != 0 {
		t.Errorf("a job without windows must read back as an empty slice: %#v", none.Windows)
	}

	// A refused window leaves no half-written job.
	_, err = s.Jobs().CreateJob(ctx, port.JobRecord{
		Kind: "bot.review_requested", Name: "bad", Spec: json.RawMessage(`{}`), Mode: "preview",
		Windows: []schedule.Window{{Days: 0xff, Start: 0, End: 60, Every: 5, TZ: "UTC"}},
	})
	if err == nil {
		t.Fatal("days_mask 255 must be refused by the CHECK constraint")
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&n); err != nil || n != 1 {
		t.Errorf("jobs = %d, %v; the refused job was left behind", n, err)
	}

	// Deleting the job takes its windows with it.
	if err := s.Jobs().DeleteJob(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM schedules").Scan(&n); err != nil || n != 0 {
		t.Errorf("schedules left behind: %d, %v", n, err)
	}
}

func TestScheduleAttentionIsStoredAndNotTouchedByUpdate(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	j, _ := s.Jobs().CreateJob(ctx, port.JobRecord{Kind: "bot.review_requested", Name: "bot", Spec: json.RawMessage(`{}`), Mode: "preview", Enabled: true})

	at := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	if err := s.Jobs().SetAttention(ctx, j.ID, "gh_not_authenticated", at); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Jobs().GetJob(ctx, j.ID)
	if got.AttentionCode != "gh_not_authenticated" || got.AttentionAt == nil || !got.AttentionAt.Equal(at) {
		t.Fatalf("attention = %q %v", got.AttentionCode, got.AttentionAt)
	}

	got.Name = "renamed"
	if err := s.Jobs().UpdateJob(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, _ := s.Jobs().GetJob(ctx, j.ID)
	if again.AttentionCode != "gh_not_authenticated" {
		t.Errorf("UpdateJob changed the attention state: %q", again.AttentionCode)
	}

	if err := s.Jobs().ClearAttention(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	cleared, _ := s.Jobs().GetJob(ctx, j.ID)
	if cleared.AttentionCode != "" || cleared.AttentionAt != nil {
		t.Errorf("after clear = %q %v", cleared.AttentionCode, cleared.AttentionAt)
	}
	if err := s.Jobs().ClearAttention(ctx, j.ID); err != nil {
		t.Errorf("clearing twice: %v", err)
	}
	if err := s.Jobs().SetAttention(ctx, 999, "x", at); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("SetAttention on a missing job = %v", err)
	}
	if err := s.Jobs().ClearAttention(ctx, 999); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("ClearAttention on a missing job = %v", err)
	}
}

func TestScheduleTrackedPullRequests(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	r1, _ := s.Repos().Create(ctx, newRepo("a", "one"))
	r2, _ := s.Repos().Create(ctx, newRepo("a", "two"))
	t0 := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	ts := s.Tracked()
	mkJob := func(name, mode string) int64 {
		j, err := s.Jobs().CreateJob(ctx, port.JobRecord{
			Kind: "bot.review_requested", Name: name, Spec: json.RawMessage(`{}`), Mode: mode, Enabled: true,
			RepositoryIDs: []int64{r1.ID},
		})
		if err != nil {
			t.Fatal(err)
		}
		return j.ID
	}
	previewJob := mkJob("preview", "preview")
	postJob := mkJob("post", "post")

	if err := ts.Track(ctx, r1.ID, 7, "review_requested", t0); err != nil {
		t.Fatal(err)
	}
	if err := ts.Track(ctx, r1.ID, 9, "review_requested", t0); err != nil {
		t.Fatal(err)
	}
	if err := ts.Track(ctx, 999, 1, "review_requested", t0); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("tracking for a missing repository = %v", err)
	}

	if err := ts.MarkReviewed(ctx, previewJob, "preview", r1.ID, 7, "abc123", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Tracking again (a search hit) keeps the reviewed head and first-seen time.
	if err := ts.Track(ctx, r1.ID, 7, "review_requested", t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	open, err := ts.ListOpen(ctx, previewJob, "preview", []int64{r1.ID, r2.ID})
	if err != nil || len(open) != 2 {
		t.Fatalf("ListOpen = %+v, %v", open, err)
	}
	p7 := open[0]
	if p7.Number != 7 || p7.LastHeadOID != "abc123" || !p7.FirstSeenAt.Equal(t0) || p7.LastRunAt == nil || !p7.LastRunAt.Equal(t0.Add(time.Hour)) || p7.State != "open" || p7.Source != "review_requested" {
		t.Errorf("tracked #7 = %+v", p7)
	}
	if open[1].LastHeadOID != "" || open[1].LastRunAt != nil {
		t.Errorf("a never-reviewed pull request has no head: %+v", open[1])
	}

	// What a job reviewed is its own: another job, or the same job in the other
	// mode, still sees the pull request as new.
	for _, other := range []struct {
		job  int64
		mode string
	}{{postJob, "post"}, {previewJob, "post"}, {postJob, "preview"}} {
		got, err := ts.ListOpen(ctx, other.job, other.mode, []int64{r1.ID})
		if err != nil || len(got) != 2 || got[0].LastHeadOID != "" || got[0].LastRunAt != nil {
			t.Errorf("job %d in %s mode must not see the head of another: %+v, %v", other.job, other.mode, got, err)
		}
	}
	if err := ts.MarkReviewed(ctx, postJob, "post", r1.ID, 7, "def456", t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := ts.MarkReviewed(ctx, previewJob, "preview", r1.ID, 7, "abc999", t0.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := ts.ListOpen(ctx, postJob, "post", []int64{r1.ID}); got[0].LastHeadOID != "def456" {
		t.Errorf("post head = %q", got[0].LastHeadOID)
	}
	if got, _ := ts.ListOpen(ctx, previewJob, "preview", []int64{r1.ID}); got[0].LastHeadOID != "abc999" {
		t.Errorf("a second mark replaces the head: %q", got[0].LastHeadOID)
	}
	if err := s.Jobs().DeleteJob(ctx, postJob); err != nil {
		t.Fatal(err)
	}
	if got, _ := ts.ListOpen(ctx, postJob, "post", []int64{r1.ID}); got[0].LastHeadOID != "" {
		t.Errorf("deleting a job drops what it reviewed: %q", got[0].LastHeadOID)
	}

	// Only the asked repositories; closed ones drop out; a reopened one returns.
	if got, _ := ts.ListOpen(ctx, previewJob, "preview", []int64{r2.ID}); len(got) != 0 {
		t.Errorf("repository two: %+v", got)
	}
	if got, err := ts.ListOpen(ctx, previewJob, "preview", nil); err != nil || len(got) != 0 {
		t.Errorf("no repositories: %+v, %v", got, err)
	}
	if err := ts.SetState(ctx, r1.ID, 9, "merged"); err != nil {
		t.Fatal(err)
	}
	if got, _ := ts.ListOpen(ctx, previewJob, "preview", []int64{r1.ID}); len(got) != 1 || got[0].Number != 7 {
		t.Errorf("after merge: %+v", got)
	}
	if err := ts.Track(ctx, r1.ID, 9, "review_requested", t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := ts.ListOpen(ctx, previewJob, "preview", []int64{r1.ID}); len(got) != 2 {
		t.Errorf("a reopened pull request is tracked again: %+v", got)
	}
	if err := ts.SetState(ctx, r1.ID, 404, "closed"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("SetState on an unknown row = %v", err)
	}
	if err := ts.MarkReviewed(ctx, previewJob, "preview", r1.ID, 404, "x", t0); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("MarkReviewed on an unknown row = %v", err)
	}
	if err := ts.MarkReviewed(ctx, 9999, "preview", r1.ID, 7, "x", t0); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("MarkReviewed for an unknown job = %v", err)
	}

	// Deleting the repository removes what it tracked.
	if err := s.Repos().Delete(ctx, r1.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := ts.ListOpen(ctx, previewJob, "preview", []int64{r1.ID}); len(got) != 0 {
		t.Errorf("tracked rows of a deleted repository: %+v", got)
	}
}

func TestScheduleCostMeterSumsFinishedTasksOfTheDay(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	run := newRun(t, s, time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC), "running")
	finish := func(cost float64, at time.Time) {
		k := newTask(t, s, run.ID, "succeeded", "")
		k.CostUSD, k.FinishedAt = cost, &at
		if err := s.Runs().UpdateTask(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	finish(0.5, time.Date(2026, 11, 10, 23, 59, 0, 0, time.UTC)) // yesterday
	finish(1.25, time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC))
	finish(0.75, time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC))
	newTask(t, s, run.ID, "running", "") // not finished: no cost yet

	got, err := s.Costs().CostSince(ctx, time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC))
	if err != nil || got != 2.0 {
		t.Errorf("CostSince(today) = %v, %v; want 2", got, err)
	}
	if got, _ := s.Costs().CostSince(ctx, time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)); got != 0 {
		t.Errorf("CostSince(tomorrow) = %v, want 0", got)
	}
	// A zone ahead of UTC: local midnight is the previous UTC evening.
	ist := time.FixedZone("EET", 3*3600)
	if got, _ := s.Costs().CostSince(ctx, time.Date(2026, 11, 11, 0, 0, 0, 0, ist)); got != 2.5 {
		t.Errorf("CostSince(local midnight, UTC+3) = %v, want 2.5", got)
	}
}

// 0003 is reversible, and rows written before it read back as "no attention".
func TestScheduleMigration0003(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) < 3 || ms[2].name != "scheduler" {
		t.Fatalf("migration 3 = %+v", ms)
	}
	if err := s.migrateTo(ctx, ms, 2); err != nil {
		t.Fatalf("down to 2: %v", err)
	}
	if rows, err := s.db.Query("SELECT attention_code FROM jobs"); err == nil {
		_ = rows.Close()
		t.Error("jobs.attention_code still exists after reversing 0003")
	}
	// A job written at schema 2 survives the upgrade with no attention.
	if _, err := s.db.Exec(`INSERT INTO jobs (kind, name, spec_json, spec_version, mode, enabled, created_at)
		VALUES ('review.pr', 'old', '{}', 1, 'preview', 1, '2026-11-10T00:00:00.000Z')`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateTo(ctx, ms, len(ms)); err != nil {
		t.Fatalf("up again: %v", err)
	}
	list, err := s.Jobs().ListJobs(ctx)
	if err != nil || len(list) != 1 || list[0].AttentionCode != "" || list[0].AttentionAt != nil || len(list[0].Windows) != 0 {
		t.Errorf("job after upgrade = %+v, %v", list, err)
	}
}
