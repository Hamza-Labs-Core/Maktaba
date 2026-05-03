# Story 24.3 — Database consistency and constraints

The schema enforces integrity constraints; no business logic relies on
"the application will be correct."

## Acceptance criteria

- AC1. Foreign keys on every relation; `ON DELETE` is explicit
  (`CASCADE` for child rows; `RESTRICT` for cross-aggregate refs).
- AC2. Unique constraints enforce business invariants:
  `videos.content_hash` unique (per the resolution chosen in the
  cross-doc review — see architecture §8.1 and Epic 9 Story 9.4),
  `(library_id, video_id)` unique, `(video_id, segment_idx)` unique,
  `users.username` unique. Where the unique scope must permit
  history rows (e.g., `transcripts (video_id, audio_track_id,
  backend, model)`), the partial-unique-on-`is_active=true`
  pattern is used per Epic 1 Story 3.5 / architecture §8.1.
- AC3. Check constraints validate enum-shaped fields (`videos.state
  IN (...)`, `processing_jobs.state IN (...)`); SQLite parity tested.
  The full list of valid `videos.state` values
  (`DISCOVERED, PROBED, AUDIO_EXTRACTED, READY_NO_AUDIO,
  TRANSCRIBED, INDEXED, THUMBNAILED, READY, MISSING, SUPERSEDED,
  CORRUPTED, FAILED`) lives in architecture §3 and is the source
  for the CHECK constraint.
- AC4. Soft deletes use `deleted_at TIMESTAMPTZ NULL` with a partial
  unique index where applicable; hard deletes are restricted to
  admin-driven `gc` operations.

## Test cases

- TC1. FK enforcement: deleting a `library` cascades to its `videos`,
  which cascades to `segments`; counted before and after.
- TC2. Unique violation: two writers attempt to insert the same
  `(content_hash)`; one succeeds, one gets a clean unique-violation
  error with the conflicting field named.
- TC3. State enum: an attempt to set `videos.state = 'unknown'`
  fails the check constraint, not in app code.

## Edge cases

- EC1. SQLite missing `ON DELETE CASCADE` enforcement unless
  `PRAGMA foreign_keys = ON` — the connection bootstrap sets it; a
  test asserts.
- EC2. Concurrent state-machine transitions — the state column has
  a `CHECK` and the update has a `WHERE state = expected_prev`
  guard; one transition wins, the other returns "stale transition."
- EC3. Migration that adds a NOT NULL column — pattern is documented:
  ship as nullable, backfill, add NOT NULL, ship.
