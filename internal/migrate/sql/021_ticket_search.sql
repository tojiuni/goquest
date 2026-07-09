-- Full-text search (goq-ticket-search-api): trigram indexes make the case-insensitive
-- substring `q` filter (ILIKE '%term%' over title/title_name/description) index-usable
-- instead of a sequential scan. Correctness is unchanged without these; they are a
-- performance aid. Re-run safe: extension and indexes are IF NOT EXISTS.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_tickets_title_trgm ON tickets USING gin (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_tickets_title_name_trgm ON tickets USING gin (title_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_tickets_description_trgm ON tickets USING gin (description gin_trgm_ops);
