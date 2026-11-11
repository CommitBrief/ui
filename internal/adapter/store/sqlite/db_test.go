// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/port"
)

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// testClock returns a clock that advances one second per call, so backup and
// quarantine names never collide and are ordered.
func testClock() func() time.Time {
	var n atomic.Int64
	base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return base.Add(time.Duration(n.Add(1)) * time.Second) }
}

// dbPath is a database path inside a directory whose name has a space, like
// "CommitBrief UI" on macOS and Windows.
func dbPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "CommitBrief UI", "ui.db")
}

func openAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path, WithLogger(quietLog), WithClock(testClock()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// latestVersion is the schema version of the embedded migration set.
func latestVersion(t *testing.T) int {
	t.Helper()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	return len(ms)
}

// openAtInit opens the database with only migration 0001, so a test can play a
// later release on top of it with a synthetic migration.
func openAtInit(t *testing.T, path string) *Store {
	t.Helper()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), path, WithLogger(quietLog), WithClock(testClock()),
		func(o *options) { o.migrations = ms[:1] })
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func tableNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func pragma(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var v string
	if err := db.QueryRow("PRAGMA " + name).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return v
}

func TestMigrationOnEmptyDatabase(t *testing.T) {
	path := dbPath(t)
	s := openAt(t, path)

	want := []string{"job_targets", "jobs", "repositories", "runs", "schedules", "schema_migrations", "settings", "tasks", "tracked_prs"}
	if got := tableNames(t, s.db); !equalStrings(got, want) {
		t.Errorf("tables = %v, want %v", got, want)
	}
	v, err := currentVersion(context.Background(), s.db)
	if want := latestVersion(t); err != nil || v != want {
		t.Errorf("schema version = %d, %v; want %d", v, err, want)
	}
	if got := pragma(t, s.db, "journal_mode"); got != "wal" {
		t.Errorf("journal_mode = %q, want wal", got)
	}
	if got := pragma(t, s.db, "foreign_keys"); got != "1" {
		t.Errorf("foreign_keys = %q, want 1", got)
	}
	if got := pragma(t, s.db, "integrity_check"); got != "ok" {
		t.Errorf("integrity_check = %q", got)
	}
	if rec := s.Recovery(); rec != nil {
		t.Errorf("a healthy new database reported a recovery: %+v", rec)
	}
	// A brand-new database has nothing worth backing up.
	if got := s.listBackups(); len(got) != 0 {
		t.Errorf("fresh database produced backups: %v", got)
	}
}

func TestFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes do not apply on Windows")
	}
	path := dbPath(t)
	s := openAt(t, path)
	// A write makes sure the WAL side files exist.
	if err := s.Settings().Set(context.Background(), "x", "y"); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("directory mode = %o, want 700", got)
	}
	// The database and every WAL side file that exists must be owner-only.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		fi, err := os.Stat(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got&0o077 != 0 {
			t.Errorf("%s mode = %o, want no group/other access", filepath.Base(path+suffix), got)
		}
	}
}

func TestSecondOpenIsIdempotent(t *testing.T) {
	path := dbPath(t)
	ctx := context.Background()

	s1 := openAt(t, path)
	if err := s1.Settings().Set(ctx, "language", "tr"); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := openAt(t, path)
	var rows int
	if err := s2.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&rows); err != nil || rows != latestVersion(t) {
		t.Errorf("schema_migrations rows = %d, %v; want %d", rows, err, latestVersion(t))
	}
	if v, ok, err := s2.Settings().Get(ctx, "language"); err != nil || !ok || v != "tr" {
		t.Errorf("setting after reopen = %q, %v, %v", v, ok, err)
	}
	if got := s2.listBackups(); len(got) != 0 {
		t.Errorf("reopening an up-to-date database must not back it up: %v", got)
	}
	// Running the migrator again is a no-op too.
	if err := s2.migrate(ctx); err != nil {
		t.Errorf("re-migrate: %v", err)
	}
}

func TestOpenRejectsBadPaths(t *testing.T) {
	ctx := context.Background()
	if _, err := Open(ctx, "relative/ui.db"); err == nil {
		t.Error("relative path accepted")
	}
	if _, err := Open(ctx, filepath.Join(t.TempDir(), "a?b", "ui.db")); err == nil {
		t.Error("path with '?' accepted")
	}
}

func TestOpenHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(ctx, dbPath(t), WithLogger(quietLog)); !errors.Is(err, context.Canceled) {
		t.Errorf("Open with canceled context = %v, want context.Canceled", err)
	}
}

// --- migrations -----------------------------------------------------------

// twoMigrations is the real 0001 plus a synthetic 0002 that adds a column and
// a row, standing in for a future release. Tests that use it open the database
// with openAtInit, so the synthetic step is the one that runs.
func twoMigrations(t *testing.T) []migration {
	t.Helper()
	real, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	return append(real[:1:1], migration{
		version: 2,
		name:    "add_note",
		up:      "ALTER TABLE settings ADD COLUMN note TEXT; INSERT INTO settings (key, value, note) VALUES ('from_0002', '1', 'n');",
		down:    "DELETE FROM settings WHERE key = 'from_0002'; ALTER TABLE settings DROP COLUMN note;",
	})
}

func TestMigrationBacksUpExistingDataFirst(t *testing.T) {
	ctx := context.Background()
	s := openAtInit(t, dbPath(t))
	if err := s.Settings().Set(ctx, "keep", "me"); err != nil {
		t.Fatal(err)
	}

	if err := s.migrateTo(ctx, twoMigrations(t), 2); err != nil {
		t.Fatalf("migrate to 2: %v", err)
	}
	if v, _ := currentVersion(ctx, s.db); v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}

	backups := s.listBackups()
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want exactly one", backups)
	}
	if filepath.Base(filepath.Dir(backups[0])) != backupDirName {
		t.Errorf("backup %s is not under %s/", backups[0], backupDirName)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(backups[0]); err != nil || fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("backup mode = %v, %v; want owner-only", fi.Mode().Perm(), err)
		}
	}

	// The backup is the database as it was at version 1: it has the data but
	// not the 0002 column.
	bdb, err := sql.Open("sqlite", backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bdb.Close() }()
	var value string
	if err := bdb.QueryRow("SELECT value FROM settings WHERE key = 'keep'").Scan(&value); err != nil || value != "me" {
		t.Errorf("backup lacks the pre-migration data: %q, %v", value, err)
	}
	if _, err := bdb.Query("SELECT note FROM settings"); err == nil {
		t.Error("backup already contains the 0002 column")
	}
	if v, err := currentVersion(ctx, bdb); err != nil || v != 1 {
		t.Errorf("backup schema version = %d, %v; want 1", v, err)
	}
}

func TestFailedMigrationRollsBackAndKeepsBackup(t *testing.T) {
	ctx := context.Background()
	s := openAtInit(t, dbPath(t))
	if err := s.Settings().Set(ctx, "keep", "me"); err != nil {
		t.Fatal(err)
	}
	ms := append(twoMigrations(t)[:1], migration{
		version: 2, name: "broken",
		up:   "CREATE TABLE half_done (x INTEGER); SELECT nonsense FROM nowhere;",
		down: "DROP TABLE half_done;",
	})
	if err := s.migrateTo(ctx, ms, 2); err == nil {
		t.Fatal("broken migration succeeded")
	}
	if v, _ := currentVersion(ctx, s.db); v != 1 {
		t.Errorf("version after failed migration = %d, want 1", v)
	}
	for _, name := range tableNames(t, s.db) {
		if name == "half_done" {
			t.Error("a failed migration left a partial table behind")
		}
	}
	if len(s.listBackups()) != 1 {
		t.Errorf("backups = %v, want the pre-migration one", s.listBackups())
	}
	if v, ok, _ := s.Settings().Get(ctx, "keep"); !ok || v != "me" {
		t.Error("data lost by a failed migration")
	}
}

func TestMigrationsAreReversible(t *testing.T) {
	ctx := context.Background()
	s := openAtInit(t, dbPath(t))
	ms := twoMigrations(t)

	if err := s.migrateTo(ctx, ms, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateTo(ctx, ms, 0); err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	if got := tableNames(t, s.db); !equalStrings(got, []string{"schema_migrations"}) {
		t.Errorf("tables after full rollback = %v", got)
	}
	if err := s.migrateTo(ctx, ms, 2); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if v, _ := currentVersion(ctx, s.db); v != 2 {
		t.Errorf("version = %d, want 2", v)
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	path := dbPath(t)
	s := openAt(t, path)
	if _, err := s.db.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES (99, 'x')"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), path, WithLogger(quietLog))
	if !errors.Is(err, ErrNewerSchema) {
		t.Errorf("Open on a newer schema = %v, want ErrNewerSchema", err)
	}
}

func TestLoadMigrationsValidates(t *testing.T) {
	if _, err := loadMigrations(); err != nil {
		t.Fatalf("embedded migrations invalid: %v", err)
	}
	if err := validateMigrations([]migration{{version: 2, name: "x", up: "a", down: "b"}}); err == nil {
		t.Error("gap at the start accepted")
	}
	if err := validateMigrations([]migration{{version: 1, name: "x", up: "a"}}); err == nil {
		t.Error("missing down accepted")
	}
}

func TestBackupsArePruned(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, dbPath(t))
	for i := 0; i < maxBackups+3; i++ {
		if _, err := s.backupBeforeMigration(ctx, s.db, 1); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(s.listBackups()); got != maxBackups {
		t.Errorf("kept %d backups, want %d", got, maxBackups)
	}
}

// --- corruption and recovery ---------------------------------------------

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCorruptDatabaseWithoutBackupStartsEmptyAndKeepsTheDamagedFile(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	garbage := bytes.Repeat([]byte("this is not a sqlite database "), 400)
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path, WithLogger(quietLog), WithClock(testClock()))
	if err != nil {
		t.Fatalf("Open on a damaged database with no backup must start empty, got: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	rec := s.Recovery()
	if rec == nil {
		t.Fatal("Recovery() = nil after the database was set aside")
	}
	if rec.Kind != port.RecoveryStartedEmpty || rec.RestoredFrom != "" || rec.Reason == "" {
		t.Errorf("Recovery = %+v, want kind started_empty with a reason and no restore source", rec)
	}
	if rec.BackupsDir != filepath.Join(filepath.Dir(path), backupDirName) {
		t.Errorf("BackupsDir = %q", rec.BackupsDir)
	}
	// Nothing is deleted: the damaged file is next to the database, under a
	// UTC-stamped name, byte for byte as it was.
	if filepath.Dir(rec.DamagedFile) != filepath.Dir(path) ||
		!strings.HasPrefix(filepath.Base(rec.DamagedFile), "ui.db.corrupt-2026") {
		t.Errorf("DamagedFile = %q, want <dir>/ui.db.corrupt-<UTC stamp>", rec.DamagedFile)
	}
	if !bytes.Equal(readFile(t, rec.DamagedFile), garbage) {
		t.Error("the damaged file was altered or lost")
	}

	// The live database is a fresh, fully migrated, writable one.
	if v, err := currentVersion(ctx, s.db); err != nil || v != latestVersion(t) {
		t.Errorf("schema version = %d, %v; want %d", v, err, latestVersion(t))
	}
	if list, err := s.Repos().List(ctx); err != nil || len(list) != 0 {
		t.Errorf("the new database is not empty: %v, %v", list, err)
	}
	if err := s.Settings().Set(ctx, "k", "v"); err != nil {
		t.Errorf("the new database is not writable: %v", err)
	}
	if got := pragma(t, s.db, "integrity_check"); got != "ok" {
		t.Errorf("integrity_check = %q", got)
	}
}

// Backups that exist but cannot be used are not deleted either, and the
// application still starts (empty).
func TestCorruptDatabaseWithUnusableBackupsStartsEmptyAndKeepsThem(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	backups := filepath.Join(filepath.Dir(path), backupDirName)
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(backups, "ui-20261110T100000.000Z-v0001.db")
	badBytes := bytes.Repeat([]byte("also broken "), 500)
	if err := os.WriteFile(bad, badBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("broken "), 700), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path, WithLogger(quietLog), WithClock(testClock()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if rec := s.Recovery(); rec == nil || rec.Kind != port.RecoveryStartedEmpty {
		t.Fatalf("Recovery = %+v, want started_empty", rec)
	}
	if !bytes.Equal(readFile(t, bad), badBytes) {
		t.Error("an unusable backup was altered or deleted")
	}
}

// Two recoveries in a row (even with the same clock reading) never overwrite
// the earlier damaged copy.
func TestRecoveriesDoNotOverwriteEarlierDamagedCopies(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	frozen := func() time.Time { return time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC) }

	var kept []string
	for i, junk := range []string{"first damage ", "second damage "} {
		if err := os.WriteFile(path, bytes.Repeat([]byte(junk), 500), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Open(ctx, path, WithLogger(quietLog), WithClock(frozen))
		if err != nil {
			t.Fatalf("recovery %d: %v", i+1, err)
		}
		kept = append(kept, s.Recovery().DamagedFile)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if kept[0] == kept[1] {
		t.Fatalf("both recoveries used %s", kept[0])
	}
	for i, junk := range []string{"first damage ", "second damage "} {
		if !bytes.Equal(readFile(t, kept[i]), bytes.Repeat([]byte(junk), 500)) {
			t.Errorf("damaged copy %d (%s) was overwritten", i+1, kept[i])
		}
	}
}

func TestCorruptDatabaseIsRestoredFromBackup(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	clock := testClock()

	initOnly := twoMigrations(t)[:1]
	s, err := Open(ctx, path, WithLogger(quietLog), WithClock(clock), func(o *options) { o.migrations = initOnly })
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Settings().Set(ctx, "before_backup", "kept"); err != nil {
		t.Fatal(err)
	}
	// A migration to a (synthetic) version 2 leaves a v1 backup behind.
	if err := s.migrateTo(ctx, twoMigrations(t), 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Settings().Set(ctx, "after_backup", "lost"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	garbage := bytes.Repeat([]byte("overwritten by a failing disk "), 400)
	for _, suf := range []string{"-wal", "-shm"} {
		_ = os.Remove(path + suf)
	}
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}

	// The shipped binary knows every embedded migration: the v1 backup that is
	// restored is migrated forward on the way in.
	r, err := Open(ctx, path, WithLogger(quietLog), WithClock(clock))
	if err != nil {
		t.Fatalf("Open after corruption: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	rec := r.Recovery()
	if rec == nil {
		t.Fatal("Recovery() = nil after a restore")
	}
	if rec.Kind != port.RecoveryRestoredFromBackup || rec.Reason == "" || rec.RestoredFrom == "" || rec.DamagedFile == "" || rec.BackupsDir == "" {
		t.Errorf("Recovery incomplete: %+v", rec)
	}
	if !bytes.Equal(readFile(t, rec.DamagedFile), garbage) {
		t.Error("the damaged database was not kept aside intact")
	}
	if v, ok, _ := r.Settings().Get(ctx, "before_backup"); !ok || v != "kept" {
		t.Errorf("data from the backup missing: %q, %v", v, ok)
	}
	if _, ok, _ := r.Settings().Get(ctx, "after_backup"); ok {
		t.Error("data newer than the backup survived a restore from that backup")
	}
	if got := pragma(t, r.db, "integrity_check"); got != "ok" {
		t.Errorf("restored database integrity_check = %q", got)
	}
}

func TestPageLevelCorruptionIsDetectedAndSetAside(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	s, err := Open(ctx, path, WithLogger(quietLog), WithClock(testClock()))
	if err != nil {
		t.Fatal(err)
	}
	// Enough rows that the table spans several pages.
	for i := 0; i < 400; i++ {
		if err := s.Settings().Set(ctx, "key-"+string(rune('a'+i%26))+string(rune('a'+i/26)), string(bytes.Repeat([]byte("x"), 200))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	data := readFile(t, path)
	if len(data) < 6*4096 {
		t.Skipf("database too small (%d bytes) to damage a data page", len(data))
	}
	// Keep the header intact, destroy later pages.
	for i := 3 * 4096; i < len(data) && i < 6*4096; i++ {
		data[i] = 0xAB
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	damaged := readFile(t, path)
	r, err := Open(ctx, path, WithLogger(quietLog), WithClock(testClock()))
	if err != nil {
		t.Fatalf("Open on a page-damaged database: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	rec := r.Recovery()
	if rec == nil || rec.Kind != port.RecoveryStartedEmpty {
		t.Fatalf("Recovery = %+v, want started_empty (the damage must be detected)", rec)
	}
	if !bytes.Equal(readFile(t, rec.DamagedFile), damaged) {
		t.Error("the page-damaged file was altered or lost")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a, b = append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
