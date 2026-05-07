# Story 7.2 — Cursor pagination primitive

Single shared pagination contract used by every list endpoint. No
`page`/`offset` ever — only opaque cursors over a `(sort_key, id)` tuple,
because library/video listings can shift under the user's feet (new
videos arrive, jobs change state).

**AC-1 — Opaque cursor encoding.**
- **Given** a list endpoint returning items sorted by `(updated_at DESC,
  id DESC)`,
- **When** the response is generated with a non-empty `next` page,
- **Then** the cursor is a base64url-encoded JSON `{u: "<RFC3339>", i:
  "<uuid>", v: 1}` with no padding, and is shorter than 128 bytes.

**AC-2 — Stable iteration under concurrent writes.**
- **Given** a paginated list with a fixed cursor,
- **When** new rows are inserted between page fetches,
- **Then** the cursor never returns a row whose `(sort_key, id)` is
  greater-than-or-equal-to the cursor's tuple (no duplicates), and
  newly-inserted rows appear on subsequent fresh listings (`?cursor=`
  omitted) but not in the resumed pagination.

**AC-3 — Limit and bounds.**
- **Given** a request with `?limit=N`,
- **When** N is in `[1, 200]`, the response returns at most N items.
- **When** N is missing, default 50 is used.
- **When** N is `<1` or `>200`, the response is `400 Bad Request` with
  problem+json `type: invalid-query-parameter`, `detail: "limit must be in [1,200]"`.

**Test cases:**
- Unit: encode/decode round-trip preserves `(updated_at, id)` exactly.
- Unit: a v2 cursor (future format) decoded by v1 code returns an error
  with `type: cursor-unsupported-version`, not a 500.
- Integration: list 1000 videos page-by-page (limit=50) → 20 pages, no
  duplicates, no skips, last page returns `next: null`.
- Integration: while paginating, insert a new video — the new video does
  **not** appear in the in-flight pagination but **does** appear when the
  client restarts pagination.
- Integration: request with `?cursor=garbage` returns 400 problem+json
  `type: invalid-cursor`.

**Edge cases:**
- Two rows with identical `updated_at` (microsecond collision) — the
  secondary sort by `id` (UUID v7, also time-ordered) breaks the tie
  deterministically. Test case: insert two rows with `now()` inside one
  transaction → both appear, in `id`-descending order, no infinite loop.
- A cursor whose pointed-at row was deleted between requests — the next
  page is computed as "everything strictly less than the cursor's tuple",
  so a deleted row is silently skipped. Test case: paginate, delete the
  first row of page 2, fetch page 2 → returns the rows that *would* have
  followed the deleted row, no error.
- A row whose `updated_at` was rewritten backwards by a manual SQL fix
  during pagination — the row is silently skipped or duplicated; this is
  acceptable but documented in the handler comment. Test case: out of
  scope (manual repair only).
