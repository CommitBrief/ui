// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/port"
)

const repoColumns = "id, remote_owner, remote_name, local_path, enabled, created_at"

// repoStore implements port.RepoStore on a pool or a transaction.
type repoStore struct {
	q   querier
	now func() time.Time
}

var _ port.RepoStore = repoStore{}

func (s repoStore) Create(ctx context.Context, r repo.Repository) (repo.Repository, error) {
	created := s.now().UTC().Truncate(time.Millisecond)
	res, err := s.q.ExecContext(ctx,
		"INSERT INTO repositories (remote_owner, remote_name, local_path, enabled, created_at) VALUES (?, ?, ?, ?, ?)",
		r.Owner, r.Name, nullable(r.LocalPath), boolToInt(r.Enabled), formatTime(created))
	if err != nil {
		return repo.Repository{}, mapRepoErr("create repository", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return repo.Repository{}, fmt.Errorf("sqlite: create repository: %w", err)
	}
	r.ID = id
	r.CreatedAt = created
	return r, nil
}

func (s repoStore) Get(ctx context.Context, id int64) (repo.Repository, error) {
	return scanRepo(s.q.QueryRowContext(ctx, "SELECT "+repoColumns+" FROM repositories WHERE id = ?", id))
}

func (s repoStore) GetByRemote(ctx context.Context, owner, name string) (repo.Repository, error) {
	// The columns are COLLATE NOCASE, so this comparison is case-insensitive.
	return scanRepo(s.q.QueryRowContext(ctx,
		"SELECT "+repoColumns+" FROM repositories WHERE remote_owner = ? AND remote_name = ?", owner, name))
}

func (s repoStore) List(ctx context.Context) ([]repo.Repository, error) {
	rows, err := s.q.QueryContext(ctx,
		"SELECT "+repoColumns+" FROM repositories ORDER BY remote_owner COLLATE NOCASE, remote_name COLLATE NOCASE, id")
	if err != nil {
		return nil, fmt.Errorf("sqlite: list repositories: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []repo.Repository{}
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list repositories: %w", err)
	}
	return out, nil
}

func (s repoStore) Update(ctx context.Context, r repo.Repository) error {
	res, err := s.q.ExecContext(ctx,
		"UPDATE repositories SET remote_owner = ?, remote_name = ?, local_path = ? WHERE id = ?",
		r.Owner, r.Name, nullable(r.LocalPath), r.ID)
	if err != nil {
		return mapRepoErr("update repository", err)
	}
	return requireAffected(res, "update repository")
}

func (s repoStore) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	res, err := s.q.ExecContext(ctx, "UPDATE repositories SET enabled = ? WHERE id = ?", boolToInt(enabled), id)
	if err != nil {
		return fmt.Errorf("sqlite: set repository enabled: %w", err)
	}
	return requireAffected(res, "set repository enabled")
}

func (s repoStore) Delete(ctx context.Context, id int64) error {
	res, err := s.q.ExecContext(ctx, "DELETE FROM repositories WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("sqlite: delete repository: %w", err)
	}
	return requireAffected(res, "delete repository")
}

// scanner is the part of *sql.Row and *sql.Rows that scanRepo needs.
type scanner interface{ Scan(dest ...any) error }

func scanRepo(sc scanner) (repo.Repository, error) {
	var (
		r       repo.Repository
		local   sql.NullString
		enabled int
		created string
	)
	if err := sc.Scan(&r.ID, &r.Owner, &r.Name, &local, &enabled, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.Repository{}, port.ErrNotFound
		}
		return repo.Repository{}, fmt.Errorf("sqlite: read repository: %w", err)
	}
	t, err := parseTime(created)
	if err != nil {
		return repo.Repository{}, err
	}
	r.LocalPath = local.String
	r.Enabled = enabled != 0
	r.CreatedAt = t
	return r, nil
}

func mapRepoErr(op string, err error) error {
	if isUnique(err) {
		return fmt.Errorf("sqlite: %s: %w", op, port.ErrConflict)
	}
	return fmt.Errorf("sqlite: %s: %w", op, err)
}

func requireAffected(res sql.Result, op string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: %s: %w", op, err)
	}
	if n == 0 {
		return port.ErrNotFound
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
