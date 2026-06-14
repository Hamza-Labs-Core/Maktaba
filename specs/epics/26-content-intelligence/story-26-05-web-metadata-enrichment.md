# Story 26.5 — Web metadata enrichment

## Description

Fill metadata gaps by matching the library's videos against external
sources and storing the results as **candidates** — never overwriting
user edits. Enrichment is the networked half of the epic; it runs as the
out-of-band `enrich` job ([Story 26.7](story-26-07-background-enrichment-pipeline.md)),
keyed off the parsed title (26.1) and classification (26.2), and writes
`media_metadata_enrichment` (candidate matches per video×provider) plus
`web_metadata_cache` (raw provider responses with TTL).

Providers (each behind a thin adapter over one shared rate-limited,
cached `WebClient`):

| Provider | For | Matches by | Pulls |
|----------|-----|-----------|-------|
| **TMDb** | movies, TV | title + year (+ S/E) | official title, overview, cast, genres, rating, poster, backdrop, `tmdb:` id, linked `imdb:` id |
| **OMDb** | movies, TV (fallback/cross-check) | imdb id or title+year | IMDb rating, plot, awards, runtime |
| **YouTube Data** | any (esp. ripped uploads) | title query | description, tags, category, channel, view count, `youtube:` id |
| **MusicBrainz** | music videos | artist + title text | artist, album, track, genre, `mbid:` |
| **Wikipedia/Wikidata** | documentaries, educational | topic/title | summary, `wikidata:Q…`, related article links |

Provider selection is driven by `video_classification.content_type`: a
`film` queries TMDb/OMDb; a `music_video` queries MusicBrainz; a
`documentary`/`lecture` queries Wikidata; everything may fall back to
YouTube. Each provider is skipped if the operator configured no API key.

## Acceptance criteria

- For a video, the `enrich` job calls the providers selected by content
  type, and writes one `media_metadata_enrichment` row **per candidate
  match** with: `provider`, `external_id` (stable, e.g.
  `tmdb:movie:603`), `mapped` (JSONB of normalised fields), a
  `confidence` score, and `fetched_at`.
- **Candidates are staging, not truth.** Enrichment **never** writes
  `videos.title`/`description`/`poster_path` directly. Promotion happens
  only on explicit accept ([Story 26.6](story-26-06-enrichment-ui.md)) or
  high-confidence auto-accept the user opted into.
- **User-owned fields are never overwritten**, even on accept of other
  fields: `media_field_provenance` records which video fields the user
  edited; accept skips those fields and reports the skip.
- Raw provider responses are cached in `web_metadata_cache` keyed by
  `(provider, request_hash)` with a per-provider TTL; a cache hit makes
  re-enrich free and offline.
- Every provider call goes through the shared client's **per-provider
  token-bucket rate limiter**, retry-with-backoff, and a **daily call
  cap**; a tripped breaker pauses that provider only and is surfaced in
  Settings.
- Matching uses parsed-title fields, not the raw filename: title + year
  (+ S/E for TV); the top candidate's `confidence` reflects title
  similarity, year match, and (for TV) S/E alignment.
- **Re-enrich is idempotent by id**: if a video already has an accepted
  `external_id`, re-enrich refreshes that record's `mapped` fields by id
  rather than re-searching.
- Posters/backdrops are downloaded only from the provider's
  allow-listed CDN host, size/dimension-capped, content-type sniffed, and
  re-encoded through the existing thumbnail path (no raw remote bytes
  become `poster_path`).
- A provider with no configured key is skipped silently (logged once);
  enrichment proceeds with whatever providers are enabled.

## Test cases

- `test_tmdb_movie_match` — `Movie (2024)` parsed → TMDb candidate with
  `external_id`, mapped overview/genres/poster, `confidence≥0.8`.
- `test_tv_match_uses_season_episode` — `Show.S01E02` → TMDb TV episode
  candidate matched on S/E, not just show.
- `test_candidates_do_not_touch_videos` — after enrich, `videos.title`
  etc. unchanged; only `media_metadata_enrichment` rows written.
- `test_user_field_provenance_protected` — user-edited `title` →
  accepting a TMDb candidate updates `description`/`poster` but leaves
  `title`, and the response lists `title` as skipped.
- `test_cache_makes_reenrich_offline` — enrich once (network), then
  enrich again with network stubbed to raise → served from
  `web_metadata_cache`, no error.
- `test_rate_limit_token_bucket` — burst of N enrich calls → provider
  calls are throttled to the configured rate; excess deferred, not
  dropped.
- `test_breaker_isolates_provider` — force TMDb 429 storm → TMDb breaker
  trips, OMDb/YouTube still run; pipeline unaffected.
- `test_reenrich_by_id_idempotent` — accepted `tmdb:movie:603` →
  re-enrich refreshes by id, no duplicate candidate, no new search.
- `test_missing_key_skips_provider` — MusicBrainz key unset → provider
  skipped, log emitted once, other providers run.
- `test_poster_download_hardening` — oversized/wrong-host poster URL →
  rejected; no file written; logged.
- `test_musicbrainz_music_video` — `Artist - Track.mkv` classified
  `music_video` → MusicBrainz candidate with artist/album/`mbid`.

## Edge cases

- **No match found.** Zero candidate rows; the video is marked
  `enriched` (job done) with `match=none`; the UI shows "no match,
  search manually" (26.6). Not an error.
- **Multiple plausible matches** (remakes, common titles). All stored
  as candidates ranked by confidence; the UI lets the user pick (26.6).
- **Ambiguous year** (parser unsure). Enrichment widens the year window
  (±1) for the search and lowers confidence accordingly.
- **Provider outage / 5xx.** Retried with backoff; on exhaustion the job
  is re-queued later (Story 26.7), not failed permanently; partial
  results from other providers are still written.
- **Rate-limit / quota exhausted.** Remaining videos for that provider
  are deferred to the next window; the cache prevents re-paying for
  already-fetched ids.
- **Stale cache.** Past TTL, the next enrich re-fetches; an accepted
  record keeps serving its last-good `mapped` fields until refreshed.
- **Locale.** Enrichment requests the library's locale where the
  provider supports it (e.g. TMDb `language=ar`); falls back to the
  default locale otherwise. It does not translate text itself.
- **Privacy.** Only the parsed title/year/topic strings leave the box —
  never the transcript, never file paths, never the media bytes.
