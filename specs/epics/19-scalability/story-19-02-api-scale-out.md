# Story 19.2 — Horizontal scale-out for the API service

The API is stateless; running N replicas behind any L7 LB must work
without sticky sessions and without losing WebSocket events.

## Acceptance criteria

- AC1. Two API replicas behind an LB serve identical responses for the
  same request; cookies and JWTs validate on either replica.
- AC2. A WebSocket client connected to replica A receives events
  triggered on replica B within p95 ≤ 250 ms, fanned out via Postgres
  `LISTEN/NOTIFY`.
- AC3. The WS replay surface is the canonical mechanism for missed-event
  recovery across replicas; the in-memory ring buffer described by Epic 7
  Story 7.16 is a same-replica fast path that backs onto the same
  durable surface. Specifically:
  - The NOTIFY payload size is bounded ≤ 8 KiB; events larger than that
    store the payload in an `events(id, channel, payload, created_at)`
    Postgres table and notify with the row id only.
  - Every job-/video-/library-affecting event written by an API replica
    is also persisted to `events` with a monotonic `id`. Clients
    reconnecting present a `last_event_id` and the API replays from
    `events` (cross-replica safe). Within a single replica, the in-memory
    ring is consulted first as an optimization.
  - `events` retention is 7 days by default, configurable; rows older
    than the retention window are pruned by a scheduled task. A
    persistent unique `last_event_id` is preserved even after pruning
    (sequences are not reset).
- AC4. Replica restart (rolling) does not drop in-flight WebSocket
  subscriptions for clients connected to the other replica.

## Test cases

- TC1. Round-robin: alternate two replicas for 1,000 requests; all
  succeed; session continuity is preserved.
- TC2. WS fan-out: connect 100 clients across both replicas, fire 1,000
  job-progress events; every client receives every event in order.
- TC3. Rolling restart: bounce replica A while 50 clients are connected
  there; clients reconnect to replica B, missed events are replayed
  from the `events` table by `last_event_id`.
- TC4. Retention: insert 7-day-old rows; the pruner removes them and
  `last_event_id` continues to advance monotonically (no sequence
  rollback).

## Edge cases

- EC1. Postgres `LISTEN/NOTIFY` queue overflow under burst — the API
  switches to a poll-the-events-table fallback within 5 s of the first
  dropped notification. The "burst" threshold is `≥ 100 dropped
  notifications observed within 60 s` (configurable).
- EC2. Clock skew across replicas: any `now()`-using logic uses
  Postgres `now()`, not Go `time.Now()`, for tie-breaking.
- EC3. JWKS rotation across replicas: a key rotated on replica A is
  visible to replica B within ≤ 5 minutes (TTL of the JWKS cache).
