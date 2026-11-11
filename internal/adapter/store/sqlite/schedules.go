// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"fmt"

	"github.com/CommitBrief/ui/internal/domain/schedule"
)

// Time windows live in the schedules table, one row per window, and travel with
// their job: the job store reads and writes them, so a job and its windows are
// always saved together.

// insertWindows stores the windows of a job. The columns carry CHECKs, so a
// window the domain would refuse is refused here as well.
func insertWindows(ctx context.Context, q querier, jobID int64, ws []schedule.Window) error {
	for _, w := range ws {
		if _, err := q.ExecContext(ctx,
			"INSERT INTO schedules (job_id, days_mask, start_minute, end_minute, every_minutes, tz) VALUES (?, ?, ?, ?, ?, ?)",
			jobID, int(w.Days), w.Start, w.End, w.Every, w.TZ); err != nil {
			return fmt.Errorf("sqlite: add job window: %w", err)
		}
	}
	return nil
}

// windows returns the windows per job: of one job, or of every job when jobID is
// 0, in insertion order. A job without windows is absent from the map.
func (s jobStore) windows(ctx context.Context, jobID int64) (map[int64][]schedule.Window, error) {
	query, args := "SELECT job_id, days_mask, start_minute, end_minute, every_minutes, tz FROM schedules ORDER BY job_id, id", []any(nil)
	if jobID != 0 {
		query, args = "SELECT job_id, days_mask, start_minute, end_minute, every_minutes, tz FROM schedules WHERE job_id = ? ORDER BY id", []any{jobID}
	}
	rows, err := s.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list job windows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64][]schedule.Window{}
	for rows.Next() {
		var (
			jid  int64
			mask int
			w    schedule.Window
		)
		if err := rows.Scan(&jid, &mask, &w.Start, &w.End, &w.Every, &w.TZ); err != nil {
			return nil, fmt.Errorf("sqlite: list job windows: %w", err)
		}
		w.Days = schedule.Days(mask)
		out[jid] = append(out[jid], w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list job windows: %w", err)
	}
	return out, nil
}
