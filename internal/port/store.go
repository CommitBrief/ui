// SPDX-License-Identifier: GPL-3.0-or-later

package port

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/CommitBrief/ui/internal/domain/repo"
)

// Store errors. Adapters return these (possibly wrapped); the application
// layer turns them into domain error codes.
var (
	// ErrNotFound means the addressed row does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrConflict means a uniqueness constraint was violated.
	ErrConflict = errors.New("store: conflict")
)

// RepoStore persists repository definitions. (owner, name) is unique
// case-insensitively; violating it returns ErrConflict.
type RepoStore interface {
	// Create inserts r (ID and CreatedAt are assigned by the store) and
	// returns the stored row.
	Create(ctx context.Context, r repo.Repository) (repo.Repository, error)
	// Get returns the repository with the given ID or ErrNotFound.
	Get(ctx context.Context, id int64) (repo.Repository, error)
	// GetByRemote finds a repository by owner and name (case-insensitive) or
	// returns ErrNotFound.
	GetByRemote(ctx context.Context, owner, name string) (repo.Repository, error)
	// List returns every repository ordered by owner then name
	// (case-insensitive).
	List(ctx context.Context) ([]repo.Repository, error)
	// Update replaces owner, name and local path of the row with r.ID. It does
	// not touch the enabled flag (see SetEnabled), so a form save and a list
	// toggle cannot overwrite each other. ErrNotFound when the row does not
	// exist, ErrConflict on a duplicate.
	Update(ctx context.Context, r repo.Repository) error
	// SetEnabled sets only the enabled flag of the row. ErrNotFound when it
	// does not exist.
	SetEnabled(ctx context.Context, id int64, enabled bool) error
	// Delete removes the row; job targets and tracked PRs of the repository
	// go with it, run history is kept with the repository reference cleared.
	// ErrNotFound when it does not exist.
	Delete(ctx context.Context, id int64) error
}

// SettingsStore persists UI-only settings (UI-0006 section 3) as strings. The
// CLI's own configuration is never stored here.
type SettingsStore interface {
	// Get returns the value and whether the key is set.
	Get(ctx context.Context, key string) (string, bool, error)
	// Set creates or replaces the value of key.
	Set(ctx context.Context, key, value string) error
	// Delete unsets key; deleting an unset key is not an error.
	Delete(ctx context.Context, key string) error
	// All returns every setting.
	All(ctx context.Context) (map[string]string, error)
}

// Tx is the transactional view of the stores: everything reached through it
// commits or rolls back together. Later phases add the job and run stores to
// it.
type Tx interface {
	Repos() RepoStore
	Settings() SettingsStore
}

// TxRunner runs fn in one database transaction. fn returning an error rolls
// the transaction back and that error is returned; nil commits.
type TxRunner interface {
	InTx(ctx context.Context, fn func(Tx) error) error
}

// Recovery kinds: what the store did when the database file failed its
// integrity check at start-up.
const (
	// RecoveryRestoredFromBackup: the newest usable pre-migration backup was
	// restored. Data written after that backup is gone from the live database.
	RecoveryRestoredFromBackup = "restored_from_backup"
	// RecoveryStartedEmpty: no backup was usable, so the application started
	// with an empty database. Nothing was deleted: the damaged file is kept.
	RecoveryStartedEmpty = "started_empty"
)

// DatabaseRecovery describes a start-up recovery. Paths are local file names
// meant for the user to see.
type DatabaseRecovery struct {
	// Kind is RecoveryRestoredFromBackup or RecoveryStartedEmpty.
	Kind string
	// DamagedFile is where the damaged database was set aside
	// (ui.db.corrupt-<UTC stamp>).
	DamagedFile string
	// RestoredFrom is the backup that was restored; empty for
	// RecoveryStartedEmpty.
	RestoredFrom string
	// BackupsDir holds the pre-migration backups, which are never deleted by a
	// recovery.
	BackupsDir string
	// Reason is the integrity failure, for logs only.
	Reason string
}

// RecoverySource reports a database recovery that the user has not yet seen:
// one from this start-up, or one from an earlier run that was never
// acknowledged. Recovery returns nil when there is none.
type RecoverySource interface {
	Recovery() *DatabaseRecovery
	// AcknowledgeRecovery records that the user has seen the notice, so it is
	// not reported again at the next start. The damaged file and the backups
	// are kept.
	AcknowledgeRecovery() error
}

// JobRecord is a stored job definition (table jobs). spec is opaque to the
// store; its schema is owned by the job kind and versioned by SpecVersion.
//
// JobStore and RunStore are declared here as signatures only; the SQLite
// implementations arrive with the scheduler and history phases (08 and 12),
// which may refine them.
type JobRecord struct {
	ID          int64
	Kind        string
	Name        string
	Spec        json.RawMessage
	SpecVersion int
	// Mode is "preview" or "post".
	Mode      string
	Enabled   bool
	CreatedAt time.Time
	// RepositoryIDs are the job's targets (table job_targets).
	RepositoryIDs []int64
}

// JobStore persists job definitions and their repository targets.
type JobStore interface {
	CreateJob(ctx context.Context, j JobRecord) (JobRecord, error)
	GetJob(ctx context.Context, id int64) (JobRecord, error)
	ListJobs(ctx context.Context) ([]JobRecord, error)
	UpdateJob(ctx context.Context, j JobRecord) error
	DeleteJob(ctx context.Context, id int64) error
}

// RunRecord is one execution of a job (table runs).
type RunRecord struct {
	ID    int64
	JobID int64
	// Trigger is "schedule", "manual" or "catchup".
	Trigger    string
	StartedAt  time.Time
	FinishedAt *time.Time
	Status     string
	ErrorCode  string
	CostUSD    float64
}

// TaskRecord is one repository (or pull request) unit of work inside a run
// (table tasks).
type TaskRecord struct {
	ID           int64
	RunID        int64
	RepositoryID int64
	// PRNumber is 0 for a local review.
	PRNumber     int
	Status       string
	ExitCode     *int
	CLIVersion   string
	HeadOID      string
	Verdict      string
	FindingsJSON json.RawMessage
	ReportJSON   json.RawMessage
	LogPath      string
	CostUSD      float64
}

// RunStore persists runs and their tasks.
type RunStore interface {
	CreateRun(ctx context.Context, r RunRecord) (RunRecord, error)
	UpdateRun(ctx context.Context, r RunRecord) error
	ListRuns(ctx context.Context, jobID int64, limit int) ([]RunRecord, error)
	CreateTask(ctx context.Context, t TaskRecord) (TaskRecord, error)
	UpdateTask(ctx context.Context, t TaskRecord) error
	ListTasks(ctx context.Context, runID int64) ([]TaskRecord, error)
}
