// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/CommitBrief/ui/internal/port"
)

// trackedStore implements port.TrackedPRStore on a pool or a transaction.
type trackedStore struct {
	q querier
}

var _ port.TrackedPRStore = trackedStore{}

func (s trackedStore) Track(ctx context.Context, repositoryID int64, number int, source string, at time.Time) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO tracked_prs (repository_id, number, first_seen_at, state, source) VALUES (?, ?, ?, 'open', ?)
		 ON CONFLICT (repository_id, number) DO UPDATE SET state = 'open'`,
		repositoryID, number, formatTime(at.UTC().Truncate(time.Millisecond)), source)
	if err != nil {
		if isForeignKey(err) {
			return fmt.Errorf("sqlite: track pull request: %w", port.ErrNotFound)
		}
		return fmt.Errorf("sqlite: track pull request: %w", err)
	}
	return nil
}

func (s trackedStore) ListOpen(ctx context.Context, jobID int64, mode string, repositoryIDs []int64) ([]port.TrackedPR, error) {
	if len(repositoryIDs) == 0 {
		return []port.TrackedPR{}, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(repositoryIDs)), ",")
	args := make([]any, 0, len(repositoryIDs)+2)
	args = append(args, jobID, mode)
	for _, id := range repositoryIDs {
		args = append(args, id)
	}
	rows, err := s.q.QueryContext(ctx,
		`SELECT t.repository_id, t.number, t.first_seen_at, b.last_head_oid, b.last_run_at, t.state, t.source
		   FROM tracked_prs t
		   LEFT JOIN bot_reviews b ON b.repository_id = t.repository_id AND b.number = t.number AND b.job_id = ? AND b.mode = ?
		  WHERE t.state = 'open' AND t.repository_id IN (`+marks+`)
		  ORDER BY t.repository_id, t.number`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list tracked pull requests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []port.TrackedPR{}
	for rows.Next() {
		var (
			t       port.TrackedPR
			first   string
			head    sql.NullString
			lastRun sql.NullString
		)
		if err := rows.Scan(&t.RepositoryID, &t.Number, &first, &head, &lastRun, &t.State, &t.Source); err != nil {
			return nil, fmt.Errorf("sqlite: list tracked pull requests: %w", err)
		}
		if t.FirstSeenAt, err = parseTime(first); err != nil {
			return nil, err
		}
		t.LastHeadOID = head.String
		if lastRun.Valid {
			at, err := parseTime(lastRun.String)
			if err != nil {
				return nil, err
			}
			t.LastRunAt = &at
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list tracked pull requests: %w", err)
	}
	return out, nil
}

func (s trackedStore) SetState(ctx context.Context, repositoryID int64, number int, state string) error {
	res, err := s.q.ExecContext(ctx, "UPDATE tracked_prs SET state = ? WHERE repository_id = ? AND number = ?", state, repositoryID, number)
	if err != nil {
		return fmt.Errorf("sqlite: set pull request state: %w", err)
	}
	return requireAffected(res, "set pull request state")
}

func (s trackedStore) MarkReviewed(ctx context.Context, jobID int64, mode string, repositoryID int64, number int, headOID string, at time.Time) error {
	// Selecting from tracked_prs makes a pull request nobody tracks affect no row.
	res, err := s.q.ExecContext(ctx,
		`INSERT INTO bot_reviews (job_id, repository_id, number, mode, last_head_oid, last_run_at)
		 SELECT ?, repository_id, number, ?, ?, ? FROM tracked_prs WHERE repository_id = ? AND number = ?
		 ON CONFLICT (job_id, repository_id, number, mode) DO UPDATE SET last_head_oid = excluded.last_head_oid, last_run_at = excluded.last_run_at`,
		jobID, mode, headOID, formatTime(at.UTC().Truncate(time.Millisecond)), repositoryID, number)
	if err != nil {
		if isForeignKey(err) {
			return fmt.Errorf("sqlite: mark pull request reviewed: %w", port.ErrNotFound)
		}
		return fmt.Errorf("sqlite: mark pull request reviewed: %w", err)
	}
	return requireAffected(res, "mark pull request reviewed")
}

// costMeter implements port.CostMeter.
type costMeter struct {
	q querier
}

var _ port.CostMeter = costMeter{}

func (m costMeter) CostSince(ctx context.Context, since time.Time) (float64, error) {
	var sum float64
	err := m.q.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(cost_usd), 0) FROM tasks WHERE finished_at >= ?", formatTime(since.UTC())).Scan(&sum)
	if err != nil {
		return 0, fmt.Errorf("sqlite: sum task cost: %w", err)
	}
	return sum, nil
}
