// SPDX-License-Identifier: GPL-3.0-or-later

// Package schedule is the domain of "when does a job run": a Window names the
// days of the week, a time range and an interval, in an IANA time zone, for
// example "Monday to Friday, 09:30 to 17:45, every 15 minutes". It performs no
// I/O and needs no cron library: the model is small and a cron expression does
// not describe it cleanly.
//
// Slots are wall-clock times: Start, Start+Every, Start+2*Every, ... up to but
// excluding End, counted from local midnight of each active day. They follow the
// clock on the wall across daylight-saving changes (09:00 stays 09:00), so on a
// 23-hour day the slots are 23 hours apart in elapsed time and on a 25-hour day
// 25. A slot that falls in the hour a spring change skips runs right after the
// gap (the first instant of the new offset, whichever side of the gap the
// standard library would have put it on); slots that collapse onto the same
// instant count once. A wall-clock time
// that occurs twice in autumn counts once.
package schedule

import (
	"fmt"
	"sort"
	"sync"
	"time"
	_ "time/tzdata" // the IANA zones, for machines with no zoneinfo database (Windows); LoadLocation falls back to this copy
)

// Days is the set of weekdays a window is active on. Bit 0 is Monday and bit 6
// is Sunday, the encoding of schedules.days_mask.
type Days uint8

// The days of the week.
const (
	Monday Days = 1 << iota
	Tuesday
	Wednesday
	Thursday
	Friday
	Saturday
	Sunday

	// Weekdays is Monday to Friday.
	Weekdays = Monday | Tuesday | Wednesday | Thursday | Friday
	// EveryDay is all seven days.
	EveryDay = Weekdays | Saturday | Sunday
)

// Has reports whether the set contains the weekday.
func (d Days) Has(wd time.Weekday) bool {
	// time.Weekday counts from Sunday = 0; the mask from Monday = bit 0.
	return d&(1<<((int(wd)+6)%7)) != 0
}

// Limits of a window.
const (
	MinutesPerDay = 24 * 60
	// MaxTZLen bounds the time zone name.
	MaxTZLen = 64
	// searchDays bounds how far NextDue looks ahead: a valid window has a slot
	// in any 7 consecutive days; the margin covers the daylight-saving shifts.
	searchDays = 9
)

// Window is one time window of a job; a job may have several. Minutes count
// from local midnight in TZ.
type Window struct {
	// Days is the set of active weekdays; at least one.
	Days Days `json:"days"`
	// Start is the first slot, in minutes from local midnight, 0 to 1439.
	Start int `json:"start"`
	// End is the end of the window, exclusive, after Start and at most 1440.
	End int `json:"end"`
	// Every is the interval between slots in minutes; at least 1.
	Every int `json:"every"`
	// TZ is the IANA name of the time zone, for example "Europe/Istanbul".
	TZ string `json:"tz"`
}

// Error names the field of a window that is not acceptable. Field is the JSON
// name: days, start, end, every or tz.
type Error struct {
	Field string
}

func (e *Error) Error() string { return "schedule: invalid " + e.Field }

// Validate checks every field and returns an *Error for the first bad one.
func (w Window) Validate() error {
	if w.Days == 0 || w.Days&^EveryDay != 0 {
		return &Error{Field: "days"}
	}
	if w.Start < 0 || w.Start >= MinutesPerDay {
		return &Error{Field: "start"}
	}
	if w.End <= w.Start || w.End > MinutesPerDay {
		return &Error{Field: "end"}
	}
	if w.Every < 1 || w.Every > MinutesPerDay {
		return &Error{Field: "every"}
	}
	if _, err := w.location(); err != nil {
		return &Error{Field: "tz"}
	}
	return nil
}

var locations sync.Map // name -> *time.Location

// location resolves TZ. "Local" and the empty name are refused: a window must
// not change meaning when the machine's zone changes.
func (w Window) location() (*time.Location, error) {
	if w.TZ == "" || w.TZ == "Local" || len(w.TZ) > MaxTZLen {
		return nil, fmt.Errorf("schedule: time zone %q is not acceptable", w.TZ)
	}
	if v, ok := locations.Load(w.TZ); ok {
		return v.(*time.Location), nil
	}
	loc, err := time.LoadLocation(w.TZ)
	if err != nil {
		return nil, fmt.Errorf("schedule: time zone %q: %w", w.TZ, err)
	}
	locations.Store(w.TZ, loc)
	return loc, nil
}

// Location returns the window's time zone.
func (w Window) Location() (*time.Location, error) { return w.location() }

// SlotsOn returns the slots of the local calendar day that contains day, in
// the window's zone, in ascending order. It is empty when the weekday is not
// active or the window is invalid.
func (w Window) SlotsOn(day time.Time) []time.Time {
	loc, err := w.location()
	if err != nil || w.Validate() != nil {
		return nil
	}
	d := day.In(loc)
	return w.slotsOn(d.Year(), d.Month(), d.Day(), d.Weekday(), loc)
}

func (w Window) slotsOn(y int, m time.Month, d int, wd time.Weekday, loc *time.Location) []time.Time {
	slots := w.slotList(y, m, d, wd, loc)
	out := make([]time.Time, len(slots))
	for i, s := range slots {
		out[i] = s.at
	}
	return out
}

// slot is one run time; shifted says its wall-clock time does not exist on that
// day (a spring-forward gap) and it was moved to the end of the gap.
type slot struct {
	at      time.Time
	shifted bool
}

func (w Window) slotList(y int, m time.Month, d int, wd time.Weekday, loc *time.Location) []slot {
	if !w.Days.Has(wd) {
		return nil
	}
	var out []slot
	for min := w.Start; min < w.End; min += w.Every {
		s := slot{at: time.Date(y, m, d, min/60, min%60, 0, 0, loc)}
		// time.Date leaves the choice for a wall-clock time inside a spring gap
		// open (before the gap for America/New_York, after it for
		// Europe/Istanbul); the slot is moved to the first instant after the gap.
		if a := s.at; a.Day() != d || a.Hour()*60+a.Minute() != min {
			s.at, s.shifted = gapEnd(s.at), true
		}
		// Slots that land on the same instant count once.
		if n := len(out); n > 0 && !s.at.After(out[n-1].at) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// gapEnd returns the first instant of the offset that follows the transition
// next to t, or t itself when its neighbourhood has no offset change. A day has
// at most one transition within a few hours of a slot, so the offset switches
// once between t-3h and t+3h and the first second of the new offset is found by
// bisection.
func gapEnd(t time.Time) time.Time {
	lo, hi := t.Add(-3*time.Hour), t.Add(3*time.Hour)
	_, offLo := lo.Zone()
	_, offHi := hi.Zone()
	if offLo == offHi {
		return t
	}
	l, h := lo.Unix(), hi.Unix() // offset(l) = offLo, offset(h) = offHi
	for h-l > 1 {
		mid := l + (h-l)/2
		if _, off := time.Unix(mid, 0).In(t.Location()).Zone(); off == offLo {
			l = mid
		} else {
			h = mid
		}
	}
	return time.Unix(h, 0).In(t.Location())
}

// NextDue returns the first slot strictly after the given instant, or false
// when the window is invalid.
func (w Window) NextDue(after time.Time) (time.Time, bool) {
	loc, err := w.location()
	if err != nil || w.Validate() != nil {
		return time.Time{}, false
	}
	a := after.In(loc)
	for i := 0; i < searchDays; i++ {
		// AddDate on the local date keeps the wall clock across a change.
		day := time.Date(a.Year(), a.Month(), a.Day()+i, 12, 0, 0, 0, loc)
		for _, s := range w.slotsOn(day.Year(), day.Month(), day.Day(), day.Weekday(), loc) {
			if s.After(after) {
				return s, true
			}
		}
	}
	return time.Time{}, false
}

// Missed returns the slots in (lastTick, now] that belong to the window
// instance that contains now: slots of earlier days or of earlier, closed
// windows are not returned, because catching up on them would run a job outside
// the hours the user chose. It is empty when now is outside the window. After a
// sleep, one or more returned slots mean "run once now"; a normal tick returns
// at most the slot that just came due.
func (w Window) Missed(lastTick, now time.Time) []time.Time {
	loc, err := w.location()
	if err != nil || w.Validate() != nil {
		return nil
	}
	n := now.In(loc)
	if !w.Days.Has(n.Weekday()) {
		return nil
	}
	mod := n.Hour()*60 + n.Minute()
	inside := mod >= w.Start && mod < w.End
	var out []time.Time
	for _, s := range w.slotList(n.Year(), n.Month(), n.Day(), n.Weekday(), loc) {
		if !s.at.After(lastTick) || s.at.After(now) {
			continue
		}
		// Outside the window by the wall clock nothing is caught up, except the
		// slot a spring gap moved to the end of the gap: the wall-clock range it
		// belonged to may lie wholly inside the gap, and the slot would never run.
		// It keeps one interval of its own.
		if !inside && (!s.shifted || now.Sub(s.at) >= time.Duration(w.Every)*time.Minute) {
			continue
		}
		out = append(out, s.at)
	}
	return out
}

// NextDueOf is the earliest NextDue of the windows; false when none has one.
func NextDueOf(ws []Window, after time.Time) (time.Time, bool) {
	var best time.Time
	found := false
	for _, w := range ws {
		if t, ok := w.NextDue(after); ok && (!found || t.Before(best)) {
			best, found = t, true
		}
	}
	return best, found
}

// MissedOf merges Missed over the windows, ascending and without duplicates.
func MissedOf(ws []Window, lastTick, now time.Time) []time.Time {
	var all []time.Time
	for _, w := range ws {
		all = append(all, w.Missed(lastTick, now)...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Before(all[j]) })
	out := all[:0]
	for _, t := range all {
		if n := len(out); n > 0 && out[n-1].Equal(t) {
			continue
		}
		out = append(out, t)
	}
	return out
}
