# Story 24.4 — Concurrency and locking

Concurrent writes against the same row, or claims for the same job, are
serialized correctly without livelocks.

## Acceptance criteria

- AC1. Job claim uses `SELECT … FOR UPDATE SKIP LOCKED` (architecture
  §7.3); exactly-once across N workers verified by
  [Epic 19 Story 19.4 TC2](../19-scalability/story-19-04-pipeline-scale-out.md).
- AC2. Watch-progress writes are **last-writer-wins per (user, video)
  with no monotonicity check.** The Epic 7 Story 7.11 contract is
  authoritative: a stale POST with a `position_sec` lower than the
  current stored position is still accepted (a user dragging the
  scrubber back is a normal action). Earlier wording in this story
  that required a `seek=true` flag and rejected lower values is
  superseded; the player API does not carry such a flag, and
  monotonicity at this layer would silently drop legitimate user
  rewinds. The audit row continues to record the sequence of
  writes for forensic trace.
- AC3. ChromaDB upserts use a documented single-writer rule (one
  Pipeline process at a time) per architecture §10.3. **Scope.**
  This applies to the embedded ChromaDB store shipped in v1; multi-
  writer is reserved for the deferred ChromaDB-server deployment.
  Pipeline horizontal scale-out is therefore bounded by this
  single-writer constraint for stages that write embeddings; see
  [Story 19.4](../19-scalability/story-19-04-pipeline-scale-out.md).
  A startup peer-detection check refuses the second writer.
- AC4. Postgres advisory locks gate per-resource serialization (per-
  GPU-device, per-cache-eviction); locks are released on connection
  close and on explicit unlock; no orphaned locks after a worker
  crash.

## Test cases

- TC1. Race on watch progress: 10 concurrent writes to the same
  (user, video); the final value is the latest write that arrived
  at the server, regardless of whether `position_sec` was higher
  or lower than the previous; the audit log records the full
  sequence.
- TC2. Rewind accepted: a write with `position_sec = 30` followed by
  a write with `position_sec = 10` results in stored `position_sec
  = 10`; no rejection, no `seek` flag required.
- TC3. Advisory lock release on crash: hold a lock; kill the holder;
  the next acquirer succeeds within the connection-timeout window.
- TC4. ChromaDB single-writer: two Pipeline processes pointed at
  the same Chroma path are detected at startup; the second logs a
  warning and refuses to write (consistent with Story 19.4 TC4).

## Edge cases

- EC1. SKIP LOCKED with priority queues — the priority ordering is
  preserved across concurrent claimers because `ORDER BY priority,
  created_at` is part of the claim query.
- EC2. Long-held advisory lock — the holder must heartbeat; > 3×
  heartbeat without progress is force-released by the reaper.
- EC3. Watch-progress for a deleted video — the write is dropped
  with a `category=stale_resource` log, not an error.
- EC4. Debounce vs. upsert. The Epic 7 Story 7.11 contract says
  every POST is accepted with 200 OK but only the last per second
  is persisted (server-side debounce). This is consistent with
  AC2 above: the persisted value is the most recent write to
  survive the debounce window.
