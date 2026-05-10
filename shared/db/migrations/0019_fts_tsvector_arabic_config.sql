-- +goose Up
-- +goose StatementBegin
--
-- Slot 0019 (Story 5.2 / plan-05-02) — Arabic-aware FTS configuration.
--
-- Provides:
--   - `maktaba_normalize(text)` — symmetric pre-tokenization normalize
--     applied on both the indexed text (slot 0021's tsv generated column)
--     and the query path. Strips combining marks, unifies alef variants,
--     normalizes ya/taa-marbuta. Pure SQL (no extensions required).
--   - `language_to_regconfig(lang)` — maps ISO 639-1 to a Postgres text
--     search config (`'public.arabic_simple'` for `ar`, `'english'` for
--     `en`, `'simple'` otherwise). Stable, IMMUTABLE, can be inlined into
--     a STORED generated column.
--   - `arabic_simple` text-search config — copy of `simple` with a
--     dedicated name so language-aware queries can use it explicitly.
--
CREATE EXTENSION IF NOT EXISTS pg_trgm;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION maktaba_normalize(t TEXT) RETURNS TEXT
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
    -- 1) lowercase (Arabic has no case but Latin does).
    -- 2) strip combining marks (tashkeel: U+064B..U+0652, U+0670, U+0640
    --    tatweel) using regexp.
    -- 3) unify alef variants (إ أ آ ٱ → ا).
    -- 4) ya variants (ى → ي), taa marbuta → ha (ة → ه).
    -- 5) collapse whitespace.
    SELECT regexp_replace(
        translate(
            regexp_replace(
                lower(t),
                '[ً-ْٰـ]',
                '',
                'g'
            ),
            'إأآٱىة',
            'اااايه'
        ),
        '\s+', ' ', 'g'
    );
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_ts_config WHERE cfgname = 'arabic_simple'
    ) THEN
        CREATE TEXT SEARCH CONFIGURATION arabic_simple ( COPY = simple );
    END IF;
END$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION language_to_regconfig(lang TEXT) RETURNS regconfig
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
    SELECT CASE
        WHEN lang = 'ar' THEN 'public.arabic_simple'::regconfig
        WHEN lang = 'en' THEN 'pg_catalog.english'::regconfig
        WHEN lang = 'simple' THEN 'pg_catalog.simple'::regconfig
        ELSE 'pg_catalog.simple'::regconfig
    END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS language_to_regconfig(TEXT);
-- +goose StatementEnd

-- +goose StatementBegin
DROP TEXT SEARCH CONFIGURATION IF EXISTS arabic_simple;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS maktaba_normalize(TEXT);
-- +goose StatementEnd
