-- Phase A: contract-plane reject cascade adds cancellation_note metadata.
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS cancellation_note TEXT;
