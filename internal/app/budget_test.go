// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/settings"
	"github.com/CommitBrief/ui/internal/port"
)

// fakeClock is a port.Clock a test moves by hand. The location of the time it
// returns is the zone "today" is measured in.
type fakeClock struct {
	mu  sync.Mutex
	t   time.Time
	loc *time.Location
}

func newFakeClock(loc *time.Location, y int, m time.Month, d, h, min, sec int) *fakeClock {
	return &fakeClock{t: time.Date(y, m, d, h, min, sec, 0, loc), loc: loc}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t.In(c.loc)
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func finishedTask(db *memDB, cost float64, at time.Time) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.nextTask++
	db.tasks[db.nextTask] = port.TaskRecord{ID: db.nextTask, Status: "succeeded", CostUSD: cost, FinishedAt: &at}
}

func TestBudgetCountsTheLocalDay(t *testing.T) {
	ctx := context.Background()
	ist, _ := time.LoadLocation("Europe/Istanbul")
	clock := newFakeClock(ist, 2026, time.November, 11, 14, 0, 0)
	db := newMemDB()
	b := NewBudget(BudgetDeps{Settings: db, Costs: db, Clock: clock})

	// No budget set: never exceeded, whatever was spent.
	finishedTask(db, 50, clock.Now())
	if st, err := b.Status(ctx); err != nil || st.Exceeded || st.LimitUSD != 0 || st.SpentUSD != 50 {
		t.Fatalf("no budget: %+v, %v", st, err)
	}
	if over, _ := b.Exceeded(ctx); over {
		t.Error("a missing budget means no limit")
	}

	_ = db.Set(ctx, settings.KeyDailyBudgetUSD, "10")
	// 50 USD is already spent today.
	if over, _ := b.Exceeded(ctx); !over {
		t.Error("50 spent against 10 is exceeded")
	}

	// Yesterday's spend (23:59 local) is not today's; 00:00 local is.
	db2 := newMemDB()
	b2 := NewBudget(BudgetDeps{Settings: db2, Costs: db2, Clock: clock})
	_ = db2.Set(ctx, settings.KeyDailyBudgetUSD, "10")
	finishedTask(db2, 9, time.Date(2026, time.November, 10, 23, 59, 0, 0, ist))
	if st, _ := b2.Status(ctx); st.SpentUSD != 0 || st.Exceeded {
		t.Errorf("yesterday's spend counted today: %+v", st)
	}
	finishedTask(db2, 4, time.Date(2026, time.November, 11, 0, 0, 0, 0, ist))
	if st, _ := b2.Status(ctx); st.SpentUSD != 4 || st.Exceeded || !st.DayStart.Equal(time.Date(2026, time.November, 11, 0, 0, 0, 0, ist)) {
		t.Errorf("after midnight: %+v", st)
	}

	// Reaching the limit exactly is exceeding it.
	finishedTask(db2, 6, clock.Now())
	if st, _ := b2.Status(ctx); st.SpentUSD != 10 || !st.Exceeded {
		t.Errorf("10 of 10: %+v", st)
	}

	// A new day starts empty: the clock moving past midnight resets the spend.
	clock.Set(time.Date(2026, time.November, 12, 0, 5, 0, 0, ist))
	if st, _ := b2.Status(ctx); st.SpentUSD != 0 || st.Exceeded {
		t.Errorf("next day: %+v", st)
	}
}

func TestBudgetIgnoresADamagedSetting(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.UTC, 2026, time.November, 11, 14, 0, 0)
	db := newMemDB()
	b := NewBudget(BudgetDeps{Settings: db, Costs: db, Clock: clock})
	finishedTask(db, 3, clock.Now())
	for _, bad := range []string{"-5", "abc", "NaN", "99999999"} {
		_ = db.Set(ctx, settings.KeyDailyBudgetUSD, bad)
		if over, err := b.Exceeded(ctx); err != nil || over {
			t.Errorf("setting %q: exceeded=%v err=%v; a damaged row falls back to no limit", bad, over, err)
		}
	}
}
