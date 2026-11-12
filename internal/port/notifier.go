// SPDX-License-Identifier: GPL-3.0-or-later

package port

import (
	"context"

	"github.com/CommitBrief/ui/internal/domain/errcode"
)

// NotificationKind says why the user is told something. The UI notifies for
// exactly four reasons: a review found something, a run failed, the daily
// budget is spent, and an update of the application itself is available.
type NotificationKind string

const (
	// NotifyNewFindings: a scheduled run reviewed pull requests and found issues.
	NotifyNewFindings NotificationKind = "new_findings"
	// NotifyRunFailed: a scheduled run failed or needs the user's attention.
	NotifyRunFailed NotificationKind = "run_failed"
	// NotifyBudgetExceeded: the daily cost budget is spent; no new task starts
	// until tomorrow.
	NotifyBudgetExceeded NotificationKind = "budget_exceeded"
	// NotifyUpdateAvailable: the signed release feed offers a newer version of
	// CommitBrief UI. Nothing is installed until the user confirms.
	NotifyUpdateAvailable NotificationKind = "update_available"
)

// Notification is what the scheduler wants the user to know. It carries facts,
// not text: the adapter words it for the operating system.
type Notification struct {
	Kind    NotificationKind
	JobID   int64
	JobName string
	RunID   int64
	// Findings and PullRequests count the findings and the reviewed pull
	// requests of a NotifyNewFindings run.
	Findings     int
	PullRequests int
	// ErrorCode is the failure class of a NotifyRunFailed run.
	ErrorCode errcode.Code
	// NeedsAttention is true when the job stopped until the user fixes
	// something (it will not run again by itself).
	NeedsAttention bool
	// BudgetUSD and SpentUSD describe a NotifyBudgetExceeded.
	BudgetUSD float64
	SpentUSD  float64
	// Version is the offered version of a NotifyUpdateAvailable (a validated
	// semantic version).
	Version string
}

// Notifier delivers a notification to the user (a native desktop notification).
// It is best effort: a notification that cannot be shown is logged by the
// implementation and never fails the caller. It must also return promptly: the
// scheduler calls it from its tick, and showing a notification can block for
// minutes (an operating system permission dialog), so an implementation queues
// the notification and shows it from its own goroutine.
type Notifier interface {
	Notify(ctx context.Context, n Notification)
}
