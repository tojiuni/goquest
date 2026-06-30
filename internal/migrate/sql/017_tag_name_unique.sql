-- Workspace-scoped, case-insensitive uniqueness for tag names.
-- Enables get-or-create by name (see internal/tag GetOrCreate) and guards
-- against duplicate tags differing only by case within one workspace.
--
-- PRE-CHECK (run manually against the cluster DB before deploying — the
-- migration runner only logs failures, it does not abort):
--   SELECT workspace_id, LOWER(name), COUNT(*)
--   FROM tags GROUP BY 1,2 HAVING COUNT(*) > 1;
-- Resolve any duplicates (rename/merge) first, otherwise this index is
-- silently not created. After deploy verify with: \d tags
CREATE UNIQUE INDEX IF NOT EXISTS tags_workspace_lower_name_key
    ON tags (workspace_id, LOWER(name));
