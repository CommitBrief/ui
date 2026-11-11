// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package platform

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes a non-blocking exclusive flock(2). flock locks belong to the
// open file description, so a second AcquireFileLock in the same process
// conflicts as well, which is what the tests rely on.
func lockFile(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EWOULDBLOCK):
			return ErrLocked
		default:
			return fmt.Errorf("platform: lock file: %w", err)
		}
	}
}

func unlockFile(f *os.File) error {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_UN); err != nil {
		return fmt.Errorf("platform: unlock file: %w", err)
	}
	return nil
}
