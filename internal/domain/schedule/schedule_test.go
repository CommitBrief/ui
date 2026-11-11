// SPDX-License-Identifier: GPL-3.0-or-later

package schedule

import (
	"errors"
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func at(loc *time.Location, y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, loc)
}

func hhmm(ts []time.Time, loc *time.Location) []string {
	out := make([]string, len(ts))
	for i, s := range ts {
		out[i] = s.In(loc).Format("15:04")
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestScheduleValidate(t *testing.T) {
	ok := Window{Days: Weekdays, Start: 9*60 + 30, End: 17*60 + 45, Every: 15, TZ: "Europe/Istanbul"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid window: %v", err)
	}
	cases := []struct {
		name  string
		mod   func(*Window)
		field string
	}{
		{"no days", func(w *Window) { w.Days = 0 }, "days"},
		{"day bit 8", func(w *Window) { w.Days = 0x80 }, "days"},
		{"negative start", func(w *Window) { w.Start = -1 }, "start"},
		{"start 24:00", func(w *Window) { w.Start = 1440 }, "start"},
		{"end before start", func(w *Window) { w.End = w.Start }, "end"},
		{"end after midnight", func(w *Window) { w.End = 1441 }, "end"},
		{"zero interval", func(w *Window) { w.Every = 0 }, "every"},
		{"huge interval", func(w *Window) { w.Every = 1441 }, "every"},
		{"empty tz", func(w *Window) { w.TZ = "" }, "tz"},
		{"local tz", func(w *Window) { w.TZ = "Local" }, "tz"},
		{"unknown tz", func(w *Window) { w.TZ = "Mars/Olympus" }, "tz"},
		{"path-like tz", func(w *Window) { w.TZ = "../../etc/passwd" }, "tz"},
	}
	for _, tc := range cases {
		w := ok
		tc.mod(&w)
		var e *Error
		if err := w.Validate(); !errors.As(err, &e) || e.Field != tc.field {
			t.Errorf("%s: Validate = %v, want field %s", tc.name, err, tc.field)
		}
	}
}

func TestScheduleDaysMaskUsesMondayAsBitZero(t *testing.T) {
	if !Monday.Has(time.Monday) || Monday.Has(time.Tuesday) || !Sunday.Has(time.Sunday) || Sunday.Has(time.Saturday) {
		t.Error("Monday must be bit 0 and Sunday bit 6")
	}
	if Weekdays != 0b0011111 || EveryDay != 0b1111111 {
		t.Errorf("Weekdays = %b, EveryDay = %b", Weekdays, EveryDay)
	}
}

// Slots are Start + k*Every inside [Start, End): End itself is not a slot, and a
// last slot that does not land on End is kept.
func TestScheduleSlotsAreStartPlusEveryAndExcludeEnd(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	w := Window{Days: Weekdays, Start: 9*60 + 30, End: 11 * 60, Every: 45, TZ: "Europe/Istanbul"}
	got := hhmm(w.SlotsOn(at(ist, 2026, time.November, 11, 3, 0)), ist) // a Wednesday
	if want := []string{"09:30", "10:15"}; !equalStrings(got, want) {
		t.Errorf("slots = %v, want %v (11:00 is End, excluded)", got, want)
	}

	w = Window{Days: Weekdays, Start: 9 * 60, End: 10 * 60, Every: 15, TZ: "Europe/Istanbul"}
	if got, want := hhmm(w.SlotsOn(at(ist, 2026, time.November, 11, 0, 0)), ist), []string{"09:00", "09:15", "09:30", "09:45"}; !equalStrings(got, want) {
		t.Errorf("slots = %v, want %v", got, want)
	}

	// Saturday is not active.
	if s := w.SlotsOn(at(ist, 2026, time.November, 14, 9, 0)); len(s) != 0 {
		t.Errorf("Saturday slots = %v, want none", s)
	}
}

func TestScheduleNextDueBoundaries(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	w := Window{Days: Weekdays, Start: 9 * 60, End: 10 * 60, Every: 30, TZ: "Europe/Istanbul"}
	tests := []struct {
		name  string
		after time.Time
		want  time.Time
	}{
		{"before the window", at(ist, 2026, time.November, 11, 8, 0), at(ist, 2026, time.November, 11, 9, 0)},
		{"exactly on a slot: strictly after", at(ist, 2026, time.November, 11, 9, 0), at(ist, 2026, time.November, 11, 9, 30)},
		{"between slots", at(ist, 2026, time.November, 11, 9, 10), at(ist, 2026, time.November, 11, 9, 30)},
		{"after the last slot", at(ist, 2026, time.November, 11, 9, 30), at(ist, 2026, time.November, 12, 9, 0)},
		{"friday evening skips the weekend", at(ist, 2026, time.November, 13, 18, 0), at(ist, 2026, time.November, 16, 9, 0)},
	}
	for _, tc := range tests {
		got, ok := w.NextDue(tc.after)
		if !ok || !got.Equal(tc.want) {
			t.Errorf("%s: NextDue = %v, %v; want %v", tc.name, got, ok, tc.want)
		}
	}
	if _, ok := (Window{}).NextDue(time.Now()); ok {
		t.Error("an invalid window has no next slot")
	}
}

func TestScheduleMissedIsLimitedToTheCurrentWindow(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	w := Window{Days: Weekdays, Start: 9 * 60, End: 17 * 60, Every: 15, TZ: "Europe/Istanbul"}

	// Slept from 09:50 to 11:20: every slot in between is "missed", the caller
	// runs once. Slots are returned in order.
	got := w.Missed(at(ist, 2026, time.November, 11, 9, 50), at(ist, 2026, time.November, 11, 11, 20))
	if len(got) != 6 || hhmm(got, ist)[0] != "10:00" || hhmm(got, ist)[5] != "11:15" {
		t.Errorf("missed = %v", hhmm(got, ist))
	}

	// A normal tick: exactly the slot that just came due.
	got = w.Missed(at(ist, 2026, time.November, 11, 9, 14), at(ist, 2026, time.November, 11, 9, 15))
	if len(got) != 1 || !got[0].Equal(at(ist, 2026, time.November, 11, 9, 15)) {
		t.Errorf("tick missed = %v", got)
	}
	// ... and none before it.
	if got := w.Missed(at(ist, 2026, time.November, 11, 9, 15), at(ist, 2026, time.November, 11, 9, 16)); len(got) != 0 {
		t.Errorf("a slot already seen is reported again: %v", got)
	}

	// Outside the window nothing is caught up: after the close, before the open,
	// on a weekend.
	for name, now := range map[string]time.Time{
		"after close":  at(ist, 2026, time.November, 11, 17, 5),
		"at close":     at(ist, 2026, time.November, 11, 17, 0),
		"before open":  at(ist, 2026, time.November, 12, 8, 59),
		"on a weekend": at(ist, 2026, time.November, 14, 10, 0),
	} {
		if got := w.Missed(at(ist, 2026, time.November, 11, 9, 0), now); len(got) != 0 {
			t.Errorf("%s: missed = %v, want none", name, got)
		}
	}

	// A slept-through night: the slots of yesterday's window are dropped, today's
	// first slot (already past) is the one catch-up.
	got = w.Missed(at(ist, 2026, time.November, 11, 16, 50), at(ist, 2026, time.November, 12, 9, 10))
	if len(got) != 1 || !got[0].Equal(at(ist, 2026, time.November, 12, 9, 0)) {
		t.Errorf("overnight missed = %v", got)
	}
}

// Europe/Istanbul's last daylight-saving change was the spring of 2016: on 27
// March the clock jumped from 03:00 to 04:00. Slots keep the wall clock.
func TestScheduleDSTIstanbulSpringForward(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	w := Window{Days: EveryDay, Start: 2 * 60, End: 5 * 60, Every: 30, TZ: "Europe/Istanbul"}

	// 03:00 and 03:30 do not exist: they resolve to 04:00 and 04:30, which are
	// also real slots, so four distinct slots remain.
	got := w.SlotsOn(at(ist, 2016, time.March, 27, 12, 0))
	if want := []string{"02:00", "02:30", "04:00", "04:30"}; !equalStrings(hhmm(got, ist), want) {
		t.Errorf("spring day slots = %v, want %v", hhmm(got, ist), want)
	}
	for i := 1; i < len(got); i++ {
		if !got[i].After(got[i-1]) {
			t.Errorf("slots not strictly ascending: %v", got)
		}
	}

	// The next day is a normal 24-hour day.
	next := w.SlotsOn(at(ist, 2016, time.March, 28, 12, 0))
	if want := []string{"02:00", "02:30", "03:00", "03:30", "04:00", "04:30"}; !equalStrings(hhmm(next, ist), want) {
		t.Errorf("day after = %v, want %v", hhmm(next, ist), want)
	}

	// 09:00 stays 09:00 on the wall across the change; only the elapsed time
	// between the two 09:00s is 23 hours.
	nine := Window{Days: EveryDay, Start: 9 * 60, End: 9*60 + 1, Every: 1, TZ: "Europe/Istanbul"}
	a, _ := nine.NextDue(at(ist, 2016, time.March, 25, 9, 0))
	b, _ := nine.NextDue(a)
	if a.In(ist).Hour() != 9 || b.In(ist).Hour() != 9 || b.Sub(a) != 23*time.Hour {
		t.Errorf("09:00 slots %v -> %v (%v apart), want 23h across the spring change", a, b, b.Sub(a))
	}

	// Since September 2016 Turkey has no daylight saving: every day is 24 hours.
	c, _ := nine.NextDue(at(ist, 2026, time.March, 28, 9, 0))
	d, _ := nine.NextDue(c)
	if d.Sub(c) != 24*time.Hour {
		t.Errorf("2026 spring: %v apart, want 24h", d.Sub(c))
	}
}

func TestScheduleDSTNewYorkSpringForward(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	// 2026-03-08: 02:00 EST jumps to 03:00 EDT.
	w := Window{Days: EveryDay, Start: 1 * 60, End: 4 * 60, Every: 30, TZ: "America/New_York"}
	got := w.SlotsOn(at(ny, 2026, time.March, 8, 12, 0))
	if want := []string{"01:00", "01:30", "03:00", "03:30"}; !equalStrings(hhmm(got, ny), want) {
		t.Errorf("slots = %v, want %v", hhmm(got, ny), want)
	}

	nine := Window{Days: EveryDay, Start: 9 * 60, End: 9*60 + 1, Every: 1, TZ: "America/New_York"}
	a, _ := nine.NextDue(at(ny, 2026, time.March, 6, 9, 0))
	b, _ := nine.NextDue(a)
	if b.In(ny).Hour() != 9 || b.Sub(a) != 23*time.Hour {
		t.Errorf("09:00 -> 09:00 is %v, want 23h", b.Sub(a))
	}
}

func TestScheduleDSTNewYorkFallBack(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	// 2026-11-01: 02:00 EDT falls back to 01:00 EST, so 01:00-01:59 happens
	// twice. Each wall-clock slot runs once.
	w := Window{Days: EveryDay, Start: 0*60 + 30, End: 3 * 60, Every: 30, TZ: "America/New_York"}
	got := w.SlotsOn(at(ny, 2026, time.November, 1, 12, 0))
	if want := []string{"00:30", "01:00", "01:30", "02:00", "02:30"}; !equalStrings(hhmm(got, ny), want) {
		t.Errorf("slots = %v, want %v", hhmm(got, ny), want)
	}
	for i := 1; i < len(got); i++ {
		if !got[i].After(got[i-1]) {
			t.Errorf("slots not strictly ascending: %v", got)
		}
	}

	// Walking NextDue from before midnight visits each slot exactly once.
	var seen []string
	cur := at(ny, 2026, time.November, 1, 0, 0)
	for {
		n, ok := w.NextDue(cur)
		if !ok || n.In(ny).Day() != 1 {
			break
		}
		seen = append(seen, n.In(ny).Format("15:04"))
		cur = n
	}
	if want := []string{"00:30", "01:00", "01:30", "02:00", "02:30"}; !equalStrings(seen, want) {
		t.Errorf("NextDue walk = %v, want %v", seen, want)
	}

	nine := Window{Days: EveryDay, Start: 9 * 60, End: 9*60 + 1, Every: 1, TZ: "America/New_York"}
	a, _ := nine.NextDue(at(ny, 2026, time.October, 30, 9, 0))
	b, _ := nine.NextDue(a)
	if b.In(ny).Hour() != 9 || b.Sub(a) != 25*time.Hour {
		t.Errorf("09:00 -> 09:00 is %v, want 25h", b.Sub(a))
	}
}

// The window is read in its own zone, not the zone of the instant handed in.
func TestScheduleUsesTheWindowsZoneNotTheCallers(t *testing.T) {
	w := Window{Days: Weekdays, Start: 9 * 60, End: 10 * 60, Every: 60, TZ: "America/New_York"}
	ny := mustLoc(t, "America/New_York")
	// 14:00 UTC on a Wednesday is 09:00 in New York.
	now := time.Date(2026, time.November, 11, 14, 0, 0, 0, time.UTC)
	got := w.Missed(now.Add(-time.Second), now)
	if len(got) != 1 || !got[0].Equal(at(ny, 2026, time.November, 11, 9, 0)) {
		t.Errorf("missed = %v", got)
	}
}

func TestScheduleMergesWindows(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	a := Window{Days: Weekdays, Start: 9 * 60, End: 10 * 60, Every: 30, TZ: "Europe/Istanbul"}
	b := Window{Days: Weekdays, Start: 9*60 + 30, End: 11 * 60, Every: 30, TZ: "Europe/Istanbul"}
	got := MissedOf([]Window{a, b}, at(ist, 2026, time.November, 11, 8, 0), at(ist, 2026, time.November, 11, 9, 45))
	// 09:00 (first window), 09:30 (both windows, once).
	if want := []string{"09:00", "09:30"}; !equalStrings(hhmm(got, ist), want) {
		t.Errorf("merged = %v, want %v", hhmm(got, ist), want)
	}
	n, ok := NextDueOf([]Window{a, b}, at(ist, 2026, time.November, 11, 9, 31))
	if !ok || !n.Equal(at(ist, 2026, time.November, 11, 10, 0)) {
		t.Errorf("NextDueOf = %v, %v", n, ok)
	}
	if _, ok := NextDueOf(nil, time.Now()); ok {
		t.Error("no windows, no next slot")
	}
}

// ticksOf walks 30-second ticks over the local day and returns every slot the
// way the scheduler would see it: from Missed, each slot once.
func ticksOf(w Window, from, to time.Time) []time.Time {
	var out []time.Time
	last := from
	for now := from.Add(30 * time.Second); !now.After(to); now = now.Add(30 * time.Second) {
		out = append(out, w.Missed(last, now)...)
		last = now
	}
	return out
}

// A slot whose wall-clock time falls in the spring gap runs at the end of the
// gap, on every zone and whichever side time.Date would have put it on. Before,
// America/New_York resolved 02:30 to 01:30 EST, the slot never came due, and
// NextDue announced a time that did not run.
func TestScheduleDSTSlotInsideTheGapRunsRightAfterIt(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	dayStart, dayEnd := at(ny, 2026, time.March, 8, 0, 0), at(ny, 2026, time.March, 8, 23, 59)
	afterGap := time.Date(2026, time.March, 8, 7, 0, 0, 0, time.UTC) // 03:00 EDT, the first instant after the gap

	cases := map[string]struct {
		w    Window
		want []string
	}{
		"one slot inside the gap":        {Window{Days: EveryDay, Start: 150, End: 600, Every: 600, TZ: "America/New_York"}, []string{"03:00"}},
		"window wholly inside the gap":   {Window{Days: EveryDay, Start: 120, End: 180, Every: 30, TZ: "America/New_York"}, []string{"03:00"}},
		"slots on both sides of the gap": {Window{Days: EveryDay, Start: 60, End: 240, Every: 45, TZ: "America/New_York"}, []string{"01:00", "01:45", "03:00", "03:15"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			slots := tc.w.SlotsOn(at(ny, 2026, time.March, 8, 12, 0))
			if !equalStrings(hhmm(slots, ny), tc.want) {
				t.Fatalf("slots = %v, want %v", hhmm(slots, ny), tc.want)
			}
			next, ok := tc.w.NextDue(dayStart)
			if !ok || !next.Equal(slots[0]) {
				t.Errorf("NextDue = %v, want the first slot %v", next, slots[0])
			}
			ran := ticksOf(tc.w, dayStart, dayEnd)
			if !equalStrings(hhmm(ran, ny), tc.want) {
				t.Errorf("with 30 s ticks the job ran at %v, want %v", hhmm(ran, ny), tc.want)
			}
		})
	}

	// The moved slot is exactly the first instant after the gap.
	one := cases["one slot inside the gap"].w
	if got := one.SlotsOn(at(ny, 2026, time.March, 8, 12, 0)); len(got) != 1 || !got[0].Equal(afterGap) {
		t.Errorf("slot = %v, want %v", got, afterGap)
	}

	// The window wholly inside the gap keeps the one interval of its own: a
	// catch-up well after it is not a run.
	inside := cases["window wholly inside the gap"].w
	if got := inside.Missed(at(ny, 2026, time.March, 8, 3, 5), at(ny, 2026, time.March, 8, 3, 40)); len(got) != 0 {
		t.Errorf("late catch-up of a gap slot: %v", got)
	}
}
