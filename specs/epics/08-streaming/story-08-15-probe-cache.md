# Story 8.15 — Probe cache

Per §4 intro: a session lookup needs the file path and probe metadata.
The Streaming Service caches probes in-memory (LRU) and falls back to
Postgres `media_info` (written by Pipeline at the probe stage).

**AC-1 — Cache hit path.**
- **Given** a session for a video whose probe is in the LRU,
- **When** OpenSession reads the metadata,
- **Then** no DB query is issued.

**AC-2 — DB fallback.**
- **Given** a cold cache,
- **When** OpenSession is processed,
- **Then** one `SELECT … FROM videos JOIN media_info … JOIN audio_tracks
  …` query populates the cache and the response.

**AC-3 — On-disk re-probe is forbidden.**
- **Given** a video whose `media_info` row is missing,
- **When** OpenSession is processed,
- **Then** the response is `FAILED_PRECONDITION` with `detail:
  "video-not-probed"`. Streaming never invokes ffprobe itself; the API
  enqueues a probe job (Epic 7 Story 7.5 `/process`) and the user
  retries.

**AC-4 — Eviction on EvictHashCache.**
- **Given** Pipeline calls `streaming.EvictHashCache(content_hash)`
  after a re-probe,
- **When** the call lands,
- **Then** the in-memory probe entries keyed by that hash are dropped;
  the next OpenSession will re-read from `media_info`. (See Epic 8
  Story 8.8 AC-3.)

**Test cases:**
- Integration: 1000 OpenSessions for the same video → 1 DB query.
- Integration: video with missing media_info → FAILED_PRECONDITION.
- Integration: cache eviction after `media_info_cache_size` (default
  10,000 entries) follows LRU.
- Integration: EvictHashCache invalidates the probe cache (verified by
  asserting the next OpenSession issues a DB query).

**Edge cases:**
- `media_info` updated by Pipeline (re-probe after file change) — the
  cache is invalidated by Pipeline calling Streaming's
  `EvictHashCache` (Story 8.8 AC-3) at the same time it invalidates the
  remux cache.
- Concurrent OpenSession for the same uncached video — single-flight
  pattern ensures one DB query, all callers wait.
