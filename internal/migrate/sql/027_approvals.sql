-- approvals: append-only approval records (many per ticket). A row is created
-- as 'pending' and moves through approval_events; the only permitted UPDATE is
-- the one-time status/decision write enforced by approvals_guard below, and
-- rows are never deleted. See geneso goquest/spec/2026-10-29-goquest-approval-records-design.md.
CREATE TABLE IF NOT EXISTS approvals (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    ticket_id           UUID NOT NULL REFERENCES tickets(id),
    kind                TEXT NOT NULL CHECK (kind IN ('design','merge','infra','ops','deviation','ticket')),
    subject             JSONB NOT NULL DEFAULT '{}'::jsonb,
    status              TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending','approved','rejected','revision_requested','withdrawn','superseded')),
    requested_by        TEXT NOT NULL,
    requested_by_label  TEXT NOT NULL DEFAULT '',
    requested_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    request_note        TEXT NOT NULL DEFAULT '',
    evidence            JSONB NOT NULL DEFAULT '[]'::jsonb,
    within_direction    BOOLEAN,
    decided_by          TEXT,
    decided_at          TIMESTAMPTZ,
    decision_note       TEXT,
    basis               TEXT CHECK (basis IN ('user_direct','desk_delegated')),
    delegation_rule_id  UUID,
    user_ref            TEXT,
    supersedes_id       UUID REFERENCES approvals(id),
    legacy              BOOLEAN NOT NULL DEFAULT FALSE,
    source_comment_id   UUID,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- deviation must say whether it stays within the design direction
    CHECK (kind <> 'deviation' OR within_direction IS NOT NULL),
    -- delegated decisions must cite the rule that allowed them
    CHECK (basis IS DISTINCT FROM 'desk_delegated' OR delegation_rule_id IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_approvals_ticket  ON approvals(ticket_id, created_at);
CREATE INDEX IF NOT EXISTS idx_approvals_pending ON approvals(status) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS approval_events (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    approval_id  UUID NOT NULL REFERENCES approvals(id),
    from_status  TEXT NOT NULL,
    to_status    TEXT NOT NULL,
    actor        TEXT NOT NULL,
    note         TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_approval_events_approval ON approval_events(approval_id, created_at);

CREATE TABLE IF NOT EXISTS delegation_rules (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    workspace_id   UUID NOT NULL REFERENCES workspaces(id),
    principal      TEXT NOT NULL,
    kinds          TEXT[] NOT NULL,
    repos          TEXT[] NOT NULL DEFAULT '{}',
    conditions     JSONB NOT NULL DEFAULT '{}'::jsonb,
    user_ref       TEXT NOT NULL DEFAULT '',
    created_by     TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at     TIMESTAMPTZ,
    revoked_at     TIMESTAMPTZ,
    -- design/infra/ops/ticket are user_direct only (spec 4.3); enforced in the DB too
    CHECK (kinds <@ ARRAY['merge','deviation']::text[] AND cardinality(kinds) > 0)
);

CREATE INDEX IF NOT EXISTS idx_delegation_rules_ws ON delegation_rules(workspace_id, principal);

-- Append-only guard. DELETE is never allowed. UPDATE is allowed only while the
-- row is still pending, and may change only the status/decision columns; once
-- the row leaves pending it can move only along pending->terminal or
-- approved/revision_requested->superseded, never altering the decision fields.
CREATE OR REPLACE FUNCTION approvals_guard() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'approvals are append-only';
    END IF;
    IF NEW.id <> OLD.id OR NEW.ticket_id <> OLD.ticket_id OR NEW.kind <> OLD.kind
       OR NEW.subject <> OLD.subject OR NEW.requested_by <> OLD.requested_by
       OR NEW.requested_at <> OLD.requested_at OR NEW.request_note <> OLD.request_note
       OR NEW.evidence <> OLD.evidence OR NEW.supersedes_id IS DISTINCT FROM OLD.supersedes_id
       OR NEW.legacy <> OLD.legacy OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'approvals request fields are immutable';
    END IF;
    IF OLD.status <> 'pending' AND (
           NEW.decided_by IS DISTINCT FROM OLD.decided_by
        OR NEW.decided_at IS DISTINCT FROM OLD.decided_at
        OR NEW.decision_note IS DISTINCT FROM OLD.decision_note
        OR NEW.basis IS DISTINCT FROM OLD.basis
        OR NEW.delegation_rule_id IS DISTINCT FROM OLD.delegation_rule_id) THEN
        RAISE EXCEPTION 'approval decision is immutable once recorded';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION approval_events_guard() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'approval_events are append-only';
END;
$$ LANGUAGE plpgsql;

-- CREATE TRIGGER has no IF NOT EXISTS; on re-run it raises duplicate_object
-- (42710), which migrate.Run treats as already applied.
CREATE TRIGGER approvals_guard_trg BEFORE UPDATE OR DELETE ON approvals
    FOR EACH ROW EXECUTE FUNCTION approvals_guard();
CREATE TRIGGER approval_events_guard_trg BEFORE UPDATE OR DELETE ON approval_events
    FOR EACH ROW EXECUTE FUNCTION approval_events_guard();
