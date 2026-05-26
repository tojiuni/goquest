-- Backfill project_id for subtasks that have a parent but no project, so SpiceDB
-- ticket→project tuples can be written for them (fine-grained visibility). New
-- subtasks inherit the parent's project at create time; this fixes pre-existing
-- rows so cmd/backfill-spicedb (WHERE project_id IS NOT NULL) picks them up.
--
-- Single-level: a subtask of an orphan subtask is only fixed on the next pass.
-- Re-running is safe (idempotent — already-set rows are excluded by the NULL guard).
UPDATE tickets c
SET project_id = p.project_id
FROM tickets p
WHERE c.parent_id = p.id
  AND c.project_id IS NULL
  AND p.project_id IS NOT NULL;
