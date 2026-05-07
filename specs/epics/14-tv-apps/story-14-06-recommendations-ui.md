# Story 14.6 — Recommendations UI

A row "Because you watched X" populated from semantic-similarity over
the user's recently watched titles. The data source is owned by
[Story 14.7](story-14-07-recommendations-api.md); this story owns only
the client surface.

**Anchors:** [`architecture.md` §6.5](../../architecture.md). Depends on
[Story 14.7](story-14-07-recommendations-api.md).

## AC

- Source: server-side endpoint
  [`GET /api/recommendations`](story-14-07-recommendations-api.md)
  returning a list of `{title, items[], reason}` rows.
- Reason: "Because you watched X", "More like Y", "Speakers you follow",
  "Newly added in your favorite library".
- Up to 5 rows; each up to 20 items.
- Row composition is deterministic per user per day (cached server-side
  for 24 h, recomputed nightly — see Story 14.7).
- "Not interested" affordance hides items / reasons; persisted per user.

## TC

- Watch three sermons by the same speaker: a "More from {speaker}" row
  appears within 24 h.
- Hide a row: remains hidden on next launch.
- New user with no history: rec rows show "Newly added" and "Editor's
  picks" only.

## EC

- All recommendations would have ≤ 1 item: row hidden rather than
  half-empty.
- A "Speakers you follow" row when no speaker is followed: silently
  omitted.
- Cold-start (no watch history): no personalized rows; only newly added
  and editor's picks.
