-- 022_org.sql — goq-org-backend: org reference axes + assignee_type 'org'.
-- Idempotent (IF NOT EXISTS / catalog guards), spec GQT-SPC-20260720-01.

-- assignee_type ENUM('human','agent') → TEXT + CHECK so 'org' can be added
-- transactionally (channel in 003_helpdesk.sql uses the same style).
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_type WHERE typname = 'assignee_type') THEN
    -- Drop the enum-typed default first: PostgreSQL cannot auto-cast the
    -- default expression ('human'::assignee_type) during the type change.
    ALTER TABLE tickets ALTER COLUMN assignee_type DROP DEFAULT;
    ALTER TABLE tickets ALTER COLUMN assignee_type TYPE TEXT
      USING assignee_type::TEXT;
    ALTER TABLE tickets ALTER COLUMN assignee_type SET DEFAULT 'human';
    DROP TYPE assignee_type;
  END IF;
END $$;

ALTER TABLE tickets DROP CONSTRAINT IF EXISTS tickets_assignee_type_check;
ALTER TABLE tickets ADD CONSTRAINT tickets_assignee_type_check
  CHECK (assignee_type IN ('human','agent','org'));

-- Org reference axes. UUIDs refer to lymphhub organizations — no FK, the
-- source of truth is external.
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS requester_org_id UUID;
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS assignee_org_id  UUID;

CREATE INDEX IF NOT EXISTS idx_tickets_requester_org
  ON tickets(requester_org_id) WHERE requester_org_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tickets_assignee_org
  ON tickets(assignee_org_id) WHERE assignee_org_id IS NOT NULL;
