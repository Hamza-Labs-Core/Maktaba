-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0182: every library's YouTube sources come from its linked videos.
--
-- The link stage already learns each video's YouTube id, channel and playlists (from its
-- .info.json sidecar or its title). youtube_sources was seeded once from youtube_channels,
-- which keeps one library per channel (channel_id is its key): a channel that several
-- libraries share (one playlist each) gave a source to one of them and none to the rest.
--
--   youtube_channel_libraries   every library a channel's linked videos are in, and how
--                               many (the many-to-many youtube_channels.library_id cannot
--                               hold). youtube_channels.library_id stays the one library
--                               whose listing reads the channel (youtube_sync). Written by
--                               the derivation (library_mgmt/youtube_derive.py).
--   youtube_source_decisions    one row per library: what the derivation decided from its
--                               linked videos and why (outcome playlist | channel | clash
--                               | mixed | none; the ids it chose, the ids it created, the
--                               library that already holds a clashing channel or playlist,
--                               the ids it looked at). The card shows a clash as the
--                               duplicate hint.
--   youtube_sources.derived_at  when the derivation made the source (NULL: made another
--                               way). A derived source is made off; the derivation never
--                               changes a source's mode.
--
-- No backfill here: the derivation runs over the existing links from the pipeline (the
-- reconciler's pass, and the link stage for each new link), not from SQL. Additive: a
-- nullable column and two new tables a previous release ignores. Copied to the machines
-- (ids and counts, no secret); the workers may write them (the link stage runs anywhere).
--
-- Runs outside a transaction because the index is built CONCURRENTLY.
--
ALTER TABLE youtube_sources ADD COLUMN IF NOT EXISTS derived_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_channel_libraries (
    channel_id     TEXT        NOT NULL,
    library_id     UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    -- the library's linked videos that are this channel's
    linked_videos  INTEGER     NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, library_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_source_decisions (
    library_id         UUID        PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    outcome            TEXT        NOT NULL
                       CHECK (outcome IN ('playlist', 'channel', 'clash', 'mixed', 'none')),
    kind               TEXT        CHECK (kind IN ('channel', 'playlist')),
    -- the channel or playlists the library's linked videos come from (its sources)
    external_ids       JSONB       NOT NULL DEFAULT '[]'::jsonb,
    -- those of them this decision made a source for (off)
    created            JSONB       NOT NULL DEFAULT '[]'::jsonb,
    -- a clash: the channel or playlist another library already holds, and that library
    clash_external_id  TEXT,
    clash_library_id   UUID        REFERENCES libraries(id) ON DELETE SET NULL,
    linked_videos      INTEGER     NOT NULL DEFAULT 0,
    -- the linked videos the chosen channel or playlists hold
    matched_videos     INTEGER     NOT NULL DEFAULT 0,
    reason             TEXT        NOT NULL,
    -- every channel and real playlist of the library's linked videos when it was decided
    -- (the link stage decides again only for one it has not seen)
    seen               JSONB       NOT NULL DEFAULT '[]'::jsonb,
    decided_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS youtube_channel_libraries_library_idx
    ON youtube_channel_libraries (library_id);
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- the link stage (any machine) and the reconciler's pass derive the sources
        GRANT SELECT, INSERT, UPDATE, DELETE ON youtube_channel_libraries TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE, DELETE ON youtube_source_decisions TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE ON youtube_sources TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE ON youtube_channels TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_source_decisions;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_channel_libraries;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE youtube_sources DROP COLUMN IF EXISTS derived_at;
-- +goose StatementEnd
