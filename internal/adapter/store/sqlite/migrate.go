// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

// Migrations are numbered SQL files, NNNN_name.sql (up) and NNNN_name.down.sql
// (down), applied in order, each in its own transaction, and recorded in
// schema_migrations. Released migrations are never edited; a change is a new
// migration. A copy of the database is taken before every migration that runs
// on existing data.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

var migrationNameRe = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)(\.down)?\.sql$`)

type migration struct {
	version int
	name    string
	up      string
	down    string
}

// loadMigrations reads and validates the embedded migrations: versions start at
// 1 and are contiguous, and every migration has both an up and a down file.
func loadMigrations() ([]migration, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("sqlite: read migrations: %w", err)
	}
	byVersion := map[int]*migration{}
	for _, e := range entries {
		m := migrationNameRe.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("sqlite: migration file %q does not match NNNN_name[.down].sql", e.Name())
		}
		version, _ := strconv.Atoi(m[1])
		body, err := migrationFiles.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("sqlite: read migration %s: %w", e.Name(), err)
		}
		mg := byVersion[version]
		if mg == nil {
			mg = &migration{version: version, name: m[2]}
			byVersion[version] = mg
		}
		if mg.name != m[2] {
			return nil, fmt.Errorf("sqlite: migration %04d has two names (%q and %q)", version, mg.name, m[2])
		}
		if m[3] == ".down" {
			mg.down = string(body)
		} else {
			mg.up = string(body)
		}
	}
	out := make([]migration, 0, len(byVersion))
	for _, mg := range byVersion {
		out = append(out, *mg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, validateMigrations(out)
}

func validateMigrations(ms []migration) error {
	for i, m := range ms {
		if m.version != i+1 {
			return fmt.Errorf("sqlite: migration versions must be contiguous from 1; found %04d at position %d", m.version, i+1)
		}
		if m.up == "" || m.down == "" {
			return fmt.Errorf("sqlite: migration %04d_%s needs both an up and a down file", m.version, m.name)
		}
	}
	return nil
}

// migrate brings the schema to the latest embedded version.
func (s *Store) migrate(ctx context.Context) error {
	ms := s.migrations
	if ms == nil {
		var err error
		if ms, err = loadMigrations(); err != nil {
			return err
		}
	}
	return s.migrateTo(ctx, ms, len(ms))
}

// migrateTo moves the schema to target (0 means empty). Going up backs the
// database up before each migration that runs on existing data. Going down
// exists for tests and tooling: it discards data, and the application never
// calls it.
func (s *Store) migrateTo(ctx context.Context, ms []migration, target int) error {
	if target < 0 || target > len(ms) {
		return fmt.Errorf("sqlite: migration target %d out of range 0..%d", target, len(ms))
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("sqlite: create schema_migrations: %w", err)
	}
	current, err := currentVersion(ctx, s.db)
	if err != nil {
		return err
	}
	if current > len(ms) {
		return fmt.Errorf("%w (database at version %d, application knows %d)", ErrNewerSchema, current, len(ms))
	}

	for current < target {
		m := ms[current] // versions are contiguous: ms[current] is current+1
		if current > 0 {
			backup, err := s.backupBeforeMigration(ctx, s.db, current)
			if err != nil {
				return err
			}
			s.log.Info("database backed up before migration", "backup", backup, "from_version", current, "to_version", m.version)
		}
		if err := s.applyStep(ctx, m, true); err != nil {
			return err
		}
		current = m.version
	}
	for current > target {
		m := ms[current-1]
		if err := s.applyStep(ctx, m, false); err != nil {
			return err
		}
		current = m.version - 1
	}
	return nil
}

// applyStep runs one migration (up or down) and updates schema_migrations in
// the same transaction. The expected current version is re-read under the
// write lock so that a concurrent migrator cannot apply a step twice.
func (s *Store) applyStep(ctx context.Context, m migration, up bool) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin migration %04d: %w", m.version, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	current, err := currentVersion(ctx, tx)
	if err != nil {
		return err
	}
	want := m.version - 1
	if !up {
		want = m.version
	}
	if current != want {
		return fmt.Errorf("sqlite: migration %04d expected schema version %d, found %d", m.version, want, current)
	}

	body, verb := m.up, "apply"
	if !up {
		body, verb = m.down, "reverse"
	}
	if _, err = tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("sqlite: %s migration %04d_%s: %w", verb, m.version, m.name, err)
	}
	if up {
		_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", m.version, formatTime(s.now()))
	} else {
		_, err = tx.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = ?", m.version)
	}
	if err != nil {
		return fmt.Errorf("sqlite: record migration %04d: %w", m.version, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit migration %04d: %w", m.version, err)
	}
	return nil
}

// currentVersion is the highest applied migration, 0 for a fresh database.
func currentVersion(ctx context.Context, q querier) (int, error) {
	var v sql.NullInt64
	if err := q.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&v); err != nil {
		return 0, fmt.Errorf("sqlite: read schema version: %w", err)
	}
	return int(v.Int64), nil
}
