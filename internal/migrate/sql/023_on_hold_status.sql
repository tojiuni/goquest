-- 023_on_hold_status.sql
-- Add on_hold ticket status for intentionally-paused work (goq-ticket-on-hold-status).
-- Idempotent.

ALTER TYPE ticket_status ADD VALUE IF NOT EXISTS 'on_hold';

-- NOTE: ALTER TYPE ADD VALUE cannot be rolled back in PostgreSQL.
-- To undo this migration, the enum type must be dropped and recreated manually.
