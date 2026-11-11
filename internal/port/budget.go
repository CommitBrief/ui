// SPDX-License-Identifier: GPL-3.0-or-later

package port

import (
	"context"
	"time"
)

// BudgetGate answers "may another paid task start now?" (the daily cost budget
// of the Settings screen). A job kind asks it right before it calls the CLI.
type BudgetGate interface {
	// Exceeded reports whether today's spend has reached the budget. With no
	// budget set (0) it is always false. An error means the spend could not be
	// read; callers then let the task proceed and log it.
	Exceeded(ctx context.Context) (bool, error)
}

// CostMeter sums what finished tasks cost.
type CostMeter interface {
	// CostSince returns the summed cost_usd of the tasks that finished at or
	// after since.
	CostSince(ctx context.Context, since time.Time) (float64, error)
}
