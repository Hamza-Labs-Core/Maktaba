-- +goose Up
-- +goose StatementBegin
-- Slot 0235 (Epic 49 story 49.8): keyword search's terms dictionary, as on Postgres (one row
-- per library and term: the key, a spelling as written, the number of documents holding it),
-- and whether a document's words are counted in it. Prefix lookups use the primary key; SQLite
-- has no trigram index, and did-you-mean works out similarity in the API.
CREATE TABLE IF NOT EXISTS search_terms (
    library_id TEXT    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    term       TEXT    NOT NULL,
    shown      TEXT    NOT NULL,
    df         INTEGER NOT NULL,
    PRIMARY KEY (library_id, term)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS search_terms_term ON search_terms (term);
-- +goose StatementEnd

-- +goose StatementBegin
-- the named speakers, per library, by each word start of the name (as on Postgres)
CREATE TABLE IF NOT EXISTS search_speakers (
    library_id TEXT    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    key        TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    df         INTEGER NOT NULL,
    PRIMARY KEY (library_id, key, name)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS search_speakers_key ON search_speakers (key);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS speakers_name_idx ON speakers (name);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE video_search_docs ADD COLUMN IF NOT EXISTS terms_counted INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
-- the keyword_index stage is at version 2 (its documents' words counted: the graph's
-- KEYWORD_INDEX_VERSION), as a freshly migrated database's seed must match the graph. The
-- seed of slot 0233 again, at version 2 (a stage the graph sync already moved on keeps its
-- own): no row's data is lost on the way up, and the Down puts version 1 back, so a
-- rollback runs the Down scripts and keeps the database (updater's Reversible)
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('keyword_index', 'Keyword index', '["video","library"]', 'cpu', 'speech', 5, 2, 0, 0, 3, '[]', '[]', '[]', 0, 0, 0, 'auto', 'any', 1, NULL)
ON CONFLICT (stage) DO UPDATE SET version = 2 WHERE job_stages.version < 2;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 1 WHERE stage = 'keyword_index' AND version = 2;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE video_search_docs DROP COLUMN terms_counted;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS speakers_name_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS search_speakers;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS search_terms;
-- +goose StatementEnd
