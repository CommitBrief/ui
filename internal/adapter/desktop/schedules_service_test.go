// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/domain/run"
)

func TestSchedulerEventNameMatchesTheApplication(t *testing.T) {
	if app.EventSchedulerStatus != "scheduler:status" {
		t.Errorf("the event name changed to %q; update the RegisterEvent call in schedules_service.go", app.EventSchedulerStatus)
	}
}

// A service over a Schedules that has no scheduler answers with the classified
// error and nothing else: no text from the cause reaches the frontend.
func TestSchedulesServiceSchedulerCallsShapeErrors(t *testing.T) {
	svc := NewSchedulesService(app.NewSchedules(app.SchedulesDeps{}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"status": func() error { _, err := svc.SchedulerStatus(ctx); return err },
		"pause":  func() error { _, err := svc.PauseScheduler(ctx); return err },
		"resume": func() error { _, err := svc.ResumeScheduler(ctx); return err },
	} {
		var re *run.Error
		if err := call(); !errors.As(err, &re) || re.Code != run.CodeInternal || re.Err != nil {
			t.Errorf("%s: err = %#v, want a bare internal error", name, err)
		}
	}
	// Without the stores the tracked list is empty, not an error.
	if got, err := svc.TrackedPRs(ctx); err != nil || got == nil || len(got) != 0 {
		t.Errorf("TrackedPRs = %+v, %v", got, err)
	}
}
