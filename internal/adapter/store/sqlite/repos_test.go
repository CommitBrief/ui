// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/port"
)

func newRepo(owner, name string) repo.Repository {
	return repo.Repository{Owner: owner, Name: name, Enabled: true}
}

func TestRepoCRUD(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	rs := s.Repos()

	created, err := rs.Create(ctx, repo.Repository{Owner: "CommitBrief", Name: "ui", LocalPath: "/work/ui", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.CreatedAt.IsZero() {
		t.Fatalf("Create did not assign id/created_at: %+v", created)
	}

	got, err := rs.Get(ctx, created.ID)
	if err != nil || got != created {
		t.Fatalf("Get = %+v, %v; want %+v", got, err, created)
	}
	// Lookup by remote ignores case.
	byRemote, err := rs.GetByRemote(ctx, "commitbrief", "UI")
	if err != nil || byRemote.ID != created.ID {
		t.Errorf("GetByRemote = %+v, %v", byRemote, err)
	}

	// No local path is stored as NULL and comes back empty; disabled sticks.
	noPath, err := rs.Create(ctx, repo.Repository{Owner: "a", Name: "b", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	var isNull bool
	if err := s.db.QueryRow("SELECT local_path IS NULL FROM repositories WHERE id = ?", noPath.ID).Scan(&isNull); err != nil || !isNull {
		t.Errorf("empty local path not stored as NULL: %v, %v", isNull, err)
	}
	if got, _ := rs.Get(ctx, noPath.ID); got.LocalPath != "" || got.Enabled {
		t.Errorf("round trip of an unset path / disabled repo: %+v", got)
	}

	created.Name = "ui-renamed"
	created.LocalPath = ""
	if err := rs.Update(ctx, created); err != nil {
		t.Fatal(err)
	}
	if got, _ := rs.Get(ctx, created.ID); got.Name != "ui-renamed" || got.LocalPath != "" || !got.Enabled {
		t.Errorf("after Update: %+v", got)
	}
	if err := rs.SetEnabled(ctx, created.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := rs.Get(ctx, created.ID); got.Enabled || got.Name != "ui-renamed" {
		t.Errorf("after SetEnabled: %+v", got)
	}

	list, err := rs.List(ctx)
	if err != nil || len(list) != 2 || list[0].Owner != "a" || list[1].Owner != "CommitBrief" {
		t.Errorf("List = %+v, %v (want ordered by owner, case-insensitively)", list, err)
	}

	if err := rs.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Get(ctx, created.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := rs.Delete(ctx, created.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("second Delete = %v, want ErrNotFound", err)
	}
	if err := rs.Update(ctx, created); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("Update of a deleted row = %v, want ErrNotFound", err)
	}
	if err := rs.SetEnabled(ctx, created.ID, true); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("SetEnabled of a deleted row = %v, want ErrNotFound", err)
	}
	if _, err := rs.GetByRemote(ctx, "nobody", "nothing"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("GetByRemote miss = %v, want ErrNotFound", err)
	}
}

func TestEmptyListIsNotNil(t *testing.T) {
	list, err := openAt(t, dbPath(t)).Repos().List(context.Background())
	if err != nil || list == nil || len(list) != 0 {
		t.Errorf("List on an empty store = %#v, %v; want empty non-nil slice", list, err)
	}
}

func TestUniqueOwnerNameViolation(t *testing.T) {
	ctx := context.Background()
	rs := openAt(t, dbPath(t)).Repos()

	first, err := rs.Create(ctx, newRepo("CommitBrief", "ui"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dup := range []repo.Repository{newRepo("CommitBrief", "ui"), newRepo("commitbrief", "UI")} {
		if _, err := rs.Create(ctx, dup); !errors.Is(err, port.ErrConflict) {
			t.Errorf("Create(%s/%s) = %v, want ErrConflict", dup.Owner, dup.Name, err)
		}
	}

	other, err := rs.Create(ctx, newRepo("CommitBrief", "cli"))
	if err != nil {
		t.Fatal(err)
	}
	other.Name = "ui"
	if err := rs.Update(ctx, other); !errors.Is(err, port.ErrConflict) {
		t.Errorf("Update into an existing pair = %v, want ErrConflict", err)
	}
	// Updating a row to its own pair is fine.
	first.LocalPath = "/x"
	if err := rs.Update(ctx, first); err != nil {
		t.Errorf("Update of an unchanged pair: %v", err)
	}
}

func TestInTxCommitsAndRollsBack(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))

	if err := s.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.Repos().Create(ctx, newRepo("a", "one")); err != nil {
			return err
		}
		return tx.Settings().Set(ctx, "k", "v")
	}); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	err := s.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.Repos().Create(ctx, newRepo("a", "two")); err != nil {
			return err
		}
		if err := tx.Settings().Set(ctx, "k", "changed"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx = %v, want boom", err)
	}
	list, _ := s.Repos().List(ctx)
	if len(list) != 1 || list[0].Name != "one" {
		t.Errorf("rolled-back create is visible: %+v", list)
	}
	if v, _, _ := s.Settings().Get(ctx, "k"); v != "v" {
		t.Errorf("rolled-back setting is visible: %q", v)
	}

	// A panic inside fn rolls back and propagates.
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was swallowed")
			}
		}()
		_ = s.InTx(ctx, func(tx port.Tx) error {
			_, _ = tx.Repos().Create(ctx, newRepo("a", "three"))
			panic("kaboom")
		})
	}()
	if list, _ := s.Repos().List(ctx); len(list) != 1 {
		t.Errorf("panicked transaction was committed: %+v", list)
	}
}

// Deleting a repository removes what is only meaningful with it (job targets,
// tracked PRs) and keeps run history with the reference cleared.
func TestDeleteRepositoryCascadeRules(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	r, err := s.Repos().Create(ctx, newRepo("o", "n"))
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO jobs (id, kind, name, spec_json, mode, created_at) VALUES (1, 'review.pr', 'j', '{}', 'preview', 't')`)
	exec(`INSERT INTO job_targets (job_id, repository_id) VALUES (1, ?)`, r.ID)
	exec(`INSERT INTO tracked_prs (repository_id, number, first_seen_at, state, source) VALUES (?, 7, 't', 'open', 'manual')`, r.ID)
	exec(`INSERT INTO runs (id, job_id, "trigger", started_at, status) VALUES (1, 1, 'manual', 't', 'done')`)
	exec(`INSERT INTO tasks (id, run_id, repository_id, status) VALUES (1, 1, ?, 'done')`, r.ID)

	if err := s.Repos().Delete(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	count := func(q string) (n int) {
		t.Helper()
		if err := s.db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("SELECT COUNT(*) FROM job_targets"); n != 0 {
		t.Errorf("job_targets left behind: %d", n)
	}
	if n := count("SELECT COUNT(*) FROM tracked_prs"); n != 0 {
		t.Errorf("tracked_prs left behind: %d", n)
	}
	if n := count("SELECT COUNT(*) FROM tasks WHERE repository_id IS NULL"); n != 1 {
		t.Errorf("task history must survive with a cleared repository, got %d", n)
	}

	// Deleting the job keeps its runs the same way.
	exec(`DELETE FROM jobs WHERE id = 1`)
	if n := count("SELECT COUNT(*) FROM runs WHERE job_id IS NULL"); n != 1 {
		t.Errorf("run history must survive job deletion, got %d", n)
	}
}

func TestSchemaConstraints(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	bad := []string{
		`INSERT INTO repositories (remote_owner, remote_name, enabled, created_at) VALUES ('a', 'b', 2, 't')`,
		`INSERT INTO jobs (kind, name, spec_json, mode, created_at) VALUES ('k', 'n', '{}', 'nope', 't')`,
		`INSERT INTO job_targets (job_id, repository_id) VALUES (99, 99)`,
		`INSERT INTO runs ("trigger", started_at, status) VALUES ('sometimes', 't', 's')`,
		`INSERT INTO tracked_prs (repository_id, number, first_seen_at, state, source) VALUES (1, 1, 't', 'open', 's')`,
	}
	for _, q := range bad {
		if _, err := s.db.ExecContext(ctx, q); err == nil {
			t.Errorf("constraint did not reject: %s", q)
		}
	}
}

// The form save (Update) and the list toggle (SetEnabled) each write only
// their own columns, so neither can undo the other even when one of them works
// from a stale copy of the row.
func TestUpdateAndSetEnabledDoNotClobberEachOther(t *testing.T) {
	ctx := context.Background()
	rs := openAt(t, dbPath(t)).Repos()
	r, err := rs.Create(ctx, repo.Repository{Owner: "o", Name: "old", LocalPath: "/work/x", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	stale := r // a reader that fetched the row before both writes below

	if err := rs.SetEnabled(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	stale.Name = "renamed" // the form save, written from the stale copy (Enabled: true)
	if err := rs.Update(ctx, stale); err != nil {
		t.Fatal(err)
	}
	got, _ := rs.Get(ctx, r.ID)
	if got.Enabled || got.Name != "renamed" || got.LocalPath != "/work/x" {
		t.Errorf("Update re-enabled the row or lost a field: %+v", got)
	}

	if err := rs.SetEnabled(ctx, r.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ = rs.Get(ctx, r.ID)
	if !got.Enabled || got.Name != "renamed" {
		t.Errorf("SetEnabled reverted the rename: %+v", got)
	}
}
