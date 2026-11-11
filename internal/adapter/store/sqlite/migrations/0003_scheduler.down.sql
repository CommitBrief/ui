DROP TABLE bot_reviews;
ALTER TABLE tracked_prs ADD COLUMN last_run_at TEXT;
ALTER TABLE tracked_prs ADD COLUMN last_head_oid TEXT;
DROP INDEX idx_tasks_finished;
ALTER TABLE jobs DROP COLUMN attention_at;
ALTER TABLE jobs DROP COLUMN attention_code;
