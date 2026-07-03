-- Risk tier: declarative scrutiny level; medium/high route PR-merge auto-completion to pending_approval.
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS risk_tier text NOT NULL DEFAULT 'low';
