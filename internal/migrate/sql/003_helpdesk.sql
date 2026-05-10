-- 003_helpdesk.sql
-- Phase H0: helpdesk data model — workspaces.kind, ticket helpdesk columns,
-- ticket_comments. Idempotent (uses IF NOT EXISTS / IF EXISTS guards).

-- ── workspaces.kind ─────────────────────────────────────────────────
ALTER TABLE workspaces
  ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'general'
    CHECK (kind IN ('general','customer-support','internal-helpdesk'));

-- ── tickets helpdesk columns ────────────────────────────────────────
ALTER TABLE tickets
  ADD COLUMN IF NOT EXISTS channel          TEXT NOT NULL DEFAULT 'agent'
    CHECK (channel IN ('web','email','telegram','agent','api'));
ALTER TABLE tickets
  ADD COLUMN IF NOT EXISTS requester_email  TEXT;
ALTER TABLE tickets
  ADD COLUMN IF NOT EXISTS requester_name   TEXT;
ALTER TABLE tickets
  ADD COLUMN IF NOT EXISTS public_token     TEXT UNIQUE;
ALTER TABLE tickets
  ADD COLUMN IF NOT EXISTS public_token_exp TIMESTAMPTZ;
ALTER TABLE tickets
  ADD COLUMN IF NOT EXISTS locale           TEXT NOT NULL DEFAULT 'ko';

CREATE INDEX IF NOT EXISTS idx_tickets_requester_email ON tickets(requester_email);
CREATE INDEX IF NOT EXISTS idx_tickets_public_token    ON tickets(public_token);

-- ── ticket_comments ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS ticket_comments (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  ticket_id   UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  author_id   TEXT NOT NULL,
  author_kind TEXT NOT NULL CHECK (author_kind IN ('staff','agent','customer','system')),
  body        TEXT NOT NULL,
  is_internal BOOLEAN NOT NULL DEFAULT FALSE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_ticket_comments_ticket ON ticket_comments(ticket_id, created_at);
