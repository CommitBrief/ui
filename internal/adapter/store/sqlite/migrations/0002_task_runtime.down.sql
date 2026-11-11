DROP INDEX idx_tasks_status;
ALTER TABLE tasks DROP COLUMN finished_at;
ALTER TABLE tasks DROP COLUMN started_at;
ALTER TABLE tasks DROP COLUMN queued_at;
ALTER TABLE tasks DROP COLUMN error_code;
ALTER TABLE tasks DROP COLUMN spec_json;
ALTER TABLE tasks DROP COLUMN kind;
