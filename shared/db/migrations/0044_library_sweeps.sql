-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0044 (Epic 9 / Story 9.3) — sweep telemetry.
--
CREATE TABLE IF NOT EXISTS library_sweeps (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id      UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    started_at      TIMESTAMPTZ  NOT NULL,
    finished_at     TIMESTAMPTZ,
    scanned         INTEGER      NOT NULL DEFAULT 0,
    new_videos      INTEGER      NOT NULL DEFAULT 0,
    moved_videos    INTEGER      NOT NULL DEFAULT 0,
    removed_videos  INTEGER      NOT NULL DEFAULT 0,
    errors_jsonb    JSONB
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS library_sweeps_lookup
    ON library_sweeps (library_id, started_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS library_sweeps_lookup;
DROP TABLE IF EXISTS library_sweeps;
-- +goose StatementEnd
