-- 0001_init: the full v0.1 schema (UI-0006). Later phases only add migrations;
-- this file is never edited once released.
--
-- Conventions: ids are INTEGER AUTOINCREMENT (never reused, history rows keep
-- pointing at the right thing); timestamps are UTC text
-- "YYYY-MM-DDTHH:MM:SS.mmmZ"; booleans are INTEGER 0/1; JSON is TEXT.

CREATE TABLE repositories (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    -- GitHub logins and repository names are case-insensitive, so are the
    -- columns (NOCASE folds ASCII, which is all the validators allow).
    remote_owner TEXT    NOT NULL COLLATE NOCASE,
    remote_name  TEXT    NOT NULL COLLATE NOCASE,
    -- NULL: no local checkout; reviews then run with --repo and without the
    -- project's own rules.
    local_path   TEXT,
    enabled      INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at   TEXT    NOT NULL,
    UNIQUE (remote_owner, remote_name)
);

CREATE TABLE jobs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    -- Open set: review.local, review.pr, bot.review_requested, ...
    kind         TEXT    NOT NULL,
    name         TEXT    NOT NULL,
    spec_json    TEXT    NOT NULL,
    spec_version INTEGER NOT NULL DEFAULT 1,
    mode         TEXT    NOT NULL CHECK (mode IN ('preview', 'post')),
    enabled      INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at   TEXT    NOT NULL
);

CREATE TABLE job_targets (
    job_id        INTEGER NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    PRIMARY KEY (job_id, repository_id)
);
CREATE INDEX idx_job_targets_repository ON job_targets (repository_id);

-- One row per time window; a job may have several. days_mask: bit 0 = Monday
-- ... bit 6 = Sunday. Minutes count from local midnight in tz (IANA name); the
-- window follows the wall clock across DST changes.
CREATE TABLE schedules (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id        INTEGER NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    days_mask     INTEGER NOT NULL CHECK (days_mask BETWEEN 0 AND 127),
    start_minute  INTEGER NOT NULL CHECK (start_minute BETWEEN 0 AND 1439),
    end_minute    INTEGER NOT NULL CHECK (end_minute BETWEEN 0 AND 1440),
    every_minutes INTEGER NOT NULL CHECK (every_minutes > 0),
    tz            TEXT    NOT NULL
);
CREATE INDEX idx_schedules_job ON schedules (job_id);

CREATE TABLE tracked_prs (
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    number        INTEGER NOT NULL CHECK (number > 0),
    first_seen_at TEXT    NOT NULL,
    last_head_oid TEXT,
    last_run_at   TEXT,
    state         TEXT    NOT NULL CHECK (state IN ('open', 'closed', 'merged')),
    source        TEXT    NOT NULL,
    PRIMARY KEY (repository_id, number)
);

-- Run history outlives the job that produced it: deleting a job clears
-- runs.job_id instead of deleting the history.
CREATE TABLE runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id      INTEGER REFERENCES jobs (id) ON DELETE SET NULL,
    "trigger"   TEXT    NOT NULL CHECK ("trigger" IN ('schedule', 'manual', 'catchup')),
    started_at  TEXT    NOT NULL,
    finished_at TEXT,
    status      TEXT    NOT NULL,
    error_code  TEXT,
    cost_usd    REAL
);
CREATE INDEX idx_runs_job ON runs (job_id);
CREATE INDEX idx_runs_started ON runs (started_at);

-- Likewise tasks outlive their repository: deleting a repository clears
-- tasks.repository_id; deleting a run deletes its tasks.
CREATE TABLE tasks (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id        INTEGER NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    repository_id INTEGER REFERENCES repositories (id) ON DELETE SET NULL,
    pr_number     INTEGER,
    status        TEXT    NOT NULL,
    exit_code     INTEGER,
    cli_version   TEXT,
    head_oid      TEXT,
    verdict       TEXT,
    findings_json TEXT,
    report_json   TEXT,
    log_path      TEXT,
    cost_usd      REAL
);
CREATE INDEX idx_tasks_run ON tasks (run_id);
CREATE INDEX idx_tasks_repository ON tasks (repository_id);

-- UI-only settings (UI-0006 section 3); the CLI's own config is never kept here.
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) WITHOUT ROWID;
