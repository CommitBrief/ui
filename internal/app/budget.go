// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"fmt"
	"time"

	"github.com/CommitBrief/ui/internal/domain/settings"
	"github.com/CommitBrief/ui/internal/port"
)

// BudgetDeps are what the daily cost budget needs.
type BudgetDeps struct {
	// Settings holds the budget (settings.KeyDailyBudgetUSD); it is read at every
	// check, so a change in the Settings screen applies at once.
	Settings port.SettingsStore
	// Costs sums what the day's finished tasks cost (cost_usd).
	Costs port.CostMeter
	// Clock decides when "today" started: local midnight in the zone of the
	// time it returns.
	Clock port.Clock
}

// Budget tracks the day's spend against the daily limit. Unattended work asks it
// before it starts a paid task (the scheduler before it starts a run, the bot
// right before it calls the CLI). The spend is the summed cost_usd of every task
// that finished since local midnight, whoever started it; a task that is still
// running counts when it finishes, so one run can overshoot the limit by its own
// cost.
type Budget struct {
	d BudgetDeps
}

var _ port.BudgetGate = (*Budget)(nil)

// NewBudget wires the budget.
func NewBudget(d BudgetDeps) *Budget { return &Budget{d: d} }

// BudgetStatus is today's spend against the limit.
type BudgetStatus struct {
	// LimitUSD is the daily budget; 0 means no limit.
	LimitUSD float64 `json:"limitUsd"`
	SpentUSD float64 `json:"spentUsd"`
	// Exceeded is true when there is a limit and the spend has reached it.
	Exceeded bool `json:"exceeded"`
	// DayStart is the local midnight the spend is counted from.
	DayStart time.Time `json:"dayStart"`
}

// Status reads the limit and the day's spend.
func (b *Budget) Status(ctx context.Context) (BudgetStatus, error) {
	now := b.d.Clock.Now()
	y, m, d := now.Date()
	st := BudgetStatus{DayStart: time.Date(y, m, d, 0, 0, 0, 0, now.Location())}

	rows, err := b.d.Settings.All(ctx)
	if err != nil {
		return BudgetStatus{}, fmt.Errorf("app: read the daily budget: %w", err)
	}
	st.LimitUSD = settings.FromStored(rows).DailyBudgetUSD
	spent, err := b.d.Costs.CostSince(ctx, st.DayStart)
	if err != nil {
		return BudgetStatus{}, fmt.Errorf("app: sum today's cost: %w", err)
	}
	st.SpentUSD = spent
	st.Exceeded = st.LimitUSD > 0 && st.SpentUSD >= st.LimitUSD
	return st, nil
}

// Exceeded implements port.BudgetGate.
func (b *Budget) Exceeded(ctx context.Context) (bool, error) {
	st, err := b.Status(ctx)
	if err != nil {
		return false, err
	}
	return st.Exceeded, nil
}
