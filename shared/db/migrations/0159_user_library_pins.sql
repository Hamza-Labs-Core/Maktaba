-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0159 (Epic 45, story 45.4) - browsing the libraries. Each user keeps the libraries
-- they watch at hand: user_library_pins holds them in the order they chose (position 0
-- first). The API lets a user pin only a library they may see, at most 20.
--
-- The indexes serve the browsing rows: Recently Added across the visible libraries
-- (created_at, id), the same row in one library (library_id, created_at), and a library's
-- grid by publish date, which is the file's mtime (library_id, mtime).
--
-- In a machine's copy like playback_state (a user's own data, read by the API only).
-- maktaba_worker: the default rights. Additive. NO TRANSACTION for the indexes on videos
-- (CONCURRENTLY: the table is large), as slot 0150.
--
CREATE TABLE IF NOT EXISTS user_library_pins (
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    position   INTEGER     NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, library_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_created_at_idx
    ON videos (created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_library_created_idx
    ON videos (library_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_library_mtime_idx
    ON videos (library_id, mtime DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_library_mtime_idx;
DROP INDEX IF EXISTS videos_library_created_idx;
DROP INDEX IF EXISTS videos_created_at_idx;
DROP TABLE IF EXISTS user_library_pins;
-- +goose StatementEnd
