// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/port"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// TrackedPRs over the real store: the SQL filter, not a fake, keeps closed and
// merged pull requests out of the list the screen shows.
func TestTrackedPRsOverTheRealStore(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	r1, _ := s.Repos().Create(ctx, newRepo("octo", "alpha"))
	t0 := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	j, err := s.Jobs().CreateJob(ctx, port.JobRecord{
		Kind: "bot.review_requested", Name: "bot", Spec: json.RawMessage(`{}`), Mode: "preview", Enabled: true,
		RepositoryIDs: []int64{r1.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := s.Tracked()
	for _, n := range []int{7, 8, 9, 10} {
		if err := ts.Track(ctx, r1.ID, n, "review_requested", t0); err != nil {
			t.Fatal(err)
		}
	}
	if err := ts.MarkReviewed(ctx, j.ID, "preview", r1.ID, 7, "abc123", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := ts.SetState(ctx, r1.ID, 9, "closed"); err != nil {
		t.Fatal(err)
	}
	if err := ts.SetState(ctx, r1.ID, 10, "merged"); err != nil {
		t.Fatal(err)
	}

	svc := app.NewSchedules(app.SchedulesDeps{
		Jobs: s.Jobs(), Repos: s.Repos(), Tracked: ts, Clock: fixedClock{t0},
	})
	got, err := svc.TrackedPRs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Number != 7 || got[1].Number != 8 {
		t.Fatalf("TrackedPRs = %+v, want the open #7 and #8 only", got)
	}
	if got[0].Repository != "octo/alpha" || got[0].LastHeadOID != "abc123" || got[0].LastRunAt == nil || got[0].JobID != j.ID {
		t.Errorf("row 0 = %+v", got[0])
	}
	if got[1].LastHeadOID != "" || got[1].LastRunAt != nil {
		t.Errorf("a pull request nothing reviewed has no head: %+v", got[1])
	}
}
