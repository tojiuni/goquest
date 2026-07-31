-- ticket_mentions: @mention of a lymphhub actor in a ticket description or
-- comment. mentioned_actor_id is a lymphhub actor id (resolved/validated
-- against project membership at write time, not enforced by FK — lymphhub
-- is an external system). comment_id is NULL for a description mention.
-- seen_at NULL means unread; set once the mentioned actor views/dismisses it.
CREATE TABLE IF NOT EXISTS ticket_mentions (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    ticket_id           UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    comment_id          UUID REFERENCES ticket_comments(id) ON DELETE CASCADE,
    mentioned_actor_id  TEXT NOT NULL,
    source              TEXT NOT NULL CHECK (source IN ('description', 'comment')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by          TEXT NOT NULL,
    seen_at             TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_ticket_mentions_actor_unseen ON ticket_mentions(mentioned_actor_id, seen_at);
CREATE INDEX IF NOT EXISTS idx_ticket_mentions_ticket        ON ticket_mentions(ticket_id);
