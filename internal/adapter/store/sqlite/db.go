// SPDX-License-Identifier: GPL-3.0-or-later

// Package sqlite is the SQLite adapter of the store ports (UI-0006): one
// database file in the OS config directory, WAL mode, embedded numbered
// migrations, an integrity check on every start and a file backup before each
// migration. The driver is modernc.org/sqlite (pure Go).
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	sqlitedrv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/CommitBrief/ui/internal/port"
)

// timeLayout is the stored form of every timestamp: UTC, millisecond
// precision, sortable as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

// maxOpenConns bounds the connection pool shared by the UI and the scheduler.
// SQLite has one writer at a time anyway; the rest are WAL readers.
const maxOpenConns = 4

// ErrNewerSchema means the database was migrated by a newer version of the
// application than this one; it is not touched.
var ErrNewerSchema = errors.New("sqlite: database schema is newer than this application")

type options struct {
	log *slog.Logger
	now func() time.Time
	// Test seams (unexported options): the migration set and the rename used to
	// set a damaged database aside.
	migrations []migration
	rename     func(oldpath, newpath string) error
}

// Option configures Open.
type Option func(*options)

// WithLogger sets the logger for backup and recovery events.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.log = l } }

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option { return func(o *options) { o.now = now } }

// Store is the SQLite-backed implementation of the store ports.
type Store struct {
	db       *sql.DB
	path     string
	log      *slog.Logger
	now      func() time.Time
	recovery *port.DatabaseRecovery
	// migrations overrides the embedded set (tests); rename is os.Rename
	// unless a test replaces it.
	migrations []migration
	rename     func(oldpath, newpath string) error
}

// Open opens (creating it when missing) the database at path, verifies it and
// brings the schema up to date. A database that fails its integrity check is
// never fatal and never deleted: the damaged file is set aside as
// <path>.corrupt-<UTC stamp>, the newest usable pre-migration backup is restored
// in its place or, when there is none, the application starts with an empty
// database. Either way Recovery reports what happened.
//
// The parent directory is created with mode 0700 and the file with 0600; the
// WAL side files inherit the file's mode.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	o := options{log: slog.Default(), now: time.Now, rename: os.Rename}
	for _, opt := range opts {
		opt(&o)
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("sqlite: database path must be absolute, got %q", path)
	}
	// The driver splits the DSN at the first '?', so a path containing one
	// cannot be expressed. Config directories never do; refuse loudly.
	if strings.ContainsAny(path, "?\x00") {
		return nil, fmt.Errorf("sqlite: database path %q contains an unsupported character", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("sqlite: create directory: %w", err)
	}
	if err := touch(path); err != nil {
		return nil, err
	}

	s := &Store{path: path, log: o.log, now: o.now, migrations: o.migrations, rename: o.rename}

	// A recovery that was never acknowledged (the process died, or a later
	// step failed, after the damaged file was set aside) is reported again.
	pending, err := s.readRecoveryMarker()
	if err != nil {
		s.log.Warn("ignoring an unreadable recovery marker", "error", err)
	}

	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	if problem := verify(ctx, db); problem != nil {
		_ = db.Close()
		var corrupt *corruptProblem
		if !errors.As(problem, &corrupt) {
			return nil, problem
		}
		db, s.recovery, err = s.recoverDatabase(ctx, corrupt.detail)
		if err != nil {
			return nil, err
		}
		s.log.Warn("database was damaged; the damaged file was kept aside",
			"kind", s.recovery.Kind, "restored_from", s.recovery.RestoredFrom,
			"damaged_file", s.recovery.DamagedFile, "reason", s.recovery.Reason)
	} else {
		s.recovery = pending
	}
	s.db = db

	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database. The WAL is checkpointed when the last
// connection closes.
func (s *Store) Close() error { return s.db.Close() }

// Path is the database file.
func (s *Store) Path() string { return s.path }

// Recovery reports the recovery Open performed, or one that is still waiting to
// be acknowledged from an earlier run, or nil.
func (s *Store) Recovery() *port.DatabaseRecovery { return s.recovery }

// Repos is the repository store on the shared pool.
func (s *Store) Repos() port.RepoStore { return repoStore{q: s.db, now: s.now} }

// Settings is the settings store on the shared pool.
func (s *Store) Settings() port.SettingsStore { return settingsStore{q: s.db} }

// Jobs is the job store on the shared pool.
func (s *Store) Jobs() port.JobStore { return jobStore{q: s.db, now: s.now} }

// Runs is the run and task store on the shared pool.
func (s *Store) Runs() port.RunStore { return runStore{q: s.db} }

// InTx implements port.TxRunner.
func (s *Store) InTx(ctx context.Context, fn func(port.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				err = errors.Join(err, fmt.Errorf("sqlite: rollback: %w", rbErr))
			}
		}
	}()
	if err = fn(txView{tx: tx, now: s.now}); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit: %w", err)
	}
	return nil
}

var (
	_ port.TxRunner = (*Store)(nil)
	_ port.Tx       = txView{}

	_ port.RecoverySource = (*Store)(nil)
)

type txView struct {
	tx  *sql.Tx
	now func() time.Time
}

func (v txView) Repos() port.RepoStore        { return repoStore{q: v.tx, now: v.now} }
func (v txView) Settings() port.SettingsStore { return settingsStore{q: v.tx} }
func (v txView) Jobs() port.JobStore          { return jobStore{q: v.tx, now: v.now} }
func (v txView) Runs() port.RunStore          { return runStore{q: v.tx} }

// querier is what both *sql.DB and *sql.Tx offer; the stores run on either.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// openDB opens the pool. Pragmas ride in the DSN so that every pooled
// connection gets them: foreign keys are a per-connection setting.
// _txlock=immediate makes every transaction take the write lock up front, so a
// read-then-write transaction cannot dead-end in SQLITE_BUSY halfway.
func openDB(path string) (*sql.DB, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	return db, nil
}

// corruptProblem marks a verify failure that is database damage (as opposed
// to, say, a locked file or a canceled context).
type corruptProblem struct{ detail string }

func (p *corruptProblem) Error() string { return "sqlite: corrupt: " + p.detail }

// verify connects and runs PRAGMA integrity_check. It returns a
// *corruptProblem for damage and an ordinary error for everything else.
func verify(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return classify(err)
	}
	defer func() { _ = rows.Close() }()
	var first string
	n := 0
	for rows.Next() {
		var msg string
		if err := rows.Scan(&msg); err != nil {
			return classify(err)
		}
		if n == 0 {
			first = msg
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return classify(err)
	}
	if n != 1 || first != "ok" {
		if first == "" {
			first = "integrity_check returned no result"
		}
		return &corruptProblem{detail: first}
	}
	return nil
}

// classify turns SQLITE_CORRUPT and SQLITE_NOTADB into a corruptProblem and
// wraps anything else.
func classify(err error) error {
	var se *sqlitedrv.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB:
			return &corruptProblem{detail: se.Error()}
		}
	}
	return fmt.Errorf("sqlite: integrity check: %w", err)
}

// isUnique reports a UNIQUE or PRIMARY KEY violation.
func isUnique(err error) bool {
	var se *sqlitedrv.Error
	if !errors.As(err, &se) {
		return false
	}
	switch se.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
		return true
	}
	return false
}

// touch creates the file with mode 0600 when it does not exist yet, so the
// driver never creates it with a looser mode.
func touch(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("sqlite: cannot open database file: %w", err)
		}
		return fmt.Errorf("sqlite: create database file: %w", err)
	}
	return f.Close()
}

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("sqlite: bad stored timestamp %q: %w", s, err)
	}
	return t, nil
}
