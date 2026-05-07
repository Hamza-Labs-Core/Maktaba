# Story 19.3 — Horizontal scale-out for the streaming service

Sessions are pinned to the box that owns the FFmpeg subprocess; LB must
route accordingly. Migration is by clean reopen, not by FFmpeg state
transfer.

## Acceptance criteria

- AC1. Two streaming replicas behind a sticky-session LB (consistent
  hash on `session_id`) serve manifests and segments without cross-
  replica cache misses for a single session.
- AC2. `OpenSession` selects the local replica's session store; the
  signed URL embeds the replica's cache origin.
- AC3. If a client's hashed replica is down, the LB reroutes to the next
  replica; the client receives a `session_invalidated` and reopens —
  watch position is preserved (server-side from Postgres).
- AC4. `EvictHashCache` propagates to all replicas via gRPC fan-out.

## Test cases

- TC1. Pin: open 100 sessions; verify each session's segment requests
  always hit the same replica.
- TC2. Failover: kill replica A mid-session; client reopens, resumes
  within 5 s, no duplicated segment download by FFmpeg on replica B.
- TC3. Eviction fan-out: trigger `EvictHashCache(content_hash=X)` via
  any replica; both replicas drop X from their LRU within 1 s.

## Edge cases

- EC1. Two clients sharing a `session_id` due to a buggy proxy — the
  session store rejects with `409` and the second client gets a fresh
  session.
- EC2. Replica disk fills: LRU stops admitting new segments, returns
  `503` for cold transcodes; the LB drains that replica.
- EC3. Time-skew between replicas affects HLS segment timestamps —
  segments are timestamped from FFmpeg's PTS, not wall-clock.
