// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CommitBrief/ui/internal/adapter/store/sqlite"
	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/domain/update"
	"github.com/CommitBrief/ui/internal/platform"
	"github.com/CommitBrief/ui/internal/port"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func testDirs(t *testing.T) platform.Dirs {
	t.Helper()
	root := t.TempDir()
	d := platform.Dirs{Config: filepath.Join(root, "state"), Data: filepath.Join(root, "state"), Log: filepath.Join(root, "logs")}
	if err := d.Ensure(); err != nil {
		t.Fatal(err)
	}
	return d
}

// A process that does not hold the data-directory lock must not touch the
// database at all: no creation, no migration, no recovery. This is what keeps
// a second launch from migrating or restoring the database of the running
// instance.
func TestOpenReposDoesNotTouchTheDatabaseWithoutTheLock(t *testing.T) {
	dirs := testDirs(t)
	holder, err := platform.AcquireFileLock(filepath.Join(dirs.Data, lockFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Release() }()

	// A fresh directory: Open would create ui.db (and migrate it).
	_, err = openState(context.Background(), dirs, quiet)
	if !errors.Is(err, platform.ErrLocked) {
		t.Fatalf("openState = %v, want ErrLocked", err)
	}
	if _, err := os.Stat(filepath.Join(dirs.Data, dbFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ui.db was created by a process without the lock: %v", err)
	}

	// A damaged database: Open would quarantine or report it. It must stay
	// byte for byte as it was, with no sidecar files.
	garbage := bytes.Repeat([]byte("not a database "), 300)
	dbPath := filepath.Join(dirs.Data, dbFileName)
	if err := os.WriteFile(dbPath, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = openState(context.Background(), dirs, quiet); !errors.Is(err, platform.ErrLocked) {
		t.Fatalf("openState on a damaged db = %v, want ErrLocked", err)
	}
	got, err := os.ReadFile(dbPath)
	if err != nil || !bytes.Equal(got, garbage) {
		t.Errorf("the database file was modified without the lock (err=%v)", err)
	}
	entries, _ := os.ReadDir(dirs.Data)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 { // ui.db and ui.lock, nothing else
		t.Errorf("unexpected files next to the database: %v", names)
	}
}

func TestOpenReposHoldsTheLockUntilClosed(t *testing.T) {
	dirs := testDirs(t)
	st, err := openState(context.Background(), dirs, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if st.Repos == nil || st.Health == nil {
		t.Fatal("state lacks a service")
	}
	if n := st.Health.DatabaseRecovery(); n != nil {
		t.Errorf("a healthy database reported a recovery: %+v", n)
	}

	// While the first instance is open a second one is turned away.
	if _, err := openState(context.Background(), dirs, quiet); !errors.Is(err, platform.ErrLocked) {
		t.Fatalf("second openState = %v, want ErrLocked", err)
	}

	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Closing released the lock.
	again, err := openState(context.Background(), dirs, quiet)
	if err != nil {
		t.Fatalf("openState after close: %v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
}

// A failed Open (here: a database written by a newer version, which is
// refused rather than recovered) must not leave the lock behind.
func TestOpenStateReleasesTheLockWhenTheDatabaseFails(t *testing.T) {
	dirs := testDirs(t)
	first, err := openState(context.Background(), dirs, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", filepath.Join(dirs.Data, dbFileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES (99, 'x')"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	if _, err := openState(context.Background(), dirs, quiet); !errors.Is(err, sqlite.ErrNewerSchema) {
		t.Fatalf("openState = %v, want ErrNewerSchema", err)
	}
	l, err := platform.AcquireFileLock(filepath.Join(dirs.Data, lockFileName))
	if err != nil {
		t.Fatalf("lock still held after a failed open: %v", err)
	}
	_ = l.Release()
}

// The user's decision for a database that is damaged and has no usable backup:
// set the file aside under a UTC-stamped name, start with an empty database,
// delete nothing, tell the UI, and keep the way back to the data open (JSON
// import works on the new database; the backups directory is untouched).
func TestOpenStateStartsEmptyOnADamagedDatabaseAndSignalsIt(t *testing.T) {
	ctx := context.Background()

	// A JSON export made earlier, from another database.
	srcDirs := testDirs(t)
	src, err := openState(ctx, srcDirs, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Repos.Add(ctx, app.RepoInput{Owner: "CommitBrief", Name: "ui"}); err != nil {
		t.Fatal(err)
	}
	export, err := src.Repos.ExportJSON(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}

	// The damaged installation, with a (unusable) backup lying around.
	dirs := testDirs(t)
	damaged := bytes.Repeat([]byte("overwritten by a failing disk "), 400)
	dbPath := filepath.Join(dirs.Data, dbFileName)
	if err := os.WriteFile(dbPath, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	backupsDir := filepath.Join(dirs.Data, "backups")
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(backupsDir, "ui-20261110T100000.000Z-v0001.db")
	backupBytes := bytes.Repeat([]byte("broken backup "), 300)
	if err := os.WriteFile(backup, backupBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := openState(ctx, dirs, quiet)
	if err != nil {
		t.Fatalf("a damaged database must not stop the application from starting: %v", err)
	}
	defer func() { _ = st.Close() }() // Close is idempotent for the pool; the test also closes explicitly below

	// The signal the frontend turns into a notification.
	n := st.Health.DatabaseRecovery()
	if n == nil {
		t.Fatal("no recovery notice for a database that was set aside")
	}
	if n.Kind != port.RecoveryStartedEmpty || n.RestoredFrom != "" || n.BackupsDir != backupsDir {
		t.Errorf("notice = %+v", n)
	}
	if filepath.Dir(n.DamagedFile) != dirs.Data || !strings.HasPrefix(filepath.Base(n.DamagedFile), dbFileName+".corrupt-") {
		t.Errorf("DamagedFile = %q, want %s.corrupt-<UTC stamp> in the data directory", n.DamagedFile, dbFileName)
	}

	// Nothing was deleted.
	if got, err := os.ReadFile(n.DamagedFile); err != nil || !bytes.Equal(got, damaged) {
		t.Errorf("the damaged file is not intact at %s (err=%v)", n.DamagedFile, err)
	}
	if got, err := os.ReadFile(backup); err != nil || !bytes.Equal(got, backupBytes) {
		t.Errorf("the backup was altered or deleted (err=%v)", err)
	}

	// The application runs on an empty database...
	if list, err := st.Repos.List(ctx); err != nil || len(list) != 0 {
		t.Fatalf("List = %v, %v; want an empty list", list, err)
	}
	// ...and the JSON import path is open.
	res, err := st.Repos.ImportJSON(ctx, export)
	if err != nil || res.Created != 1 {
		t.Fatalf("ImportJSON into the recovered database = %+v, %v", res, err)
	}

	// The notice stays until the user dismisses it.
	if st.Health.DatabaseRecovery() == nil {
		t.Error("the notice disappeared before it was acknowledged")
	}
	if err := st.Health.AcknowledgeDatabaseRecovery(); err != nil {
		t.Fatal(err)
	}
	if st.Health.DatabaseRecovery() != nil {
		t.Error("the notice is still reported after it was acknowledged")
	}
	// The acknowledgement is persisted: the next start reports nothing.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := openState(ctx, dirs, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = next.Close() }()
	if n := next.Health.DatabaseRecovery(); n != nil {
		t.Errorf("an acknowledged recovery was reported again: %+v", n)
	}
}

// A build without a release stamp must report the development version, which
// disables the self-update; the build/*/Taskfile.yml default for APP_VERSION
// spells the same string.
func TestUnstampedBuildIsADevelopmentBuild(t *testing.T) {
	if version != update.DevVersion || !update.IsDevBuild(version) {
		t.Errorf("version = %q, want the development version %q", version, update.DevVersion)
	}
	for _, f := range []string{"darwin", "linux", "windows"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "build", f, "Taskfile.yml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `default "`+update.DevVersion+`"`) {
			t.Errorf("build/%s/Taskfile.yml does not default APP_VERSION to %s", f, update.DevVersion)
		}
	}
}
