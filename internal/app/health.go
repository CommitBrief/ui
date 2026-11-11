// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"sync/atomic"

	"github.com/CommitBrief/ui/internal/port"
)

// DatabaseNotice tells the user that the database file was damaged at start-up
// and what was done about it. The frontend shows it as a notification
// ("database recovered, old file: <path>") and offers the ways back to the
// data: importing a JSON export, or restoring a pre-migration backup from
// BackupsDir. Nothing has been deleted.
type DatabaseNotice struct {
	// Kind is port.RecoveryRestoredFromBackup ("restored_from_backup") or
	// port.RecoveryStartedEmpty ("started_empty").
	Kind string `json:"kind"`
	// DamagedFile is the path the damaged database was moved to.
	DamagedFile string `json:"damagedFile"`
	// RestoredFrom is the backup that was restored; empty when Kind is
	// "started_empty".
	RestoredFrom string `json:"restoredFrom"`
	// BackupsDir holds the pre-migration backups.
	BackupsDir string `json:"backupsDir"`
}

// Health reports the condition of the persistent state to the UI.
type Health struct {
	src   port.RecoverySource
	acked atomic.Bool
}

// NewHealth wires the service to the store's recovery report.
func NewHealth(src port.RecoverySource) *Health { return &Health{src: src} }

// DatabaseRecovery returns the pending recovery notice, or nil when the
// database was healthy or the user already dismissed the notice. It is a state
// to poll, not an event, so a frontend that finishes loading after start-up
// still sees it.
func (h *Health) DatabaseRecovery() *DatabaseNotice {
	if h.acked.Load() {
		return nil
	}
	r := h.src.Recovery()
	if r == nil {
		return nil
	}
	return &DatabaseNotice{
		Kind:         r.Kind,
		DamagedFile:  r.DamagedFile,
		RestoredFrom: r.RestoredFrom,
		BackupsDir:   r.BackupsDir,
	}
}

// AcknowledgeDatabaseRecovery dismisses the notice for good: the store records
// it, so the next start does not report it again. The damaged file and the
// backups stay on disk. When recording fails the notice stays (and the error is
// returned), so it is never lost without the user having seen it.
func (h *Health) AcknowledgeDatabaseRecovery() error {
	if err := h.src.AcknowledgeRecovery(); err != nil {
		return err
	}
	h.acked.Store(true)
	return nil
}
