// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/CommitBrief/ui/internal/port"
)

func TestJobCRUD(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	r1, _ := s.Repos().Create(ctx, newRepo("a", "one"))
	r2, _ := s.Repos().Create(ctx, newRepo("a", "two"))

	created, err := s.Jobs().CreateJob(ctx, port.JobRecord{
		Kind: "review.pr", Name: "PR 7", Spec: json.RawMessage(`{"number":7}`), Mode: "preview",
		Enabled: true, RepositoryIDs: []int64{r1.ID, r2.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.CreatedAt.IsZero() || created.SpecVersion != 1 {
		t.Fatalf("CreateJob did not assign id/created_at/spec_version: %+v", created)
	}

	got, err := s.Jobs().GetJob(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "review.pr" || got.Name != "PR 7" || string(got.Spec) != `{"number":7}` || got.Mode != "preview" ||
		!got.Enabled || !got.CreatedAt.Equal(created.CreatedAt) || len(got.RepositoryIDs) != 2 {
		t.Errorf("GetJob = %+v", got)
	}

	// Update replaces the editable fields and the target set; the kind is kept.
	got.Name = "renamed"
	got.Kind = "ignored.kind"
	got.Mode = "post"
	got.Enabled = false
	got.RepositoryIDs = []int64{r2.ID}
	if err := s.Jobs().UpdateJob(ctx, got); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Jobs().GetJob(ctx, created.ID)
	if after.Name != "renamed" || after.Kind != "review.pr" || after.Mode != "post" || after.Enabled ||
		len(after.RepositoryIDs) != 1 || after.RepositoryIDs[0] != r2.ID {
		t.Errorf("after UpdateJob = %+v", after)
	}

	list, err := s.Jobs().ListJobs(ctx)
	if err != nil || len(list) != 1 || len(list[0].RepositoryIDs) != 1 {
		t.Errorf("ListJobs = %+v, %v", list, err)
	}

	if err := s.Jobs().DeleteJob(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Jobs().GetJob(ctx, created.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("GetJob after delete = %v, want ErrNotFound", err)
	}
	var targets int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM job_targets").Scan(&targets); err != nil || targets != 0 {
		t.Errorf("job_targets left behind: %d, %v", targets, err)
	}
	if err := s.Jobs().DeleteJob(ctx, created.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("second DeleteJob = %v, want ErrNotFound", err)
	}
}

// A job with an unknown target is refused whole: no half-written job remains.
func TestCreateJobIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	r, _ := s.Repos().Create(ctx, newRepo("a", "one"))

	_, err := s.Jobs().CreateJob(ctx, port.JobRecord{
		Kind: "review.pr", Name: "x", Spec: json.RawMessage(`{}`), Mode: "preview", RepositoryIDs: []int64{r.ID, 9999},
	})
	if !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("CreateJob with a missing target = %v, want ErrNotFound", err)
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&n); err != nil || n != 0 {
		t.Errorf("a refused job was left behind: %d, %v", n, err)
	}

	// The same target twice is a conflict.
	_, err = s.Jobs().CreateJob(ctx, port.JobRecord{
		Kind: "review.pr", Name: "x", Spec: json.RawMessage(`{}`), Mode: "preview", RepositoryIDs: []int64{r.ID, r.ID},
	})
	if !errors.Is(err, port.ErrConflict) {
		t.Errorf("duplicate target = %v, want ErrConflict", err)
	}
}

// Deleting a job keeps its runs, with the job reference cleared.
func TestDeleteJobKeepsRuns(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	j, err := s.Jobs().CreateJob(ctx, port.JobRecord{Kind: "review.local", Name: "j", Spec: json.RawMessage(`{}`), Mode: "preview"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Runs().CreateRun(ctx, port.RunRecord{JobID: j.ID, Trigger: "manual", StartedAt: s.now(), Status: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Jobs().DeleteJob(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Runs().GetRun(ctx, run.ID)
	if err != nil || got.JobID != 0 {
		t.Errorf("run after job delete = %+v, %v; want JobID 0", got, err)
	}
}
