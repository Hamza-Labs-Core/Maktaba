-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0188 - word review, part 3 (Epic 52, story 52.10): every fix teaches.
--
-- 1. vocabulary: the words, names and terms a home admin taught (Mark as name, Add to
--    dictionary, a correction made three times, the Vocabulary page), per library or for
--    the whole home, per language, with the pack's folded key. word_check counts a
--    vocabulary key as known; new transcriptions get the names in a derived prompt that
--    is never part of transcribe's fingerprint.
-- 2. vocab_pairs: "the transcript said X, the person meant Y", counted.
--
-- Additive: two tables, one index.
--
CREATE TABLE IF NOT EXISTS vocabulary (
    id         UUID        PRIMARY KEY,
    scope      TEXT        NOT NULL CHECK (scope IN ('home', 'library')),
    library_id UUID        REFERENCES libraries(id) ON DELETE CASCADE,
    language   TEXT        NOT NULL,
    term       TEXT        NOT NULL,
    key        TEXT        NOT NULL,
    kind       TEXT        NOT NULL DEFAULT 'word' CHECK (kind IN ('word', 'name', 'term')),
    variants   TEXT[]      NOT NULL DEFAULT '{}',
    source     TEXT        NOT NULL DEFAULT 'manual' CHECK (source IN ('review', 'manual', 'import')),
    uses       INTEGER     NOT NULL DEFAULT 0,
    added_by   UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- the library, or '' for the home: what the uniqueness reads (NULLs never collide)
    owner      TEXT        GENERATED ALWAYS AS (COALESCE(library_id::text, '')) STORED,
    CHECK ((scope = 'home') = (library_id IS NULL)),
    UNIQUE (scope, owner, language, key, term)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS vocabulary_key_idx ON vocabulary (key, language);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS vocab_pairs (
    id           UUID    PRIMARY KEY,
    library_id   UUID    REFERENCES libraries(id) ON DELETE CASCADE,
    language     TEXT    NOT NULL,
    heard        TEXT    NOT NULL,
    heard_key    TEXT    NOT NULL,
    meant        TEXT    NOT NULL,
    uses         INTEGER NOT NULL DEFAULT 0,
    last_edit_id UUID,
    owner        TEXT    GENERATED ALWAYS AS (COALESCE(library_id::text, '')) STORED,
    UNIQUE (owner, language, heard_key, meant)
);
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- word_check and transcribe read it on any machine
        GRANT SELECT ON vocabulary TO maktaba_worker;
        GRANT SELECT ON vocab_pairs TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS vocab_pairs;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS vocabulary;
-- +goose StatementEnd
