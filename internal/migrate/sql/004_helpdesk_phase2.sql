-- 004_helpdesk_phase2.sql
-- Phase H2: API Key scope/capability/rate-limit + ticket_macros.
-- Idempotent.

-- ── project_api_keys: scope/capability/rate-limit ────────────────────────
ALTER TABLE project_api_keys
  ADD COLUMN IF NOT EXISTS allowed_capabilities TEXT[] NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS scope                TEXT NOT NULL DEFAULT 'private'
    CHECK (scope IN ('public','private')),
  ADD COLUMN IF NOT EXISTS rate_limit_per_min   INT NOT NULL DEFAULT 60,
  ADD COLUMN IF NOT EXISTS daily_quota          INT,
  ADD COLUMN IF NOT EXISTS allowed_origins      TEXT[] NOT NULL DEFAULT '{}';

-- ── ticket_macros (workspace-scoped templates for staff replies) ─────────
CREATE TABLE IF NOT EXISTS ticket_macros (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  project_id   UUID REFERENCES projects(id) ON DELETE CASCADE, -- nullable: workspace-wide
  name         TEXT NOT NULL,
  body         TEXT NOT NULL,
  created_by   TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_ticket_macros_workspace ON ticket_macros(workspace_id);
CREATE INDEX IF NOT EXISTS idx_ticket_macros_project   ON ticket_macros(project_id);
