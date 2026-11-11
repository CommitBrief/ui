// SPDX-License-Identifier: GPL-3.0-or-later

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "ui.lock")

	first, err := AcquireFileLock(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := AcquireFileLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire = %v, want ErrLocked", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Errorf("second Release must be a no-op, got %v", err)
	}

	again, err := AcquireFileLock(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := again.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestFileLockNilRelease(t *testing.T) {
	var l *FileLock
	if err := l.Release(); err != nil {
		t.Errorf("nil Release = %v", err)
	}
}

func TestFileLockCreatesPrivateFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes do not apply on Windows")
	}
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "ui.lock")
	l, err := AcquireFileLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Release() }()
	for p, want := range map[string]uint32{dir: 0o700, path: 0o600} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := uint32(fi.Mode().Perm()); got != want {
			t.Errorf("%s mode = %o, want %o", p, got, want)
		}
	}
}
