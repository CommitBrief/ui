// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"log/slog"

	"github.com/CommitBrief/ui/internal/app"
	"github.com/CommitBrief/ui/internal/domain/repo"
)

// HealthService is the Wails-bound face of app.Health. The frontend asks it,
// once it has loaded, whether the database had to be recovered and shows the
// notice it returns.
type HealthService struct {
	health *app.Health
	log    *slog.Logger
}

// NewHealthService binds the health use cases. A nil logger means
// slog.Default().
func NewHealthService(health *app.Health, log *slog.Logger) *HealthService {
	if log == nil {
		log = slog.Default()
	}
	return &HealthService{health: health, log: log}
}

// DatabaseRecovery returns the start-up recovery notice, or null when there is
// none.
func (s *HealthService) DatabaseRecovery() *app.DatabaseNotice {
	return s.health.DatabaseRecovery()
}

// AcknowledgeDatabaseRecovery dismisses the notice permanently. A failure to
// record it is logged and reported as "internal"; the notice then stays.
func (s *HealthService) AcknowledgeDatabaseRecovery() error {
	if err := s.health.AcknowledgeDatabaseRecovery(); err != nil {
		s.log.Error("acknowledging the database recovery failed", "error", err)
		return repo.New(repo.CodeInternal)
	}
	return nil
}
