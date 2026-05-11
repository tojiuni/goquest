-- 008_device_tokens.sql
-- Phase 7 (iOS): device push token registry. APNs dispatcher will read this
-- table to fan ticket events out to subscribed devices.
-- Idempotent.

CREATE TABLE IF NOT EXISTS device_tokens (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id     TEXT NOT NULL,
  platform    TEXT NOT NULL CHECK (platform IN ('ios','android')),
  apns_token  TEXT NOT NULL,     -- platform-specific token (APNs hex for iOS, FCM for android)
  app_version TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (user_id, apns_token)
);
CREATE INDEX IF NOT EXISTS idx_device_tokens_user ON device_tokens(user_id);
