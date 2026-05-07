# Story 11.1 — Library browser (grid / list view, sorting, filtering)

The user lands on `/library` and can browse all videos in any library, with
poster grid by default, optional list view, server-side pagination, and
client-driven filter chips for language, content type, duration, speaker,
tag, and library.

**Anchors:** [`architecture.md` §6.2](../../architecture.md), §9.1
(libraries), §9.2 (videos). Depends on Epic 7 Stories 7.2 (cursor
pagination), 7.4 (videos CRUD/list).

## AC

- Grid view shows poster, title, duration badge, language flag, and
  processing badge (`PROCESSING`, `READY`, `FAILED`).
- List view shows the same fields plus filename, size, modified date, and
  state in a denser row.
- Toggling between grid and list is one click and persists in
  `localStorage` per user.
- Sorting options: title (asc/desc), recently added, recently watched,
  duration (asc/desc), language. Default: recently added.
- Filter chips: language (multi), content type (multi), duration buckets
  (`<10m`, `10–30m`, `30–60m`, `1h+`), speaker (typeahead), tag (multi),
  library (single). Filters are URL-encoded so views are linkable.
- Cursor pagination: "Load more" sentinel triggers `?cursor=...&limit=60`;
  spinner overlays the existing grid until the next page arrives.
- Empty states: "Library is empty" with primary CTA "Scan now" if the user
  is admin; "No videos match these filters" with a "Clear filters" CTA.
- Filter set covers `MISSING`, `READY_NO_AUDIO`, `SUPERSEDED`, `CORRUPTED`
  state badges as well as the canonical pipeline states (per
  [REVIEW §1.3.a](../../REVIEW.md)).

## TC

- Render 1,000-video library: first paint ≤ 1.5 s on a cold cache;
  pagination scrolls smoothly at 60 fps on a 2019 MacBook Air.
- Filter by `language=ar` + `type=lecture` → URL becomes
  `?lang=ar&type=lecture`; deep-linking that URL reproduces the same view.
- Switch grid → list → grid: scroll position is preserved.
- Apply a filter mid-pagination: the cursor is reset and the grid re-fetches
  from page 0.

## EC

- A library returns 0 results because the user filtered on a tag that has
  been deleted server-side: surface a non-blocking toast and clear that
  chip from the URL.
- The poster URL 404s (cache evicted): show the placeholder poster, log
  client warning, do not break the row.
- Slow network mid-pagination (>5 s): show "Still loading…" inline.
- Mixed-direction titles (Arabic + English): titles render with
  `unicode-bidi: isolate` so neither direction bleeds into the other.
