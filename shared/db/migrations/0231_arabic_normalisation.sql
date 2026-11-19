-- +goose Up
--
-- Slot 0231 (Epic 49 story 49.4): Arabic normalisation and word forms for keyword search.
--
-- public.maktaba_normalize(text) folds the spellings one Arabic word is written in to one form:
-- harakat (U+064B-U+0652, U+0670) and tatweel (U+0640) go; أ إ آ ٱ become ا, ى becomes ي,
-- ة becomes ه, ؤ becomes و, ئ becomes ي; Arabic-Indic and Persian digits become ASCII. Latin
-- keeps the unaccent step of maktaba_search (slot 0016). One translate(): no file in the
-- server's tsearch_data (a rules file would have to reach every Postgres the migrations run
-- on: the bundles, Docker, CI's service containers, an operator's own server).
--
-- maktaba_stem is the word-forms configuration: the same normalisation, then the Snowball
-- stemmers built into PostgreSQL (arabic, english). public.maktaba_stem_prepare() strips first what
-- the Arabic stemmer leaves: the prefixes وال بال فال كال لل, and the imperfect tense's
-- ي ت ن of a three-letter root (a four-letter word, or one with ون ين ان وا after the root).
--
-- maktaba_tsv / maktaba_tsv_stem and maktaba_query / maktaba_query_stem are the two sides of
-- a match, IMMUTABLE so an index or a generated column may use them (slot 0232's documents,
-- story 49.5). Nothing here rewrites a table: transcript_segments.search_tsv (slot 0016)
-- stays as it is for the line path until story 49.13; the passages of story 49.7 match their
-- lines on the fly, bounded by the videos found (migrations README, rules 4 and 5).
--
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION maktaba_normalize(t text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $f$
  SELECT translate(t,
    'أإآٱىةؤئ٠١٢٣٤٥٦٧٨٩۰۱۲۳۴۵۶۷۸۹' || 'ًٌٍَُِّْٰـ',
    'اااايهوي01234567890123456789')
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION maktaba_stem_prepare(t text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $f$
  SELECT regexp_replace(
           regexp_replace(public.maktaba_normalize(t), '(^|\s)(وال|بال|فال|كال|لل)(?=[ء-ي]{2})', '\1', 'g'),
           '(^|\s)[يتن](?=[ء-ي]{3}(ون|ين|ان|وا)?(\s|$))', '\1', 'g')
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_ts_dict WHERE dictname = 'maktaba_arabic_stem') THEN
        EXECUTE 'CREATE TEXT SEARCH DICTIONARY maktaba_arabic_stem (TEMPLATE = snowball, Language = arabic)';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_ts_config WHERE cfgname = 'maktaba_stem') THEN
        EXECUTE 'CREATE TEXT SEARCH CONFIGURATION maktaba_stem ( COPY = simple )';
        EXECUTE 'ALTER TEXT SEARCH CONFIGURATION maktaba_stem '
             || 'ALTER MAPPING FOR asciiword, asciihword, hword_asciipart WITH english_stem';
        EXECUTE 'ALTER TEXT SEARCH CONFIGURATION maktaba_stem '
             || 'ALTER MAPPING FOR word, hword, hword_part WITH unaccent, maktaba_arabic_stem';
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION maktaba_tsv(t text) RETURNS tsvector
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $f$
  SELECT to_tsvector('public.maktaba_search'::regconfig, public.maktaba_normalize(coalesce(t, '')))
$f$;
CREATE OR REPLACE FUNCTION maktaba_tsv_stem(t text) RETURNS tsvector
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $f$
  SELECT to_tsvector('public.maktaba_stem'::regconfig, public.maktaba_stem_prepare(coalesce(t, '')))
$f$;
CREATE OR REPLACE FUNCTION maktaba_query(q text) RETURNS tsquery
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $f$
  SELECT websearch_to_tsquery('public.maktaba_search'::regconfig, public.maktaba_normalize(coalesce(q, '')))
$f$;
CREATE OR REPLACE FUNCTION maktaba_query_stem(q text) RETURNS tsquery
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $f$
  SELECT websearch_to_tsquery('public.maktaba_stem'::regconfig, public.maktaba_stem_prepare(coalesce(q, '')))
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS maktaba_query_stem(text);
DROP FUNCTION IF EXISTS maktaba_query(text);
DROP FUNCTION IF EXISTS maktaba_tsv_stem(text);
DROP FUNCTION IF EXISTS maktaba_tsv(text);
DROP TEXT SEARCH CONFIGURATION IF EXISTS maktaba_stem;
DROP TEXT SEARCH DICTIONARY IF EXISTS maktaba_arabic_stem;
DROP FUNCTION IF EXISTS public.maktaba_stem_prepare(text);
DROP FUNCTION IF EXISTS public.maktaba_normalize(text);
-- +goose StatementEnd
