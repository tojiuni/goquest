-- start_at: nullable ticket start date, added for the roadmap viewer. It marks
-- the opposite end of the date range from the existing due_at column. When
-- both start_at and due_at are set, the roadmap renders the ticket as a
-- [start_at, due_at] bar. When only due_at is set, it renders as a single
-- point marker at due_at (GQT-SPC-2026080702).
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS start_at timestamptz NULL;
