-- Reverses 0001_init: drops everything it created, children first. The data is
-- gone; the pre-migration backup is the safety net, not this file.

DROP TABLE settings;
DROP TABLE tasks;
DROP TABLE runs;
DROP TABLE tracked_prs;
DROP TABLE schedules;
DROP TABLE job_targets;
DROP TABLE jobs;
DROP TABLE repositories;
