# Plan 26.10 — Cross-library series view — implementation

> Implementation plan for [story-26-10-series-view.md](story-26-10-series-view.md).
> Self-contained. **Read path only — no new tables.** Cross-links: reads
> `series`/`series_episodes` ([Plan 26.3](plan-26-03-series-detection.md)),
> enrichment facts ([26.5](plan-26-05-web-metadata-enrichment.md)), and
> existing watch progress (`play_state`/`watch_history`, Epic 08/14).
> Series edit/merge/split endpoints are owned by
> [Plan 26.3](plan-26-03-series-detection.md); this plan adds list,
> episode-grid, and missing-episode read endpoints + the web browser.

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **Cross-library list with ACL filtering at the query**, not post-hoc. The `GET /api/series` query joins the user's accessible libraries; forbidden episodes are excluded from counts and missing-detection for that user. | Story AC: a forbidden episode must not leak into counts/gaps. Filtering in SQL keeps counts correct. |
| D2 | **Watch progress read from the existing playback store** (`play_state`/`watch_history`); no Epic-26 progress table. | Story AC: single source of truth; device-synced for free. |
| D3 | **Missing-episode detection is range-based over present episodes**, extended by an enriched season episode-count when available (trailing-missing). Absolute-numbered series detect gaps in the absolute sequence. | Story AC + edge cases; don't invent totals unless enrichment supplies one. |
| D4 | **Pagination + virtualised grid** for large catalogues. | Story edge case: large catalogues. |
| D5 | **User overrides surfaced as-is** (names/reordering from 26.3 flags). | Story AC. |

---

## 1. API (Go, extends `api/internal/handlers/series`)

```
GET /api/series?library_id=&sort=&page=     → cross-library list (D1, D4)
GET /api/series/{id}/episodes               → seasons → episodes + watch state (D2)
GET /api/series/{id}/missing                → gaps per season (D3)
```

List item: `{id, name, poster_path, library_id, library_name,
season_count, episode_count, watched_count, in_progress, year}`.
`watched_count`/`in_progress` aggregate `play_state` for the caller (D2).

Episodes response: seasons grouped, each episode
`{video_id, season, episode, absolute_number, title, thumbnail,
duration_sec, progress_pct, watched}`; ordering is
`(season, episode)` or absolute when `series.metadata.numbering ==
'absolute'`.

Missing detection:

```go
func Missing(series, user) []Gap {
    eps := aclFilter(loadEpisodes(series), user)              // D1
    gaps := []Gap{}
    for season, present := range bySeasonSorted(eps) {
        gaps = append(gaps, rangeGaps(present)...)            // interior gaps (E03)
        if total := enrichedSeasonCount(series, season); total > 0 {
            gaps = append(gaps, trailingGaps(present, total)...)  // E05,E06 of a 6-ep season (D3)
        }
    }
    return gaps
}
```

Continue-watching (next unwatched/in-progress in series order) is
derived in the episodes handler and returned as `next_episode`.

## 2. Web UI (React, new routes)

```
web/src/pages/SeriesBrowser.tsx      → /series        (grid of all series)
web/src/pages/SeriesDetail.tsx       → /series/:id     (season→episode grid)
```

- **SeriesBrowser**: virtualised responsive grid of series cards
  (poster, name, library badge, `watched/episode_count` progress ring),
  sort control (name / recently-added / progress), `library_id` filter.
- **SeriesDetail**: header (poster, name, description from enrichment,
  season tabs), a season → episode grid. Each episode cell: thumbnail,
  title, duration, progress bar; **missing episodes render as ghost
  cells** ("Episode 4 — missing"). A "Continue watching" button targets
  `next_episode`. A "Specials" row for season 0. An "Accept all episode
  matches" button (26.6 batch-accept) when pending matches exist.
- RTL-correct for Arabic; design-system components throughout.

## 3. Files to create / modify

**Create:** list/episodes/missing handlers (extend
`api/internal/handlers/series`), `SeriesBrowser.tsx`,
`SeriesDetail.tsx` + tests.

**Modify:** `api/internal/router` (series read routes), web nav/router
(add `/series`), `MANIFEST.md` — **no migration** (read path).

## 4. Dependencies

- **26.3** series tables + edit/merge/split endpoints.
- **26.5/26.6** enrichment (descriptions, episode titles, season counts;
  batch-accept).
- **Epic 08/14** watch progress store.
- **Epic 10** ACL. **Epic 17** design system.

## 5. Test strategy

Go: cross-library listing + `library_id` filter; episode grouping/order
(season + absolute); missing detection (interior + trailing-from-enriched
+ absolute); watch-progress aggregation; continue-watching target; ACL
hides forbidden episodes from list/grid/counts/missing; pagination.
React: grid virtualisation; ghost cells; specials row; continue-watching;
user-override display; RTL render.

## 6. Performance

List aggregates progress via a single grouped query over indexed
`series`/`series_episodes`/`play_state`; paginated. Episode grid loads
one series at a time. Missing-detection is O(episodes-in-series). No
provider calls.
