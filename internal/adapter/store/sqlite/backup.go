// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/CommitBrief/ui/internal/port"
)

// Backups live in <db dir>/backups, named ui-<UTC timestamp>-v<schema
// version>.db, so that lexical order is chronological order. Only the newest
// maxBackups are kept.
const (
	backupDirName = "backups"
	maxBackups    = 5
)

func (s *Store) backupDir() string { return filepath.Join(filepath.Dir(s.path), backupDirName) }

// backupBeforeMigration writes a consistent copy of the live database
// (VACUUM INTO, which includes what is still in the WAL) before a migration
// runs on top of schema version `version`. It returns the backup's path.
func (s *Store) backupBeforeMigration(ctx context.Context, db *sql.DB, version int) (string, error) {
	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("sqlite: create backup directory: %w", err)
	}
	stamp := s.now().UTC().Format("20060102T150405.000Z")
	var dest string
	for i := 0; ; i++ {
		name := fmt.Sprintf("ui-%s-v%04d.db", stamp, version)
		if i > 0 {
			name = fmt.Sprintf("ui-%s-%d-v%04d.db", stamp, i, version)
		}
		dest = filepath.Join(dir, name)
		if _, err := os.Lstat(dest); errors.Is(err, os.ErrNotExist) {
			break
		}
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		_ = os.Remove(dest)
		return "", fmt.Errorf("sqlite: back up database: %w", err)
	}
	if err := os.Chmod(dest, 0o600); err != nil {
		return "", fmt.Errorf("sqlite: protect backup: %w", err)
	}
	s.pruneBackups()
	return dest, nil
}

// listBackups returns the backup files, newest first.
func (s *Store) listBackups() []string {
	matches, err := filepath.Glob(filepath.Join(s.backupDir(), "ui-*.db"))
	if err != nil {
		return nil
	}
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	return matches
}

func (s *Store) pruneBackups() {
	all := s.listBackups()
	for _, old := range all[min(len(all), maxBackups):] {
		if err := os.Remove(old); err != nil {
			s.log.Warn("could not remove an old database backup", "file", old, "error", err)
		}
	}
}

// sideSuffixes are the SQLite files that belong to a database next to the main
// file.
var sideSuffixes = []string{"", "-wal", "-shm"}

// recoverDatabase is called after Open found the database damaged. Nothing is
// deleted. It moves the damaged file (and its WAL side files) to
// <path>.corrupt-<UTC stamp>, then restores the newest backup that opens and
// passes its own integrity check; when none does it starts a new, empty
// database. If it cannot finish (canceled, or the new file cannot be created)
// the damaged files are put back so that the next start sees them again.
func (s *Store) recoverDatabase(ctx context.Context, reason string) (*sql.DB, *port.DatabaseRecovery, error) {
	quarantined := s.uniqueQuarantineName()
	rec := &port.DatabaseRecovery{
		Kind:        port.RecoveryStartedEmpty,
		DamagedFile: quarantined,
		BackupsDir:  s.backupDir(),
		Reason:      reason,
	}

	// An earlier recovery that the user has not acknowledged yet has a marker.
	// This attempt replaces it; if the attempt fails, the earlier one is put
	// back, so a failed attempt never costs that notice.
	prev, err := s.captureMarker()
	if err != nil {
		return nil, nil, err
	}

	// Record the recovery on disk before anything is moved: the name of the
	// damaged copy is already known, and the notice must survive a crash at any
	// later point (even between the last move and the creation of the new
	// database) as well as a later error such as a failed migration.
	if err := s.writeRecoveryMarker(rec); err != nil {
		return nil, nil, err
	}

	// Side files first, the main file last: if the process dies half way, the
	// main file is still where Open looks for it, the damage is found again and
	// nothing has been separated from its WAL. A failed move undoes the moves
	// already made.
	var moved []string
	for _, suf := range []string{"-wal", "-shm", ""} {
		err := s.rename(s.path+suf, quarantined+suf)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			for i := len(moved) - 1; i >= 0; i-- {
				if rbErr := s.rename(quarantined+moved[i], s.path+moved[i]); rbErr != nil {
					s.log.Error("could not undo setting the damaged database aside", "file", quarantined+moved[i], "error", rbErr)
				}
			}
			s.restoreMarker(prev)
			return nil, nil, fmt.Errorf("sqlite: set the damaged database aside: %w", err)
		}
		moved = append(moved, suf)
	}
	clearLive := func() {
		for _, suf := range sideSuffixes {
			_ = os.Remove(s.path + suf)
		}
	}
	putBack := func() {
		clearLive()
		for _, suf := range []string{"", "-wal", "-shm"} {
			if err := s.rename(quarantined+suf, s.path+suf); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.log.Error("could not put the damaged database back", "file", quarantined+suf, "error", err)
			}
		}
		s.restoreMarker(prev)
	}

	for _, backup := range s.listBackups() {
		if err := ctx.Err(); err != nil {
			putBack()
			return nil, nil, err
		}
		if err := copyFile(backup, s.path); err != nil {
			s.log.Warn("could not restore a backup", "backup", backup, "error", err)
			clearLive()
			continue
		}
		db, err := openDB(s.path)
		if err != nil {
			clearLive()
			continue
		}
		if err := verify(ctx, db); err != nil {
			s.log.Warn("backup failed its integrity check", "backup", backup, "error", err)
			_ = db.Close()
			clearLive()
			continue
		}
		rec.Kind, rec.RestoredFrom = port.RecoveryRestoredFromBackup, backup
		if err := s.writeRecoveryMarker(rec); err != nil {
			// The marker still names the damaged file; only its kind is stale.
			s.log.Warn("could not update the recovery marker", "error", err)
		}
		return db, rec, nil
	}

	// No usable backup: start empty. The backups (if any) stay where they are.
	if err := touch(s.path); err != nil {
		putBack()
		return nil, nil, err
	}
	db, err := openDB(s.path)
	if err == nil {
		err = verify(ctx, db)
	}
	if err != nil {
		if db != nil {
			_ = db.Close()
		}
		putBack()
		return nil, nil, fmt.Errorf("sqlite: start an empty database: %w", err)
	}
	return db, rec, nil
}

// uniqueQuarantineName is <path>.corrupt-<UTC stamp>, with a counter when that
// name (or one of its side files) is taken, so that an earlier damaged copy is
// never overwritten.
func (s *Store) uniqueQuarantineName() string {
	base := s.path + ".corrupt-" + s.now().UTC().Format("20060102T150405.000Z")
	name := base
	for i := 1; ; i++ {
		taken := false
		for _, suf := range sideSuffixes {
			if _, err := os.Lstat(name + suf); err == nil {
				taken = true
				break
			}
		}
		if !taken {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

// copyFile copies src to dst with mode 0600, replacing dst.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
