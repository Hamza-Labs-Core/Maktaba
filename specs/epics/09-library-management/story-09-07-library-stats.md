# Story 9.7 — Library stats query

`GET /api/libraries/{id}/stats` (Epic 7 Story 7.3 AC-6 binds the
endpoint; this story defines the *contents*, the SQL, and the
aggregate-cache table that backs sub-50 ms performance).

**AC-1 — Composition.**
- **Given** a library,
- **When** stats are requested,
- **Then** the response includes:
  ```
  total_videos
  total_duration_sec
  by_state: {DISCOVERED, PROBED, AUDIO_EXTRACTED, TRANSCRIBED, INDEXED,
             THUMBNAILED, READY, READY_NO_AUDIO, FAILED, MISSING,
             SUPERSEDED, CORRUPTED}
  processed_pct = READY / (total - SUPERSEDED - MISSING)
  by_language: { "ar": N, "en": M, "und": K, ... }
  by_content_type: { "lecture": N, "sermon": M, ... }
  storage:
    source_size_bytes
    derived_size_bytes      (transcripts + subtitles + sidecars)
  jobs:
    pending, running, paused, failed
  last_sweep: { started_at, finished_at, scanned, new_videos }
  ```
- The `by_state` enumeration matches the FSM in arch §3 with the new
  states (`MISSING`, `READY_NO_AUDIO`, `SUPERSEDED`, `CORRUPTED`)
  surfaced in REVIEW §1.3.a.

**AC-2 — Single-query performance via aggregate cache.**
- **Given** a 50,000-video library,
- **When** stats are requested,
- **Then** the response is served in under 50 ms by reading a single
  row from `library_stats_cache` (schema in [README.md](README.md)).
  The cache is maintained by:
  - a trigger on `videos` (state, language, content_type, source size),
  - a trigger on `processing_jobs` (jobs counts),
  - a sweep finalizer (`last_sweep` summary).
- A nightly reconciliation job (`maktaba-api stats-rebuild`) recomputes
  the cache from source tables and verifies invariants (counts add up
  to `total_videos`).

**AC-3 — Empty-library defaults.**
- **Given** an empty library,
- **When** stats are requested,
- **Then** every count is 0, `processed_pct = null` (not `0/0`), and
  `last_sweep = null`.

**Test cases:**
- Integration: counts add up to `total_videos` for `by_state` and
  `by_language`.
- Integration: `processed_pct` rounds to 2 decimals.
- Integration: insert/delete a video → cache row's
  `total_videos`/`by_state` reflect the change in the next stats call.
- Performance: stats query under the 50 ms budget on the 50k-video
  perf fixture (driven by the cache, not the source tables).
- Reconciliation: a deliberately corrupted cache row is detected and
  rebuilt by the nightly job; a metric `stats_cache_corruption_total`
  fires.

**Edge cases:**
- Stats requested while the library is being deleted — return 404 if
  the row is gone; otherwise return whatever is current.
- Cache trigger lag during a bulk insert (10k videos in a transaction)
  — the trigger fires once per row; the response stays consistent. For
  bulk loads outside transactional inserts, the nightly reconciliation
  job repairs drift.
