// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"time"

	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/domain/schedule"
	"github.com/CommitBrief/ui/internal/port"
)

// EventSchedulerStatus is the event emitted when the scheduler's pause changes.
// Its payload is a SchedulerStatus. The desktop adapter registers the name with
// that payload type, so the generated bindings carry both.
const EventSchedulerStatus = "scheduler:status"

// SchedulerState is the one-word summary of the scheduler the tray and the status
// bar show.
type SchedulerState string

const (
	// SchedulerRunning: not paused, and at least one enabled job that does not
	// need attention has a time window open now.
	SchedulerRunning SchedulerState = "running"
	// SchedulerPaused: the user paused the bot.
	SchedulerPaused SchedulerState = "paused"
	// SchedulerOutsideWindow: not paused, but no schedulable job has a window
	// open now (this includes having no schedulable job at all).
	SchedulerOutsideWindow SchedulerState = "outside_window"
)

// SchedulerControl is what the use cases need from the scheduler. *Scheduler
// implements it.
type SchedulerControl interface {
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Paused() bool
	Status() SchedulerSnapshot
}

// SchedulerStatus is the scheduler as the screen and the tray show it.
type SchedulerStatus struct {
	Paused bool           `json:"paused"`
	State  SchedulerState `json:"state"`
	// LastTick is when the scheduler last looked at the clock; null before its
	// first look.
	LastTick *time.Time `json:"lastTick"`
	// NextTick is when the next look is due; null when the scheduler is paused
	// or not running.
	NextTick *time.Time `json:"nextTick"`
	// Version grows with every pause or resume. Of two statuses the one with the
	// higher version is newer: a read that was slow must not replace a newer pause
	// or resume. Omitted when zero (the pause never changed in this process).
	Version uint64 `json:"version,omitempty"`
	// AttentionCount is how many jobs stopped for the user's attention and wait
	// for a resume.
	AttentionCount int `json:"attentionCount,omitempty"`
	// LastRun is the newest run of a bot job; absent when there is none.
	LastRun *LastRunSummary `json:"lastRun,omitempty"`
}

// LastRunSummary is the newest run of the bot jobs, for the tray's one-line
// summary.
type LastRunSummary struct {
	RunID int64 `json:"runId"`
	JobID int64 `json:"jobId"`
	// Status is the run's status ("queued", "running", "succeeded", ...).
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

// TrackedPRView is one pull request a bot job follows, with what that job
// reviewed last. The same pull request appears once per job that watches its
// repository. Closed and merged pull requests are not listed.
type TrackedPRView struct {
	RepositoryID int64 `json:"repositoryId"`
	// Repository is "owner/name".
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	JobID      int64  `json:"jobId"`
	JobName    string `json:"jobName"`
	// Mode is the job's mode; the reviewed head is per mode.
	Mode       job.Mode `json:"mode"`
	JobEnabled bool     `json:"jobEnabled"`
	// State is the pull request's state; always "open" here.
	State       string    `json:"state"`
	FirstSeenAt time.Time `json:"firstSeenAt"`
	// LastHeadOID is the head commit of the job's last successful review in this
	// mode; empty when it has not reviewed the pull request yet.
	LastHeadOID string `json:"lastHeadOid"`
	// LastRunAt is when that review ran; null with an empty LastHeadOID.
	LastRunAt *time.Time `json:"lastRunAt"`
}

// SchedulerStatus returns the pause, the last and next look, the one-word state,
// how many jobs wait for the user and the newest bot run.
func (s *Schedules) SchedulerStatus(ctx context.Context) (SchedulerStatus, error) {
	if s.d.Scheduler == nil {
		return SchedulerStatus{}, run.NewError(run.CodeInternal, "")
	}
	// The snapshot comes first: its version is the one this answer is current as of.
	snap := s.d.Scheduler.Status()
	out := SchedulerStatus{Paused: snap.Paused, LastTick: snap.LastTick, NextTick: snap.NextTick, Version: snap.Version}
	recs, err := s.d.Jobs.ListJobs(ctx)
	if err != nil {
		return SchedulerStatus{}, s.fail("list jobs", err)
	}
	now := s.d.Clock.Now()
	out.State = SchedulerOutsideWindow
	if snap.Paused {
		out.State = SchedulerPaused
	}
	var newest *port.RunRecord
	for _, rec := range recs {
		if rec.AttentionCode != "" {
			out.AttentionCount++
		}
		if out.State == SchedulerOutsideWindow && rec.Enabled && rec.AttentionCode == "" {
			for _, w := range rec.Windows {
				if windowOpen(w, now) {
					out.State = SchedulerRunning
					break
				}
			}
		}
		if s.d.Runs != nil && job.Kind(rec.Kind) == job.KindBotReviewRequested {
			last, err := s.d.Runs.ListRuns(ctx, rec.ID, 1)
			if err != nil {
				// The summary is a convenience; the status must not fail for it.
				s.d.Logger.Warn("reading the last run of a bot job failed", "job", rec.ID, "error", err)
				continue
			}
			if len(last) > 0 && (newest == nil || last[0].StartedAt.After(newest.StartedAt)) {
				l := last[0]
				newest = &l
			}
		}
	}
	if newest != nil {
		out.LastRun = &LastRunSummary{
			RunID: newest.ID, JobID: newest.JobID, Status: newest.Status,
			StartedAt: newest.StartedAt, FinishedAt: newest.FinishedAt,
		}
	}
	return out, nil
}

// SetSchedulerPaused pauses or resumes the scheduler and tells the screen and
// the tray. The pause is kept across restarts. A run in progress is not
// interrupted, and starting a job by hand (RunJobNow) is not affected.
//
// Changes are made and announced one at a time, so the announcements arrive in
// the order the changes happened. When the pause was saved but the rest of the
// status cannot be read, the change still succeeds and is announced with what
// the scheduler itself knows (its state word is then a best guess that the next
// read corrects): the user's click did take effect, and the screen and the tray
// must say so.
func (s *Schedules) SetSchedulerPaused(ctx context.Context, paused bool) (SchedulerStatus, error) {
	if s.d.Scheduler == nil {
		return SchedulerStatus{}, run.NewError(run.CodeInternal, "")
	}
	s.setMu.Lock()
	defer s.setMu.Unlock()
	var err error
	if paused {
		err = s.d.Scheduler.Pause(ctx)
	} else {
		err = s.d.Scheduler.Resume(ctx)
	}
	if err != nil {
		return SchedulerStatus{}, s.fail("set scheduler pause", err)
	}
	st, err := s.SchedulerStatus(ctx)
	if err != nil {
		s.d.Logger.Warn("the pause changed but the status could not be read; announcing what the scheduler knows", "paused", paused, "error", err)
		snap := s.d.Scheduler.Status()
		st = SchedulerStatus{Paused: snap.Paused, State: SchedulerRunning, LastTick: snap.LastTick, NextTick: snap.NextTick, Version: snap.Version}
		if snap.Paused {
			st.State = SchedulerPaused
		}
	}
	if s.d.Events != nil {
		s.d.Events.Emit(EventSchedulerStatus, st)
	}
	s.obsMu.Lock()
	obs := append([]func(SchedulerStatus){}, s.observers...)
	s.obsMu.Unlock()
	for _, f := range obs {
		f(st)
	}
	return st, nil
}

// SubscribeScheduler registers f to be called after every pause or resume made
// through SetSchedulerPaused (the tray keeps its menu in step with it). f runs
// on the caller's goroutine and must not block.
func (s *Schedules) SubscribeScheduler(f func(SchedulerStatus)) {
	s.obsMu.Lock()
	s.observers = append(s.observers, f)
	s.obsMu.Unlock()
}

// TrackedPRs lists the open pull requests the bot jobs follow, job by job, each
// with the head and time of the job's last review in its mode. Closed and merged
// pull requests are left out; a pull request nothing reviewed yet (a draft, the
// budget) is listed with an empty head.
func (s *Schedules) TrackedPRs(ctx context.Context) ([]TrackedPRView, error) {
	out := []TrackedPRView{}
	if s.d.Tracked == nil || s.d.Repos == nil {
		return out, nil
	}
	jobs, err := s.ListBotJobs(ctx)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return out, nil
	}
	repos, err := s.d.Repos.List(ctx)
	if err != nil {
		return nil, s.fail("list repositories", err)
	}
	names := make(map[int64]string, len(repos))
	for _, r := range repos {
		names[r.ID] = r.Slug()
	}
	for _, j := range jobs {
		open, err := s.d.Tracked.ListOpen(ctx, j.ID, string(j.Mode), j.Targets)
		if err != nil {
			return nil, s.fail("list tracked pull requests", err)
		}
		for _, t := range open {
			name, ok := names[t.RepositoryID]
			if !ok {
				continue
			}
			out = append(out, TrackedPRView{
				RepositoryID: t.RepositoryID, Repository: name, Number: t.Number,
				JobID: j.ID, JobName: j.Name, Mode: j.Mode, JobEnabled: j.Enabled,
				State: t.State, FirstSeenAt: t.FirstSeenAt, LastHeadOID: t.LastHeadOID, LastRunAt: t.LastRunAt,
			})
		}
	}
	return out, nil
}

// windowOpen reports whether now falls inside the window's hours on one of its
// days, in the window's own time zone.
func windowOpen(w schedule.Window, now time.Time) bool {
	loc, err := w.Location()
	if err != nil || w.Validate() != nil {
		return false
	}
	n := now.In(loc)
	if !w.Days.Has(n.Weekday()) {
		return false
	}
	m := n.Hour()*60 + n.Minute()
	return m >= w.Start && m < w.End
}

var _ SchedulerControl = (*Scheduler)(nil)
