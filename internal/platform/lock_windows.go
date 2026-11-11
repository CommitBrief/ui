// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes a non-blocking exclusive LockFileEx lock on the first byte.
// Handles are per open, so a second AcquireFileLock in the same process
// conflicts as well.
func lockFile(f *os.File) error {
	var ol windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION):
		return ErrLocked
	default:
		return fmt.Errorf("platform: lock file: %w", err)
	}
}

func unlockFile(f *os.File) error {
	var ol windows.Overlapped
	if err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol); err != nil {
		return fmt.Errorf("platform: unlock file: %w", err)
	}
	return nil
}
