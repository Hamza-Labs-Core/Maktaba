# Story 5.6 — Search query suggestions

## Description

Autocomplete dropdown for the search box.

## Acceptance criteria

- `GET /api/search/suggest?q=al` returns a ranked list of up to 8
  suggestions drawn from:
  1. The user's recent saved searches (architecture §8.5).
  2. Speaker names in the active library.
  3. High-frequency n-grams (2–4 tokens) from `transcript_units` that
     start with the prefix, computed offline via a nightly task.
- Latency target: P95 ≤ 50 ms.
- Arabic prefix matches use `pg_trgm` GIN on Postgres or FTS5 prefix
  tokens on SQLite (`MATCH 'al*'`).

## Test cases

- `test_suggest_includes_saved_search` — saved search "الحمد" → typing
  "ال" includes it.
- `test_suggest_speakers` — speakers `["Sheikh A", "Sheikh B"]` →
  typing "Sh" suggests both.
- `test_suggest_latency` — 1,000 calls; P95 ≤ 50 ms.

## Edge cases

- **Empty corpus.** Returns the user's saved searches only; no error.
- **Mixed-script prefix.** "al" returns Latin matches and the
  trigram-equivalent Arabic matches if any (typically none; that's
  fine).
