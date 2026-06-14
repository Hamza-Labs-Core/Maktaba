# Story 26.8 — YouTube search integration

## Description

Extend the existing hybrid search ([Epic 05](../05-search-indexing/README.md),
`api/internal/handlers/search`) so that, in addition to results from the
local library, a query can also search **YouTube** and present matches in
a clearly separated **"From YouTube"** section. The user can then
**import** a YouTube result's metadata onto a matching local video
(e.g. a ripped upload whose filename was useless).

Local search is unchanged and always primary; YouTube is an opt-in,
clearly-labelled augmentation, fetched server-side through the same
rate-limited/cached `WebClient` and YouTube adapter from
[Story 26.5](story-26-05-web-metadata-enrichment.md).

## Acceptance criteria

- `GET /api/search?q=…&include=youtube` returns the normal local hybrid
  results **plus** a separate `youtube` result block (title, channel,
  description snippet, thumbnail, `youtube:videoId`, published date,
  view count). Without `include=youtube`, behaviour is byte-identical to
  today.
- The YouTube block is fetched server-side (key from the secret store),
  rate-limited, cached (`youtube_search_cache`), and **never blocks**
  local results: if YouTube is slow/unavailable/keyless, local results
  return on time and the `youtube` block is empty with a `reason`
  (`disabled|no_key|rate_limited|error`).
- Each YouTube result is annotated with a **"matches?"** hint: if a
  local video's parsed title (26.1) is a strong match, the result links
  to that local video ("already in your library"); otherwise it offers
  "import metadata to…".
- **Import** (`POST /api/videos/{id}/import-youtube` with `{youtube_id}`)
  copies the YouTube metadata onto the chosen local video through the
  **same accept/provenance path** as enrichment
  ([Story 26.6](story-26-06-enrichment-ui.md)) — user-owned fields are
  protected; the action is reversible; an `youtube_imports` audit row is
  written.
- The web Search page renders the "From YouTube" section visually
  distinct from local results, with thumbnails opening on YouTube
  (external link, `rel="noopener"`), and an inline "import to a video"
  affordance.
- Library ACL applies to import (only editors of the target video's
  library may import).
- A per-instance setting (`settings.search.youtube`) and the presence of
  a YouTube Data API key gate the feature; off/keyless → the toggle is
  hidden and `include=youtube` is a no-op.

## Test cases

- `test_search_without_include_unchanged` — `?q=foo` (no include) → exact
  current behaviour; no YouTube call.
- `test_search_with_youtube_block` — `?q=foo&include=youtube` → local
  results + populated `youtube` block from a stubbed adapter.
- `test_youtube_failure_does_not_block_local` — YouTube adapter raises →
  local results returned on time; `youtube` block empty with
  `reason="error"`.
- `test_youtube_cached` — repeat query → served from
  `youtube_search_cache`, no second adapter call.
- `test_match_hint_links_local` — a YouTube result matching a local
  parsed title → annotated as already-in-library with the local video id.
- `test_import_uses_provenance` — import → metadata applied via the
  enrichment accept path; user-owned fields protected; `youtube_imports`
  row written.
- `test_import_acl` — read-only user → 403.
- `test_disabled_or_keyless_noop` — setting off or no key →
  `include=youtube` returns empty block, no adapter call.

## Edge cases

- **Rate limit / quota.** YouTube block returns `reason="rate_limited"`;
  local search unaffected; cached prior results may still be shown.
- **No matches on YouTube.** Empty block, not an error.
- **Ambiguous import target.** If the user triggers import without a
  clear local match, the UI prompts for the target video; the API
  requires an explicit `{id}`.
- **Duplicate import.** Re-importing the same `youtube_id` to the same
  video refreshes via provenance (idempotent), no duplicate audit spam
  (last-write-wins with timestamp).
- **Privacy.** Only the query string leaves the box for YouTube search;
  no library contents are sent. The feature is off by default.
- **Mixed-language queries.** Arabic queries are passed through; YouTube
  relevance is the provider's, but local results remain primary.
