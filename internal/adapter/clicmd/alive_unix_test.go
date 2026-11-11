// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package clicmd

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process with this pid still exists.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
