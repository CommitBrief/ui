// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package platform

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NewSysProcAttr returns the attributes that keep a console window from
// flashing up for the child. Assign the result to exec.Cmd.SysProcAttr before
// starting.
func NewSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
}

// jobBasicAccounting mirrors JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, which
// golang.org/x/sys/windows does not define.
type jobBasicAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// Tree is a started child and its descendants, held in a Job Object. The job is
// created with KILL_ON_JOB_CLOSE, so the tree dies with the UI even if the UI
// crashes before it can clean up.
type Tree struct {
	job windows.Handle
}

// AttachTree puts the already started process pid into a new Job Object.
//
// The child runs for a few microseconds before it is assigned, so a descendant
// it spawns in that window escapes the job. The CLI spawns nothing at startup,
// and Go's os/exec cannot create a process suspended, so this is accepted.
func AttachTree(pid int) (*Tree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("platform: create job object: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("platform: configure job object: %w", err)
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("platform: open process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(proc) }()
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("platform: assign process %d to job: %w", pid, err)
	}
	return &Tree{job: job}, nil
}

// Terminate stops every process in the tree. Windows has no graceful signal for
// console-less processes, so it is the same as Kill.
func (t *Tree) Terminate() error { return t.Kill() }

// Kill force-stops every process in the tree.
func (t *Tree) Kill() error {
	return windows.TerminateJobObject(t.job, 1)
}

// Alive reports whether any process of the tree is still running.
func (t *Tree) Alive() bool {
	var acct jobBasicAccounting
	err := windows.QueryInformationJobObject(
		t.job,
		windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&acct)),
		uint32(unsafe.Sizeof(acct)),
		nil,
	)
	if err != nil {
		return true
	}
	return acct.ActiveProcesses > 0
}

// Close releases the job handle. With KILL_ON_JOB_CLOSE this also kills any
// process of the tree that is still running.
func (t *Tree) Close() error {
	return windows.CloseHandle(t.job)
}
