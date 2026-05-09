-- Postgres bootstrap for the Maktaba compose stack (Story 22.3).
--
-- The postgres:16-alpine image already creates POSTGRES_DB owned by
-- POSTGRES_USER on first boot, so this script only adds the bits that
-- can't be set by env vars: role privileges, extensions, and the
-- application's schema_migrations baseline.
--
-- This file runs exactly once — when the data directory is empty — via
-- the official entrypoint's `/docker-entrypoint-initdb.d` hook. After
-- the volume is initialized, edits here have no effect on existing
-- installs; use a regular migration via `maktaba-api migrate` instead.

\connect maktaba

-- Required by Story 03 (semantic search) and 04 (text search).
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;

-- Story 22.4 created the goose-managed schema_migrations table; nothing
-- to seed here, but the connection above ensures the database exists.
