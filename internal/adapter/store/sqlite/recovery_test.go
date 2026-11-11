// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CommitBrief/ui/internal/port"
)

func markerPath(dbFile string) string {
	return filepath.Join(filepath.Dir(dbFile), recoveryMarkerName)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// A damaged database with its WAL side files, as a crash leaves them.
func writeDamaged(t *testing.T, path string) (db, wal, shm []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db = bytes.Repeat([]byte("damaged main file "), 300)
	wal = bytes.Repeat([]byte("uncheckpointed wal "), 100)
	shm = bytes.Repeat([]byte("shared memory "), 50)
	for suf, content := range map[string][]byte{"": db, "-wal": wal, "-shm": shm} {
		if err := os.WriteFile(path+suf, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return db, wal, shm
}

// If setting the damaged database aside fails half way, the files that were
// already moved go back (the first Open of a real damaged database does the
// same through recoverDatabase): the main file is never separated from its WAL, nothing
// is left under a .corrupt- name, and no recovery is recorded.
func TestFailedSetAsideRestoresEveryFile(t *testing.T) {
	for _, failOn := range []string{"-wal", "-shm", ""} {
		t.Run("rename of "+filepath.Base("ui.db"+failOn)+" fails", func(t *testing.T) {
			path := dbPath(t)
			db, wal, shm := writeDamaged(t, path)

			failing := func(oldpath, newpath string) error {
				// Fail only the move into the quarantine name of the chosen file.
				if strings.Contains(newpath, ".corrupt-") && strings.HasSuffix(oldpath, "ui.db"+failOn) {
					return errors.New("the file is in use by another process")
				}
				return os.Rename(oldpath, newpath)
			}
			// recoverDatabase is called directly: a real Open would let SQLite
			// discard a junk WAL while it probes the file, and the point here
			// is the move itself.
			s := &Store{path: path, log: quietLog, now: testClock(), rename: failing}
			opened, rec, err := s.recoverDatabase(context.Background(), "test")
			if err == nil {
				_ = opened.Close()
				t.Fatalf("recoverDatabase succeeded (%+v) although the damaged database could not be set aside", rec)
			}

			for suf, want := range map[string][]byte{"": db, "-wal": wal, "-shm": shm} {
				if got, err := os.ReadFile(path + suf); err != nil || !bytes.Equal(got, want) {
					t.Errorf("ui.db%s is not back in place intact (err=%v)", suf, err)
				}
			}
			leftovers, _ := filepath.Glob(path + ".corrupt-*")
			if len(leftovers) != 0 {
				t.Errorf("files left under a quarantine name: %v", leftovers)
			}
			if exists(markerPath(path)) {
				t.Error("a recovery was recorded although nothing was set aside")
			}
		})
	}
}

// The notice must survive a failure after the damaged file was set aside: here
// the backup is restored, the migration that follows fails, Open returns an
// error -- and the next start, which finds a perfectly healthy-looking database,
// still reports the recovery.
func TestRecoveryNoticeSurvivesAFailedMigration(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	clock := testClock()

	// A healthy database with one pre-migration backup (schema version 1).
	s, err := Open(ctx, path, WithLogger(quietLog), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Settings().Set(ctx, "from_backup", "yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backupBeforeMigration(ctx, s.db, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suf := range []string{"-wal", "-shm"} {
		_ = os.Remove(path + suf)
	}
	damaged := bytes.Repeat([]byte("disk failure "), 500)
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatal(err)
	}

	// Start 1: recovery succeeds, then a migration fails (the new release
	// ships a broken 0002).
	real, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	broken := append(append([]migration(nil), real...), migration{
		version: 2, name: "broken", up: "SELECT nonsense FROM nowhere;", down: "SELECT 1;",
	})
	if _, err := Open(ctx, path, WithLogger(quietLog), WithClock(clock), func(o *options) { o.migrations = broken }); err == nil {
		t.Fatal("Open succeeded although its migration is broken")
	}
	if !exists(markerPath(path)) {
		t.Fatal("no recovery marker on disk after the failed start")
	}

	// Start 2: the migration problem is gone; the database looks healthy.
	r, err := Open(ctx, path, WithLogger(quietLog), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	rec := r.Recovery()
	if rec == nil {
		t.Fatal("the recovery was forgotten: Recovery() = nil on the start after the failure")
	}
	if rec.Kind != port.RecoveryRestoredFromBackup || rec.RestoredFrom == "" || rec.BackupsDir == "" ||
		!strings.HasPrefix(filepath.Base(rec.DamagedFile), "ui.db.corrupt-") {
		t.Errorf("pending recovery = %+v", rec)
	}
	if !bytes.Equal(readFile(t, rec.DamagedFile), damaged) {
		t.Error("the damaged copy named in the notice is not intact")
	}
	if v, ok, _ := r.Settings().Get(ctx, "from_backup"); !ok || v != "yes" {
		t.Errorf("restored data missing: %q, %v", v, ok)
	}

	// Acknowledging removes the marker; the start after that is quiet.
	if err := r.AcknowledgeRecovery(); err != nil {
		t.Fatal(err)
	}
	if exists(markerPath(path)) {
		t.Error("the marker is still there after the acknowledgement")
	}
	if err := r.AcknowledgeRecovery(); err != nil {
		t.Errorf("acknowledging twice must be harmless: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	q, err := Open(ctx, path, WithLogger(quietLog), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.Close() }()
	if q.Recovery() != nil {
		t.Errorf("an acknowledged recovery was reported again: %+v", q.Recovery())
	}
}

// The process can die between setting the damaged file aside and creating the
// new database. The next start finds no ui.db, creates an empty one -- and still
// reports the recovery, because the marker was written first.
func TestRecoveryNoticeSurvivesACrashAfterSettingAside(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// State left by the crash: the damaged file already under its new name,
	// the marker written, no ui.db.
	aside := path + ".corrupt-20261111T090000.000Z"
	if err := os.WriteFile(aside, []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &Store{path: path, log: quietLog}
	if err := st.writeRecoveryMarker(&port.DatabaseRecovery{
		Kind: port.RecoveryStartedEmpty, DamagedFile: aside, BackupsDir: filepath.Join(filepath.Dir(path), backupDirName),
	}); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path, WithLogger(quietLog), WithClock(testClock()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rec := s.Recovery()
	if rec == nil || rec.Kind != port.RecoveryStartedEmpty || rec.DamagedFile != aside {
		t.Fatalf("Recovery = %+v, want the pending started_empty recovery for %s", rec, aside)
	}
}

func TestUnreadableRecoveryMarkerDoesNotBlockStartup(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath(path), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, path, WithLogger(quietLog), WithClock(testClock()))
	if err != nil {
		t.Fatalf("a malformed marker stopped the application: %v", err)
	}
	defer func() { _ = s.Close() }()
	if s.Recovery() != nil {
		t.Errorf("Recovery = %+v from a malformed marker", s.Recovery())
	}
}

func TestRecoveryMarkerIsPrivate(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	writeDamaged(t, path)
	s, err := Open(ctx, path, WithLogger(quietLog), WithClock(testClock()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if !exists(markerPath(path)) {
		t.Fatal("no marker after a recovery")
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(markerPath(path))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("marker mode = %o, want owner-only", fi.Mode().Perm())
		}
	}
	if exists(markerPath(path) + ".tmp") {
		t.Error("temp file left behind")
	}
}

// --- tur 3: a failed attempt keeps an earlier, unacknowledged notice; the
// marker is written before anything is moved ---------------------------------

const earlierMarker = `{
  "version": 1,
  "kind": "started_empty",
  "damagedFile": "/data/ui.db.corrupt-20261110T080000.000Z",
  "backupsDir": "/data/backups"
}
`

func plantEarlierMarker(t *testing.T, dbFile string) {
	t.Helper()
	if err := os.WriteFile(markerPath(dbFile), []byte(earlierMarker), 0o600); err != nil {
		t.Fatal(err)
	}
}

func wantEarlierMarker(t *testing.T, dbFile, what string) {
	t.Helper()
	got, err := os.ReadFile(markerPath(dbFile))
	if err != nil {
		t.Fatalf("%s: the earlier recovery marker is gone: %v", what, err)
	}
	if string(got) != earlierMarker {
		t.Errorf("%s: the earlier recovery marker was altered:\n%s", what, got)
	}
}

func TestFailedAttemptKeepsAnEarlierUnacknowledgedNotice(t *testing.T) {
	ctx := context.Background()

	t.Run("the new marker cannot be written", func(t *testing.T) {
		path := dbPath(t)
		db, wal, shm := writeDamaged(t, path)
		plantEarlierMarker(t, path)
		// A directory where the temp file must go makes the write fail.
		if err := os.Mkdir(markerPath(path)+".tmp", 0o700); err != nil {
			t.Fatal(err)
		}
		s := &Store{path: path, log: quietLog, now: testClock(), rename: os.Rename}
		if opened, _, err := s.recoverDatabase(ctx, "test"); err == nil {
			_ = opened.Close()
			t.Fatal("recoverDatabase succeeded although the marker could not be written")
		}
		wantEarlierMarker(t, path, "marker write failure")
		// Nothing was moved either: the marker is written first.
		for suf, want := range map[string][]byte{"": db, "-wal": wal, "-shm": shm} {
			if got, err := os.ReadFile(path + suf); err != nil || !bytes.Equal(got, want) {
				t.Errorf("ui.db%s was touched although the attempt failed before moving (err=%v)", suf, err)
			}
		}
	})

	t.Run("a move fails", func(t *testing.T) {
		path := dbPath(t)
		writeDamaged(t, path)
		plantEarlierMarker(t, path)
		failing := func(oldpath, newpath string) error {
			if strings.Contains(newpath, ".corrupt-") && strings.HasSuffix(oldpath, "-shm") {
				return errors.New("in use")
			}
			return os.Rename(oldpath, newpath)
		}
		s := &Store{path: path, log: quietLog, now: testClock(), rename: failing}
		if opened, _, err := s.recoverDatabase(ctx, "test"); err == nil {
			_ = opened.Close()
			t.Fatal("recoverDatabase succeeded")
		}
		wantEarlierMarker(t, path, "move failure")
	})

	t.Run("the attempt is canceled", func(t *testing.T) {
		path := dbPath(t)
		writeDamaged(t, path)
		plantEarlierMarker(t, path)
		// A (junk) backup makes the restore loop reach its context check.
		backups := filepath.Join(filepath.Dir(path), backupDirName)
		if err := os.MkdirAll(backups, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(backups, "ui-20261110T100000.000Z-v0001.db"), []byte("junk"), 0o600); err != nil {
			t.Fatal(err)
		}
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		s := &Store{path: path, log: quietLog, now: testClock(), rename: os.Rename}
		if opened, _, err := s.recoverDatabase(cctx, "test"); !errors.Is(err, context.Canceled) {
			if opened != nil {
				_ = opened.Close()
			}
			t.Fatalf("recoverDatabase = %v, want context.Canceled", err)
		}
		wantEarlierMarker(t, path, "cancellation")
		if got, err := os.ReadFile(path); err != nil || len(got) == 0 {
			t.Errorf("the damaged database is not back in place (err=%v)", err)
		}
	})

	t.Run("the new database cannot be created", func(t *testing.T) {
		path := dbPath(t)
		writeDamaged(t, path)
		plantEarlierMarker(t, path)
		// A directory at the database path makes touch() fail after the move.
		failing := func(oldpath, newpath string) error {
			if err := os.Rename(oldpath, newpath); err != nil {
				return err
			}
			if oldpath == path { // the main file has just been moved aside
				return os.Mkdir(path, 0o700)
			}
			return nil
		}
		s := &Store{path: path, log: quietLog, now: testClock(), rename: failing}
		if opened, _, err := s.recoverDatabase(ctx, "test"); err == nil {
			_ = opened.Close()
			t.Fatal("recoverDatabase succeeded although no database could be created")
		}
		wantEarlierMarker(t, path, "creation failure")
	})
}

// With no earlier notice a failed attempt leaves no marker behind (the other
// half of the rule above).
func TestFailedAttemptLeavesNoMarkerWhenThereWasNone(t *testing.T) {
	path := dbPath(t)
	writeDamaged(t, path)
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	backups := filepath.Join(filepath.Dir(path), backupDirName)
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backups, "ui-20261110T100000.000Z-v0001.db"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Store{path: path, log: quietLog, now: testClock(), rename: os.Rename}
	if _, _, err := s.recoverDatabase(cctx, "test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if exists(markerPath(path)) {
		t.Error("a marker was left for a recovery that did not happen")
	}
}

// The marker must exist before the first file is moved, so that a crash at any
// later point -- including right after the main file has moved and before the
// new database exists -- cannot lose the notice.
func TestRecoveryMarkerIsWrittenBeforeAnythingIsMoved(t *testing.T) {
	path := dbPath(t)
	writeDamaged(t, path)

	var sawMarkerAtMove []bool
	spy := func(oldpath, newpath string) error {
		if strings.Contains(newpath, ".corrupt-") {
			sawMarkerAtMove = append(sawMarkerAtMove, exists(markerPath(path)))
		}
		return os.Rename(oldpath, newpath)
	}
	s, err := Open(context.Background(), path, WithLogger(quietLog), WithClock(testClock()),
		func(o *options) { o.rename = spy })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	if len(sawMarkerAtMove) == 0 {
		t.Fatal("the damaged database was never moved; the test proves nothing")
	}
	for i, saw := range sawMarkerAtMove {
		if !saw {
			t.Errorf("move %d happened before the recovery marker was on disk", i+1)
		}
	}
	// And the marker names the file that was moved.
	rec, err := s.readRecoveryMarker()
	if err != nil || rec == nil || rec.DamagedFile != s.Recovery().DamagedFile {
		t.Errorf("marker = %+v, %v; want the recovery of this start", rec, err)
	}
}
