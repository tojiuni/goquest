-- 005_magic_links.sql
-- Phase H3: magic-link tokens for un-authenticated customers.
-- Idempotent.

CREATE TABLE IF NOT EXISTS magic_tokens (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  token_hash   TEXT NOT NULL UNIQUE,
  email        TEXT NOT NULL,
  ticket_id    UUID REFERENCES tickets(id) ON DELETE CASCADE, -- nullable: account-wide token
  expires_at   TIMESTAMPTZ NOT NULL,
  used_at      TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_magic_tokens_email ON magic_tokens(email);
CREATE INDEX IF NOT EXISTS idx_magic_tokens_ticket ON magic_tokens(ticket_id);
