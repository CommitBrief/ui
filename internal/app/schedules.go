// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/domain/schedule"
	"github.com/CommitBrief/ui/internal/port"
)

// MaxWindows bounds the time windows of one job.
const MaxWindows = 8

// JobRunner starts a stored job by hand. *Runs implements it.
type JobRunner interface {
	RunJob(ctx context.Context, jobID int64) (run.Run, error)
}

// SchedulesDeps are what the schedules use cases need.
type SchedulesDeps struct {
	Jobs   port.JobStore
	Runner JobRunner
	Budget *Budget
	// AutoStart is the login item; nil means this build cannot manage one.
	AutoStart port.AutoStart
	Clock     port.Clock
	Logger    *slog.Logger
	// Scheduler is the loop the pause and the status belong to; nil means the
	// pause is unavailable (the calls fail as internal).
	Scheduler SchedulerControl
	// Events receives EventSchedulerStatus when the pause changes; nil means
	// nothing is sent.
	Events port.EventEmitter
	// Repos and Tracked back the tracked pull request list; nil means it is
	// empty.
	Repos   port.RepoStore
	Tracked port.TrackedPRStore
	// Runs backs the "last run" line of the scheduler status; nil means the
	// status carries none.
	Runs port.RunStore
}

// Schedules is the use-case service behind the bot screen: it saves the bot jobs
// with their time windows, shows when each runs next and whether it needs the
// user's attention, and manages the login item and the budget view.
//
// Every error it returns is a *run.Error.
type Schedules struct {
	d SchedulesDeps

	setMu     sync.Mutex // orders SetSchedulerPaused calls and their announcements
	obsMu     sync.Mutex
	observers []func(SchedulerStatus)
}

// NewSchedules wires the service.
func NewSchedules(d SchedulesDeps) *Schedules {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Schedules{d: d}
}

// BotJobInput is a bot job as the user edits it. ID 0 creates a job.
type BotJobInput struct {
	ID   int64    `json:"id"`
	Name string   `json:"name"`
	Mode job.Mode `json:"mode"`
	// Enabled lets the scheduler run the job.
	Enabled bool `json:"enabled"`
	// Targets are the repositories the bot watches.
	Targets []int64 `json:"targets"`
	// IncludeDrafts also reviews draft pull requests.
	IncludeDrafts bool `json:"includeDrafts"`
	// PostConsent is true when the user has just accepted the consent text of
	// post mode (the review is written under their GitHub identity and removes
	// them from the requested reviewers, UI-0008). A post job needs the consent
	// recorded once; an existing job keeps its earlier consent.
	PostConsent bool              `json:"postConsent"`
	Windows     []schedule.Window `json:"windows"`
}

// ScheduledJob is a bot job with what the screen shows about its schedule.
type ScheduledJob struct {
	ID            int64             `json:"id"`
	Name          string            `json:"name"`
	Mode          job.Mode          `json:"mode"`
	Enabled       bool              `json:"enabled"`
	Targets       []int64           `json:"targets"`
	IncludeDrafts bool              `json:"includeDrafts"`
	PostConsentAt *time.Time        `json:"postConsentAt"`
	Windows       []schedule.Window `json:"windows"`
	// NextDue is the next slot of any window; null when there is none or the
	// job is not scheduled (disabled, needs attention).
	NextDue *time.Time `json:"nextDue"`
	// AttentionCode is the failure class that stopped the job; empty when the
	// job is fine. The job stays quiet until ResumeJob.
	AttentionCode errcode.Code `json:"attentionCode"`
	AttentionAt   *time.Time   `json:"attentionAt"`
	CreatedAt     time.Time    `json:"createdAt"`
}

// ListBotJobs returns the bot jobs, oldest first.
func (s *Schedules) ListBotJobs(ctx context.Context) ([]ScheduledJob, error) {
	recs, err := s.d.Jobs.ListJobs(ctx)
	if err != nil {
		return nil, s.fail("list jobs", err)
	}
	out := []ScheduledJob{}
	for _, rec := range recs {
		if job.Kind(rec.Kind) != job.KindBotReviewRequested {
			continue
		}
		out = append(out, s.view(rec))
	}
	return out, nil
}

// SaveBotJob creates (ID 0) or replaces a bot job and its time windows. Saving an
// existing job clears its attention state: an edit is the user's fix.
func (s *Schedules) SaveBotJob(ctx context.Context, in BotJobInput) (ScheduledJob, error) {
	if len(in.Windows) > MaxWindows {
		return ScheduledJob{}, run.NewError(run.CodeScheduleInvalid, "windows")
	}
	for i, w := range in.Windows {
		if err := w.Validate(); err != nil {
			var se *schedule.Error
			field := "windows"
			if errors.As(err, &se) {
				field = fmt.Sprintf("windows[%d].%s", i, se.Field)
			}
			return ScheduledJob{}, run.NewError(run.CodeScheduleInvalid, field)
		}
	}

	var existing port.JobRecord
	if in.ID != 0 {
		var err error
		existing, err = s.d.Jobs.GetJob(ctx, in.ID)
		switch {
		case errors.Is(err, port.ErrNotFound):
			return ScheduledJob{}, run.NewError(run.CodeJobNotFound, "")
		case err != nil:
			return ScheduledJob{}, s.fail("read job", err)
		}
		if job.Kind(existing.Kind) != job.KindBotReviewRequested {
			return ScheduledJob{}, run.NewError(run.CodeJobInvalid, "id")
		}
	}

	spec := job.BotReviewRequestedSpec{IncludeDrafts: in.IncludeDrafts}
	if in.Mode == job.ModePost {
		if in.ID != 0 {
			if old, err := job.ParseBotReviewRequestedSpec(existing.Spec); err == nil {
				spec.PostConsentAt = old.PostConsentAt
			}
		}
		if spec.PostConsentAt == nil && in.PostConsent {
			now := s.d.Clock.Now().UTC().Truncate(time.Second)
			spec.PostConsentAt = &now
		}
	}
	// A preview job carries no consent: switching back to post asks again.

	def := job.Job{
		ID: in.ID, Kind: job.KindBotReviewRequested, Name: in.Name, Spec: spec.Marshal(),
		SpecVersion: job.CurrentSpecV1, Mode: in.Mode, Enabled: in.Enabled, Targets: in.Targets,
	}
	if err := def.Validate(); err != nil {
		return ScheduledJob{}, s.fail("validate job", err)
	}
	if err := spec.ValidateFor(in.Mode); err != nil {
		return ScheduledJob{}, s.fail("validate job spec", err)
	}

	rec := port.JobRecord{
		ID: in.ID, Kind: string(job.KindBotReviewRequested), Name: def.Name, Spec: def.Spec, SpecVersion: def.SpecVersion,
		Mode: string(in.Mode), Enabled: in.Enabled, RepositoryIDs: in.Targets, Windows: in.Windows,
	}
	var err error
	if in.ID == 0 {
		rec, err = s.d.Jobs.CreateJob(ctx, rec)
	} else {
		err = s.d.Jobs.UpdateJob(ctx, rec)
		if err == nil {
			err = s.d.Jobs.ClearAttention(ctx, in.ID)
		}
		if err == nil {
			rec, err = s.d.Jobs.GetJob(ctx, in.ID)
		}
	}
	switch {
	case errors.Is(err, port.ErrNotFound):
		// The job was read above, so what is missing is a target repository.
		return ScheduledJob{}, run.NewError(run.CodeRepositoryNotFound, "targets")
	case errors.Is(err, port.ErrConflict):
		return ScheduledJob{}, run.NewError(run.CodeJobInvalid, "targets")
	case err != nil:
		return ScheduledJob{}, s.fail("save job", err)
	}
	return s.view(rec), nil
}

// botJob reads a stored job and insists it is a bot job: this service manages
// those and nothing else, so another kind's id is refused like an unknown one
// would be, but as invalid input.
func (s *Schedules) botJob(ctx context.Context, id int64) (port.JobRecord, error) {
	rec, err := s.d.Jobs.GetJob(ctx, id)
	switch {
	case errors.Is(err, port.ErrNotFound):
		return port.JobRecord{}, run.NewError(run.CodeJobNotFound, "")
	case err != nil:
		return port.JobRecord{}, s.fail("read job", err)
	case job.Kind(rec.Kind) != job.KindBotReviewRequested:
		return port.JobRecord{}, run.NewError(run.CodeJobInvalid, "id")
	}
	return rec, nil
}

// SetJobEnabled turns the scheduling of a job on or off; its windows and
// attention state are kept.
func (s *Schedules) SetJobEnabled(ctx context.Context, id int64, enabled bool) (ScheduledJob, error) {
	rec, err := s.botJob(ctx, id)
	if err != nil {
		return ScheduledJob{}, err
	}
	rec.Enabled = enabled
	if err := s.d.Jobs.UpdateJob(ctx, rec); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return ScheduledJob{}, run.NewError(run.CodeJobNotFound, "")
		}
		return ScheduledJob{}, s.fail("update job", err)
	}
	return s.view(rec), nil
}

// ResumeJob clears the attention state, so the scheduler runs the job again.
func (s *Schedules) ResumeJob(ctx context.Context, id int64) (ScheduledJob, error) {
	if _, err := s.botJob(ctx, id); err != nil {
		return ScheduledJob{}, err
	}
	if err := s.d.Jobs.ClearAttention(ctx, id); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return ScheduledJob{}, run.NewError(run.CodeJobNotFound, "")
		}
		return ScheduledJob{}, s.fail("clear attention", err)
	}
	rec, err := s.d.Jobs.GetJob(ctx, id)
	if err != nil {
		return ScheduledJob{}, s.fail("read job", err)
	}
	return s.view(rec), nil
}

// DeleteJob removes a bot job and its windows; its runs stay in the history.
func (s *Schedules) DeleteJob(ctx context.Context, id int64) error {
	if _, err := s.botJob(ctx, id); err != nil {
		return err
	}
	if err := s.d.Jobs.DeleteJob(ctx, id); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return run.NewError(run.CodeJobNotFound, "")
		}
		return s.fail("delete job", err)
	}
	return nil
}

// RunJobNow starts a run of the job right away, outside its windows.
func (s *Schedules) RunJobNow(ctx context.Context, id int64) (run.Run, error) {
	if _, err := s.botJob(ctx, id); err != nil {
		return run.Run{}, err
	}
	out, err := s.d.Runner.RunJob(ctx, id)
	if err != nil {
		return run.Run{}, err // the run service already classified it
	}
	return out, nil
}

// BudgetStatus returns today's spend against the daily budget.
func (s *Schedules) BudgetStatus(ctx context.Context) (BudgetStatus, error) {
	if s.d.Budget == nil {
		return BudgetStatus{}, nil
	}
	st, err := s.d.Budget.Status(ctx)
	if err != nil {
		return BudgetStatus{}, s.fail("budget status", err)
	}
	return st, nil
}

// AutostartEnabled reports whether the application starts at login.
func (s *Schedules) AutostartEnabled(ctx context.Context) (bool, error) {
	if s.d.AutoStart == nil {
		return false, run.NewError(run.CodeAutostartUnsupported, "")
	}
	on, err := s.d.AutoStart.Enabled(ctx)
	if err != nil {
		return false, s.autostartFail("read", err)
	}
	return on, nil
}

// SetAutostart turns the login item on or off (off by default, UI-0002).
func (s *Schedules) SetAutostart(ctx context.Context, enabled bool) error {
	if s.d.AutoStart == nil {
		return run.NewError(run.CodeAutostartUnsupported, "")
	}
	var err error
	if enabled {
		err = s.d.AutoStart.Enable(ctx)
	} else {
		err = s.d.AutoStart.Disable(ctx)
	}
	if err != nil {
		return s.autostartFail("write", err)
	}
	return nil
}

func (s *Schedules) autostartFail(op string, err error) error {
	if errors.Is(err, port.ErrAutostartUnsupported) {
		return run.NewError(run.CodeAutostartUnsupported, "")
	}
	if errors.Is(err, context.Canceled) {
		return run.WrapError(run.CodeCanceled, "", err)
	}
	s.d.Logger.Error("the login item could not be changed", "op", op, "error", err)
	return run.WrapError(run.CodeAutostartFailed, "", err)
}

// view builds what the screen shows about a stored job.
func (s *Schedules) view(rec port.JobRecord) ScheduledJob {
	v := ScheduledJob{
		ID: rec.ID, Name: rec.Name, Mode: job.Mode(rec.Mode), Enabled: rec.Enabled,
		Targets: rec.RepositoryIDs, Windows: rec.Windows, AttentionCode: errcode.Code(rec.AttentionCode),
		AttentionAt: rec.AttentionAt, CreatedAt: rec.CreatedAt,
	}
	if v.Targets == nil {
		v.Targets = []int64{}
	}
	if v.Windows == nil {
		v.Windows = []schedule.Window{}
	}
	if spec, err := job.ParseBotReviewRequestedSpec(rec.Spec); err == nil {
		v.IncludeDrafts, v.PostConsentAt = spec.IncludeDrafts, spec.PostConsentAt
	}
	if rec.Enabled && rec.AttentionCode == "" {
		if t, ok := schedule.NextDueOf(rec.Windows, s.d.Clock.Now()); ok {
			v.NextDue = &t
		}
	}
	return v
}

// fail turns an internal error into the *run.Error the frontend sees: domain
// errors keep their code and field, the cause of anything else goes to the log.
func (s *Schedules) fail(op string, err error) error {
	var re *run.Error
	if errors.As(err, &re) {
		return re
	}
	var je *job.Error
	if errors.As(err, &je) {
		return run.WrapError(run.CodeJobInvalid, je.Field, je)
	}
	if errors.Is(err, context.Canceled) {
		return run.WrapError(run.CodeCanceled, "", err)
	}
	s.d.Logger.Error("schedules service call failed", "op", op, "error", err)
	return run.WrapError(run.CodeInternal, "", err)
}
