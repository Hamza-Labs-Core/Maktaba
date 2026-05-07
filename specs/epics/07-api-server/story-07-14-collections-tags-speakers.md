# Story 7.14 — Collections, tags, speakers

Endpoints from §9.6.

**AC-1 — Collection CRUD with item ordering.**
- **Given** a collection and a list of `{video_id, position}` entries,
- **When** items are POSTed individually or replaced via PATCH on the
  collection,
- **Then** `position` is the canonical order; gaps are allowed; ties are
  broken by `(position, video_id)`. Re-ordering a single item is a single
  UPDATE.

**AC-2 — Smart collection.**
- **Given** a collection with `is_smart = true` and `smart_query` JSON,
- **When** the collection is read,
- **Then** items are computed live from `smart_query` (not stored in
  `collection_items`), with the same shape and pagination as a regular
  collection.

**AC-3 — Tag delta semantics.**
- **Given** a video with tags `[a, b, c]`,
- **When** `PATCH /api/videos/{id}/tags` is called with `{add: [d],
  remove: [b]}`,
- **Then** the resulting tags are `[a, c, d]`. Both `add` and `remove`
  are optional; absent means "no change."

**AC-4 — Speaker rename and merge.**
- **Given** two speakers,
- **When** `POST /api/speakers/merge {keep, drop}` is called,
- **Then** `segment_speakers.speaker_id = drop` rows are rewritten to
  `keep` in one transaction, the `drop` row is deleted, and the response
  is 200 with `affected_segments`.

**Test cases:**
- Integration: item ordering survives a re-order roundtrip.
- Integration: smart collection respects pagination cursor for the
  underlying live query.
- Integration: tag dedup — adding an existing tag is a no-op, not 409.
- Integration: speaker merge is one transaction; killing the API
  mid-merge leaves no half-state.

**Edge cases:**
- A `smart_query` with a deeply nested filter that is invalid at read
  time — return 200 with `items: []` and a `warning` field; never 500.
- Rename a speaker to a name another speaker already has — 409 `type:
  speaker-name-exists`; suggest merge instead.
- Tag name normalization: trim whitespace, NFC unicode normalize, fold
  case-insensitively for uniqueness checks but preserve original
  casing for display (Epic 9 Story 9.12 owns the schema). Test case:
  tags `"Tafsir"` and `"tafsir"` produce one row.
