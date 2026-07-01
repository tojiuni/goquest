-- VCS linking: persist PR source (head) and target (base) branches.
ALTER TABLE vcs_links ADD COLUMN IF NOT EXISTS source_branch text NOT NULL DEFAULT '';
ALTER TABLE vcs_links ADD COLUMN IF NOT EXISTS target_branch text NOT NULL DEFAULT '';
