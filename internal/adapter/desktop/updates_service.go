// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"errors"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/domain/update"
)

// The event the updater emits as its status changes. The name is a literal
// because the binding generator only finds RegisterEvent calls with constant
// arguments; TestUpdateEventNameMatchesTheApplication pins it to the app
// constant.
func init() {
	application.RegisterEvent[update.Status]("updates:status")
}

// UpdatesService is the Wails-bound face of the self-update use cases: the
// state and preferences, a manual check, and the install the user confirms.
// Progress does not come through these calls but as the updates:status event.
// Like the other services it adds no logic beyond shaping errors: every error
// leaves as an *update.Error, serialized as {code, field}; causes (paths, URLs,
// process output) stay in the log.
type UpdatesService struct {
	updates *app.Updates
	log     *slog.Logger
}

// NewUpdatesService binds the use cases. A nil logger means slog.Default().
func NewUpdatesService(u *app.Updates, log *slog.Logger) *UpdatesService {
	if log == nil {
		log = slog.Default()
	}
	return &UpdatesService{updates: u, log: log}
}

// View returns the updater's state and the user's preferences.
func (s *UpdatesService) View(ctx context.Context) (app.UpdatesView, error) {
	out, err := s.updates.View(ctx)
	return out, s.fail("read update state", err)
}

// SetPreferences stores the channel ("stable" or "prerelease") and whether the
// application checks once a day.
func (s *UpdatesService) SetPreferences(ctx context.Context, channel string, autoCheck bool) (app.UpdatesView, error) {
	out, err := s.updates.SetPreferences(ctx, channel, autoCheck)
	return out, s.fail("set update preferences", err)
}

// Check looks for an update now. Nothing is downloaded.
func (s *UpdatesService) Check(ctx context.Context) (app.UpdatesView, error) {
	out, err := s.updates.Check(ctx)
	return out, s.fail("check for updates", err)
}

// Install downloads and verifies the update the last check found, puts it in
// place and restarts the application into it. It returns once the restart has
// been handed over; progress arrives as updates:status events.
func (s *UpdatesService) Install(ctx context.Context) error {
	return s.fail("install the update", s.updates.Install(ctx))
}

// fail hands the frontend a copy of the classified error with no cause. The
// application layer classifies everything; an error that is somehow not an
// *update.Error is reported as "internal".
func (s *UpdatesService) fail(op string, err error) error {
	if err == nil {
		return nil
	}
	var ue *update.Error
	if !errors.As(err, &ue) {
		s.log.Error("update operation failed", "op", op, "error", err)
		return update.New(update.CodeInternal, "")
	}
	switch ue.Code {
	case update.CodeInternal:
		s.log.Error("update operation failed", "op", op, "error", ue.Err)
	case update.CodeCanceled:
		// The user moved on; not worth a line.
	default:
		if ue.Err != nil {
			s.log.Debug("update operation failed", "op", op, "code", ue.Code, "cause", ue.Err)
		}
	}
	return &update.Error{Code: ue.Code, Field: ue.Field}
}
