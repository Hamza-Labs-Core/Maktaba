# Story 11.4 — Search interface (instant search, faceted filters, time-coded results)

A `/search` page (and a header search box on every other page) that
debounces user input, hits `POST /api/search` with hybrid mode by default,
shows highlighted snippets with timestamp deep-links, and supports facet
filters (language, library, speaker, content type, date range).

**Anchors:** [`architecture.md` §9.3](../../architecture.md). Depends on
Epic 7 Stories 7.8 (search), 7.9 (saved searches); Epic 1 Stories 5.1–5.6
(indexing).

## AC

- Header search box debounce: 200 ms; suggestions appear in a dropdown
  using `GET /api/search/suggest?q=...`.
- Results page shows hits grouped by video, with per-hit snippet (≤ 200
  chars) and a clickable timestamp like `[06:12 → 06:24]` that opens
  `/watch/{id}?t=372`.
- Match coordinates returned by the API are **segment-level**
  (`matches[].segment_id, start_sec, end_sec`); the indexer converts
  unit-level scoring back to segment IDs server-side via the
  `transcript_units.segment_ids[0]` rule (resolves
  [REVIEW §4.2](../../REVIEW.md)).
- Facet sidebar: counts per facet are returned in the search response and
  rendered as collapsible groups.
- Mode toggle: FTS / Semantic / Hybrid (default). Persists per user.
- Empty state: "No matches for «query»" with suggestions ("did you mean…",
  pulled from `suggest`).
- Saved searches: a "Save this search" button stores the current query +
  filters via `POST /api/search/save`; saved searches appear in the
  sidebar under "My Searches".
- The result list virtualizes — 1,000 hits paint smoothly.
- Highlighted spans use `<mark>` and respect bidi (Arabic queries
  highlight Arabic substrings, not Latin lookalikes).
- The client-side budget for displaying results is aligned with the
  server budget settled per [REVIEW §1.4.d](../../REVIEW.md): show
  spinner only after 500 ms; show "Search took longer than expected"
  after 2 s.

## TC

- Type `الحمد لله` in the box: results stream in within the canonical
  budget (p95 ≤ 500 ms warm cache, p95 ≤ 2 s cold) on the household
  scale (15,000 hours indexed). Top hit's timestamp deep-link plays the
  exact second.
- Switch mode FTS → Semantic on the same query: the result set may differ;
  the URL updates with `?mode=semantic`.
- Type a 1-character query: no request fires (min length 2 by default).
- Save search "Sermons mentioning تفسير ≥ 30 min": appears under My
  Searches; clicking reproduces the URL.

## EC

- Server returns `total: 0` but suggestions: surface the suggestions
  prominently.
- Query contains unbalanced quotes or FTS5-illegal characters: client
  sanitizes by escaping rather than failing.
- Backend search timeout (>5 s): show "Search took too long, retry?" with
  a Retry button; do not silently swap to a partial index.
- A very long query (> 1 KB): client refuses to submit (HTTP 400 prevented
  client-side) with an inline message.
- Mixed-script query (Arabic + English): RTL/LTR fragments rendered with
  isolates; suggestions don't reorder the user's typed characters.
