-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0033 (Epic 7 / Story 7.14) — manual + smart collections.
--
-- ``is_smart=false`` collections store explicit (video_id, position)
-- rows in ``collection_items``. ``is_smart=true`` collections compute
-- their items live from ``smart_query`` JSON (same shape as search
-- filters) and ``collection_items`` is empty.
--
CREATE TABLE IF NOT EXISTS collections (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id  UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name        TEXT         NOT NULL,
    description TEXT,
    is_smart    BOOLEAN      NOT NULL DEFAULT false,
    smart_query JSONB,
    created_by  UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (library_id, name)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS collection_items (
    collection_id UUID NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    video_id      UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    position      INTEGER NOT NULL DEFAULT 0,
    added_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, video_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS collection_items_position_idx
    ON collection_items (collection_id, position);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS collection_items_position_idx;
DROP TABLE IF EXISTS collection_items;
DROP TABLE IF EXISTS collections;
-- +goose StatementEnd
