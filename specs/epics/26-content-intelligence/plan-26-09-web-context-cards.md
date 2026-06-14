# Plan 26.9 — Web context cards — implementation

> Implementation plan for [story-26-09-web-context-cards.md](story-26-09-web-context-cards.md).
> Self-contained. **Read path only — no new tables, no view-time network
> calls.** Cross-links: reads accepted enrichment
> ([Plan 26.5](plan-26-05-web-metadata-enrichment.md)/[26.6](plan-26-06-enrichment-ui.md)),
> `video_entities`/`video_topics` ([Plan 26.2](plan-26-02-transcript-topic-extraction.md)),
> `series_episodes` ([Plan 26.3](plan-26-03-series-detection.md)),
> collections (26.4), and the existing recommendation machinery
> (Epic 14, including `recommendation_dismissals`).

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **Pure read aggregation in one endpoint**, `GET /api/videos/{id}/context`. No new tables. | Story: card is a view over already-produced data. |
| D2 | **No provider calls at view time.** Everything was fetched during `enrich`; the card serves the last accepted `mapped` fields even past cache TTL. | Story AC + performance: the page must render inline, offline-tolerant. |
| D3 | **Related-in-library has typed reasons**, computed from local joins: `same_series`, `shared_cast`, `shared_topic`, `same_collection`. | Story AC: each related item carries why. Joins use existing indexes (slots 0074/0075/0046/0033). |
| D4 | **"More like this" reuses Epic 14 recommendations**, honouring `recommendation_dismissals`. | Story AC: don't build a second recommender; respect existing hides. |
| D5 | **ACL-filtered**: related/recommended exclude videos in libraries the caller can't access. | Story AC + libraryacl. |
| D6 | **Partial cards are first-class**: omit absent facts/sections rather than render nulls. | Story AC. |

---

## 1. API (Go, `api/internal/handlers/context` or extend `videos`)

```
GET /api/videos/{id}/context
```

Response:

```json
{
  "facts": {                                  // from accepted enrichment (D2); omit if none
    "rating": {"tmdb": 8.7, "imdb": 8.7},
    "runtime_min": 136, "genres": ["Sci-Fi"], "content_rating": "R",
    "cast": [{"entity_id": "...", "name": "...", "role": "..."}],
    "director": "...", "summary": "...", "summary_lang": "en",
    "attribution": [{"provider": "tmdb", "url": "https://..."}]
  },
  "related_in_library": [                      // D3
    {"video_id": "...", "title": "...", "reason": "same_series"},
    {"video_id": "...", "title": "...", "reason": "shared_cast", "via": "<entity name>"}
  ],
  "more_like_this": [                          // D4
    {"video_id": "...", "title": "...", "score": 0.82}
  ]
}
```

Handler aggregation:

```go
func Context(videoID, user) ContextCard {
    facts := buildFacts(acceptedEnrichment(videoID))            // D2, D6
    related := []
    related = append(related, seriesSiblings(videoID)...)       // series_episodes
    related = append(related, sharedCast(videoID)...)           // video_entities ∩ (D3)
    related = append(related, sharedTopic(videoID)...)          // video_topics
    related = append(related, sameCollection(videoID)...)       // smart-query membership
    related = dedupeAndCap(aclFilter(related, user))            // D5
    mlt := aclFilter(recommendations.MoreLikeThis(videoID, user), user)  // D4 (respects dismissals)
    return ContextCard{Facts: facts, RelatedInLibrary: related, MoreLikeThis: mlt}
}
```

`sharedCast` joins `video_entities` on `entity_id` (canonical id, so
homonyms don't cross-link, story edge case) using the
`video_entities_entity_idx` from slot 0074. `seriesSiblings` is capped
(±N around the current episode) with a "see all in series" pointer to
26.10.

## 2. Web UI (React, `web/src/pages/VideoDetail.tsx`)

- A **Context card** component below the player: facts block (rating
  chips, genres, runtime, cast list, summary with a "Data from TMDb ↗"
  attribution link `rel="noopener"`), a **Related** rail (grouped/badged
  by reason), and a **More like this** rail.
- Partial rendering: each sub-block renders only if its data exists
  (D6). Un-enriched video → only "More like this" + a "Find metadata"
  CTA linking into the enrichment flow (26.6).
- Reuses design-system `Card`, `Chip`, horizontal scroller, `Skeleton`,
  `EmptyState`. RTL-correct for Arabic summaries/names.

## 3. Files to create / modify

**Create:** `api/internal/handlers/context/*` (or a `Context` method on
the videos handler); web Context card component + test.

**Modify:** `VideoDetail.tsx` (mount the card), `api/internal/router`
(register route). **No migration.**

## 4. Dependencies

- **26.2** entities/topics, **26.3** series, **26.4** collections,
  **26.5/26.6** accepted enrichment, **Epic 14** recommendations +
  dismissals, **Epic 10** ACL.

## 5. Test strategy

Go: complete vs. partial payload; series-sibling/shared-cast/shared-topic
reasons; `more_like_this` returned even with network stubbed to raise
(proves locality, story `test_more_like_this_local_only`); dismissals
respected; ACL filters related/recommended; un-enriched minimal card.
React: card renders facts/related/MLT; partial omission; attribution
link; un-enriched CTA; RTL.

## 6. Performance

Single endpoint, all local joins over indexed columns; target inline
with the video page (<50 ms typical). No fan-out to providers ever (D2);
a missing fact is simply absent, never an on-view fetch.
