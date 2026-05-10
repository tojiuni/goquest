-- 006_audit_log.sql
-- Phase H6: append-only audit log of significant actions.
-- Idempotent.

CREATE TABLE IF NOT EXISTS audit_log (
  id          BIGSERIAL PRIMARY KEY,
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  actor       TEXT NOT NULL,         -- user_id or 'apikey:<id>' or 'system'
  action      TEXT NOT NULL,         -- e.g. 'workspace.member.add'
  target_kind TEXT NOT NULL,         -- e.g. 'workspace', 'ticket', 'api_key'
  target_id   TEXT NOT NULL,
  detail      JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_audit_log_target ON audit_log(target_kind, target_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_actor  ON audit_log(actor);
CREATE INDEX IF NOT EXISTS idx_audit_log_time   ON audit_log(occurred_at DESC);
