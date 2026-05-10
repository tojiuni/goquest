-- Phase 0: Workspace / Project / Tag / XP / Ticket 확장
-- idempotent: CREATE IF NOT EXISTS, ADD COLUMN IF NOT EXISTS

-- ── 새 ENUM 타입 ─────────────────────────────────────────────────

DO $$ BEGIN
  CREATE TYPE workspace_role AS ENUM ('owner','admin','member','viewer');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
  CREATE TYPE project_role AS ENUM ('admin','member','viewer');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
  CREATE TYPE workspace_type AS ENUM ('personal','shared');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
  CREATE TYPE project_status AS ENUM ('active','archived');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
  CREATE TYPE project_difficulty AS ENUM ('easy','normal','hard','legendary');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
  CREATE TYPE ticket_visibility AS ENUM ('project','restricted');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
  CREATE TYPE completion_mode AS ENUM ('manual','answer','agent','subtask');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ── 신규 테이블 ───────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS workspaces (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  name        TEXT NOT NULL,
  slug        TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  type        workspace_type NOT NULL DEFAULT 'shared',
  owner_id    TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS workspace_members (
  workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id      TEXT NOT NULL,
  role         workspace_role NOT NULL DEFAULT 'member',
  xp           INT NOT NULL DEFAULT 0,
  level        INT NOT NULL DEFAULT 1,
  title        TEXT NOT NULL DEFAULT 'Novice',
  joined_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY  (workspace_id, user_id)
);

CREATE TABLE IF NOT EXISTS workspace_invitations (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  email        TEXT,
  token        TEXT NOT NULL UNIQUE,
  role         workspace_role NOT NULL DEFAULT 'member',
  invited_by   TEXT NOT NULL,
  expires_at   TIMESTAMPTZ NOT NULL,
  used_at      TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS projects (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  status       project_status NOT NULL DEFAULT 'active',
  difficulty   project_difficulty,
  reward_xp    INT NOT NULL DEFAULT 0,
  created_by   TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (workspace_id, name)
);

CREATE TABLE IF NOT EXISTS project_members (
  project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  user_id     TEXT NOT NULL,
  role        project_role NOT NULL DEFAULT 'member',
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (project_id, user_id)
);

CREATE TABLE IF NOT EXISTS tags (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  color        TEXT NOT NULL DEFAULT '#1677ff',
  created_by   TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (workspace_id, name)
);

CREATE TABLE IF NOT EXISTS ticket_tags (
  ticket_id UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  tag_id    UUID NOT NULL REFERENCES tags(id)    ON DELETE CASCADE,
  PRIMARY KEY (ticket_id, tag_id)
);

CREATE TABLE IF NOT EXISTS ticket_attempts (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  ticket_id    UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  user_id      TEXT NOT NULL,
  answer       TEXT NOT NULL,
  is_correct   BOOLEAN,
  ai_feedback  TEXT,
  attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS xp_levels (
  level        INT PRIMARY KEY,
  xp_required  INT NOT NULL,
  title        TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS project_api_keys (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  project_id   UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  key_hash     TEXT NOT NULL UNIQUE,
  created_by   TEXT NOT NULL,
  expires_at   TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS ticket_templates (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  title           TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  type            ticket_type,
  priority        ticket_priority,
  completion_mode completion_mode NOT NULL DEFAULT 'manual',
  completion_spec JSONB NOT NULL DEFAULT '{}',
  reward_xp       INT NOT NULL DEFAULT 0,
  created_by      TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── tickets 테이블 확장 ────────────────────────────────────────────

ALTER TABLE tickets
  ADD COLUMN IF NOT EXISTS project_id      UUID REFERENCES projects(id),
  ADD COLUMN IF NOT EXISTS parent_id       UUID REFERENCES tickets(id) ON DELETE CASCADE,
  ADD COLUMN IF NOT EXISTS visibility      ticket_visibility NOT NULL DEFAULT 'project',
  ADD COLUMN IF NOT EXISTS completion_mode completion_mode NOT NULL DEFAULT 'manual',
  ADD COLUMN IF NOT EXISTS completion_spec JSONB NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS reward_xp       INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS due_at          TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS callback_url    TEXT;

-- ── 인덱스 ────────────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_workspace_members_user ON workspace_members(user_id);
CREATE INDEX IF NOT EXISTS idx_workspace_invitations_token ON workspace_invitations(token);
CREATE INDEX IF NOT EXISTS idx_workspace_invitations_email ON workspace_invitations(email);
CREATE INDEX IF NOT EXISTS idx_projects_workspace ON projects(workspace_id);
CREATE INDEX IF NOT EXISTS idx_project_members_user ON project_members(user_id);
CREATE INDEX IF NOT EXISTS idx_tags_workspace ON tags(workspace_id);
CREATE INDEX IF NOT EXISTS idx_ticket_tags_ticket ON ticket_tags(ticket_id);
CREATE INDEX IF NOT EXISTS idx_ticket_tags_tag ON ticket_tags(tag_id);
CREATE INDEX IF NOT EXISTS idx_tickets_project ON tickets(project_id);
CREATE INDEX IF NOT EXISTS idx_tickets_parent ON tickets(parent_id);
CREATE INDEX IF NOT EXISTS idx_attempts_ticket_user ON ticket_attempts(ticket_id, user_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_project ON project_api_keys(project_id);
CREATE INDEX IF NOT EXISTS idx_templates_project ON ticket_templates(project_id);

-- ── XP 레벨 시드 ─────────────────────────────────────────────────
-- ON CONFLICT DO NOTHING → 재실행 안전

INSERT INTO xp_levels (level, xp_required, title) VALUES
  (1,       0, 'Novice'),
  (2,     500, 'Apprentice'),
  (3,    1200, 'Journeyman'),
  (4,    2500, 'Adept'),
  (5,    5000, 'Knight'),
  (6,    9000, 'Veteran'),
  (7,   15000, 'Elite'),
  (8,   25000, 'Master'),
  (9,   40000, 'Grand Master'),
  (10,  60000, 'Champion'),
  (15, 150000, 'Hero'),
  (20, 300000, 'Legend'),
  (30, 700000, 'Mythic'),
  (50,1500000, 'Immortal')
ON CONFLICT (level) DO NOTHING;
