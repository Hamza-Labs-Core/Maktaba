---
schema: 1
kind: pr
pr: 672
title: Epic 52, word review: suspect transcript words go to a home admin, in every language
date: 2026-11-06
session: d2e3c2e0-a8b8-4feb-a315-6a40998058f2
---

## What happened
- Every story of Epic 52 in one pull request: word evidence packed in `transcript_segments.word_data`, the language packs (Arabic, English, a generic Hunspell pack for 11 LibreOffice dictionaries, the fallback) with a conformance suite, the Quran check against the bundled Tanzil text, the `word_check` stage (`review.words`) with its backfill, the review API (home admins only) with edits, undo and carry-over, the vocabulary, the web queue, fixes from the transcript tab, the Remote apps on Android and Apple, the catalog entries and pack recipes, and `SeedNew` for components a later release adds.
- Main took slots 0184 and 0185 while this was built; the epic's migrations were renumbered to 0186-0188 at the merge (MANIFEST, SCHEMA_REV in three places, comments).

## What we learned
- The spec's `word_data` layout is 9 bytes a word, not the 10 it states: count the fields before trusting a size in a spec.
- `word_check` must follow `second_listen`, not `transcribe`: since slot 0174 the transcript the later stages read is the second listen's.
- With `not_in_dictionary` at the spec's 0.5 and the text-only threshold at 0.6, an old transcript could never be flagged from its text, so the backfill would have found nothing; it weighs 0.65.
- A first seed leaves some needed assets with no desired row on purpose (no row reads as wanted); a later seed of new ids must not write those, or a second boot turns transcription's models absent.
- A Go enqueue of a stage the database's `job_stages` does not have answers `unknown_stage` without an error: a new stage needs its row in its migration, as slot 0174 did for the second listen.
- `SchemaRev` lives in three places (pipeline, streaming and `api/internal/cluster/schemaguard`), not two as the migrations README says.
- On this Mac, `cmd/maktaba-server`'s held-TV test fails while the live app's green slot holds port 8792; it is the environment, not the code.

## Open
- The real gold set (corrected references and the 20 hearing-test spots) and the calibration curves fitted from it; the CI subset is synthetic.
- The update rehearsal's "one component more" step (story 52.17 criterion 6), and Playwright and native UI tests of the review screens.
