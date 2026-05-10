-- +goose Up
-- +goose StatementBegin
--
-- Slot 0043 (Epic 9 / Story 9.16) — canonical library_roots store.
--
-- The transitional `libraries.roots TEXT[]` column from slot 0001 is
-- deprecated; this table is the canonical normalized store. Each row is
-- one root path; `(library_id, path_canonical)` is unique. The
-- canonical form is computed by Python (resolve symlinks, strip trailing
-- slashes, normalize `..`) and stored alongside the user-facing form.
--
CREATE TABLE IF NOT EXISTS library_roots (
    id              TEXT     PRIMARY KEY,
    library_id      TEXT     NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    path            TEXT     NOT NULL,
    path_canonical  TEXT     NOT NULL,
    added_at        TEXT     NOT NULL DEFAULT (datetime('now')),
    UNIQUE (library_id, path_canonical)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Global lookup index: an overlap check is "is any other library's
-- canonical root a prefix of this one or vice-versa?" — answered by
-- scanning this index.
CREATE INDEX IF NOT EXISTS library_roots_canonical_idx
    ON library_roots (path_canonical);
-- +goose StatementEnd

-- +goose StatementBegin
-- Backfill from the transitional libraries.roots array. SQLite stores
-- the array as a JSON-encoded string (per dbtypes.go's stringArray),
-- so json_each gives us the elements.
INSERT INTO library_roots (id, library_id, path, path_canonical)
SELECT
    lower(hex(randomblob(16))),
    l.id,
    j.value,
    j.value
FROM libraries l, json_each(l.roots) j
WHERE NOT EXISTS (
    SELECT 1 FROM library_roots r
    WHERE r.library_id = l.id AND r.path_canonical = j.value
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS library_roots_canonical_idx;
DROP TABLE IF EXISTS library_roots;
-- +goose StatementEnd
