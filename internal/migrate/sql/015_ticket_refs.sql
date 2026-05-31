-- ticket_refs: optional N-ary references from a ticket to gopedia L1 docs.
-- id-only (no title/snippet cache); live link policy; CASCADE on ticket delete.
CREATE TABLE IF NOT EXISTS ticket_refs (
    ticket_id       UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    gopedia_l1_id   TEXT NOT NULL,
    position        INT  NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by      TEXT NOT NULL,
    PRIMARY KEY (ticket_id, gopedia_l1_id)
);

CREATE INDEX IF NOT EXISTS idx_ticket_refs_gopedia ON ticket_refs(gopedia_l1_id);
CREATE INDEX IF NOT EXISTS idx_ticket_refs_ticket  ON ticket_refs(ticket_id, position);
