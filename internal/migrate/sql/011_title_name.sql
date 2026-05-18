-- Add title_name column for human-readable ticket identifiers (e.g. GOQ-vault-portforward).
-- NULL is allowed — not all tickets require a title_name.
-- Uniqueness is scoped per project: same title_name cannot exist twice within the same project.
-- NULLs and tickets without a project are excluded from the constraint.

ALTER TABLE tickets ADD COLUMN title_name VARCHAR(255) NULL;

CREATE UNIQUE INDEX tickets_project_title_name_key
    ON tickets (project_id, title_name)
    WHERE project_id IS NOT NULL AND title_name IS NOT NULL;
