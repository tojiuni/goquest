-- 010_approval_statuses.sql
-- Add pending_approval and pending_revision ticket statuses for human approval flow.
-- Idempotent.

ALTER TYPE ticket_status ADD VALUE IF NOT EXISTS 'pending_approval';
ALTER TYPE ticket_status ADD VALUE IF NOT EXISTS 'pending_revision';

-- NOTE: ALTER TYPE ADD VALUE cannot be rolled back in PostgreSQL.
-- To undo this migration, the enum type must be dropped and recreated manually.
