-- 0002_task_runtime: what a task needs to be planned, queued, run and shown
-- (phase 08). 0001 is never edited. All columns are nullable: rows written
-- before this migration (none ship in v0.1) read back as "unknown".

-- The job kind that planned the task (review.local, review.pr, ...) and the
-- kind-owned JSON it planned it with.
ALTER TABLE tasks ADD COLUMN kind TEXT;
ALTER TABLE tasks ADD COLUMN spec_json TEXT;
-- The errcode a task that did not succeed ended with.
ALTER TABLE tasks ADD COLUMN error_code TEXT;
-- Timestamps in the 0001 text form; the task row is created queued.
ALTER TABLE tasks ADD COLUMN queued_at TEXT;
ALTER TABLE tasks ADD COLUMN started_at TEXT;
ALTER TABLE tasks ADD COLUMN finished_at TEXT;

-- Start-up looks for unfinished tasks; the retention sweep skips them.
CREATE INDEX idx_tasks_status ON tasks (status);
