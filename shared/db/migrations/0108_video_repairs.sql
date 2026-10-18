-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0108 (plan 06-11, mediaintel parity) - what the repair_broken stage did to each file
-- whose probe failed: fetched from YouTube again and swapped in (repaired), read fine after
-- all (readable), could not be repaired this time (failed) or has no YouTube id (no_id).
-- The broken file itself is kept beside the new one (backup_path) unless the rules say not.
--
CREATE TABLE IF NOT EXISTS video_repairs (
    video_id     UUID        PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    library_id   UUID        REFERENCES libraries(id) ON DELETE CASCADE,
    youtube_id   TEXT,
    state        TEXT        NOT NULL CHECK (state IN ('repaired','readable','failed','no_id')),
    message      TEXT,
    backup_path  TEXT,
    attempts     INT         NOT NULL DEFAULT 1,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS video_repairs_library_idx ON video_repairs (library_id, state);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS video_repairs_library_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS video_repairs;
-- +goose StatementEnd
