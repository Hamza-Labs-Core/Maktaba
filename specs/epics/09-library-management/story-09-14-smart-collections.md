# Story 9.14 — Smart collections

`collections (is_smart=true, smart_query JSONB)` + Epic 7 Story 7.9
storage. Items are computed live from `smart_query` per Epic 7 Story
7.14 AC-2.

**AC-1 — Filter shape compatibility.**
- **Given** the same `smart_query` JSON as a saved search,
- **When** materialized,
- **Then** the result set equals the search result set. The two features
  share one filter language and one resolver.

**AC-2 — Live computation.**
- **Given** a smart collection,
- **When** `GET /api/collections/{id}/items` is called,
- **Then** the items are computed from `smart_query` at request time;
  no caching of items; respect cursor pagination from Epic 7 Story 7.2.

**AC-3 — Conversion to manual.**
- **Given** a smart collection,
- **When** the user clicks "freeze" (POST `/convert?freeze=true`),
- **Then** the current item set is materialized into `collection_items`
  in order, `is_smart` flips to false, `smart_query` is moved to
  `frozen_from_query` for audit.

**Test cases:**
- Integration: smart collection's items respond identically to the
  search endpoint with the same query.
- Integration: freeze materializes and a subsequent insert in the
  underlying catalog does *not* affect the frozen collection.

**Edge cases:**
- A smart collection backed by a query that returns 100k items —
  pagination must hold; no in-memory materialization.
- Settings change that invalidates a smart query (e.g. removed a tag) —
  the live computation returns 200 with `items: []` and `warning`
  (matches Epic 7 Story 7.14 AC).
