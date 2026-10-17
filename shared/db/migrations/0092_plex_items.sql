-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0092 (plan 06-11 sections 10.6 and 17, plex_index queue) - Plex's own record of
-- every library item, saved once per library by the Plex extension's
-- plex_index stage. plex_collection and dizquetv_show read it here instead of
-- asking Plex again, so a busy Plex (loudness analysis, butler tasks) stalls
-- one listing per library, not thousands of metadata calls.
--
-- Not external_links: that table is one row per (target, subject) with no
-- payload, and Plex lists items no Maktaba video maps to (outside the path
-- map). The video -> rating key link stays in external_links (target
-- 'plex_item'), written by the same stage.
--
CREATE TABLE IF NOT EXISTS plex_items (
    server           TEXT        NOT NULL,
    rating_key       TEXT        NOT NULL,
    section_key      TEXT        NOT NULL,
    section_title    TEXT        NOT NULL,
    library_id       UUID        REFERENCES libraries(id) ON DELETE SET NULL,
    video_id         UUID        REFERENCES videos(id) ON DELETE SET NULL,
    file             TEXT,
    duration_ms      BIGINT,
    plex_updated_at  BIGINT,
    detail           JSONB       NOT NULL,
    seen_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (server, rating_key)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS plex_items_section_idx ON plex_items (server, section_key);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS plex_items_video_idx ON plex_items (video_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plex_items;
-- +goose StatementEnd
