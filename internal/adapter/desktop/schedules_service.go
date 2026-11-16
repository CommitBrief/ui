// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"errors"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/domain/run"
)

// The event emitted when the scheduler is paused or resumed. The name is a
// literal because the binding generator only finds RegisterEvent calls with
// constant arguments; TestSchedulerEventNameMatchesTheApplication pins it to the
// app constant.
func init() {
	application.RegisterEvent[app.SchedulerStatus]("scheduler:status")
}

// SchedulesService is the Wails-bound face of the scheduled-bot use cases: the
// review-requested bot jobs with their time windows, resuming a job that needs
// attention, starting one now, the daily budget view and the login item. Like the
// other services it adds no logic beyond shaping errors: every error leaves as a
// *run.Error, serialized as {code, field}; causes (paths, process output) stay in
// the log.
type SchedulesService struct {
	schedules *app.Schedules
	log       *slog.Logger
}

// NewSchedulesService binds the use cases. A nil logger means slog.Default().
func NewSchedulesService(schedules *app.Schedules, log *slog.Logger) *SchedulesService {
	if log == nil {
		log = slog.Default()
	}
	return &SchedulesService{schedules: schedules, log: log}
}

// ListBotJobs returns the bot jobs, oldest first, each with its windows, next
// slot and attention state.
func (s *SchedulesService) ListBotJobs(ctx context.Context) ([]app.ScheduledJob, error) {
	out, err := s.schedules.ListBotJobs(ctx)
	return out, s.fail("list bot jobs", err)
}

// SaveBotJob creates (id 0) or replaces a bot job and its windows. A job in post
// mode is saved only when the user accepted the consent text (postConsent).
func (s *SchedulesService) SaveBotJob(ctx context.Context, in app.BotJobInput) (app.ScheduledJob, error) {
	out, err := s.schedules.SaveBotJob(ctx, in)
	return out, s.fail("save bot job", err)
}

// SetBotJobEnabled turns the scheduling of a job on or off.
func (s *SchedulesService) SetBotJobEnabled(ctx context.Context, id int64, enabled bool) (app.ScheduledJob, error) {
	out, err := s.schedules.SetJobEnabled(ctx, id, enabled)
	return out, s.fail("enable bot job", err)
}

// ResumeBotJob lets the scheduler run a job that stopped for attention again.
func (s *SchedulesService) ResumeBotJob(ctx context.Context, id int64) (app.ScheduledJob, error) {
	out, err := s.schedules.ResumeJob(ctx, id)
	return out, s.fail("resume bot job", err)
}

// DeleteBotJob removes a bot job; its runs stay in the history.
func (s *SchedulesService) DeleteBotJob(ctx context.Context, id int64) error {
	return s.fail("delete bot job", s.schedules.DeleteJob(ctx, id))
}

// RunBotJobNow starts a run of the job right away, outside its windows.
func (s *SchedulesService) RunBotJobNow(ctx context.Context, id int64) (run.Run, error) {
	out, err := s.schedules.RunJobNow(ctx, id)
	return out, s.fail("run bot job", err)
}

// BudgetStatus returns today's spend against the daily budget.
func (s *SchedulesService) BudgetStatus(ctx context.Context) (app.BudgetStatus, error) {
	out, err := s.schedules.BudgetStatus(ctx)
	return out, s.fail("budget status", err)
}

// AutostartEnabled reports whether the application starts when the user logs in.
func (s *SchedulesService) AutostartEnabled(ctx context.Context) (bool, error) {
	out, err := s.schedules.AutostartEnabled(ctx)
	return out, s.fail("read autostart", err)
}

// SetAutostart turns the login item on or off. It is off until the user asks.
func (s *SchedulesService) SetAutostart(ctx context.Context, enabled bool) error {
	return s.fail("write autostart", s.schedules.SetAutostart(ctx, enabled))
}

// SchedulerStatus returns whether the scheduler is paused (and, in one word,
// whether it is running or outside every window) with its last and next look.
func (s *SchedulesService) SchedulerStatus(ctx context.Context) (app.SchedulerStatus, error) {
	out, err := s.schedules.SchedulerStatus(ctx)
	return out, s.fail("scheduler status", err)
}

// PauseScheduler stops the scheduler from starting runs until it is resumed. The
// pause is kept across restarts; a run in progress and "run now" are not
// affected. It emits the scheduler:status event.
func (s *SchedulesService) PauseScheduler(ctx context.Context) (app.SchedulerStatus, error) {
	out, err := s.schedules.SetSchedulerPaused(ctx, true)
	return out, s.fail("pause scheduler", err)
}

// ResumeScheduler lets the scheduler start runs again. It emits the
// scheduler:status event.
func (s *SchedulesService) ResumeScheduler(ctx context.Context) (app.SchedulerStatus, error) {
	out, err := s.schedules.SetSchedulerPaused(ctx, false)
	return out, s.fail("resume scheduler", err)
}

// TrackedPRs lists the open pull requests the bot jobs follow, with the head and
// time of each job's last review.
func (s *SchedulesService) TrackedPRs(ctx context.Context) ([]app.TrackedPRView, error) {
	out, err := s.schedules.TrackedPRs(ctx)
	return out, s.fail("list tracked pull requests", err)
}

// fail hands the frontend a copy of the classified error with no cause.
func (s *SchedulesService) fail(op string, err error) error {
	if err == nil {
		return nil
	}
	var re *run.Error
	if !errors.As(err, &re) {
		s.log.Error("schedules operation failed", "op", op, "error", err)
		return run.NewError(run.CodeInternal, "")
	}
	if re.Code == run.CodeInternal && re.Err != nil {
		s.log.Error("schedules operation failed", "op", op, "error", re.Err)
	}
	return &run.Error{Code: re.Code, Field: re.Field}
}
