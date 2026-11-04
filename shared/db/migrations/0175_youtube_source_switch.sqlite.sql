-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0175: youtube_sources.last_on_mode (the source's last
-- mode other than off; NULL: never on), backfilled from the sources on now, and
-- youtube_source_guesses (the channel a library with no source looks like). SQLite has
-- no machines' copy, so no exclusion.
--
ALTER TABLE youtube_sources ADD COLUMN IF NOT EXISTS last_on_mode TEXT
    CHECK (last_on_mode IN ('new_and_backfill', 'new_only'));
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE youtube_sources SET last_on_mode = mode
 WHERE mode IN ('new_and_backfill', 'new_only') AND last_on_mode IS NOT mode;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_source_guesses (
    library_id         TEXT    PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    kind               TEXT    CHECK (kind IN ('channel', 'playlist')),
    external_id        TEXT,
    url                TEXT,
    title              TEXT,
    evidence           TEXT    CHECK (evidence IN ('info_json', 'filename')),
    sample_youtube_id  TEXT,
    ids_found          INTEGER NOT NULL DEFAULT 0,
    ids_matched        INTEGER NOT NULL DEFAULT 0,
    looked_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK ((external_id IS NULL) = (kind IS NULL) AND (external_id IS NULL) = (url IS NULL))
);
-- +goose StatementEnd

-- +goose StatementBegin
-- the Tartube import (Admin > Libraries): every library's sources from Tartube's records,
-- off, and what it downloaded seeded done; on the home (the pipeline's graph has it)
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('youtube_tartube', 'Tartube import', '["catalog"]', 'network_yt', 'sources', 4, 1, 0, 0, 1, '[]', '[]', '[]', 0, 0, 0, 'auto', 'home', 1, NULL)
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'youtube_tartube';
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_source_guesses;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE youtube_sources DROP COLUMN last_on_mode;
-- +goose StatementEnd
