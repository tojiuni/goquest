CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TYPE ticket_status AS ENUM ('pending','assigned','in_progress','completed','failed','cancelled');
CREATE TYPE ticket_type AS ENUM ('task','bug','feature','incident','request');
CREATE TYPE ticket_priority AS ENUM ('urgent','high','normal','low');
CREATE TYPE assignee_type AS ENUM ('human','agent');
CREATE TYPE ticket_domain AS ENUM ('infra','data','code','monitor','knowledge','pm');

CREATE TABLE tickets (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    type            ticket_type NOT NULL DEFAULT 'task',
    status          ticket_status NOT NULL DEFAULT 'pending',
    priority        ticket_priority NOT NULL DEFAULT 'normal',
    assignee_type   assignee_type NOT NULL DEFAULT 'human',
    assignee_id     TEXT NOT NULL DEFAULT '',
    requester_id    TEXT NOT NULL,
    source_bot      TEXT,
    domain          ticket_domain,
    agent_context   JSONB,
    vikunja_task_id BIGINT,
    metadata        JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);

CREATE INDEX idx_tickets_status ON tickets(status);
CREATE INDEX idx_tickets_domain ON tickets(domain);
CREATE INDEX idx_tickets_assignee ON tickets(assignee_id);
CREATE INDEX idx_tickets_requester ON tickets(requester_id);
CREATE INDEX idx_tickets_created ON tickets(created_at DESC);
