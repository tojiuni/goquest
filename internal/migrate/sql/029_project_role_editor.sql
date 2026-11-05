-- project_role gains 'editor' (goq-project-editor-role): works tickets without
-- admin's management powers. ADD VALUE IF NOT EXISTS makes the boot-time re-run
-- a no-op. The new value is not used in this file (it cannot be inside the
-- transaction that adds it).
ALTER TYPE project_role ADD VALUE IF NOT EXISTS 'editor';
