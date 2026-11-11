// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/CommitBrief/ui/internal/port"
)

// recoveryMarkerName is the file, next to the database, that records a
// recovery until the user acknowledges it. It exists so that the notice cannot
// be lost: not when the process dies right after the damaged file was set
// aside, and not when a later step of Open (a migration, say) fails and the
// next start finds a healthy-looking database.
const recoveryMarkerName = "recovery-pending.json"

type recoveryMarker struct {
	Version      int    `json:"version"`
	Kind         string `json:"kind"`
	DamagedFile  string `json:"damagedFile"`
	RestoredFrom string `json:"restoredFrom,omitempty"`
	BackupsDir   string `json:"backupsDir"`
	Reason       string `json:"reason,omitempty"`
}

func (s *Store) recoveryMarkerPath() string {
	return filepath.Join(filepath.Dir(s.path), recoveryMarkerName)
}

// writeRecoveryMarker replaces the marker atomically (temp file + rename), mode
// 0600.
func (s *Store) writeRecoveryMarker(r *port.DatabaseRecovery) error {
	data, err := json.MarshalIndent(recoveryMarker{
		Version: 1, Kind: r.Kind, DamagedFile: r.DamagedFile, RestoredFrom: r.RestoredFrom,
		BackupsDir: r.BackupsDir, Reason: r.Reason,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("sqlite: encode recovery marker: %w", err)
	}
	return s.writeMarkerBytes(append(data, '\n'))
}

// writeMarkerBytes replaces the marker file with data, atomically. A failure
// leaves the previous marker untouched.
func (s *Store) writeMarkerBytes(data []byte) error {
	final := s.recoveryMarkerPath()
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("sqlite: write recovery marker: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("sqlite: write recovery marker: %w", err)
	}
	return nil
}

// previousMarker is the marker file as it was before a recovery attempt, so
// that a failed attempt can leave an unacknowledged earlier notice exactly as it
// found it.
type previousMarker struct {
	data []byte
	had  bool
}

func (s *Store) captureMarker() (previousMarker, error) {
	data, err := os.ReadFile(s.recoveryMarkerPath())
	if errors.Is(err, os.ErrNotExist) {
		return previousMarker{}, nil
	}
	if err != nil {
		return previousMarker{}, fmt.Errorf("sqlite: read recovery marker: %w", err)
	}
	return previousMarker{data: data, had: true}, nil
}

// restoreMarker undoes this attempt's marker: the earlier one is written back
// byte for byte, or, when there was none, the marker is removed.
func (s *Store) restoreMarker(prev previousMarker) {
	if !prev.had {
		s.removeRecoveryMarker()
		return
	}
	if err := s.writeMarkerBytes(prev.data); err != nil {
		s.log.Error("could not restore the earlier recovery marker", "error", err)
	}
}

// readRecoveryMarker returns the pending recovery, or nil when there is none.
// An unreadable marker is an error the caller logs; it never blocks start-up.
func (s *Store) readRecoveryMarker() (*port.DatabaseRecovery, error) {
	data, err := os.ReadFile(s.recoveryMarkerPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: read recovery marker: %w", err)
	}
	var m recoveryMarker
	if err := json.Unmarshal(data, &m); err != nil || m.Kind == "" || m.DamagedFile == "" {
		return nil, errors.New("sqlite: recovery marker is malformed")
	}
	return &port.DatabaseRecovery{
		Kind: m.Kind, DamagedFile: m.DamagedFile, RestoredFrom: m.RestoredFrom,
		BackupsDir: m.BackupsDir, Reason: m.Reason,
	}, nil
}

func (s *Store) removeRecoveryMarker() {
	if err := os.Remove(s.recoveryMarkerPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.log.Warn("could not remove the recovery marker", "error", err)
	}
}

// AcknowledgeRecovery implements port.RecoverySource: the user has seen the
// notice, so the marker is removed and the next start reports nothing. The
// damaged file and the backups stay.
func (s *Store) AcknowledgeRecovery() error {
	if err := os.Remove(s.recoveryMarkerPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("sqlite: acknowledge recovery: %w", err)
	}
	return nil
}
