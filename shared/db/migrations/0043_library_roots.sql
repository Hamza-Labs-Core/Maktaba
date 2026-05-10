-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0043 (Epic 9 / Story 9.16) — canonical library_roots store.
--
-- The transitional `libraries.roots TEXT[]` column from slot 0001 is
-- deprecated but kept transitionally for one release; this table is the
-- canonical normalized store (architecture §8.1). Each row is one root
-- path; `(library_id, path_canonical)` is unique. The canonical form is
-- computed by the Pipeline service (resolve symlinks, strip trailing
-- slashes, normalize `..`) before insert.
--
CREATE TABLE IF NOT EXISTS library_roots (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id      UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    path            TEXT         NOT NULL,
    path_canonical  TEXT         NOT NULL,
    added_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (library_id, path_canonical)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Global lookup index: an overlap check is "is any other library's
-- canonical root a prefix of this one or vice-versa?" — answered by
-- scanning this index. We don't enforce overlap as a UNIQUE because
-- prefix matching needs a range scan, not a uniqueness constraint.
CREATE INDEX CONCURRENTLY IF NOT EXISTS library_roots_canonical_idx
    ON library_roots (path_canonical);
-- +goose StatementEnd

-- +goose StatementBegin
-- Backfill from the transitional libraries.roots array.
INSERT INTO library_roots (library_id, path, path_canonical)
SELECT l.id, root, root
FROM libraries l, unnest(l.roots) AS root
ON CONFLICT (library_id, path_canonical) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS library_roots_canonical_idx;
DROP TABLE IF EXISTS library_roots;
-- +goose StatementEnd
