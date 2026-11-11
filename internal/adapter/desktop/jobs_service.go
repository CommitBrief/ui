// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"errors"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/port"
)

// The events the run service emits. The names are literals because the
// binding generator only finds RegisterEvent calls with constant arguments;
// TestEventNamesMatchTheApplication pins them to the app constants. Registering
// a payload type makes Wails check it on every Emit and gives the generated
// TypeScript a typed listener.
func init() {
	application.RegisterEvent[app.TaskLogEvent]("task:log")
	application.RegisterEvent[app.TaskStatusEvent]("task:status")
	application.RegisterEvent[app.RunStatusEvent]("run:status")
}

// JobsService is the Wails-bound face of the manual jobs use cases: start a
// local review or a pull request preview, list and read runs, cancel, read a
// task's log. Live progress does not come through these calls but as the
// events task:log, task:status and run:status. Like the other services it adds
// no logic beyond shaping errors: every error leaves as a *run.Error,
// serialized as {code, field}; causes (paths, process output) stay in the log.
//
// Stored jobs (RunJob) are not bound: no screen creates them before phase 13.
type JobsService struct {
	runs *app.Runs
	log  *slog.Logger
}

// NewJobsService binds the use cases. A nil logger means slog.Default().
func NewJobsService(runs *app.Runs, log *slog.Logger) *JobsService {
	if log == nil {
		log = slog.Default()
	}
	return &JobsService{runs: runs, log: log}
}

// StartLocalReview queues a review of the staged, unstaged or ranged changes of
// a repository's local checkout and returns the new run (its tasks queued).
func (s *JobsService) StartLocalReview(ctx context.Context, in app.LocalReviewInput) (run.Run, error) {
	out, err := s.runs.StartLocalReview(ctx, in)
	return out, s.fail("start local review", err)
}

// StartPRPreview queues a preview review of a pull request: it is reviewed with
// --no-post and nothing is written to GitHub.
func (s *JobsService) StartPRPreview(ctx context.Context, in app.PRPreviewInput) (run.Run, error) {
	out, err := s.runs.StartPRPreview(ctx, in)
	return out, s.fail("start pull request preview", err)
}

// ListRuns returns the newest runs first (at most limit; 0 means the default),
// each with its tasks but without findings and report: those come with GetRun.
func (s *JobsService) ListRuns(ctx context.Context, limit int) ([]run.Run, error) {
	out, err := s.runs.ListRuns(ctx, limit)
	return out, s.fail("list runs", err)
}

// GetRun returns one run with its tasks, findings and report.
func (s *JobsService) GetRun(ctx context.Context, id int64) (run.Run, error) {
	out, err := s.runs.GetRun(ctx, id)
	return out, s.fail("get run", err)
}

// CancelRun cancels every unfinished task of a run. Canceling a finished run is
// not an error.
func (s *JobsService) CancelRun(ctx context.Context, id int64) error {
	return s.fail("cancel run", s.runs.CancelRun(ctx, id))
}

// CancelTask cancels one task. Canceling a finished task is not an error.
func (s *JobsService) CancelTask(ctx context.Context, id int64) error {
	return s.fail("cancel task", s.runs.CancelTask(ctx, id))
}

// TaskLog returns the tail of a task's stderr log, secrets masked.
func (s *JobsService) TaskLog(ctx context.Context, taskID int64) (app.TaskLogView, error) {
	out, err := s.runs.TaskLog(ctx, taskID)
	return out, s.fail("read task log", err)
}

// fail hands the frontend a copy of the classified error with no cause. The
// application layer classifies everything; an error that is somehow not a
// *run.Error is reported as "internal".
func (s *JobsService) fail(op string, err error) error {
	if err == nil {
		return nil
	}
	var re *run.Error
	if !errors.As(err, &re) {
		s.log.Error("jobs operation failed", "op", op, "error", err)
		return run.NewError(run.CodeInternal, "")
	}
	if re.Code == run.CodeInternal && re.Err != nil {
		s.log.Error("jobs operation failed", "op", op, "error", re.Err)
	}
	return &run.Error{Code: re.Code, Field: re.Field}
}

// wailsEvents delivers events to every window through the Wails application.
type wailsEvents struct{}

// NewEventEmitter returns the port.EventEmitter backed by the running Wails
// application. It resolves the application at emit time, so it can be built
// before the application exists; an event emitted while there is none is
// dropped.
func NewEventEmitter() port.EventEmitter { return wailsEvents{} }

// Emit implements port.EventEmitter. The payload must be exactly the type the
// event was registered with; Wails refuses (and reports) a mismatch.
func (wailsEvents) Emit(name string, payload any) {
	if a := application.Get(); a != nil {
		a.Event.Emit(name, payload)
	}
}
