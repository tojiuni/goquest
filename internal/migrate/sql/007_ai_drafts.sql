-- 007_ai_drafts.sql
-- Phase H5: AI auto-routing + gardener gate.
-- Idempotent.

-- ticket_comments: drafts (not visible to customer until published)
ALTER TABLE ticket_comments
  ADD COLUMN IF NOT EXISTS is_draft        BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS gardener_score  REAL;
CREATE INDEX IF NOT EXISTS idx_ticket_comments_draft ON ticket_comments(is_draft) WHERE is_draft = TRUE;

-- workspace AI policy (workspace-scoped, project override deferred)
CREATE TABLE IF NOT EXISTS workspace_ai_policy (
  workspace_id        UUID PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
  gardener_threshold  REAL NOT NULL DEFAULT 0.7,
  fallback_action     TEXT NOT NULL DEFAULT 'escalate-to-staff'
    CHECK (fallback_action IN ('escalate-to-staff','auto-retry','dead-letter')),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
