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
// commits or rolls back together.
type Tx interface {
	Repos() RepoStore
	Settings() SettingsStore
	Jobs() JobStore
	Runs() RunStore
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
	// CreateJob inserts j (ID and CreatedAt are assigned by the store) with its
	// targets and returns the stored row. A target that is not a stored
	// repository returns ErrNotFound.
	CreateJob(ctx context.Context, j JobRecord) (JobRecord, error)
	// GetJob returns the job with its targets or ErrNotFound.
	GetJob(ctx context.Context, id int64) (JobRecord, error)
	// ListJobs returns every job, oldest first, with its targets.
	ListJobs(ctx context.Context) ([]JobRecord, error)
	// UpdateJob replaces name, spec, mode, enabled and targets of the row with
	// j.ID (the kind never changes). ErrNotFound when it does not exist.
	UpdateJob(ctx context.Context, j JobRecord) error
	// DeleteJob removes the job and its targets; its runs stay with the job
	// reference cleared. ErrNotFound when it does not exist.
	DeleteJob(ctx context.Context, id int64) error
}

// RunRecord is one execution of a job (table runs). JobID is 0 for a one-off
// run that has no stored job.
type RunRecord struct {
	ID    int64
	JobID int64
	// Trigger is "schedule", "manual" or "catchup".
	Trigger string
	// StartedAt is when the run was created (queued); the table has no
	// separate queue time.
	StartedAt  time.Time
	FinishedAt *time.Time
	Status     string
	ErrorCode  string
	CostUSD    float64
}

// TaskRecord is one repository (or pull request) unit of work inside a run
// (table tasks).
type TaskRecord struct {
	ID    int64
	RunID int64
	// Kind and Spec are what the job kind planned (columns added by migration
	// 0002).
	Kind string
	Spec json.RawMessage
	// RepositoryID is 0 when the task has no repository (or it was deleted).
	RepositoryID int64
	// PRNumber is 0 for a local review.
	PRNumber   int
	Status     string
	ErrorCode  string
	ExitCode   *int
	CLIVersion string
	HeadOID    string
	Verdict    string
	// FindingsJSON and ReportJSON are empty in a listing made without payload.
	FindingsJSON json.RawMessage
	ReportJSON   json.RawMessage
	// FindingCount is the length of the stored findings array; filled by every
	// read, payload or not.
	FindingCount int
	LogPath      string
	CostUSD      float64
	QueuedAt     time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
}

// TaskLogRef names the log file of a task, for the retention sweep.
type TaskLogRef struct {
	TaskID int64
	Path   string
}

// RunStore persists runs and their tasks.
type RunStore interface {
	// CreateRun inserts r (ID assigned by the store) and returns the stored row.
	CreateRun(ctx context.Context, r RunRecord) (RunRecord, error)
	// UpdateRun replaces status, error code, cost and finish time of the row.
	// ErrNotFound when it does not exist.
	UpdateRun(ctx context.Context, r RunRecord) error
	// GetRun returns the run or ErrNotFound.
	GetRun(ctx context.Context, id int64) (RunRecord, error)
	// ListRuns returns the newest runs first. jobID 0 means every run; limit
	// bounds the result.
	ListRuns(ctx context.Context, jobID int64, limit int) ([]RunRecord, error)
	// CreateTask inserts t (ID assigned by the store) and returns the stored row.
	CreateTask(ctx context.Context, t TaskRecord) (TaskRecord, error)
	// UpdateTask replaces everything a task gains while it runs: status, error
	// code, result columns, log path, cost and timestamps. ErrNotFound when it
	// does not exist.
	UpdateTask(ctx context.Context, t TaskRecord) error
	// GetTask returns the task with its payload or ErrNotFound.
	GetTask(ctx context.Context, id int64) (TaskRecord, error)
	// ListTasks returns the tasks of a run in creation order. Without payload
	// the findings and report columns are left out (a history list must not
	// carry megabytes); FindingCount is filled either way.
	ListTasks(ctx context.Context, runID int64, withPayload bool) ([]TaskRecord, error)
	// AbortUnfinished marks every queued or running task and run canceled (with
	// error code "canceled" and finish time at). It is called at start-up: the
	// previous process ended before they did. It returns how many tasks it
	// changed.
	AbortUnfinished(ctx context.Context, at time.Time) (int, error)
	// ExpiredLogs lists the log files of finished tasks whose run started
	// before the cutoff or is not among the keepNewest most recent runs.
	ExpiredLogs(ctx context.Context, before time.Time, keepNewest int) ([]TaskLogRef, error)
	// ClearLog forgets the log path of a task whose file was removed.
	ClearLog(ctx context.Context, taskID int64) error
}
