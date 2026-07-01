-- VCS linking: repo→project registration + branch/commit/PR links.
CREATE TABLE IF NOT EXISTS vcs_repos (
    id              UUID PRIMARY KEY,
    provider        TEXT NOT NULL,
    repo_full_name  TEXT NOT NULL,
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    auto_transition BOOLEAN NOT NULL DEFAULT TRUE,
    merge_target    TEXT NOT NULL DEFAULT 'completed',
    active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS vcs_repos_provider_repo_uq
    ON vcs_repos (provider, repo_full_name);

CREATE TABLE IF NOT EXISTS vcs_links (
    id             UUID PRIMARY KEY,
    ticket_id      UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    provider       TEXT NOT NULL,
    repo_full_name TEXT NOT NULL,
    kind           TEXT NOT NULL,          -- branch | commit | pull_request
    external_ref   TEXT NOT NULL,          -- branch name | sha | PR number
    url            TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT 'open',
    title          TEXT NOT NULL DEFAULT '',
    actor          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS vcs_links_uq
    ON vcs_links (ticket_id, provider, repo_full_name, kind, external_ref);
CREATE INDEX IF NOT EXISTS vcs_links_ticket_idx ON vcs_links (ticket_id);
CREATE INDEX IF NOT EXISTS vcs_links_branch_idx
    ON vcs_links (provider, repo_full_name, external_ref) WHERE kind = 'branch';
