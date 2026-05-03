# Story 8.10 — Concurrency caps and backpressure

Per §10.4: per-host max concurrent transcodes defaults to
`(num_cores / 4)`. New sessions over the cap fall back to direct play
with a quality cap or queue with "starting soon" UI hint.

> **Note on configuration.** The architecture's `streaming.toml` example
> shows a hard `max_concurrent = 4`; in practice the value is
> auto-derived from `(num_cores / 4)` unless the operator sets it
> explicitly. The example file should carry a comment to that effect.

**AC-1 — Slot accounting.**
- **Given** a host whose effective `max_transcode = (num_cores / 4)`,
- **When** that many sessions are open in transcode mode,
- **Then** the next `OpenSession` (transcode-required) returns
  `RESOURCE_EXHAUSTED` with `details.suggested_action="queue"|"direct-cap"`.

**AC-2 — Direct-cap fallback.**
- **Given** the source can be direct-played at 720p (downsampling not
  required) but the matrix recommended transcode for adaptive,
- **When** slots are full,
- **Then** the session is opened in direct mode capped at 720p, and the
  response carries `mode="direct-degraded"`.

**AC-3 — Queue mode.**
- **Given** the request is queue-tolerant (web player passes
  `accept_queue=true`),
- **When** slots are full,
- **Then** the session is recorded with `state='queued'` and the API
  returns 202 with `position` and `eta_sec`. When a slot frees, the
  next queued session is promoted; the API notifies the client over WS
  (extends Epic 7 Story 7.10).

**Test cases:**
- Integration: open 5 transcode sessions on a 16-core host (effective
  cap of 4) → 4 succeed, 5th queues; closing one promotes the queued
  session within 5 s.
- Integration: under cap pressure, direct-playable videos still open
  even when transcode is exhausted.

**Edge cases:**
- Cap lowered at runtime via settings → existing sessions are *not*
  killed; new ones respect the new cap. Documented in operations.
- A queued session whose client disconnects before promotion is reaped
  by the queue cleaner (every 30 s) and counted in
  `queued_sessions_abandoned_total`.
