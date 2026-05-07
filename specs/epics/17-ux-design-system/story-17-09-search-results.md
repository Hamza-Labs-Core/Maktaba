# Story 17.9 — Search results presentation

Search results are scannable, with hits grouped by video, snippets
truncated, timestamps clickable, facet counts visible.

**Anchors:** [`architecture.md` §9.3](../../architecture.md). Implements
the visual language consumed by
[Story 11.4](../11-web-ui/story-11-04-search-interface.md).

## AC

- Result group: poster, title, language flag, duration; right side: hit
  count + first 3 snippets.
- Snippet: ≤ 160 chars; `<mark>` highlights the query; ellipsis
  on either side.
- Timestamp chip: `[06:12]` clickable, opens player at that second.
- Facet sidebar: language, library, speaker, content type, date,
  duration. Collapsible.
- "Why this result" disclosure: shows BM25 score and semantic similarity
  (admin only by default).
- Sort: Best match (default), Most recent, Most matches.

## TC

- A query with 1,200 hits across 80 videos: render the first 20 video
  groups; pagination for the rest.
- Click a timestamp: the player opens at the exact second.
- Facet count drops to 0: facet entry hidden, not greyed.

## EC

- A query that hits a video deleted between search and click: surface
  "Video no longer available" inline.
- Snippet contains an unbalanced bidi span: bidi isolate prevents UI
  bleed.
- A speaker facet with 1 hit: sorted to the bottom.
