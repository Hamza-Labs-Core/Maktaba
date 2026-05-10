-- +goose Up
-- +goose StatementBegin
--
-- Slot 0044 (Epic 9 / Story 9.3) — sweep telemetry.
--
-- One row per periodic-sweep run. `errors_jsonb` is a JSON array of
-- `{path, error}` entries (capped at 100 entries by the sweeper; the
-- rest land in logs).
--
CREATE TABLE IF NOT EXISTS library_sweeps (
    id              TEXT     PRIMARY KEY,
    library_id      TEXT     NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    started_at      TEXT     NOT NULL,
    finished_at     TEXT,
    scanned         INTEGER  NOT NULL DEFAULT 0,
    new_videos      INTEGER  NOT NULL DEFAULT 0,
    moved_videos    INTEGER  NOT NULL DEFAULT 0,
    removed_videos  INTEGER  NOT NULL DEFAULT 0,
    errors_jsonb    TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS library_sweeps_lookup
    ON library_sweeps (library_id, started_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS library_sweeps_lookup;
DROP TABLE IF EXISTS library_sweeps;
-- +goose StatementEnd
