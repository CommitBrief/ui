// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sqlitedrv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/CommitBrief/ui/internal/domain/schedule"
	"github.com/CommitBrief/ui/internal/port"
)

// jobStore implements port.JobStore on a pool or a transaction.
type jobStore struct {
	q   querier
	now func() time.Time
}

var _ port.JobStore = jobStore{}

const jobColumns = "id, kind, name, spec_json, spec_version, mode, enabled, created_at, attention_code, attention_at"

// inTx runs fn inside a transaction: the one already open when q is a
// transaction, otherwise a new one on the pool. Multi-statement writes (a job
// and its targets) go through it so they are all-or-nothing either way.
func inTx(ctx context.Context, q querier, fn func(querier) error) error {
	db, ok := q.(*sql.DB)
	if !ok {
		return fn(q)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit: %w", err)
	}
	return nil
}

func (s jobStore) CreateJob(ctx context.Context, j port.JobRecord) (port.JobRecord, error) {
	created := s.now().UTC().Truncate(time.Millisecond)
	err := inTx(ctx, s.q, func(q querier) error {
		res, err := q.ExecContext(ctx,
			"INSERT INTO jobs (kind, name, spec_json, spec_version, mode, enabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			j.Kind, j.Name, string(j.Spec), specVersion(j.SpecVersion), j.Mode, boolToInt(j.Enabled), formatTime(created))
		if err != nil {
			return fmt.Errorf("sqlite: create job: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("sqlite: create job: %w", err)
		}
		j.ID = id
		if err := insertTargets(ctx, q, id, j.RepositoryIDs); err != nil {
			return err
		}
		return insertWindows(ctx, q, id, j.Windows)
	})
	if err != nil {
		return port.JobRecord{}, err
	}
	j.CreatedAt = created
	j.SpecVersion = specVersion(j.SpecVersion)
	j.RepositoryIDs = append([]int64{}, j.RepositoryIDs...)
	j.Windows = append([]schedule.Window{}, j.Windows...)
	j.AttentionCode, j.AttentionAt = "", nil
	return j, nil
}

func specVersion(v int) int {
	if v <= 0 {
		return 1
	}
	return v
}

func insertTargets(ctx context.Context, q querier, jobID int64, ids []int64) error {
	for _, rid := range ids {
		if _, err := q.ExecContext(ctx, "INSERT INTO job_targets (job_id, repository_id) VALUES (?, ?)", jobID, rid); err != nil {
			if isForeignKey(err) {
				return fmt.Errorf("sqlite: job target %d: %w", rid, port.ErrNotFound)
			}
			if isUnique(err) {
				return fmt.Errorf("sqlite: job target %d: %w", rid, port.ErrConflict)
			}
			return fmt.Errorf("sqlite: add job target: %w", err)
		}
	}
	return nil
}

func (s jobStore) GetJob(ctx context.Context, id int64) (port.JobRecord, error) {
	j, err := scanJob(s.q.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE id = ?", id))
	if err != nil {
		return port.JobRecord{}, err
	}
	targets, err := s.targets(ctx, id)
	if err != nil {
		return port.JobRecord{}, err
	}
	j.RepositoryIDs = targets[id]
	ws, err := s.windows(ctx, id)
	if err != nil {
		return port.JobRecord{}, err
	}
	j.Windows = nonNilWindows(ws[id])
	return j, nil
}

func (s jobStore) ListJobs(ctx context.Context) ([]port.JobRecord, error) {
	rows, err := s.q.QueryContext(ctx, "SELECT "+jobColumns+" FROM jobs ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("sqlite: list jobs: %w", err)
	}
	out := []port.JobRecord{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("sqlite: list jobs: %w", err)
	}
	_ = rows.Close()
	targets, err := s.targets(ctx, 0)
	if err != nil {
		return nil, err
	}
	ws, err := s.windows(ctx, 0)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].RepositoryIDs = targets[out[i].ID]
		out[i].Windows = nonNilWindows(ws[out[i].ID])
	}
	return out, nil
}

func nonNilWindows(ws []schedule.Window) []schedule.Window {
	if ws == nil {
		return []schedule.Window{}
	}
	return ws
}

// targets returns the target repository IDs per job: of one job, or of every
// job when jobID is 0. A job without targets maps to an empty (non-nil) slice.
func (s jobStore) targets(ctx context.Context, jobID int64) (map[int64][]int64, error) {
	query, args := "SELECT job_id, repository_id FROM job_targets ORDER BY job_id, repository_id", []any(nil)
	if jobID != 0 {
		query, args = "SELECT job_id, repository_id FROM job_targets WHERE job_id = ? ORDER BY repository_id", []any{jobID}
	}
	rows, err := s.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list job targets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64][]int64{}
	for rows.Next() {
		var jid, rid int64
		if err := rows.Scan(&jid, &rid); err != nil {
			return nil, fmt.Errorf("sqlite: list job targets: %w", err)
		}
		out[jid] = append(out[jid], rid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list job targets: %w", err)
	}
	if jobID != 0 && out[jobID] == nil {
		out[jobID] = []int64{}
	}
	return out, nil
}

func (s jobStore) UpdateJob(ctx context.Context, j port.JobRecord) error {
	return inTx(ctx, s.q, func(q querier) error {
		res, err := q.ExecContext(ctx,
			"UPDATE jobs SET name = ?, spec_json = ?, spec_version = ?, mode = ?, enabled = ? WHERE id = ?",
			j.Name, string(j.Spec), specVersion(j.SpecVersion), j.Mode, boolToInt(j.Enabled), j.ID)
		if err != nil {
			return fmt.Errorf("sqlite: update job: %w", err)
		}
		if err := requireAffected(res, "update job"); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, "DELETE FROM job_targets WHERE job_id = ?", j.ID); err != nil {
			return fmt.Errorf("sqlite: update job targets: %w", err)
		}
		if err := insertTargets(ctx, q, j.ID, j.RepositoryIDs); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, "DELETE FROM schedules WHERE job_id = ?", j.ID); err != nil {
			return fmt.Errorf("sqlite: update job windows: %w", err)
		}
		return insertWindows(ctx, q, j.ID, j.Windows)
	})
}

func (s jobStore) SetAttention(ctx context.Context, id int64, code string, at time.Time) error {
	res, err := s.q.ExecContext(ctx, "UPDATE jobs SET attention_code = ?, attention_at = ? WHERE id = ?",
		nullable(code), formatTime(at.UTC().Truncate(time.Millisecond)), id)
	if err != nil {
		return fmt.Errorf("sqlite: set job attention: %w", err)
	}
	return requireAffected(res, "set job attention")
}

func (s jobStore) ClearAttention(ctx context.Context, id int64) error {
	res, err := s.q.ExecContext(ctx, "UPDATE jobs SET attention_code = NULL, attention_at = NULL WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("sqlite: clear job attention: %w", err)
	}
	return requireAffected(res, "clear job attention")
}

func (s jobStore) DeleteJob(ctx context.Context, id int64) error {
	res, err := s.q.ExecContext(ctx, "DELETE FROM jobs WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("sqlite: delete job: %w", err)
	}
	return requireAffected(res, "delete job")
}

func scanJob(sc scanner) (port.JobRecord, error) {
	var (
		j         port.JobRecord
		spec      string
		enabled   int
		created   string
		attention sql.NullString
		attnAt    sql.NullString
	)
	if err := sc.Scan(&j.ID, &j.Kind, &j.Name, &spec, &j.SpecVersion, &j.Mode, &enabled, &created, &attention, &attnAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return port.JobRecord{}, port.ErrNotFound
		}
		return port.JobRecord{}, fmt.Errorf("sqlite: read job: %w", err)
	}
	t, err := parseTime(created)
	if err != nil {
		return port.JobRecord{}, err
	}
	j.Spec = json.RawMessage(spec)
	j.Enabled = enabled != 0
	j.CreatedAt = t
	if attention.Valid {
		j.AttentionCode = attention.String
		if attnAt.Valid {
			at, err := parseTime(attnAt.String)
			if err != nil {
				return port.JobRecord{}, err
			}
			j.AttentionAt = &at
		}
	}
	return j, nil
}

// isForeignKey reports a FOREIGN KEY violation.
func isForeignKey(err error) bool {
	var se *sqlitedrv.Error
	return errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
}
