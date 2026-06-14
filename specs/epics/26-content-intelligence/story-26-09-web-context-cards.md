# Story 26.9 — Web context cards

## Description

For any video that has accepted enrichment
([Story 26.5](story-26-05-web-metadata-enrichment.md) /
[26.6](story-26-06-enrichment-ui.md)), show a **context card** on the
video detail page that pulls the enriched facts together with local
cross-references:

- IMDb/TMDb rating, runtime, genres, content rating
- Cast (top-billed) and director/creator
- Plot summary / overview (the accepted one, locale-aware)
- **Related videos in *this* library** — other episodes of the same
  series (26.3), other titles by the same cast/topic, same collection
  members (26.4)
- **"More like this"** — library recommendations computed from the
  existing embedding/topic signals (reusing Epic 14 recommendation
  machinery), *not* a web call

The card is a **read path** over data already produced by 26.2/26.5 plus
existing recommendation infra; it adds **no new tables** and makes **no
new network calls** at view time (everything was fetched during enrich).

## Acceptance criteria

- `GET /api/videos/{id}/context` returns a card payload: accepted
  enrichment facts (rating, cast, genres, summary, provider attribution
  + link), `related_in_library` (series siblings, shared-cast,
  shared-topic, shared-collection — each with reason), and `more_like_this`
  (library recommendations).
- The card renders only fields that exist; a video with partial
  enrichment shows a partial card (no empty rows, no placeholders for
  missing facts).
- "Related in library" and "More like this" are computed from **local**
  signals (series links, `video_entities` cast overlap, `video_topics`,
  collection membership, embedding similarity) — **no web call at view
  time**; the endpoint is fast enough to serve inline with the video
  page.
- Provider data is attributed ("Data from TMDb") with an external link
  (`rel="noopener"`), per provider ToS.
- "More like this" respects the existing recommendation **dismissals**
  (`recommendation_dismissals`, Story 14.7) so hidden items don't
  reappear.
- The card honours library ACL; a user only sees related/recommended
  videos they can access.
- Un-enriched videos get a minimal card: just "More like this" from
  local signals + a "find metadata" CTA into the enrichment flow.

## Test cases

- `test_context_payload_complete` — fully enriched video → payload with
  rating/cast/genres/summary + related + more-like-this.
- `test_partial_enrichment_partial_card` — video missing cast → payload
  omits cast cleanly; no nulls leak to the client.
- `test_related_series_siblings` — an episode → related includes its
  series' other episodes with `reason="same_series"`.
- `test_related_shared_cast` — two videos sharing a cast entity → each
  appears in the other's related with `reason="shared_cast"`.
- `test_more_like_this_local_only` — network stubbed to raise → endpoint
  still returns "more like this" (proves locality).
- `test_more_like_this_respects_dismissals` — dismissed rec not present.
- `test_attribution_present` — TMDb-sourced facts carry attribution +
  external link.
- `test_acl_filters_related` — related/recommended exclude videos in
  libraries the user can't access.
- `test_unenriched_minimal_card` — no enrichment → card has
  more-like-this + a find-metadata CTA, no error.

## Edge cases

- **Cast/entity name collisions** (two different people, same name).
  Related-by-cast keys on the canonical `media_entities` id, not the raw
  string, so homonyms don't cross-link.
- **Very large series.** Related siblings are paginated/capped (e.g. show
  ±N around the current episode + "see all in series" → 26.10).
- **Stale enrichment.** The card serves the last accepted `mapped`
  fields even if the cache expired; a re-enrich updates them later.
- **No related, no recs** (a singleton video in a tiny library). Card
  shows just the enriched facts + CTA; empty related section is omitted.
- **Mixed locale.** Summary is shown in the accepted locale; if only a
  non-locale summary exists, it's shown with a language tag.
- **Performance.** The endpoint must not fan out to providers; if a fact
  is missing it's simply absent — it never triggers an on-view fetch.
