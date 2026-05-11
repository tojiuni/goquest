-- 009_drop_vikunja_task_id.sql
-- Vikunja decommission (2026-05): drop the legacy bridge column from tickets.
-- Idempotent.

ALTER TABLE tickets DROP COLUMN IF EXISTS vikunja_task_id;
