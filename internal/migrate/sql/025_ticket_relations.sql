-- ticket_relations: typed directed edges between tickets (blocks/depends_on/
-- duplicates). Reverse label is derived at read time, not stored. CASCADE on
-- either ticket's delete.
CREATE TABLE IF NOT EXISTS ticket_relations (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    from_ticket_id  UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    to_ticket_id    UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    relation_type   TEXT NOT NULL CHECK (relation_type IN ('blocks','depends_on','duplicates')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by      TEXT NOT NULL,
    UNIQUE (from_ticket_id, to_ticket_id, relation_type),
    CHECK (from_ticket_id <> to_ticket_id)
);

CREATE INDEX IF NOT EXISTS idx_ticket_relations_from ON ticket_relations(from_ticket_id);
CREATE INDEX IF NOT EXISTS idx_ticket_relations_to   ON ticket_relations(to_ticket_id);
