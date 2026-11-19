---
schema: 1
kind: pr
pr: 1476
title: "Arabic normalisation and word forms for search, on Postgres and SQLite"
date: 2026-11-19
session: 753035df-3990-44b1-9dff-f305bbe9c3cd
agent: claude-code
prs: [1459]
---

## What happened
- #1476 merged as a7a371d86. Epic 49 story 49.4.
  - Migration 0231 adds `maktaba_normalize(text)`, one `translate()`: harakat and tatweel go;
    أ إ آ ٱ → ا, ى → ي, ة → ه, ؤ → و, ئ → ي; Arabic-Indic and Persian digits become ASCII.
  - It adds the `maktaba_stem` configuration (Snowball Arabic and English) with
    `maktaba_stem_prepare()` in front: it strips وال بال فال كال لل, and the imperfect ي ت ن
    before a three-letter root.
  - It adds the IMMUTABLE pairs `maktaba_tsv`/`maktaba_query` and
    `maktaba_tsv_stem`/`maktaba_query_stem`.
  - On SQLite, `api/internal/textnorm` folds text the same way in Go. A test keeps the Go
    and SQL foldings equal.
- It differs from the spec in two ways:
  - a SQL function instead of an `unaccent` rules file, which every Postgres the migrations
    run on would need in its `tsearch_data`;
  - no rebuild of `transcript_segments.search_tsv` (23 M rows), since the migration rules
    forbid rewrites.
- GLM-5.3's review found no blockers or majors. Its minors are issue #1477.

## What we learned
- PostgreSQL 17 and later build an index with `search_path` set to `pg_catalog` only. A SQL
  function used in an index expression must call the others schema-qualified (`public.`),
  or the build fails with "function does not exist" during inlining. It passed every
  expression test until story 49.5's prototype built an index on it. The test now builds
  an index on each function.
- A `SchemaRev` bump alone trips `affected-tests`: it needs a test change or a
  `No-Test-Change:` trailer.
- Snowball's English stemmer gives different stems for related words (patiently → patient,
  patience → patienc). A word-form test must use words that share a stem.

## Open
- Issue #1477: review minors (the stemmer's remaining roughness is for story 49.9's
  relevance check).
