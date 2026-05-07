# Story 5.2 — FTS5 / `tsvector` exact-phrase index (unit-backed)

## Description

The deterministic, cheap layer of search.

> **Resolves REVIEW §1.1.d.** Both the SQLite (FTS5) and Postgres
> (`tsvector`) layers index `transcript_units`, not `transcript_segments`.
> The architecture document's `transcripts_fts` (originally over
> `transcript_segments`) is replaced by a unit-backed structure under
> the same logical name. This story is the single owner of the change.

## Acceptance criteria

- For Postgres, a `tsvector` column on `transcript_units` with a GIN
  index, plus `pg_trgm` for prefix queries:

  ```sql
  ALTER TABLE transcript_units
    ADD COLUMN tsv tsvector
    GENERATED ALWAYS AS (
      to_tsvector(
        coalesce(language_to_regconfig(language), 'simple'),
        text
      )
    ) STORED;
  CREATE INDEX transcript_units_tsv ON transcript_units USING GIN (tsv);
  CREATE INDEX transcript_units_text_trgm
    ON transcript_units USING GIN (text gin_trgm_ops);
  ```

  where `language_to_regconfig` maps `ar → arabic`, `en → english`,
  `und → simple`.
- For SQLite, a contentless FTS5 virtual table named `transcripts_fts`
  is created over `transcript_units`:

  ```sql
  CREATE VIRTUAL TABLE transcripts_fts USING fts5(
      text,
      transcript_id UNINDEXED,
      unit_id UNINDEXED,
      language UNINDEXED,
      tokenize = 'unicode61 remove_diacritics 2'
  );
  ```

  Triggers on `transcript_units` (INSERT/UPDATE/DELETE) keep the FTS
  table in sync. The same logical table name `transcripts_fts` is used
  on both engines so application code does not branch on dialect for
  the table name.
- Diacritic-insensitive matching is enabled — for SQLite via the
  `unicode61 remove_diacritics 2` tokenizer above, for Postgres via the
  `arabic` text-search config we ship (Pipeline owns the dictionary
  files in `shared/db/tsearch/`).
- Backfilling FTS on a 15,000 h library (architecture §10.1) finishes in
  under 30 minutes on the reference hardware.

## Test cases

- `test_fts_match_exact` — index a unit with text "الحمد لله رب
  العالمين" → query "الحمد لله" returns it; query "العالمين" returns
  it.
- `test_fts_match_diacritics_stripped` — unit with diacritics-bearing
  Arabic text → query without diacritics matches.
- `test_fts_proximity_query` — query `"الحمد" NEAR/3 "العالمين"` →
  matches a unit where the words are within 3 tokens.
- `test_fts_indexed_on_insert` — insert a unit; immediately query →
  result returned without any explicit reindex.
- `test_fts_language_specific_stopwords` — Arabic unit with stopword
  `في`; query just `في` → recall is reduced (matches stopword removal).
- `test_fts_table_name_consistent_across_engines` — application code
  builds queries against `transcripts_fts` without dialect branching;
  passes on both Postgres (view-shimmed) and SQLite (native FTS5).

## Edge cases

- **Mixed-language unit** (Arabic with English code-switching). The
  `tsvector` is built with `'simple'` config when `language = 'und'`;
  for typed mixed content the dominant language tag is used and the
  English tokens are simply stemmed under Arabic rules — accepted, since
  semantic recall covers this case.
- **Backfill on existing data.** A migration that adds the GIN index
  uses `CREATE INDEX CONCURRENTLY` (Postgres) so the live API does not
  stall.
- **FTS query with no results.** Returns `total = 0`, `hits = []`;
  never errors. Empty queries are rejected at the API layer.
