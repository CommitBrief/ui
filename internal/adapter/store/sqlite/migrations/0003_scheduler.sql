-- 0003_scheduler: what the scheduler, the bot and the daily budget need
-- (phase 12). 0001 and 0002 are never edited.

-- The failure class that stopped the scheduler from running a job (gh not
-- logged in, the CLI too old, ...). NULL means the job is fine; the scheduler
-- skips a job with a value until the user clears it.
ALTER TABLE jobs ADD COLUMN attention_code TEXT;
ALTER TABLE jobs ADD COLUMN attention_at TEXT;

-- The daily budget sums the cost of the tasks that finished today.
CREATE INDEX idx_tasks_finished ON tasks (finished_at);

-- tracked_prs is the set of pull requests the bot follows; it is shared by every
-- job. "Which head did this job review" is per job and per mode, so it moved to
-- bot_reviews: a post job must not skip a head that only a preview job looked at
-- (and the other way round), and switching a job's mode starts from a clean
-- slate. The two columns it replaces were never written.
ALTER TABLE tracked_prs DROP COLUMN last_head_oid;
ALTER TABLE tracked_prs DROP COLUMN last_run_at;

CREATE TABLE bot_reviews (
    job_id        INTEGER NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    repository_id INTEGER NOT NULL,
    number        INTEGER NOT NULL,
    mode          TEXT    NOT NULL CHECK (mode IN ('preview', 'post')),
    last_head_oid TEXT    NOT NULL,
    last_run_at   TEXT    NOT NULL,
    PRIMARY KEY (job_id, repository_id, number, mode),
    FOREIGN KEY (repository_id, number) REFERENCES tracked_prs (repository_id, number) ON DELETE CASCADE
);
