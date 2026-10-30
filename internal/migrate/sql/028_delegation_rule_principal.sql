-- A delegation rule may name only a session account (machine-token actor id),
-- never a person's email. Re-run raises duplicate_object (42710), which
-- migrate.Run treats as already applied.
ALTER TABLE delegation_rules ADD CONSTRAINT delegation_rules_principal_not_person
    CHECK (btrim(principal) <> '' AND position('@' in principal) = 0);
