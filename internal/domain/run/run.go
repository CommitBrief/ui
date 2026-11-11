// SPDX-License-Identifier: GPL-3.0-or-later

// Package run is the domain of one execution of a job: the Run, its Tasks
// (one repository or pull request each), the status state machine and the
// Outcome of a task. It is independent of the job kind. It performs no I/O.
package run

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/job"
)

// Status is the state of a run or a task.
//
//	queued -> running -> succeeded | failed | canceled | attention
//	queued -> canceled
//
// attention means the work cannot succeed until the user fixes something (the
// CLI is missing or too old, gh is not logged in, the provider is not an API
// provider, ...); failed is everything else that went wrong.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
	StatusAttention Status = "attention"
)

// Valid reports whether s is one of the six statuses.
func (s Status) Valid() bool {
	switch s {
	case StatusQueued, StatusRunning, StatusSucceeded, StatusFailed, StatusCanceled, StatusAttention:
		return true
	}
	return false
}

// Terminal reports whether s is an end state: nothing leaves it.
func (s Status) Terminal() bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusCanceled, StatusAttention:
		return true
	}
	return false
}

// CanTransitionTo reports whether the state machine allows s -> next.
func (s Status) CanTransitionTo(next Status) bool {
	switch s {
	case StatusQueued:
		return next == StatusRunning || next == StatusCanceled
	case StatusRunning:
		return next.Terminal()
	}
	return false
}

// Trigger is what started a run.
type Trigger string

const (
	TriggerManual   Trigger = "manual"
	TriggerSchedule Trigger = "schedule"
	TriggerCatchup  Trigger = "catchup"
)

// Valid reports whether t is a known trigger (the database enforces the same
// set).
func (t Trigger) Valid() bool {
	return t == TriggerManual || t == TriggerSchedule || t == TriggerCatchup
}

// Task is one unit of work inside a run: a review of one repository's local
// changes or of one pull request. Spec is kind-owned, written by the kind's
// Plan and read back by its Execute.
type Task struct {
	ID    int64 `json:"id"`
	RunID int64 `json:"runId"`
	// Kind is the job kind that planned the task.
	Kind job.Kind `json:"kind"`
	// RepositoryID is 0 when the task has no repository (or it was deleted).
	RepositoryID int64 `json:"repositoryId"`
	// PRNumber is 0 for a local review.
	PRNumber int             `json:"prNumber"`
	Spec     json.RawMessage `json:"spec"`
	Status   Status          `json:"status"`
	// ErrorCode is the errcode of a task that did not succeed.
	ErrorCode  errcode.Code `json:"errorCode"`
	ExitCode   *int         `json:"exitCode"`
	CLIVersion string       `json:"cliVersion"`
	HeadOID    string       `json:"headOid"`
	Verdict    string       `json:"verdict"`
	// Findings and Report are the stored results (JSON arrays/objects).
	Findings json.RawMessage `json:"findings,omitempty"`
	Report   json.RawMessage `json:"report,omitempty"`
	// FindingCount is the number of stored findings, known even when a
	// listing leaves the payload out.
	FindingCount int `json:"findingCount"`
	// LogPath is where the raw stderr was written; it never leaves the
	// process, HasLog tells the screen whether a log can be read.
	LogPath    string     `json:"-"`
	HasLog     bool       `json:"hasLog"`
	CostUSD    float64    `json:"costUsd"`
	QueuedAt   time.Time  `json:"queuedAt"`
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`

	// LockKeys are the exclusion keys the task needs while it runs; two tasks
	// that share a key never run at the same time. Set by Plan, in memory only.
	LockKeys []string `json:"-"`
}

// Run is one execution of a job. JobID is 0 for a one-off run that has no
// stored job.
type Run struct {
	ID         int64        `json:"id"`
	JobID      int64        `json:"jobId"`
	Trigger    Trigger      `json:"trigger"`
	Status     Status       `json:"status"`
	ErrorCode  errcode.Code `json:"errorCode"`
	CostUSD    float64      `json:"costUsd"`
	StartedAt  time.Time    `json:"startedAt"`
	FinishedAt *time.Time   `json:"finishedAt"`
	Tasks      []Task       `json:"tasks"`
}

// Outcome is what a job kind reports for a task that ran to the end (or as far
// as it got: a degraded review has a report and still an error). Status and
// error code are derived from the error by Classify, not set by the kind.
type Outcome struct {
	ExitCode   *int
	CLIVersion string
	HeadOID    string
	Verdict    string
	Findings   json.RawMessage
	Report     json.RawMessage
	CostUSD    float64
}

// attentionCodes are the failure classes the user has to fix before a retry
// can help (UI-0004 section 5; architecture "Yeniden deneme").
var attentionCodes = map[errcode.Code]bool{
	errcode.CLINotFound:        true,
	errcode.CLITooOld:          true,
	errcode.GHNotFound:         true,
	errcode.GHNotAuthenticated: true,
	errcode.NotARepo:           true,
	errcode.ProviderNotAPI:     true,
	errcode.SelfPR:             true,
	errcode.CapabilityMissing:  true,
}

// Classify maps the error of a finished task to its end status and error code.
// nil is success; a canceled context or a Canceled code is canceled; the codes
// the user has to act on are attention; everything else is failed.
func Classify(err error) (Status, errcode.Code) {
	if err == nil {
		return StatusSucceeded, ""
	}
	code := errcode.CodeOf(err)
	switch {
	case code == errcode.Canceled:
		return StatusCanceled, errcode.Canceled
	case attentionCodes[code]:
		return StatusAttention, code
	default:
		return StatusFailed, code
	}
}

// Aggregate derives a run's status from the statuses of its tasks. A run is
// running while any task is not terminal and at least one has started; queued
// while none has; otherwise the worst end state wins: failed, then attention,
// then canceled, then succeeded. A run without tasks is succeeded.
func Aggregate(tasks []Status) Status {
	var queued, running, failed, attention, canceled int
	for _, s := range tasks {
		switch s {
		case StatusQueued:
			queued++
		case StatusRunning:
			running++
		case StatusFailed:
			failed++
		case StatusAttention:
			attention++
		case StatusCanceled:
			canceled++
		}
	}
	switch {
	case running > 0:
		return StatusRunning
	case queued > 0 && queued < len(tasks):
		// Some tasks finished, some wait (for a lock or a worker): still going.
		return StatusRunning
	case queued > 0:
		return StatusQueued
	case failed > 0:
		return StatusFailed
	case attention > 0:
		return StatusAttention
	case canceled > 0:
		return StatusCanceled
	default:
		return StatusSucceeded
	}
}

// RunErrorCode picks the error code a finished run reports: the code of its
// first task that did not succeed, by task order.
func RunErrorCode(tasks []Task) errcode.Code {
	for _, t := range tasks {
		if t.Status.Terminal() && t.Status != StatusSucceeded && t.ErrorCode != "" {
			return t.ErrorCode
		}
	}
	return ""
}

// TotalCost sums the cost of the tasks.
func TotalCost(tasks []Task) float64 {
	var sum float64
	for _, t := range tasks {
		sum += t.CostUSD
	}
	return sum
}

// LockKeyForPath is the exclusion key of a local checkout: two reviews that
// run inside the same working tree are serialized (architecture "Eşzamanlılık",
// V7). p is the stored, already cleaned path; goos is the operating system the
// path belongs to, because Windows paths compare case-insensitively.
func LockKeyForPath(goos, p string) string {
	if goos == "windows" {
		p = lowerASCII(p)
	}
	return "path:" + p
}

// LockKeyForPR is the exclusion key of one pull request: the same pull request
// is never reviewed twice at once. GitHub names are case-insensitive.
func LockKeyForPR(repoSlug string, number int) string {
	return "pr:" + lowerASCII(repoSlug) + "#" + strconv.Itoa(number)
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
