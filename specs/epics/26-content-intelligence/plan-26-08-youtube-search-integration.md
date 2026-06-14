# Plan 26.8 — YouTube search integration — implementation

> Implementation plan for [story-26-08-youtube-search-integration.md](story-26-08-youtube-search-integration.md).
> Self-contained. Cross-links: extends the existing hybrid search
> (`api/internal/handlers/search`, [Epic 05](../05-search-indexing/README.md));
> reuses the YouTube adapter + shared `WebClient`
> ([Plan 26.5](plan-26-05-web-metadata-enrichment.md)); import reuses the
> enrichment accept/provenance path
> ([Plan 26.6](plan-26-06-enrichment-ui.md)). Writes slot 0080.

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **`include=youtube` is purely additive**; without it, search is byte-identical to today. | Story AC: zero regression to local search. |
| D2 | **YouTube fetched concurrently with local, with a hard timeout; never blocks local results.** Local results return on their own deadline; the youtube block is filled if it arrives, else returns empty + `reason`. | Story AC: local primary, web augmentation degrades gracefully. |
| D3 | **The YouTube call goes through the 26.5 adapter + shared client** (rate limit, cache, breaker, key from secret store). New cache table `youtube_search_cache` (or reuse `web_metadata_cache` with provider=youtube). | Don't duplicate the fetch core; one rate-limited, cached path. |
| D4 | **Match hints computed locally** by comparing each YouTube result's title against `media_parsed_titles`/`videos` via the same normalisation the parser uses. | Cheap, local; lets the UI say "already in your library" vs "import to…". |
| D5 | **Import reuses the enrichment accept path** (provenance-aware, reversible) — a YouTube result is just another candidate `provider=youtube`. | Story AC: same protection + reversibility as enrichment; no parallel write path. |
| D6 | **Feature gated by `settings.search.youtube` + a configured key.** Off/keyless ⇒ toggle hidden, `include=youtube` is a no-op. | Story AC. |

---

## 1. API changes (Go, `api/internal/handlers/search`)

The search handler gains an optional `include` query param. When it
contains `youtube` and the feature is enabled:

```go
func Search(q SearchQuery) SearchResponse {
    localCh := go runLocalHybrid(q)                  // unchanged path
    var ytCh <-chan YTBlock
    if q.Includes("youtube") && youtubeEnabled() {
        ytCh = go fetchYouTube(q.Q, timeout=YT_TIMEOUT)   // D2
    }
    local := <-localCh                               // returns on its own deadline
    yt := drainOrEmpty(ytCh)                         // D2: empty+reason if not ready
    return SearchResponse{Results: local, YouTube: yt}
}
```

`YTBlock`: `{items: [{youtube_id, title, channel, description_snippet,
thumbnail, published_at, view_count, match: {state, video_id?}}],
reason?: "disabled|no_key|rate_limited|error"}`.

`fetchYouTube` calls the Python pipeline's YouTube adapter. Since search
lives in the Go API and the adapter in the Python pipeline, the call
crosses the existing API↔pipeline boundary
(`api/internal/grpcclients`): add a `WebSearch` RPC to the pipeline’s
service (thin wrapper over `enrich/providers/youtube.py:search_text`),
or — if search already calls the pipeline for hybrid ranking — piggyback
on that channel. **Decision:** add a `pipeline.WebSearch(query)` RPC; it
reuses the cached/rate-limited adapter so the Go side stays thin.

Match hints (D4) are computed in Go against the local index: normalise
each YT title, look up `media_parsed_titles.show_name`/`videos.title`;
`state ∈ {in_library, importable}`.

## 2. Import endpoint

```
POST /api/videos/{id}/import-youtube   { youtube_id }
```

Handler: fetch the YouTube item by id via the adapter (cached), write it
as a `media_metadata_enrichment` candidate (`provider='youtube'`,
`external_id='youtube:<id>'`), then invoke the **same accept logic** as
[Plan 26.6](plan-26-06-enrichment-ui.md) (provenance-aware, reversible),
and write a `youtube_imports` audit row. ACL enforced via libraryacl.

## 3. Data model — migration slot 0080

```sql
-- Slot 0080 (Epic 26 / Story 26.8)
CREATE TABLE IF NOT EXISTS youtube_search_cache (
    query_hash  TEXT PRIMARY KEY,
    response    JSONB NOT NULL,
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS youtube_imports (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id    UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    youtube_id  TEXT NOT NULL,
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (video_id, youtube_id)
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS youtube_search_cache_expiry_idx
    ON youtube_search_cache (expires_at);
```

(`youtube_search_cache` is separate from `web_metadata_cache` only for
clarity; an implementation may fold it into the latter with
`provider='youtube_search'`.)

## 4. Web UI (React, `web/src/pages/Search.tsx`)

- A `?include=youtube` is sent when the user enables the "Also search
  YouTube" toggle (shown only when the feature is enabled).
- A visually distinct **"From YouTube"** section below local results:
  cards with thumbnail, title, channel, view count; thumbnail/title link
  to YouTube (`target=_blank rel="noopener noreferrer"`).
- Each card shows its match state: a badge "In your library →" linking to
  the local video, or an "Import metadata to…" button that opens a
  picker (or pre-fills when a confident local match exists) and POSTs to
  `import-youtube`.
- `reason` states render a quiet inline note ("YouTube unavailable",
  "rate limited"), never an error toast that disrupts local results.

## 5. Files to create / modify

**Create:** migration pair; `youtube_imports`/import handler; web
"From YouTube" section + import picker + tests.

**Modify:** `api/internal/handlers/search` (include param + merge),
pipeline `WebSearch` RPC (`grpcclients` + pipeline service), Settings
(YouTube toggle + key — shares the 26.5 key), `Search.tsx`, `router`,
`MANIFEST.md` (slot 0080).

## 6. Dependencies

- **26.5** YouTube adapter + shared client + secret-stored key.
- **26.6** accept/provenance path (import).
- **Epic 05** search handler. **Epic 10** ACL.

## 7. Test strategy

Go: `?q` without include is unchanged (golden); with include returns a
populated block from a stubbed RPC; adapter failure → local on time +
empty block with `reason`; cache hit avoids a second call; import goes
through provenance (user-owned protected) + writes audit + ACL 403 for
read-only. React: section renders distinctly; match-state badges;
import picker; `reason` note rendering.

## 8. Security / privacy

Only the query string egresses; never library contents. Off by default.
External links carry `rel="noopener noreferrer"`. The YouTube key lives
in the secret store and is never returned/logged.
