# Story 9.13 — Collections (manual ordered)

`collections (is_smart=false)` + `collection_items` (§8.2). Endpoints in
Epic 7 Story 7.14.

**AC-1 — Ordered insertion.**
- **Given** an empty collection,
- **When** items are added with `position` 10, 20, 30,
- **Then** they read back in that order. Re-ordering one item is a single
  UPDATE.

**AC-2 — Insertion at end.**
- **Given** a POST without `position`,
- **When** processed,
- **Then** position = `MAX(position) + 10` (sparse to avoid renumbers).

**AC-3 — Compaction.**
- **Given** drift over time produces `position` values up to 1e9,
- **When** the operator runs `maktaba-api compact-collections`,
- **Then** positions are renumbered 10, 20, 30, … per collection. Online
  reads continue to work; ordering is preserved.

**Test cases:**
- Integration: 100-item collection re-ordered by drag-and-drop (10 single
  UPDATEs) → final order is read back correctly.
- Integration: compaction is idempotent (running twice yields the same
  positions).

**Edge cases:**
- Cycle prevention not applicable (collections are flat).
- Same video added twice — primary key on `(collection_id, video_id)`
  rejects with 409.
