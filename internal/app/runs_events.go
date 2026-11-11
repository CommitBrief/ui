// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/run"
)

// Event names the run service emits to the frontend (through port.EventEmitter).
// The desktop adapter registers each name with its payload type, so the
// generated bindings carry both.
const (
	// EventTaskLog carries one line of a task's stderr while it runs.
	EventTaskLog = "task:log"
	// EventTaskStatus carries a task's status change.
	EventTaskStatus = "task:status"
	// EventRunStatus carries a run's status change.
	EventRunStatus = "run:status"
)

// TaskLogEvent is the payload of EventTaskLog. The line is already masked.
type TaskLogEvent struct {
	RunID  int64  `json:"runId"`
	TaskID int64  `json:"taskId"`
	Line   string `json:"line"`
}

// TaskStatusEvent is the payload of EventTaskStatus.
type TaskStatusEvent struct {
	RunID     int64        `json:"runId"`
	TaskID    int64        `json:"taskId"`
	Status    run.Status   `json:"status"`
	ErrorCode errcode.Code `json:"errorCode"`
}

// RunStatusEvent is the payload of EventRunStatus.
type RunStatusEvent struct {
	RunID     int64        `json:"runId"`
	Status    run.Status   `json:"status"`
	ErrorCode errcode.Code `json:"errorCode"`
}
