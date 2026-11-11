// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"errors"
	"testing"

	"github.com/CommitBrief/ui/internal/port"
)

type fakeRecovery struct {
	r      *port.DatabaseRecovery
	ackErr error
	acks   int
}

func (f *fakeRecovery) Recovery() *port.DatabaseRecovery { return f.r }

func (f *fakeRecovery) AcknowledgeRecovery() error {
	if f.ackErr != nil {
		return f.ackErr
	}
	f.acks++
	f.r = nil
	return nil
}

func TestHealthReportsNothingForAHealthyDatabase(t *testing.T) {
	if got := NewHealth(&fakeRecovery{}).DatabaseRecovery(); got != nil {
		t.Errorf("DatabaseRecovery = %+v, want nil", got)
	}
}

func TestHealthReportsRecoveryUntilAcknowledged(t *testing.T) {
	src := &fakeRecovery{r: &port.DatabaseRecovery{
		Kind:        port.RecoveryStartedEmpty,
		DamagedFile: "/data/ui.db.corrupt-20261111T100000.000Z",
		BackupsDir:  "/data/backups",
		Reason:      "file is not a database",
	}}
	h := NewHealth(src)

	got := h.DatabaseRecovery()
	if got == nil {
		t.Fatal("no notice for a recovered database")
	}
	if got.Kind != "started_empty" || got.DamagedFile != "/data/ui.db.corrupt-20261111T100000.000Z" ||
		got.RestoredFrom != "" || got.BackupsDir != "/data/backups" {
		t.Errorf("notice = %+v", got)
	}
	// Polling does not consume it.
	if h.DatabaseRecovery() == nil {
		t.Error("the notice vanished without an acknowledgement")
	}
	if err := h.AcknowledgeDatabaseRecovery(); err != nil {
		t.Fatal(err)
	}
	if src.acks != 1 {
		t.Errorf("the store was told %d times, want once (the acknowledgement must be persisted)", src.acks)
	}
	if h.DatabaseRecovery() != nil {
		t.Error("the notice is still reported after it was acknowledged")
	}
}

// If the acknowledgement cannot be recorded, the notice must stay: it is never
// dropped without having been durably dismissed.
func TestHealthKeepsTheNoticeWhenAcknowledgingFails(t *testing.T) {
	src := &fakeRecovery{
		r:      &port.DatabaseRecovery{Kind: port.RecoveryStartedEmpty, DamagedFile: "/d/x"},
		ackErr: errors.New("read-only file system"),
	}
	h := NewHealth(src)
	if err := h.AcknowledgeDatabaseRecovery(); err == nil {
		t.Fatal("a failed acknowledgement was reported as success")
	}
	if h.DatabaseRecovery() == nil {
		t.Error("the notice was dropped although it was not persisted as dismissed")
	}
}

func TestHealthReportsRestoreFromBackup(t *testing.T) {
	h := NewHealth(&fakeRecovery{r: &port.DatabaseRecovery{
		Kind: port.RecoveryRestoredFromBackup, DamagedFile: "/d/x", RestoredFrom: "/d/backups/b.db", BackupsDir: "/d/backups",
	}})
	if got := h.DatabaseRecovery(); got == nil || got.Kind != "restored_from_backup" || got.RestoredFrom != "/d/backups/b.db" {
		t.Errorf("notice = %+v", got)
	}
}
