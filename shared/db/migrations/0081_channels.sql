-- +goose Up
-- +goose StatementBegin
--
-- Slot 0081 (Epic 27 / Story 27.1) — virtual channel definitions.
--
-- A "channel" is a programming rule over the user's library that the
-- scheduler (Story 27.2) turns into a continuous wall-clock-anchored
-- timeline. The record itself stores no schedule — only the rule:
--
--   * `mode`          one of shuffle / marathon / schedule / smart_mix.
--   * `mode_config`   the disjoint per-mode config as a validated JSONB
--                     blob (D1) — keeps the table stable while the four
--                     modes carry very different knobs.
--   * `source_filter` the `smart_query` JSON shape reused verbatim from
--                     Story 7.14 (D2) — "which videos feed this channel".
--   * `slug`          stable external id; XMLTV/M3U guides bind to it, so
--                     a rename must NOT change it (D4).
--   * `number`        the dial position, unique WITHIN a scope (D3). The
--                     scope is the library, or the multi-library bucket
--                     for a null library_id.
--
-- `library_id` null ⇒ a multi-library channel (sources span libraries
-- via `source_filter`); ACL is then enforced per-resolved-video.
--
CREATE TABLE IF NOT EXISTS channels (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id    UUID        REFERENCES libraries(id) ON DELETE CASCADE,  -- null = multi-library
    number        INTEGER     NOT NULL,
    name          TEXT        NOT NULL,
    slug          TEXT        NOT NULL,
    logo_path     TEXT,
    category      TEXT        NOT NULL DEFAULT 'general',
    mode          TEXT        NOT NULL
                              CHECK (mode IN ('shuffle','marathon','schedule','smart_mix')),
    mode_config   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    source_filter JSONB,                                  -- smart_query shape (D2)
    transition    TEXT        NOT NULL DEFAULT 'cut'
                              CHECK (transition IN ('cut','crossfade')),
    enabled       BOOLEAN     NOT NULL DEFAULT true,
    sort_order    INTEGER     NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Number + slug unique within scope: a fixed sentinel UUID stands in for
-- the multi-library (null) bucket so the partial index covers it too (D3).
CREATE UNIQUE INDEX IF NOT EXISTS channels_scope_number_uniq
    ON channels (COALESCE(library_id, '00000000-0000-0000-0000-000000000000'::uuid), number);
CREATE UNIQUE INDEX IF NOT EXISTS channels_scope_slug_uniq
    ON channels (COALESCE(library_id, '00000000-0000-0000-0000-000000000000'::uuid), slug);
CREATE INDEX IF NOT EXISTS channels_enabled_idx ON channels (enabled, sort_order, number);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS channels_enabled_idx;
DROP INDEX IF EXISTS channels_scope_slug_uniq;
DROP INDEX IF EXISTS channels_scope_number_uniq;
DROP TABLE IF EXISTS channels;
-- +goose StatementEnd
