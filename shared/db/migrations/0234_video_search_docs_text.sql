-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0234 (Epic 49 story 49.7): keyword search's documents get their normalised text as
-- columns, and the BM25 indexes are built on those columns.
--
-- A BM25 query's ORDER BY ... LIMIT k works its score out again for every row it returns
-- (the index hands the rows back in order, and the executor checks the order). Over slot
-- 0232's expression indexes, that meant normalising title, speakers and body, three arrays,
-- for each of the k rows: about 0.6 ms a row, 285 ms for the top 500 of a 45,000-document
-- library. Over a stored column it is the column, already normalised: 0.15 to 0.25 ms a row.
--
--   search_text  maktaba_normalize(title, speakers and body, a line each)     maktaba_search
--   stem_text    maktaba_stem_prepare(the same)                               maktaba_stem
--
-- Both are generated (STORED): whatever writes title, speakers or body, the columns follow.
-- Adding them rewrites video_search_docs once, here (one row a video). The indexes are
-- video_search_docs_bm25_text and video_search_docs_bm25_stem_text, and
-- maktaba_search_index_statements() now builds those: it drops slot 0232's two expression
-- indexes once they are no longer needed, and the API builds the new ones after the start,
-- as before (api/internal/searchindex).
--
ALTER TABLE video_search_docs
    ADD COLUMN IF NOT EXISTS search_text text GENERATED ALWAYS AS
        (public.maktaba_normalize(title || E'\n' || speakers || E'\n' || body)) STORED,
    ADD COLUMN IF NOT EXISTS stem_text text GENERATED ALWAYS AS
        (public.maktaba_stem_prepare(title || E'\n' || speakers || E'\n' || body)) STORED;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION maktaba_search_index_statements() RETURNS SETOF text
LANGUAGE plpgsql STABLE SET search_path = public, pg_temp AS $f$
DECLARE
    i record;
    valid boolean;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_textsearch') THEN
        RETURN;
    END IF;
    FOR i IN SELECT * FROM (VALUES
        ('video_search_docs_bm25_text', 'search_text', 'public.maktaba_search'),
        ('video_search_docs_bm25_stem_text', 'stem_text', 'public.maktaba_stem')
    ) AS v(name, col, cfg) LOOP
        SELECT x.indisvalid INTO valid FROM pg_index x
         WHERE x.indexrelid = to_regclass('public.' || i.name);
        CONTINUE WHEN valid;
        IF valid IS NOT NULL THEN -- a build that did not finish: made again
            RETURN NEXT format('DROP INDEX CONCURRENTLY IF EXISTS public.%I', i.name);
        END IF;
        RETURN NEXT format('CREATE INDEX CONCURRENTLY IF NOT EXISTS %I ON public.video_search_docs USING bm25 '
            '(%I) WITH (text_config = %L)', i.name, i.col, i.cfg);
    END LOOP;
    -- slot 0232's expression indexes: nothing queries them any more
    FOR i IN SELECT c.relname AS name FROM pg_class c
              WHERE c.relname IN ('video_search_docs_bm25', 'video_search_docs_bm25_stem')
                AND c.relnamespace = 'public'::regnamespace AND c.relkind = 'i' LOOP
        RETURN NEXT format('DROP INDEX CONCURRENTLY IF EXISTS public.%I', i.name);
    END LOOP;
END
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- slot 0232's function (its expression indexes)
CREATE OR REPLACE FUNCTION maktaba_search_index_statements() RETURNS SETOF text
LANGUAGE plpgsql STABLE SET search_path = public, pg_temp AS $f$
DECLARE
    i record;
    valid boolean;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_textsearch') THEN
        RETURN;
    END IF;
    FOR i IN SELECT * FROM (VALUES
        ('video_search_docs_bm25', 'public.maktaba_normalize', 'public.maktaba_search'),
        ('video_search_docs_bm25_stem', 'public.maktaba_stem_prepare', 'public.maktaba_stem')
    ) AS v(name, fn, cfg) LOOP
        SELECT x.indisvalid INTO valid FROM pg_index x
         WHERE x.indexrelid = to_regclass('public.' || i.name);
        CONTINUE WHEN valid;
        IF valid IS NOT NULL THEN -- a build that did not finish: made again
            RETURN NEXT format('DROP INDEX CONCURRENTLY IF EXISTS public.%I', i.name);
        END IF;
        RETURN NEXT format('CREATE INDEX CONCURRENTLY IF NOT EXISTS %I ON public.video_search_docs USING bm25 '
            '((ARRAY[%s(title), %2$s(speakers), %2$s(body)])) WITH (text_config = %L)', i.name, i.fn, i.cfg);
    END LOOP;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS video_search_docs_bm25_text;
DROP INDEX IF EXISTS video_search_docs_bm25_stem_text;
ALTER TABLE video_search_docs DROP COLUMN IF EXISTS stem_text, DROP COLUMN IF EXISTS search_text;
-- +goose StatementEnd
