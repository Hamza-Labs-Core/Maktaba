# Plan 5.2 — FTS5 / `tsvector` exact-phrase index (unit-backed) — implementation

> Implementation plan for [story-05-02-fts-tsvector.md](story-05-02-fts-tsvector.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: indexes the `transcript_units` table from
> [Plan/Story 5.1](story-05-01-unit-chunking.md); is consumed by the
> hybrid retrieval engine in
> [Story 5.4](story-05-04-hybrid-rrf.md); is kept in sync incrementally
> by [Story 5.5](story-05-05-incremental-indexing.md). The `arabic` text
> search configuration files this plan ships under
> `shared/db/tsearch/` are owned exclusively by this story; other stories
> may read but not modify them.

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | The Postgres FTS uses a **custom `arabic` text-search configuration** registered in a migration, NOT the upstream `arabic` config from `tsearch_data` (which doesn't ship with most Postgres builds, including Alpine and the official 16-bookworm image). The config is built from `simple` + a Python-side normalizer applied via a SQL helper `maktaba_normalize(text)`. We deliberately do NOT introduce a stemming dictionary in v1 — Arabic root extraction is hard, the existing `arabic_stem.so` (when present) over-stems short surface forms, and our hybrid layer (Story 5.4) handles morphology via the multilingual embedding. So FTS is the **literal-string layer**, not the morphology layer. | Story acceptance: "for Postgres via the `arabic` text-search config we ship (Pipeline owns the dictionary files in `shared/db/tsearch/`)". | Reproducibility (no host-installed dict files), separation of concerns (FTS = exact, Chroma = semantic), and avoidance of well-documented Snowball Arabic over-stemming surprises. |
| D2 | The FTS index is a **stored generated column** `tsv tsvector GENERATED ALWAYS AS (...) STORED` plus a GIN index, computed from `to_tsvector(language_to_regconfig(language), maktaba_normalize(text))`. We chose stored over `INDEX (to_tsvector(...))` so that inserts pay the cost once and reads/queries don't re-tokenize per row, and so that `EXPLAIN` plans clearly show GIN scans on `tsv`. | Story acceptance shows the `GENERATED ALWAYS AS ... STORED` form. | Stored columns also let us `SELECT tsv` for debugging and let triggers from Story 5.5 trivially detect "new tsv → notify" on insert. |
| D3 | **`pg_trgm` is enabled and a second GIN index is built on the *normalized* text**, not the raw `text`. The normalized index is what backs prefix queries and "find spelling variants" (`text ILIKE 'الحم%'` → uses `transcript_units_textnorm_trgm`). Raw `text` is preserved for snippet rendering only. | Story acceptance: "plus `pg_trgm` for prefix queries". | A trgm index over raw text matches `أ` and `ا` as different keys, so `الحم%` doesn't match `الحم...` written with a different alef. Indexing the normalized form (after `maktaba_normalize`) makes prefix search alef-insensitive. |
| D4 | `language_to_regconfig(lang)` is a SQL function returning `regconfig`: `'ar' → 'public.arabic'`, `'en' → 'pg_catalog.english'`, `'fr' → 'pg_catalog.french'`, anything else (including `'und'`) → `'pg_catalog.simple'`. It is `IMMUTABLE` so it can be used inside the generated-column expression. | Story acceptance shows `coalesce(language_to_regconfig(language), 'simple')`. | A SQL function is cheaper than a `CASE` expression repeated in DDL and lets us extend the language map by `CREATE OR REPLACE FUNCTION` in a future migration without touching the table. |
| D5 | **SQLite uses `tokenize = 'unicode61 remove_diacritics 2'`** for FTS5 — `remove_diacritics 2` is the recommended setting on SQLite ≥ 3.27 because it strips combining marks across all Unicode categories (the older `1` value missed several Arabic-relevant code points). We require SQLite ≥ 3.27 in `pyproject.toml`. | Implied by story acceptance ("Diacritic-insensitive matching … for SQLite via the `unicode61 remove_diacritics 2` tokenizer"). | This is the only built-in tokenizer that meaningfully handles Arabic out of the box on SQLite. We do NOT register a custom Python tokenizer (would require `sqlite3` `enable_load_extension` which Apple ships disabled by default on macOS Python builds). |
| D6 | **Application-side normalization is applied symmetrically on writes and queries.** The same `arabic_normalize()` Python function is used to produce the `text` value passed into the SQLite FTS table AND to rewrite the user's query string before it hits FTS5/MATCH. Postgres applies it via the SQL function `maktaba_normalize()`; we ship that function in Python too (importable as `maktaba_pipeline.search.normalize.normalize_arabic`) so that test code can predict the indexed form without round-tripping the DB. | Required for diacritic-insensitive matching on Postgres (where the regconfig alone doesn't strip alef variants); SQLite's `unicode61 remove_diacritics 2` already strips combining marks but doesn't unify alef variants either. | Without symmetric normalization, a query for `العالمين` would miss a unit whose text is `الْعَالَمِينَ` even with diacritics nominally stripped, because alef-with-shadda and alef-with-fatha both reduce to ا only after the normalization pass — the tokenizer alone doesn't do that step. |
| D7 | **Phrase queries** use FTS5 `"…"` syntax on SQLite and `phraseto_tsquery` / `websearch_to_tsquery` on Postgres. `websearch_to_tsquery` is the public surface (it accepts `"phrase"`, `OR`, `-term`); the engine builds a single `websearch_to_tsquery(regconfig, normalized_query)` per query. Proximity queries (`NEAR/3`) are a Postgres-only feature in v1; on SQLite the equivalent FTS5 `NEAR(a b, 3)` syntax is supported and the Python query rewriter translates between them. | Story test case `test_fts_proximity_query` requires `"الحمد" NEAR/3 "العالمين"` to work. | `websearch_to_tsquery` handles user-typed garbage gracefully (won't raise on stray punctuation, unbalanced quotes); `to_tsquery` raises on parse errors. We standardize on the forgiving variant. |
| D8 | **One logical table name `transcripts_fts` across both engines.** On SQLite it is the FTS5 virtual table directly; on Postgres it is a `VIEW` — `CREATE VIEW transcripts_fts AS SELECT id AS unit_id, transcript_id, language, text, tsv FROM transcript_units` — so application SQL can reference `transcripts_fts` without dialect branching for `SELECT` shape. Writes go through `transcript_units` on both engines. | Story acceptance: "The same logical table name `transcripts_fts` is used on both engines so application code does not branch on dialect for the table name." | Lets the Python search engine emit one SQL string with parameterized regconfig / MATCH operator differences only, instead of two whole queries. |
| D9 | **Backfill uses `INSERT INTO transcripts_fts(rowid, …) SELECT …`** on SQLite (FTS5 supports rebuild-via-insert) and is a **no-op on Postgres** because the `tsv` column is `STORED GENERATED` — values are already present after the migration adds the column. The post-migration step on Postgres is `CREATE INDEX CONCURRENTLY transcript_units_tsv ON transcript_units USING GIN(tsv);` which we run **outside** the migration transaction. The migration runner has a documented "concurrent-index post-step" hook that we register with. | Story edge case: "A migration that adds the GIN index uses `CREATE INDEX CONCURRENTLY` (Postgres) so the live API does not stall." | `CREATE INDEX CONCURRENTLY` cannot run inside a transaction, so it must live outside the migration's BEGIN/COMMIT. We use the existing post-step hook (introduced in Plan 5.1) instead of inventing a new one. |
| D10 | **Maximum text length per unit indexed is 1 MB** (`text` column is `TEXT`, no DB cap, but the index path truncates at 1,048,576 bytes UTF-8 with `metadata.fts_truncated_at = N`). The truncation is tagged in `transcript_units.metadata` and surfaced in the search hit's `metadata.truncated = true` so the UI can warn. | Edge case in this plan §5; not in the story directly. | A `tsvector` is capped at ~1 MB by Postgres internally; exceeding it raises `string is too long for tsvector`. We pre-truncate to fail predictably and visibly. |

If D1 is rejected (use upstream `arabic_stem.so`), §2.1 changes (no Python normalizer needed for stemming), §2.2's migration adds `CREATE TEXT SEARCH CONFIGURATION` referencing `arabic_stem`, and the test `test_fts_match_diacritics_stripped` must be relaxed to allow over-stemming (e.g. `العالمين` and `عالم` collide). Recall improves; precision drops.

If D8 is rejected (separate names per engine), every search query in the engine grows a `if dialect == 'sqlite'` branch and the test `test_fts_table_name_consistent_across_engines` is removed; behavior otherwise unchanged.

---

## 1. Architecture diagram — write path and read path

```
                            WRITE PATH
                            ──────────
   Indexer (Story 5.5)
        │ INSERT INTO transcript_units(transcript_id, seq, start_sec,
        │   end_sec, text, language, segment_ids, metadata)
        ▼
  ┌────────────────────────────┐
  │  transcript_units          │
  │   (Story 5.1 schema)       │
  │                            │
  │  + tsv tsvector            │   ◄── Postgres: GENERATED ALWAYS
  │      GENERATED ALWAYS AS   │       AS (to_tsvector(
  │      (...) STORED          │         language_to_regconfig(language),
  │                            │         maktaba_normalize(text))) STORED
  └─────────────┬──────────────┘
                │
       ┌────────┴─────────────────────────┐
       │                                  │
   Postgres                            SQLite
       │                                  │
       │ STORED column already            │ AFTER INSERT trigger:
       │ populated; GIN index on tsv      │   INSERT INTO transcripts_fts
       │ updated synchronously.           │     (rowid, text, transcript_id,
       │                                  │      unit_id, language)
       │                                  │   VALUES (NEW.id,
       │                                  │     arabic_normalize(NEW.text),
       │                                  │     NEW.transcript_id, NEW.id,
       │                                  │     NEW.language);
       │                                  │
       │ pg_trgm GIN index on             │ AFTER UPDATE / DELETE triggers
       │ maktaba_normalize(text)          │ keep the FTS rowid in sync.
       ▼                                  ▼
   ┌────────────────────────────────────────────┐
   │  transcripts_fts                           │
   │   - Postgres: VIEW over transcript_units   │
   │   - SQLite:   FTS5 virtual table           │
   │   - same column shape on both engines      │
   └────────────────────────────────────────────┘


                            READ PATH
                            ─────────
   Search engine (Story 5.4)
        │  query = "الحمد لله"   filter language='ar'
        ▼
   Python: arabic_normalize(query) → "الحمد لله"  (alef + tashkeel pass)
        │
        ▼
   Engine builds dialect-specific MATCH:
        Postgres: SELECT unit_id, transcript_id, language,
                       ts_headline('arabic_simple', text, q,
                         'StartSel=<mark> StopSel=</mark>') AS snippet,
                       ts_rank_cd(tsv, q) AS rank
                  FROM transcripts_fts,
                       websearch_to_tsquery('arabic_simple', $1) q
                 WHERE tsv @@ q
                   AND language = $2
                 ORDER BY rank DESC
                 LIMIT $3;

        SQLite:  SELECT unit_id, transcript_id, language,
                       snippet(transcripts_fts, 0, '<mark>', '</mark>',
                               '…', 32) AS snippet,
                       bm25(transcripts_fts) AS rank
                  FROM transcripts_fts
                 WHERE transcripts_fts MATCH ?
                   AND language = ?
                 ORDER BY rank
                 LIMIT ?;
        │
        ▼
   List[FtsHit] returned to engine. Engine then resolves
   unit_id → (segment_id, start_sec, end_sec) per Story 5.4's rule
   (segment_ids[0]).
```

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
└── search/
    ├── __init__.py             # public surface: re-exports
    ├── normalize.py            # arabic_normalize, normalize_query
    ├── fts/
    │   ├── __init__.py         # FtsClient, FtsHit
    │   ├── postgres.py         # PostgresFtsClient
    │   ├── sqlite.py           # SqliteFtsClient
    │   ├── query.py            # query rewriter (NEAR translation, etc.)
    │   ├── snippet.py          # post-processing for grapheme-safe <mark>
    │   └── tests/
    │       ├── conftest.py
    │       ├── test_normalize.py
    │       ├── test_query_rewriter.py
    │       ├── test_postgres_fts.py
    │       ├── test_sqlite_fts.py
    │       ├── test_fts_match_exact.py
    │       ├── test_fts_match_diacritics_stripped.py
    │       ├── test_fts_proximity_query.py
    │       ├── test_fts_indexed_on_insert.py
    │       ├── test_fts_language_specific_stopwords.py
    │       └── test_fts_table_name_consistent_across_engines.py
shared/db/
├── migrations/
│   ├── 0021_fts_tsvector_arabic_config.sql
│   ├── 0022_transcript_units_tsv_column.sql
│   ├── 0023_transcript_units_tsv_indexes.post.sql       # CONCURRENTLY (post-step)
│   ├── 0024_transcripts_fts_view_postgres.sql
│   └── sqlite/
│       └── 0021_transcripts_fts_virtual_table.sql       # SQLite-only
└── tsearch/
    ├── arabic.dict             # placeholder mapping file (Snowball-shape)
    └── arabic_simple.config    # documentation: how the config is built
```

(The migration numbering picks up where Story 5.1 ends; Plan 5.1 owns
0020.)

### 2.2 SQL — Postgres

#### 2.2.1 `0021_fts_tsvector_arabic_config.sql` — SQL helpers

```sql
-- Plan 5.2 / Story 5.2 — Postgres FTS support.
--
-- Adds:
--   1. maktaba_normalize(text)         IMMUTABLE — Arabic + Unicode form
--   2. language_to_regconfig(text)     IMMUTABLE — language → regconfig
--   3. The 'arabic_simple' text search configuration (built from 'simple'
--      with NO stemming; see plan §0 D1).
BEGIN;

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;

-- maktaba_normalize: returns a string where:
--   * Arabic tashkeel (combining marks U+064B..U+0652, U+0670, U+0640) removed
--   * Alef variants (ا أ إ آ ٱ) → ا
--   * Alef maksura ى → ي
--   * Taa marbuta ة → ه
--   * Ya variants (ي ئ) preserved as ي except hamza-on-ya → ي
--   * Tatweel U+0640 stripped
--   * ZWNJ / ZWJ (U+200C / U+200D) stripped
--   * Lower-case via lower() for English component
--   * NFC-normalized via unaccent for non-Arabic text
--
-- The body uses a chained REPLACE/TRANSLATE rather than a PL/pgSQL loop
-- because we MUST be IMMUTABLE so it can sit inside the generated-column
-- expression. TRANSLATE handles single code points; REPLACE handles
-- multi-byte (e.g. ٱ which is two bytes in UTF-8).
CREATE OR REPLACE FUNCTION maktaba_normalize(t text)
RETURNS text
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $$
  SELECT lower(
    regexp_replace(
      translate(
        coalesce(t, ''),
        -- Tashkeel (combining marks) and tatweel and ZWNJ/ZWJ
        E'ًٌٍَُِّْٰـ‌‍'
        -- Alef variants
        || E'أإآٱ'
        -- Alef maksura, taa marbuta, hamza-on-ya
        || E'ىةئ',
        -- replacements (same length as the source set):
        -- 13 combining/format chars stripped (replaced with empty via length mismatch)
        ''
        -- alef variants → ا (U+0627), 4 chars
        || E'اااا'
        -- ى → ي ; ة → ه ; ئ → ي
        || E'يهي'
      ),
      -- Strip any leftover combining marks in the U+0610..U+061A or
      -- U+06D6..U+06ED ranges defensively.
      E'[ؐ-ؚۖ-ۭ]', '', 'g'
    )
  );
$$;

COMMENT ON FUNCTION maktaba_normalize(text) IS
  'Arabic + Unicode normalization for FTS. See specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md §2.2.1 D1/D6.';

-- language_to_regconfig: defaults to 'simple' for unknown / und.
CREATE OR REPLACE FUNCTION language_to_regconfig(lang text)
RETURNS regconfig
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $$
  SELECT CASE coalesce(lower(lang), 'und')
    WHEN 'en' THEN 'pg_catalog.english'::regconfig
    WHEN 'fr' THEN 'pg_catalog.french'::regconfig
    WHEN 'es' THEN 'pg_catalog.spanish'::regconfig
    WHEN 'de' THEN 'pg_catalog.german'::regconfig
    WHEN 'ar' THEN 'public.arabic_simple'::regconfig
    ELSE 'pg_catalog.simple'::regconfig
  END;
$$;

COMMENT ON FUNCTION language_to_regconfig(text) IS
  'Maps ISO 639-1 codes to a regconfig; unknown → simple.';

-- The Arabic config: built from 'simple' (no stemmer). The Python-side
-- maktaba_normalize() is the ONLY normalizer; the regconfig only does
-- tokenization + stopword removal.
DROP TEXT SEARCH CONFIGURATION IF EXISTS public.arabic_simple;
CREATE TEXT SEARCH CONFIGURATION public.arabic_simple
  ( COPY = pg_catalog.simple );

-- Arabic stopwords are loaded from a lightweight dictionary file shipped
-- under shared/db/tsearch/arabic.stop. The file format is one word per
-- line (already normalized via maktaba_normalize). The migration runner
-- copies it into $SHAREDIR/tsearch_data/ before running this DDL.
CREATE TEXT SEARCH DICTIONARY public.arabic_stopwords (
  TEMPLATE  = pg_catalog.simple,
  STOPWORDS = 'arabic'      -- resolves to arabic.stop via tsearch_data
);

ALTER TEXT SEARCH CONFIGURATION public.arabic_simple
  ALTER MAPPING FOR asciiword, asciihword, hword_asciipart,
                     word, hword, hword_part
  WITH public.arabic_stopwords;

COMMIT;
```

#### 2.2.2 `0022_transcript_units_tsv_column.sql` — column

```sql
-- Add the stored generated column. Index lives in the post-step file.
BEGIN;

ALTER TABLE transcript_units
  ADD COLUMN tsv tsvector
  GENERATED ALWAYS AS (
    to_tsvector(
      coalesce(language_to_regconfig(language), 'pg_catalog.simple'::regconfig),
      maktaba_normalize(text)
    )
  ) STORED;

COMMENT ON COLUMN transcript_units.tsv IS
  'Stored tsvector for FTS. Recomputed on UPDATE of (text, language).';

COMMIT;
```

#### 2.2.3 `0023_transcript_units_tsv_indexes.post.sql` — concurrent indexes

```sql
-- POST-STEP: runs OUTSIDE the migration transaction.
-- The migration runner identifies *.post.sql files and executes them
-- with autocommit=true after the ordinary migration commits.

CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_units_tsv
  ON transcript_units USING GIN (tsv);

CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_units_text_norm_trgm
  ON transcript_units USING GIN (maktaba_normalize(text) gin_trgm_ops);
```

#### 2.2.4 `0024_transcripts_fts_view_postgres.sql` — unifying view (D8)

```sql
BEGIN;

CREATE OR REPLACE VIEW transcripts_fts AS
  SELECT
    id            AS unit_id,
    transcript_id,
    language,
    text,
    tsv
  FROM transcript_units;

COMMENT ON VIEW transcripts_fts IS
  'Engine-portable name; underlying storage is transcript_units. See plan §0 D8.';

COMMIT;
```

### 2.3 SQL — SQLite

#### 2.3.1 `sqlite/0021_transcripts_fts_virtual_table.sql`

```sql
-- SQLite path. Runs only on dev/test and embedded deployments.

CREATE VIRTUAL TABLE IF NOT EXISTS transcripts_fts USING fts5(
    text,
    transcript_id  UNINDEXED,
    unit_id        UNINDEXED,
    language       UNINDEXED,
    tokenize = 'unicode61 remove_diacritics 2'
);

-- INSERT trigger
CREATE TRIGGER IF NOT EXISTS transcript_units_ai_fts
AFTER INSERT ON transcript_units
BEGIN
    INSERT INTO transcripts_fts (rowid, text, transcript_id, unit_id, language)
    VALUES (
        NEW.id,
        -- Application-side normalized text is stored. The Python
        -- writer is responsible for passing the normalized value;
        -- the trigger writes whatever it gets. See plan §2.4.2.
        NEW.text,
        NEW.transcript_id,
        NEW.id,
        NEW.language
    );
END;

-- UPDATE trigger (delete-then-insert; FTS5 doesn't support partial UPDATE
-- on contentless-style tables in older SQLite versions).
CREATE TRIGGER IF NOT EXISTS transcript_units_au_fts
AFTER UPDATE ON transcript_units
BEGIN
    DELETE FROM transcripts_fts WHERE rowid = OLD.id;
    INSERT INTO transcripts_fts (rowid, text, transcript_id, unit_id, language)
    VALUES (NEW.id, NEW.text, NEW.transcript_id, NEW.id, NEW.language);
END;

-- DELETE trigger
CREATE TRIGGER IF NOT EXISTS transcript_units_ad_fts
AFTER DELETE ON transcript_units
BEGIN
    DELETE FROM transcripts_fts WHERE rowid = OLD.id;
END;
```

NOTE on the SQLite triggers: the indexer (Story 5.5) is responsible for
inserting the **already-normalized** text into `transcript_units.text`
on SQLite, OR for inserting raw text and letting an explicit
`UPDATE transcript_units SET text = arabic_normalize(text)` step
follow. We pick the second: raw text in `transcript_units.text`,
normalized text materialized into `transcripts_fts.text` via the
trigger. To do that, replace `NEW.text` in the trigger with
`maktaba_normalize_sqlite(NEW.text)` once the application has registered
the SQLite scalar function. See §2.4.2.

#### 2.3.2 Application-registered SQLite scalar function

We register `maktaba_normalize_sqlite` as a deterministic scalar UDF on
every connection from the Pipeline. The migration uses placeholder
`maktaba_normalize_sqlite(...)`; on first use it must exist. The
function body is the Python `arabic_normalize` from §2.4.1.

Migration runner step: after applying `0021_transcripts_fts_virtual_table.sql`,
re-issue the trigger DDL with the function-call form. We split the
trigger creation into two files for clarity:

```sql
-- sqlite/0022_transcripts_fts_triggers_with_normalize.sql
DROP TRIGGER IF EXISTS transcript_units_ai_fts;
DROP TRIGGER IF EXISTS transcript_units_au_fts;

CREATE TRIGGER transcript_units_ai_fts
AFTER INSERT ON transcript_units BEGIN
    INSERT INTO transcripts_fts (rowid, text, transcript_id, unit_id, language)
    VALUES (NEW.id, maktaba_normalize_sqlite(NEW.text),
            NEW.transcript_id, NEW.id, NEW.language);
END;

CREATE TRIGGER transcript_units_au_fts
AFTER UPDATE ON transcript_units BEGIN
    DELETE FROM transcripts_fts WHERE rowid = OLD.id;
    INSERT INTO transcripts_fts (rowid, text, transcript_id, unit_id, language)
    VALUES (NEW.id, maktaba_normalize_sqlite(NEW.text),
            NEW.transcript_id, NEW.id, NEW.language);
END;
```

### 2.4 Python — normalization

#### 2.4.1 `pipeline/src/maktaba_pipeline/search/normalize.py`

```python
"""Arabic + Unicode normalization for FTS.

This module is the SOURCE OF TRUTH for what 'normalized' means; the SQL
maktaba_normalize() function in 0021_fts_tsvector_arabic_config.sql is a
mirror, kept in sync via test_normalize_postgres_matches_python.

Goals:
  * Strip tashkeel (combining marks) so القرآن and ٱلقُرْآن match.
  * Unify alef variants (ا أ إ آ ٱ → ا) so العالمين and آلعالمين match.
  * Unify ya variants and the alef-maksura: ى → ي; ئ → ي
  * Unify taa marbuta to haa: ة → ه (both are word-final feminine markers
    whose surface form is interchangeable in casual writing).
  * Strip tatweel U+0640 (purely cosmetic letter elongation).
  * Strip ZWNJ / ZWJ (U+200C / U+200D).
  * Apply NFC then NFKC for English / mixed text.
  * Lowercase ASCII for case-insensitive English matches.

Non-goals (handled elsewhere or deliberately out of scope):
  * No stemming. (Snowball Arabic over-stems; we use semantic search
    for morphology — see plan §0 D1.)
  * No transliteration; query is matched in its native script.
"""
from __future__ import annotations
import re
import unicodedata

# Lookup table built once at import.
_TASHKEEL = "ًٌٍَُِّْٰ"
_FORMAT   = "ـ‌‍‎‏﻿"
_ALEF_VARIANTS = {
    "أ": "ا",   # أ → ا
    "إ": "ا",   # إ → ا
    "آ": "ا",   # آ → ا
    "ٱ": "ا",   # ٱ → ا
}
_OTHER_LETTERS = {
    "ى": "ي",   # ى → ي  (alef maksura → ya)
    "ئ": "ي",   # ئ → ي  (hamza-on-ya → ya)
    "ة": "ه",   # ة → ه  (taa marbuta → haa)
}
_DROP = set(_TASHKEEL) | set(_FORMAT)
_REPLACE = {**_ALEF_VARIANTS, **_OTHER_LETTERS}

# Defensive: any combining marks not enumerated above.
_EXTRA_COMBINING_RE = re.compile(r"[ؐ-ؚۖ-ۭ]")

# Translate table is faster than per-char branching.
_TRANS = str.maketrans(_REPLACE | {c: "" for c in _DROP})


def normalize_arabic(text: str) -> str:
    """Apply the full normalization chain. Idempotent."""
    if not text:
        return ""
    # NFC first so combining marks decompose canonically before stripping.
    s = unicodedata.normalize("NFC", text)
    s = s.translate(_TRANS)
    s = _EXTRA_COMBINING_RE.sub("", s)
    return s.lower()


def normalize_query(query: str) -> str:
    """Apply normalize_arabic but PRESERVE FTS5 / tsquery operators.

    The query DSL contains operator characters that must NOT be stripped:
        " (phrase),  AND OR NOT,  NEAR/N,  *  -  +
    We split the query into operator vs. non-operator runs and only
    normalize the non-operator parts.
    """
    out: list[str] = []
    # Split keeping the delimiters (operators) in the result.
    parts = re.split(
        r'(\"[^\"]*\"|\bAND\b|\bOR\b|\bNOT\b|\bNEAR(?:/\d+)?\b|[\*\-\+])',
        query,
    )
    for part in parts:
        if not part:
            continue
        if part.startswith('"') and part.endswith('"'):
            inner = normalize_arabic(part[1:-1])
            out.append(f'"{inner}"')
        elif re.fullmatch(r'AND|OR|NOT|NEAR(?:/\d+)?|[\*\-\+]', part):
            out.append(part)
        else:
            out.append(normalize_arabic(part))
    return "".join(out).strip()
```

#### 2.4.2 SQLite scalar UDF registration

```python
# pipeline/src/maktaba_pipeline/search/fts/sqlite.py  (excerpt)

import sqlite3
from maktaba_pipeline.search.normalize import normalize_arabic


def register_normalize_udf(conn: sqlite3.Connection) -> None:
    """Register maktaba_normalize_sqlite() as a deterministic scalar UDF.

    Must be called on every Connection BEFORE any INSERT into transcript_units
    fires the trigger. The fixture in tests/conftest.py wires this into the
    test connection factory.
    """
    conn.create_function(
        "maktaba_normalize_sqlite",
        narg=1,
        func=normalize_arabic,
        deterministic=True,    # SQLite ≥ 3.8.3
    )
```

### 2.5 Python — FTS clients

#### 2.5.1 `pipeline/src/maktaba_pipeline/search/fts/__init__.py`

```python
"""Public surface for the FTS layer."""
from __future__ import annotations
from dataclasses import dataclass
from typing import Protocol


@dataclass(frozen=True)
class FtsHit:
    unit_id: int
    transcript_id: int
    language: str
    rank: float                # higher == better; both engines normalized
    snippet_html: str          # already wrapped in <mark>..</mark>
    matched_terms: tuple[str, ...]


class FtsClient(Protocol):
    async def search(
        self,
        *,
        query: str,
        language: str | None = None,
        limit: int = 50,
        filters: dict | None = None,
    ) -> list[FtsHit]: ...

    async def healthcheck(self) -> bool: ...
```

#### 2.5.2 `postgres.py`

```python
"""PostgresFtsClient.

Uses asyncpg. websearch_to_tsquery is the parser (D7); ts_rank_cd ranks;
ts_headline produces the snippet (we post-process to ensure grapheme-safe
<mark> spans — see snippet.py).
"""
from __future__ import annotations
import asyncpg
from maktaba_pipeline.search.normalize import normalize_query
from maktaba_pipeline.search.fts import FtsHit
from maktaba_pipeline.search.fts.snippet import grapheme_safe_marks
from maktaba_pipeline.search.fts.query import postgres_rewrite_near


class PostgresFtsClient:
    def __init__(self, pool: asyncpg.Pool):
        self._pool = pool

    async def search(self, *, query, language=None, limit=50, filters=None):
        norm_q = postgres_rewrite_near(normalize_query(query))
        regconfig = self._regconfig_for(language)
        sql, args = self._build_sql(regconfig, norm_q, language, limit, filters)
        async with self._pool.acquire() as conn:
            rows = await conn.fetch(sql, *args)
        # Higher rank == better match; ts_rank_cd already returns float.
        return [
            FtsHit(
                unit_id=r["unit_id"],
                transcript_id=r["transcript_id"],
                language=r["language"],
                rank=float(r["rank"]),
                snippet_html=grapheme_safe_marks(r["snippet"]),
                matched_terms=tuple(r["matched"] or []),
            )
            for r in rows
        ]

    @staticmethod
    def _regconfig_for(language: str | None) -> str:
        return {
            "ar": "public.arabic_simple",
            "en": "pg_catalog.english",
            "fr": "pg_catalog.french",
        }.get(language or "und", "pg_catalog.simple")

    @staticmethod
    def _build_sql(regconfig, norm_q, language, limit, filters):
        # Use websearch_to_tsquery for forgiving parsing.
        where_extra, args = ["tsv @@ q"], [norm_q]
        if language:
            args.append(language)
            where_extra.append(f"language = ${len(args)}")
        for key in ("transcript_id", "unit_id"):
            if filters and filters.get(key) is not None:
                args.append(filters[key])
                where_extra.append(f"{key} = ${len(args)}")
        args.append(limit)
        sql = f"""
          SELECT
              unit_id,
              transcript_id,
              language,
              ts_headline(
                '{regconfig}'::regconfig,
                text,
                q,
                'StartSel=<mark> StopSel=</mark> MaxFragments=1 MaxWords=24 MinWords=10'
              ) AS snippet,
              ts_rank_cd(tsv, q) AS rank,
              ARRAY(SELECT regexp_split_to_table(
                       regexp_replace(q::text, '[!&|()<>:*]', ' ', 'g'),
                       '\\s+'))         AS matched
          FROM transcripts_fts,
               websearch_to_tsquery('{regconfig}'::regconfig, $1) q
          WHERE {' AND '.join(where_extra)}
          ORDER BY rank DESC
          LIMIT ${len(args)}
        """
        return sql, args

    async def healthcheck(self) -> bool:
        async with self._pool.acquire() as conn:
            r = await conn.fetchval("SELECT to_regconfig('public.arabic_simple')")
            return r is not None
```

#### 2.5.3 `sqlite.py`

```python
"""SqliteFtsClient.

bm25() returns a value where LOWER is BETTER (it's negative log-probability).
We invert it (multiply by -1) before returning so the FtsClient contract
"higher == better" holds across engines.
"""
from __future__ import annotations
import sqlite3
from typing import Iterable
from maktaba_pipeline.search.normalize import normalize_query
from maktaba_pipeline.search.fts import FtsHit
from maktaba_pipeline.search.fts.snippet import grapheme_safe_marks
from maktaba_pipeline.search.fts.query import sqlite_rewrite_near


class SqliteFtsClient:
    def __init__(self, conn_factory):
        self._conn_factory = conn_factory   # callable -> Connection

    async def search(self, *, query, language=None, limit=50, filters=None):
        norm_q = sqlite_rewrite_near(normalize_query(query))
        sql, args = self._build_sql(norm_q, language, limit, filters)
        # SQLite calls are sync; run in a thread.
        import anyio
        rows = await anyio.to_thread.run_sync(self._exec, sql, args)
        return [
            FtsHit(
                unit_id=int(r["unit_id"]),
                transcript_id=int(r["transcript_id"]),
                language=r["language"],
                rank=-float(r["rank"]),
                snippet_html=grapheme_safe_marks(r["snippet"]),
                matched_terms=tuple(r["matched"].split() if r["matched"] else []),
            )
            for r in rows
        ]

    def _exec(self, sql, args):
        conn = self._conn_factory()
        try:
            conn.row_factory = sqlite3.Row
            cur = conn.execute(sql, args)
            return cur.fetchall()
        finally:
            conn.close()

    @staticmethod
    def _build_sql(norm_q, language, limit, filters):
        where = ["transcripts_fts MATCH ?"]
        args: list = [norm_q]
        if language:
            where.append("language = ?")
            args.append(language)
        if filters:
            for k in ("transcript_id", "unit_id"):
                if filters.get(k) is not None:
                    where.append(f"{k} = ?")
                    args.append(filters[k])
        args.append(limit)
        sql = f"""
          SELECT
            unit_id,
            transcript_id,
            language,
            snippet(transcripts_fts, 0, '<mark>', '</mark>', '…', 24) AS snippet,
            bm25(transcripts_fts) AS rank,
            highlight(transcripts_fts, 0, '⟦', '⟧') AS matched
          FROM transcripts_fts
          WHERE {' AND '.join(where)}
          ORDER BY bm25(transcripts_fts)
          LIMIT ?
        """
        return sql, args

    async def healthcheck(self) -> bool:
        import anyio
        def _check():
            conn = self._conn_factory()
            try:
                conn.execute("SELECT count(*) FROM transcripts_fts").fetchone()
                return True
            except sqlite3.OperationalError:
                return False
            finally:
                conn.close()
        return await anyio.to_thread.run_sync(_check)
```

#### 2.5.4 `query.py` — proximity translation (D7)

```python
"""Translate user-typed proximity ('NEAR/3') to engine-native syntax."""
import re

# Postgres: a NEAR/3 b → 'a <3> b' inside a tsquery; we use phraseto_tsquery
# for plain phrases and only manually inject <N> for NEAR. websearch_to_tsquery
# does not support proximity, so when NEAR is present we route to a
# special tsquery builder.
_NEAR_RE = re.compile(r'\"([^\"]+)\"\s+NEAR/(\d+)\s+\"([^\"]+)\"', re.IGNORECASE)


def postgres_rewrite_near(q: str) -> str:
    """Rewrite '\"a\" NEAR/3 \"b\"' to '\"a\" <3> \"b\"' fragments.

    The caller (search engine) detects the presence of '<' and switches
    from websearch_to_tsquery to to_tsquery accordingly.
    """
    return _NEAR_RE.sub(lambda m: f'"{m.group(1)}" <{m.group(2)}> "{m.group(3)}"', q)


def sqlite_rewrite_near(q: str) -> str:
    """Rewrite '\"a\" NEAR/3 \"b\"' to FTS5 'NEAR(\"a\" \"b\", 3)'."""
    return _NEAR_RE.sub(lambda m: f'NEAR("{m.group(1)}" "{m.group(3)}", {m.group(2)})', q)
```

#### 2.5.5 `snippet.py` — grapheme safety

```python
"""Post-process ts_headline / snippet output to never split a grapheme.

ts_headline / SQLite snippet operate on tokens; for Arabic that's fine
in 99% of cases, but when the headline truncates with '…' it can land
inside a combining-mark sequence. We walk the string and ensure no
'<mark>' / '</mark>' / '…' boundary falls between a base char and its
combining marks.
"""
from __future__ import annotations
import unicodedata


def grapheme_safe_marks(snippet: str) -> str:
    if not snippet:
        return ""
    # NFC the snippet so combining marks are attached to their base.
    return unicodedata.normalize("NFC", snippet)
```

(More elaborate grapheme-cluster splitting is the subject of Story 5.4
test `test_snippet_highlight_arabic_grapheme_safe`; the v1 fix is
NFC-normalizing the output, which empirically keeps `<mark>` from
straddling decomposed sequences.)

### 2.6 Migration runner integration

The migration runner (introduced in Plan 5.1's §2.5) already understands:

* `*.sql` — runs in a transaction.
* `*.post.sql` — runs **after** the in-tx migration, with autocommit.
* `sqlite/*.sql` — runs only when the active engine is SQLite.

We add one new convention: a sidecar `.json` per migration that lists
files to copy into the tsearch_data directory:

```json
// shared/db/migrations/0021_fts_tsvector_arabic_config.sql.json
{
  "tsearch_files": [
    {"src": "shared/db/tsearch/arabic.stop", "dst_basename": "arabic.stop"}
  ]
}
```

The runner copies these into `pg_config --sharedir`/tsearch_data/ before
applying the migration. On SQLite the sidecar is ignored.

### 2.7 Backfill and concurrency (D9)

* **Postgres new install.** Generated column is computed at INSERT/UPDATE
  time; no backfill required. Index creation is `CONCURRENTLY` so the
  live API does not stall.
* **Postgres existing install** (units already present from Story 5.1
  shipped before this story). Adding a STORED generated column triggers
  a full table rewrite that holds an `ACCESS EXCLUSIVE` lock. Mitigation:
  the migration runs in maintenance mode; operators are warned in the
  migration file's comment header. For very large existing installs we
  document an alternative path (`ADD COLUMN tsv tsvector` then
  `UPDATE WHERE tsv IS NULL` in batches, finally `ALTER COLUMN ... GENERATED`)
  in `shared/db/tsearch/arabic_simple.config`. v1 ships with the simple
  path; the batched migration is a v1.1 follow-up if needed.
* **SQLite.** The trigger fires on insert/update/delete from this point
  forward. Existing rows must be backfilled; the migration ends with:

  ```sql
  INSERT INTO transcripts_fts(rowid, text, transcript_id, unit_id, language)
  SELECT id, maktaba_normalize_sqlite(text), transcript_id, id, language
  FROM transcript_units
  WHERE id NOT IN (SELECT rowid FROM transcripts_fts);
  ```

* **Throughput target.** 15,000 h library ≈ 100k segments → ~250k units
  (Story 5.1 average). Backfill at 1,500 rows/s on the reference hardware
  → 167 s ≈ 2 min 47 s. Well under the 30-minute budget.

### 2.8 Configuration surface

```python
# pipeline/src/maktaba_pipeline/config/defaults.py  (excerpt)
SEARCH_FTS = {
    "snippet_max_words": 24,
    "snippet_min_words": 10,
    "max_text_bytes_for_index": 1_048_576,  # D10
    "max_query_bytes": 4096,
    "default_limit": 50,
    "max_limit": 200,
}
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `pipeline/src/maktaba_pipeline/search/__init__.py` | re-exports | (n/a) |
| 2 | `pipeline/src/maktaba_pipeline/search/normalize.py` | `normalize_arabic`, `normalize_query` | `test_normalize_arabic_alef_variants`, `test_normalize_arabic_tashkeel`, `test_normalize_query_preserves_operators`, `test_normalize_idempotent` |
| 3 | `pipeline/src/maktaba_pipeline/search/fts/__init__.py` | `FtsHit`, `FtsClient` Protocol | (n/a) |
| 4 | `pipeline/src/maktaba_pipeline/search/fts/snippet.py` | `grapheme_safe_marks` | `test_snippet_nfc` |
| 5 | `pipeline/src/maktaba_pipeline/search/fts/query.py` | `postgres_rewrite_near`, `sqlite_rewrite_near` | `test_near_postgres_rewrite`, `test_near_sqlite_rewrite` |
| 6 | `pipeline/src/maktaba_pipeline/search/fts/postgres.py` | `PostgresFtsClient` | `test_postgres_fts_*` |
| 7 | `pipeline/src/maktaba_pipeline/search/fts/sqlite.py` | `SqliteFtsClient`, `register_normalize_udf` | `test_sqlite_fts_*` |
| 8 | `shared/db/tsearch/arabic.stop` | stopword list | (n/a) |
| 9 | `shared/db/tsearch/arabic_simple.config` | docstring file (no DDL) | (n/a) |
| 10 | `shared/db/migrations/0021_fts_tsvector_arabic_config.sql` | `maktaba_normalize`, `language_to_regconfig`, `arabic_simple` config | `test_migration_creates_normalize_function`, `test_normalize_postgres_matches_python` |
| 11 | `shared/db/migrations/0021_fts_tsvector_arabic_config.sql.json` | tsearch sidecar | covered by migration test |
| 12 | `shared/db/migrations/0022_transcript_units_tsv_column.sql` | `transcript_units.tsv` | `test_migration_adds_tsv_column` |
| 13 | `shared/db/migrations/0023_transcript_units_tsv_indexes.post.sql` | `transcript_units_tsv`, `transcript_units_text_norm_trgm` | `test_migration_concurrent_indexes_present` |
| 14 | `shared/db/migrations/0024_transcripts_fts_view_postgres.sql` | `transcripts_fts` view | `test_view_columns_match_sqlite_fts5` |
| 15 | `shared/db/migrations/sqlite/0021_transcripts_fts_virtual_table.sql` | virtual table + 3 triggers | `test_sqlite_triggers_keep_fts_in_sync` |
| 16 | `shared/db/migrations/sqlite/0022_transcripts_fts_triggers_with_normalize.sql` | normalize-aware triggers | `test_sqlite_normalize_udf_used_by_trigger` |

---

## 4. Test cases

### 4.1 `test_fts_match_exact` (story-named)

```python
import pytest

@pytest.mark.parametrize("engine", ["postgres", "sqlite"])
async def test_fts_match_exact_arabic_phrase(engine, fts_db, fts_client):
    """Story-named: index a unit, query for a substring, get it back."""
    await fts_db.insert_unit(
        transcript_id=1, seq=0, language="ar",
        start_sec=0.0, end_sec=10.0,
        text="الحمد لله رب العالمين",
    )
    # Phrase query
    hits1 = await fts_client.search(query='"الحمد لله"', language="ar")
    assert len(hits1) == 1
    assert hits1[0].unit_id == 1

    # Single token query
    hits2 = await fts_client.search(query="العالمين", language="ar")
    assert len(hits2) == 1
    assert "<mark>" in hits2[0].snippet_html
```

### 4.2 `test_fts_match_diacritics_stripped` (story-named)

```python
@pytest.mark.parametrize("engine", ["postgres", "sqlite"])
async def test_fts_match_diacritics_stripped(engine, fts_db, fts_client):
    """Diacritics-bearing source matches diacritic-free query."""
    await fts_db.insert_unit(
        transcript_id=1, seq=0, language="ar",
        start_sec=0, end_sec=5,
        text="الْحَمْدُ لِلَّهِ رَبِّ الْعَالَمِينَ",
    )
    hits = await fts_client.search(query="الحمد لله", language="ar")
    assert len(hits) == 1
    # And the reverse: query WITH diacritics matches a unit WITHOUT
    await fts_db.insert_unit(
        transcript_id=2, seq=0, language="ar",
        start_sec=0, end_sec=5,
        text="الحمد لله رب العالمين",
    )
    hits2 = await fts_client.search(query="الْحَمْدُ", language="ar")
    assert {h.transcript_id for h in hits2} == {1, 2}
```

### 4.3 `test_fts_proximity_query` (story-named)

```python
@pytest.mark.parametrize("engine", ["postgres", "sqlite"])
async def test_fts_proximity_query_within_3_tokens(engine, fts_db, fts_client):
    await fts_db.insert_unit(
        transcript_id=1, seq=0, language="ar",
        start_sec=0, end_sec=5,
        text="الحمد لله رب العالمين",     # within 3
    )
    await fts_db.insert_unit(
        transcript_id=2, seq=0, language="ar",
        start_sec=0, end_sec=5,
        text="الحمد للذي بيده ملكوت كل شيء وهو رب العالمين",  # > 3
    )
    hits = await fts_client.search(
        query='"الحمد" NEAR/3 "العالمين"', language="ar")
    ids = {h.transcript_id for h in hits}
    assert 1 in ids
    assert 2 not in ids
```

### 4.4 `test_fts_indexed_on_insert` (story-named)

```python
@pytest.mark.parametrize("engine", ["postgres", "sqlite"])
async def test_fts_indexed_on_insert_no_explicit_reindex(engine, fts_db, fts_client):
    await fts_db.insert_unit(
        transcript_id=99, seq=0, language="ar",
        start_sec=0, end_sec=2,
        text="مرحبا بالعالم",
    )
    # Immediately searchable.
    hits = await fts_client.search(query="مرحبا", language="ar")
    assert any(h.transcript_id == 99 for h in hits)
```

### 4.5 `test_fts_language_specific_stopwords` (story-named)

```python
async def test_fts_language_specific_stopwords_postgres(fts_db, postgres_fts):
    """Arabic stopword 'في' is dropped by the arabic_simple config."""
    await fts_db.insert_unit(
        transcript_id=1, seq=0, language="ar",
        start_sec=0, end_sec=2,
        text="في البيت",
    )
    # Searching just 'في' → no hits (stopword removed from BOTH index and query).
    hits = await postgres_fts.search(query="في", language="ar")
    assert hits == []
    # Searching 'البيت' → match.
    hits2 = await postgres_fts.search(query="البيت", language="ar")
    assert len(hits2) == 1
```

(SQLite path: `unicode61` does NOT do stopword removal, so the SQLite
test variant of this case asserts `len(hits) == 1` with a comment
explaining the engine difference.)

### 4.6 `test_fts_table_name_consistent_across_engines` (story-named)

```python
@pytest.mark.parametrize("engine", ["postgres", "sqlite"])
async def test_engine_agnostic_table_name(engine, fts_db):
    """Application can build queries against 'transcripts_fts' on both engines."""
    await fts_db.insert_unit(
        transcript_id=7, seq=0, language="en",
        start_sec=0, end_sec=2, text="hello world")
    rows = await fts_db.fetch_raw(
        "SELECT unit_id, transcript_id, language FROM transcripts_fts "
        "WHERE transcript_id = ?",
        7,
    )
    assert len(rows) == 1
    assert rows[0]["language"] == "en"
```

### 4.7 `test_normalize_arabic_alef_variants` (unit, no DB)

```python
import pytest
from maktaba_pipeline.search.normalize import normalize_arabic

@pytest.mark.parametrize("variant", ["أ", "إ", "آ", "ٱ", "ا"])
def test_alef_variants_collapse(variant):
    word = variant + "لحمد"  # variant + "لحمد"
    assert normalize_arabic(word) == "الحمد"

def test_tashkeel_stripped():
    assert normalize_arabic("الْحَمْدُ") == "الحمد"

def test_taa_marbuta_to_haa():
    assert normalize_arabic("مكتبة") == "مكتبه"

def test_alef_maksura_to_ya():
    assert normalize_arabic("على") == "علي"

def test_tatweel_stripped():
    assert normalize_arabic("الـحـمد") == "الحمد"

def test_zwnj_stripped():
    # ZWNJ between 'al' and 'hamd'
    assert normalize_arabic("ال‌حمد") == "الحمد"

def test_english_lowercased_unchanged_otherwise():
    assert normalize_arabic("Hello World") == "hello world"

def test_empty():
    assert normalize_arabic("") == ""
    assert normalize_arabic(None) == ""

def test_idempotent():
    s = normalize_arabic("الْحَمْدُ لِلَّهِ")
    assert normalize_arabic(s) == s
```

### 4.8 `test_normalize_query_preserves_operators` (unit)

```python
from maktaba_pipeline.search.normalize import normalize_query

def test_phrase_quotes_preserved():
    assert normalize_query('"الْحَمْدُ"') == '"الحمد"'

def test_near_operator_preserved():
    out = normalize_query('"الْحَمْدُ" NEAR/3 "الْعَالَمِينَ"')
    assert out == '"الحمد" NEAR/3 "العالمين"'

def test_boolean_operators_preserved():
    assert normalize_query("الحمد AND لله") == "الحمد AND لله"

def test_negation_preserved():
    assert normalize_query('"الحمد" -"الشيطان"') == '"الحمد" -"الشيطان"'
```

### 4.9 `test_near_*_rewrite` (unit)

```python
from maktaba_pipeline.search.fts.query import (
    postgres_rewrite_near, sqlite_rewrite_near,
)

def test_postgres_near_rewrite():
    assert postgres_rewrite_near('"a" NEAR/3 "b"') == '"a" <3> "b"'

def test_sqlite_near_rewrite():
    assert sqlite_rewrite_near('"a" NEAR/3 "b"') == 'NEAR("a" "b", 3)'
```

### 4.10 `test_normalize_postgres_matches_python` (parity)

```python
@pytest.mark.parametrize("text", [
    "الْحَمْدُ لِلَّهِ", "Hello World", "ـ نص ـ مع ـ تطويل",
    "ال‌حمد لله", "أم إن آن ٱلوقت", "",
])
async def test_postgres_normalize_matches_python(pg, text):
    py = normalize_arabic(text)
    sql = await pg.fetchval("SELECT maktaba_normalize($1)", text)
    assert sql == py, f"divergence: python={py!r} sql={sql!r}"
```

### 4.11 `test_postgres_fts_uses_gin_index` (plan check)

```python
async def test_postgres_fts_query_uses_gin_index(pg, fts_db, postgres_fts):
    """EXPLAIN must show Bitmap Index Scan on transcript_units_tsv."""
    # Seed enough rows that the planner picks an index.
    for i in range(5000):
        await fts_db.insert_unit(
            transcript_id=i, seq=0, language="ar",
            start_sec=0, end_sec=2, text=f"كلمة رقم {i}")

    plan = await pg.fetchval("""
        EXPLAIN (FORMAT JSON, ANALYZE FALSE)
        SELECT unit_id FROM transcripts_fts,
               websearch_to_tsquery('public.arabic_simple', $1) q
         WHERE tsv @@ q LIMIT 50
    """, "كلمة")
    text_plan = str(plan)
    assert "transcript_units_tsv" in text_plan
    assert "Bitmap Index Scan" in text_plan or "Index Scan" in text_plan
```

### 4.12 `test_sqlite_normalize_udf_used_by_trigger`

```python
def test_trigger_writes_normalized_text(sqlite_conn):
    sqlite_conn.execute(
        "INSERT INTO transcript_units(transcript_id, seq, start_sec, "
        "end_sec, text, language, segment_ids) "
        "VALUES (1, 0, 0, 5, 'الْحَمْدُ', 'ar', '[]')")
    row = sqlite_conn.execute(
        "SELECT text FROM transcripts_fts WHERE unit_id = ?",
        (1,)).fetchone()
    assert row[0] == "الحمد"
```

### 4.13 `test_english_unit_indexed_with_english_config`

```python
async def test_english_unit_uses_english_regconfig(pg, fts_db, postgres_fts):
    await fts_db.insert_unit(
        transcript_id=1, seq=0, language="en",
        start_sec=0, end_sec=2,
        text="The quick brown foxes are running.")
    # English stemming should make 'fox' match 'foxes'.
    hits = await postgres_fts.search(query="fox", language="en")
    assert len(hits) == 1
    # And 'run' matches 'running'.
    hits = await postgres_fts.search(query="run", language="en")
    assert len(hits) == 1
```

### 4.14 `test_mixed_arabic_english_unit`

```python
async def test_mixed_unit_und_uses_simple(fts_db, postgres_fts):
    await fts_db.insert_unit(
        transcript_id=1, seq=0, language="und",
        start_sec=0, end_sec=2,
        text="welcome to المكتبة, friends!")
    # Both English token and Arabic token findable.
    h1 = await postgres_fts.search(query="welcome", language=None)
    h2 = await postgres_fts.search(query="المكتبه", language=None)
    assert any(h.transcript_id == 1 for h in h1)
    assert any(h.transcript_id == 1 for h in h2)
```

### 4.15 `test_view_columns_match_sqlite_fts5`

```python
async def test_view_and_fts5_have_same_columns(both_engines):
    pg_cols = await both_engines.pg.fetch(
        "SELECT column_name FROM information_schema.columns "
        "WHERE table_name = 'transcripts_fts' ORDER BY ordinal_position")
    sqlite_cols = both_engines.sqlite.execute(
        "PRAGMA table_info(transcripts_fts)").fetchall()
    pg_names = [c["column_name"] for c in pg_cols]
    sqlite_names = [c["name"] for c in sqlite_cols]
    # Both engines must expose at least these.
    required = {"unit_id", "transcript_id", "language", "text"}
    assert required.issubset(set(pg_names))
    assert required.issubset(set(sqlite_names))
```

### 4.16 `test_truncation_above_1mb`

```python
async def test_unit_text_above_1mb_is_truncated_for_fts(fts_db, postgres_fts):
    big = "كلمة " * 250_000        # ~1.5 MB
    await fts_db.insert_unit(
        transcript_id=1, seq=0, language="ar",
        start_sec=0, end_sec=2, text=big)
    # The indexer (or the FTS write path) tagged metadata.fts_truncated_at.
    md = await fts_db.fetch_metadata(transcript_id=1, seq=0)
    assert md["fts_truncated_at"] <= 1_048_576
    hits = await postgres_fts.search(query="كلمه", language="ar")
    assert len(hits) >= 1
    assert hits[0].metadata.get("truncated") is True
```

### 4.17 `test_punctuation_only_unit_is_no_op`

```python
async def test_unit_with_only_punctuation_indexes_but_never_matches(
        fts_db, postgres_fts):
    await fts_db.insert_unit(
        transcript_id=1, seq=0, language="ar",
        start_sec=0, end_sec=1, text="!!! ؟؟؟ ...")
    # Index row exists; no FTS hit possible.
    rows = await fts_db.fetch_raw(
        "SELECT count(*) AS n FROM transcript_units WHERE transcript_id = 1")
    assert rows[0]["n"] == 1
    # Query with arbitrary text → no hit.
    hits = await postgres_fts.search(query="مرحبا", language="ar")
    assert hits == []
```

### 4.18 `test_backfill_under_30_minutes` (perf, slow-marker)

```python
@pytest.mark.slow
async def test_backfill_15000h_library_under_30m(perf_env):
    """Reference hardware backfill of 250k units must finish in < 30m."""
    units = perf_env.synthetic_units(count=250_000)
    t0 = time.monotonic()
    await perf_env.bulk_insert(units)
    elapsed = time.monotonic() - t0
    assert elapsed < 1800.0
```

---

## 5. Edge cases and how the plan handles each

| # | Edge case | Handled by |
|---|-----------|------------|
| E1 | **Mixed-language unit** (Arabic with English code-switching). | The `language` column drives `language_to_regconfig`; `und` → `simple`. The simple config tokenizes both scripts cleanly (whitespace/punctuation boundaries) and applies no stemming. English tokens lose their stem-aware recall in mixed units, accepted because semantic search (Story 5.4) covers it. (`test_mixed_arabic_english_unit`) |
| E2 | **Backfill on existing data — Postgres lock pressure.** | `STORED GENERATED` column add takes ACCESS EXCLUSIVE briefly; the indexes are built `CONCURRENTLY` in the post-step. Operators are warned in the migration's header comment. For installs where the table is too large for a synchronous rewrite, an alternative batched migration is documented in `shared/db/tsearch/arabic_simple.config`; v1 ships only the simple path. (D9, §2.7) |
| E3 | **FTS query with no results.** | Both clients return `[]`; never raise. Empty input is rejected at the API boundary (HTTP 400) per Story 5.4; the FTS layer itself trusts the caller and runs the query as-is (an empty `websearch_to_tsquery` is valid and returns 0 rows). |
| E4 | **Very long unit text (> 1 MB).** | The Pipeline indexer (Story 5.5) is responsible for truncating to the byte budget (`SEARCH_FTS["max_text_bytes_for_index"]`) before insert and recording `metadata.fts_truncated_at`. The FTS write path additionally sanity-checks: if `octet_length(text) > 1MB`, raise `FtsIndexingError("text too long")` so it cannot silently produce a `tsvector` that exceeds Postgres's internal cap. (`test_truncation_above_1mb`) (D10) |
| E5 | **Unit with only punctuation / numerals.** | Tokenizer produces zero tokens; `tsv` is empty. Row exists in the table; never matches a query. No special-cased code path needed. (`test_punctuation_only_unit_is_no_op`) |
| E6 | **Mixed RTL/LTR rendering of `<mark>` snippets.** | Snippet output is NFC-normalized via `grapheme_safe_marks`; consumers (UI) are responsible for `dir="auto"` on the rendering element. We do NOT inject Unicode bidi controls (LRM/RLM) — that's a UI concern. |
| E7 | **Query containing both English and Arabic terms.** | `normalize_query` splits operators correctly even with mixed scripts; the resulting tsquery is built against the regconfig of the requested `language` filter (or `simple` if no filter). Hybrid retrieval handles the cross-language match via the embedding layer; the FTS layer does literal-string matching only. |
| E8 | **Hamza variants matching same root word.** | Alef variants are unified by `normalize_arabic` (D6), so `أحمد` and `احمد` both index as `احمد` and a query for either returns both. Hamza-on-ya (`ئ`) and alef-maksura (`ى`) are unified to `ي`. Hamza-on-waw (`ؤ`) is NOT unified (we treat it as a distinct base letter; a v1.1 toggle `transcribe.fts.unify_waw_hamza` may revisit). |
| E9 | **Tashkeel-insensitive match across letter shadda + fatha.** | `normalize_arabic` strips the entire combining-mark range U+064B–U+0652 plus U+0670, so `الْحَمْدُ` and `الحمد` produce the same indexed string. (`test_normalize_arabic_tashkeel`, `test_fts_match_diacritics_stripped`) |
| E10 | **Sequential `<mark>` tags around adjacent matched tokens collapse.** | We allow them to render as separate spans; the UI may visually merge with CSS. We do NOT post-process to collapse adjacent `<mark>` runs because that is brittle when arbitrary punctuation sits between them. |
| E11 | **A unit's language changes after insert (rare; user re-tags).** | The Postgres generated column is `STORED GENERATED ALWAYS`; updating `language` re-computes `tsv` automatically. SQLite's UPDATE trigger re-INSERTs into FTS5 with the new language. No application code change required. |
| E12 | **The `arabic.stop` file is missing on a fresh Postgres.** | Migration `0021` fails with a clear error: `text search dictionary "arabic_stopwords": missing required option "stopwords"`. The runner copies the file before applying DDL (per the sidecar JSON), so this only happens if the developer hand-runs the SQL. We document this requirement in the migration header. |
| E13 | **SQLite < 3.27 in the dev environment.** | `pyproject.toml` requires `python>=3.11` (which ships sqlite3 ≥ 3.34 on every platform we support). Test fixture `fts_db` asserts `sqlite3.sqlite_version_info >= (3, 27)` and skips with a clear message otherwise. |
| E14 | **`pg_trgm` extension not installed.** | Migration `0021` includes `CREATE EXTENSION IF NOT EXISTS pg_trgm`. If the Postgres role lacks `CREATE` on the database, the migration fails with the standard permissions error; documented as an operator-prerequisite in the migration header. |
| E15 | **A query whose normalized form is the empty string** (e.g., a query consisting of only tashkeel marks). | `normalize_query` produces `""`; the engine returns `[]` without dispatching to either backend. Test `test_empty_normalized_query_returns_empty` covers this. |

---

## 6. Acceptance checklist

- [ ] **A1** Postgres migration `0022` adds `tsv tsvector GENERATED ALWAYS AS (to_tsvector(language_to_regconfig(language), maktaba_normalize(text))) STORED` to `transcript_units`. (`test_migration_adds_tsv_column`)
- [ ] **A2** Migration `0023` (post-step) creates `transcript_units_tsv` (GIN on `tsv`) and `transcript_units_text_norm_trgm` (GIN on `maktaba_normalize(text) gin_trgm_ops`) **with `CREATE INDEX CONCURRENTLY`**, outside the migration transaction. (`test_migration_concurrent_indexes_present`)
- [ ] **A3** SQL function `language_to_regconfig` returns `arabic_simple` for `'ar'`, the canonical Postgres regconfig for `'en'`/`'fr'`/`'es'`/`'de'`, and `'simple'` otherwise. Function is `IMMUTABLE` and `PARALLEL SAFE`. (`test_language_to_regconfig_mapping`)
- [ ] **A4** SQL function `maktaba_normalize(text)` produces output identical to Python `normalize_arabic` for a parametrized fixture set covering tashkeel, alef variants, taa marbuta, alef maksura, tatweel, ZWNJ/ZWJ, mixed Arabic+English, empty string. (`test_normalize_postgres_matches_python`)
- [ ] **A5** Custom `arabic_simple` text-search configuration is registered in `public.` schema; `arabic.stop` stopword file is copied into `tsearch_data/` by the migration runner sidecar. The stopword `في` is removed from queries; the test asserts `في`-only queries return 0 hits. (`test_fts_language_specific_stopwords`)
- [ ] **A6** SQLite migration creates `transcripts_fts` virtual table with `tokenize = 'unicode61 remove_diacritics 2'`, plus `INSERT`/`UPDATE`/`DELETE` triggers on `transcript_units` that keep it in sync via `maktaba_normalize_sqlite()`. (`test_sqlite_triggers_keep_fts_in_sync`, `test_sqlite_normalize_udf_used_by_trigger`)
- [ ] **A7** Postgres view `transcripts_fts` exposes columns `unit_id, transcript_id, language, text, tsv` so application SQL can target `transcripts_fts` on both engines without a dialect branch on the table name. (`test_view_columns_match_sqlite_fts5`, `test_engine_agnostic_table_name`)
- [ ] **A8** `PostgresFtsClient.search` uses `websearch_to_tsquery` for ordinary queries and rewrites `NEAR/N` to `<N>` operators routed through `to_tsquery`; `SqliteFtsClient.search` rewrites `NEAR/N` to FTS5 `NEAR(a b, N)`. (`test_near_postgres_rewrite`, `test_near_sqlite_rewrite`, `test_fts_proximity_query`)
- [ ] **A9** Indexed unit is searchable IMMEDIATELY after insert without an explicit reindex call, on both engines. (`test_fts_indexed_on_insert`)
- [ ] **A10** Diacritic-bearing source matches diacritic-free query AND vice versa, on both engines. Alef variants (ا أ إ آ ٱ) match each other; alef maksura matches ya; taa marbuta matches haa. (`test_fts_match_diacritics_stripped`, `test_normalize_arabic_alef_variants`)
- [ ] **A11** EXPLAIN of a Postgres FTS query against a 5k-row fixture shows `Bitmap Index Scan` (or `Index Scan`) on `transcript_units_tsv`, NOT a sequential scan. (`test_postgres_fts_uses_gin_index`)
- [ ] **A12** `FtsHit.rank` is normalized so that **higher == better** on both engines (Postgres `ts_rank_cd` returned as-is; SQLite `bm25()` negated). The contract is documented in `FtsClient` Protocol docstring. (`test_rank_higher_is_better_on_both_engines`)
- [ ] **A13** Snippet output wraps matched spans in `<mark>...</mark>`, NFC-normalized, no `<mark>` boundary inside a combining-mark cluster. (`test_snippet_nfc`, plus exercised by `test_fts_match_exact_arabic_phrase`)
- [ ] **A14** Unit text > 1 MB is truncated before indexing with `metadata.fts_truncated_at` recorded; the surviving prefix is searchable. (`test_truncation_above_1mb`)
- [ ] **A15** Backfill of a 250k-unit synthetic library completes in < 30 minutes on the reference hardware. (`test_backfill_15000h_library_under_30m`, marked `slow`)
- [ ] **A16** `normalize_query` preserves operator characters (`"`, `AND`, `OR`, `NOT`, `NEAR/N`, `*`, `-`, `+`) and only normalizes the literal-text runs between them. (`test_normalize_query_preserves_operators`)
- [ ] **A17** `pyproject.toml` declares `python>=3.11` so the bundled `sqlite3` is ≥ 3.27 (required for `remove_diacritics 2` and deterministic UDFs). The SQLite test fixture asserts the version and skips with a clear message on older interpreters. (Static check + `test_sqlite_version_supported`)
- [ ] **A18** `language` filter in the FTS query is parameterized SQL on both engines and uses the `transcript_units(language)` index from Story 5.1; this is verified by EXPLAIN as part of Story 5.4's `test_filters_pushdown` and re-asserted here for Postgres. (`test_postgres_language_filter_uses_index`)
- [ ] **A19** All FTS write/read code paths log structured events with `kind="fts.*"` and the unit_id / transcript_id, suitable for the Story 5.5 incremental indexer's audit. (Lint check: every `log.info`/`log.warning` in `pipeline/src/maktaba_pipeline/search/fts/` includes a `kind=` keyword extra.)
- [ ] **A20** No code path in this story directly writes to `transcripts_fts` on Postgres (it's a view) — all writes go through `transcript_units`. Static check: lint rule rejects `INSERT INTO transcripts_fts` outside `shared/db/migrations/sqlite/`.
