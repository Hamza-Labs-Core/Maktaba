# Story 7.9 — Saved searches

`POST /api/search/save`, `GET /api/search/saved` from §9.3.

**AC-1 — Save and replay.**
- **Given** a successful search,
- **When** the user POSTs `{name, query}` to `/api/search/save`,
- **Then** the row is stored in `saved_searches` keyed by `(user_id,
  name)`, and a subsequent `GET /api/search/saved` returns it.
- **When** `query` is replayed (the UI passes the stored `query` JSON to
  `POST /api/search` verbatim), the same hit set is returned, modulo
  catalog drift.

**AC-2 — Per-user namespacing.**
- **Given** two users save searches with the same `name`,
- **When** either lists,
- **Then** each only sees their own.

**Test cases:**
- Integration: name uniqueness per user; conflicting POST → 409
  `type: saved-search-name-exists`.
- Integration: deleting a user (Epic 10) cascades to delete their saved
  searches.

**Edge cases:**
- `query` JSON is forward-compat: an unknown filter key in stored JSON is
  ignored (logged at debug) on replay rather than failing 400. Test case:
  insert a saved search with `{filters: {future_key: "x", language:
  ["ar"]}}` → replay succeeds, ignoring `future_key`.
- Smart collections (Story 9.14) reuse this storage; ensure the schema
  supports both consumers without an enum split.
