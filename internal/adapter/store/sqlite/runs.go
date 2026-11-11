// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/CommitBrief/ui/internal/port"
)

// runStore implements port.RunStore on a pool or a transaction.
type runStore struct {
	q querier
}

var _ port.RunStore = runStore{}

const runColumns = `id, job_id, "trigger", started_at, finished_at, status, error_code, cost_usd`

// findingCountSQL is the length of the stored findings array, 0 for a task
// that has none (or whose column is not an array).
const findingCountSQL = `CASE WHEN findings_json IS NOT NULL AND json_valid(findings_json) AND json_type(findings_json) = 'array'
	THEN json_array_length(findings_json) ELSE 0 END`

const taskBaseColumns = `id, run_id, kind, spec_json, repository_id, pr_number, status, error_code, exit_code,
	cli_version, head_oid, verdict, log_path, cost_usd, queued_at, started_at, finished_at, ` + findingCountSQL

func (s runStore) CreateRun(ctx context.Context, r port.RunRecord) (port.RunRecord, error) {
	started := r.StartedAt.UTC().Truncate(time.Millisecond)
	res, err := s.q.ExecContext(ctx,
		`INSERT INTO runs (job_id, "trigger", started_at, finished_at, status, error_code, cost_usd) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		zeroNull(r.JobID), r.Trigger, formatTime(started), nullTime(r.FinishedAt), r.Status, nullable(r.ErrorCode), r.CostUSD)
	if err != nil {
		if isForeignKey(err) {
			return port.RunRecord{}, fmt.Errorf("sqlite: create run: %w", port.ErrNotFound)
		}
		return port.RunRecord{}, fmt.Errorf("sqlite: create run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return port.RunRecord{}, fmt.Errorf("sqlite: create run: %w", err)
	}
	r.ID = id
	r.StartedAt = started
	return r, nil
}

func (s runStore) UpdateRun(ctx context.Context, r port.RunRecord) error {
	res, err := s.q.ExecContext(ctx,
		"UPDATE runs SET status = ?, error_code = ?, cost_usd = ?, finished_at = ? WHERE id = ?",
		r.Status, nullable(r.ErrorCode), r.CostUSD, nullTime(r.FinishedAt), r.ID)
	if err != nil {
		return fmt.Errorf("sqlite: update run: %w", err)
	}
	return requireAffected(res, "update run")
}

func (s runStore) GetRun(ctx context.Context, id int64) (port.RunRecord, error) {
	return scanRun(s.q.QueryRowContext(ctx, "SELECT "+runColumns+" FROM runs WHERE id = ?", id))
}

func (s runStore) ListRuns(ctx context.Context, jobID int64, limit int) ([]port.RunRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	query, args := "SELECT "+runColumns+" FROM runs ORDER BY started_at DESC, id DESC LIMIT ?", []any{limit}
	if jobID != 0 {
		query = "SELECT " + runColumns + " FROM runs WHERE job_id = ? ORDER BY started_at DESC, id DESC LIMIT ?"
		args = []any{jobID, limit}
	}
	rows, err := s.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []port.RunRecord{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list runs: %w", err)
	}
	return out, nil
}

func scanRun(sc scanner) (port.RunRecord, error) {
	var (
		r        port.RunRecord
		jobID    sql.NullInt64
		started  string
		finished sql.NullString
		errCode  sql.NullString
		cost     sql.NullFloat64
	)
	if err := sc.Scan(&r.ID, &jobID, &r.Trigger, &started, &finished, &r.Status, &errCode, &cost); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return port.RunRecord{}, port.ErrNotFound
		}
		return port.RunRecord{}, fmt.Errorf("sqlite: read run: %w", err)
	}
	t, err := parseTime(started)
	if err != nil {
		return port.RunRecord{}, err
	}
	r.StartedAt = t
	r.JobID = jobID.Int64
	r.ErrorCode = errCode.String
	r.CostUSD = cost.Float64
	if r.FinishedAt, err = parseNullTime(finished); err != nil {
		return port.RunRecord{}, err
	}
	return r, nil
}

func (s runStore) CreateTask(ctx context.Context, t port.TaskRecord) (port.TaskRecord, error) {
	queued := t.QueuedAt.UTC().Truncate(time.Millisecond)
	res, err := s.q.ExecContext(ctx,
		`INSERT INTO tasks (run_id, kind, spec_json, repository_id, pr_number, status, error_code, exit_code,
			cli_version, head_oid, verdict, findings_json, report_json, log_path, cost_usd, queued_at, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.RunID, nullable(t.Kind), nullRaw(t.Spec), zeroNull(t.RepositoryID), zeroNullInt(t.PRNumber), t.Status,
		nullable(t.ErrorCode), nullIntPtr(t.ExitCode), nullable(t.CLIVersion), nullable(t.HeadOID), nullable(t.Verdict),
		nullRaw(t.FindingsJSON), nullRaw(t.ReportJSON), nullable(t.LogPath), t.CostUSD,
		formatTime(queued), nullTime(t.StartedAt), nullTime(t.FinishedAt))
	if err != nil {
		if isForeignKey(err) {
			return port.TaskRecord{}, fmt.Errorf("sqlite: create task: %w", port.ErrNotFound)
		}
		return port.TaskRecord{}, fmt.Errorf("sqlite: create task: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return port.TaskRecord{}, fmt.Errorf("sqlite: create task: %w", err)
	}
	t.ID = id
	t.QueuedAt = queued
	return t, nil
}

func (s runStore) UpdateTask(ctx context.Context, t port.TaskRecord) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE tasks SET status = ?, error_code = ?, exit_code = ?, cli_version = ?, head_oid = ?, verdict = ?,
			findings_json = ?, report_json = ?, log_path = ?, cost_usd = ?, started_at = ?, finished_at = ? WHERE id = ?`,
		t.Status, nullable(t.ErrorCode), nullIntPtr(t.ExitCode), nullable(t.CLIVersion), nullable(t.HeadOID), nullable(t.Verdict),
		nullRaw(t.FindingsJSON), nullRaw(t.ReportJSON), nullable(t.LogPath), t.CostUSD,
		nullTime(t.StartedAt), nullTime(t.FinishedAt), t.ID)
	if err != nil {
		return fmt.Errorf("sqlite: update task: %w", err)
	}
	return requireAffected(res, "update task")
}

func (s runStore) GetTask(ctx context.Context, id int64) (port.TaskRecord, error) {
	return scanTask(s.q.QueryRowContext(ctx,
		"SELECT "+taskBaseColumns+", findings_json, report_json FROM tasks WHERE id = ?", id), true)
}

func (s runStore) ListTasks(ctx context.Context, runID int64, withPayload bool) ([]port.TaskRecord, error) {
	cols := taskBaseColumns
	if withPayload {
		cols += ", findings_json, report_json"
	}
	rows, err := s.q.QueryContext(ctx, "SELECT "+cols+" FROM tasks WHERE run_id = ? ORDER BY id", runID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []port.TaskRecord{}
	for rows.Next() {
		t, err := scanTask(rows, withPayload)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list tasks: %w", err)
	}
	return out, nil
}

func scanTask(sc scanner, withPayload bool) (port.TaskRecord, error) {
	var (
		t                                          port.TaskRecord
		kind, spec, errCode, cliVer, head, verdict sql.NullString
		logPath, queued, started, finished         sql.NullString
		findings, report                           sql.NullString
		repoID, prNumber, exit                     sql.NullInt64
		cost                                       sql.NullFloat64
	)
	dest := []any{&t.ID, &t.RunID, &kind, &spec, &repoID, &prNumber, &t.Status, &errCode, &exit,
		&cliVer, &head, &verdict, &logPath, &cost, &queued, &started, &finished, &t.FindingCount}
	if withPayload {
		dest = append(dest, &findings, &report)
	}
	if err := sc.Scan(dest...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return port.TaskRecord{}, port.ErrNotFound
		}
		return port.TaskRecord{}, fmt.Errorf("sqlite: read task: %w", err)
	}
	t.Kind = kind.String
	t.Spec = rawOf(spec)
	t.RepositoryID = repoID.Int64
	t.PRNumber = int(prNumber.Int64)
	t.ErrorCode = errCode.String
	if exit.Valid {
		v := int(exit.Int64)
		t.ExitCode = &v
	}
	t.CLIVersion = cliVer.String
	t.HeadOID = head.String
	t.Verdict = verdict.String
	t.LogPath = logPath.String
	t.CostUSD = cost.Float64
	t.FindingsJSON = rawOf(findings)
	t.ReportJSON = rawOf(report)
	var err error
	if queued.Valid {
		if t.QueuedAt, err = parseTime(queued.String); err != nil {
			return port.TaskRecord{}, err
		}
	}
	if t.StartedAt, err = parseNullTime(started); err != nil {
		return port.TaskRecord{}, err
	}
	if t.FinishedAt, err = parseNullTime(finished); err != nil {
		return port.TaskRecord{}, err
	}
	return t, nil
}

// AbortUnfinished cancels what the previous process left behind. Tasks first,
// then runs: a run is never left queued or running with all its tasks ended.
func (s runStore) AbortUnfinished(ctx context.Context, at time.Time) (int, error) {
	stamp := formatTime(at)
	res, err := s.q.ExecContext(ctx,
		"UPDATE tasks SET status = 'canceled', error_code = 'canceled', finished_at = ? WHERE status IN ('queued', 'running')", stamp)
	if err != nil {
		return 0, fmt.Errorf("sqlite: abort unfinished tasks: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: abort unfinished tasks: %w", err)
	}
	if _, err := s.q.ExecContext(ctx,
		"UPDATE runs SET status = 'canceled', error_code = 'canceled', finished_at = ? WHERE status IN ('queued', 'running')", stamp); err != nil {
		return 0, fmt.Errorf("sqlite: abort unfinished runs: %w", err)
	}
	return int(n), nil
}

func (s runStore) ExpiredLogs(ctx context.Context, before time.Time, keepNewest int) ([]port.TaskLogRef, error) {
	if keepNewest < 0 {
		keepNewest = 0
	}
	rows, err := s.q.QueryContext(ctx,
		`SELECT t.id, t.log_path FROM tasks t JOIN runs r ON r.id = t.run_id
		 WHERE t.log_path IS NOT NULL AND t.log_path <> '' AND t.status NOT IN ('queued', 'running')
		   AND (r.started_at < ? OR r.id NOT IN (SELECT id FROM runs ORDER BY started_at DESC, id DESC LIMIT ?))
		 ORDER BY t.id`, formatTime(before), keepNewest)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list expired logs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []port.TaskLogRef
	for rows.Next() {
		var ref port.TaskLogRef
		if err := rows.Scan(&ref.TaskID, &ref.Path); err != nil {
			return nil, fmt.Errorf("sqlite: list expired logs: %w", err)
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list expired logs: %w", err)
	}
	return out, nil
}

func (s runStore) ClearLog(ctx context.Context, taskID int64) error {
	res, err := s.q.ExecContext(ctx, "UPDATE tasks SET log_path = NULL WHERE id = ?", taskID)
	if err != nil {
		return fmt.Errorf("sqlite: clear task log: %w", err)
	}
	return requireAffected(res, "clear task log")
}

// ----- NULL helpers -----

func zeroNull(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func zeroNullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullIntPtr(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func nullRaw(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func rawOf(s sql.NullString) json.RawMessage {
	if !s.Valid || s.String == "" {
		return nil
	}
	return json.RawMessage(s.String)
}

func parseNullTime(s sql.NullString) (*time.Time, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	t, err := parseTime(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
