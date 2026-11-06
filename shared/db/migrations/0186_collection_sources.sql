-- +goose Up
-- +goose StatementBegin
--
-- Slot 0186 (Epic 56, stories 56.06, 56.07, 56.08) - collections kept in step by a source,
-- and how a library shows its collections.
--
-- collections:
--   source_kind      'youtube_playlist', 'youtube_channel' or 'plex' for a collection a
--                    pipeline stage keeps in step (youtube_collections, plex_collections_import);
--                    NULL for an admin's own and the older automatic ones
--   source_ref       the source's id: the playlist id, the channel id, '<plex server>:<rating key>'
--   membership_hash  the members the stage wrote last (an unchanged hash writes nothing)
--   name_override, hidden_at, show_on_library, show_on_home, sort_title
--                    an admin's choices, kept across the stage's runs: a name, hidden for
--                    everyone, a row on the library's Recommended tab, a row on Home, the
--                    order key of the Collections tab
-- One collection per (library, source_kind, source_ref).
--
-- libraries.collection_mode: 'tab_only' (collections only in their tab, the default),
-- 'hide_items' (the library grid shows a collection's card in place of its videos) or
-- 'show_both'.
--
-- Additive.
--
ALTER TABLE collections ADD COLUMN IF NOT EXISTS source_kind TEXT
    CHECK (source_kind IN ('youtube_playlist', 'youtube_channel', 'plex'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS source_ref TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS membership_hash TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS name_override TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS hidden_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS show_on_library BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS show_on_home BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS sort_title TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS collections_source_uidx
    ON collections (library_id, source_kind, source_ref) WHERE source_kind IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries ADD COLUMN IF NOT EXISTS collection_mode TEXT NOT NULL DEFAULT 'tab_only'
    CHECK (collection_mode IN ('tab_only', 'hide_items', 'show_both'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE libraries DROP COLUMN IF EXISTS collection_mode;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS collections_source_uidx;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE collections
    DROP COLUMN IF EXISTS sort_title,
    DROP COLUMN IF EXISTS show_on_home,
    DROP COLUMN IF EXISTS show_on_library,
    DROP COLUMN IF EXISTS hidden_at,
    DROP COLUMN IF EXISTS name_override,
    DROP COLUMN IF EXISTS membership_hash,
    DROP COLUMN IF EXISTS source_ref,
    DROP COLUMN IF EXISTS source_kind;
-- +goose StatementEnd
