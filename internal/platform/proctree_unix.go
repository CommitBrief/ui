// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package platform

import (
	"errors"
	"fmt"
	"syscall"
)

// NewSysProcAttr returns the attributes that make a child the leader of its own
// process group, so the whole tree can be signalled at once. Assign the result
// to exec.Cmd.SysProcAttr before starting.
func NewSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// Tree is a started child and its descendants (its process group).
type Tree struct {
	pgid int
}

// AttachTree tracks the process group led by pid. The child must have been
// started with NewSysProcAttr.
func AttachTree(pid int) (*Tree, error) {
	// pgid 0 and 1 would address the caller's own group or every process.
	if pid <= 1 {
		return nil, fmt.Errorf("platform: refusing to track process %d", pid)
	}
	return &Tree{pgid: pid}, nil
}

// Terminate asks every process in the tree to stop (SIGTERM).
func (t *Tree) Terminate() error { return t.signal(syscall.SIGTERM) }

// Kill force-stops every process in the tree (SIGKILL).
func (t *Tree) Kill() error { return t.signal(syscall.SIGKILL) }

// Alive reports whether any process of the tree still exists.
func (t *Tree) Alive() bool {
	err := syscall.Kill(-t.pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Close releases the tree. It is a no-op on Unix.
func (t *Tree) Close() error { return nil }

func (t *Tree) signal(sig syscall.Signal) error {
	err := syscall.Kill(-t.pgid, sig)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
