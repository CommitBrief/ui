// SPDX-License-Identifier: GPL-3.0-or-later

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrLocked means another process (or another holder in this one) already
// holds the lock.
var ErrLocked = errors.New("platform: lock is held by another instance")

// FileLock is an exclusive, advisory, process-wide lock backed by a file. The
// operating system drops it when the holder exits or crashes, so there is no
// stale lock to clean up. It guards the application's data directory: whoever
// holds it may open the database, migrate it and restore it.
type FileLock struct {
	f *os.File
}

// AcquireFileLock takes the lock stored in the file at path (created with mode
// 0600 together with its directory 0700 when missing) without waiting. It
// returns ErrLocked when someone else holds it.
func AcquireFileLock(path string) (*FileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("platform: create lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("platform: open lock file: %w", err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &FileLock{f: f}, nil
}

// Release drops the lock. Releasing twice is a no-op. The file itself stays:
// removing it would let a second process lock a new file while the first still
// holds the old one.
func (l *FileLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	unlockErr := unlockFile(f)
	if err := f.Close(); err != nil && unlockErr == nil {
		return err
	}
	return unlockErr
}
