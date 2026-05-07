# Story 25.12 — Tier enforcement (concurrent streams + caps)

> Epic 25 · Cloud relay · Phase 3 (billing)

## Description

The free tier ships zero relay bandwidth; paid tiers ship monthly caps
and concurrent-stream limits. This story is the gate. Enforcement is at
the relay (25.9) — by the time a request reaches the relay's stream
allocator, we already know the tier and the running counts.

Tier matrix (final list lives in 25.15):

| Plan      | Price        | Relay GB / month | Concurrent streams | Servers | Notes |
|-----------|--------------|------------------|--------------------|---------|-------|
| Free      | $0           | 0                | 0                  | 1       | Push + status only |
| Pro       | $9.99 / mo   | 100              | 2                  | 2       | Single user |
| Family    | $24.99 / mo  | 500              | 5                  | 5       | Up to 5 invitees (each separate cloud account; payer is host) |
| Pro Yearly| $99 / year   | 100 / mo         | 2                  | 2       | 17% off |
| Family Yearly | $249 / yr | 500 / mo        | 5                  | 5       | 17% off |

Enforcement points:

- **Stream open.** When the relay receives a request for a stream-y
  endpoint (anything matching `/api/streams/*`,
  `/api/videos/*/play`, or HLS/DASH paths), we increment
  `cloud_streams_active` by `(server_id, user_id, stream_id, opened_at)`.
  Before incrementing, we check
  `count(server_id) < tier.concurrent_streams`. If exceeded:
  `429 too_many_streams` with body `{"limit":2,"current":2}` and
  `Retry-After: 5`.
- **Bandwidth ceiling.** The bandwidth counter (25.11) is checked at
  *stream-open time* against the user's MTD usage. If at-or-above
  cap: `402 quota_exceeded` with body
  `{"used_gb": 100.4, "cap_gb": 100, "renewal_at": "..."}`.
  Soft mode (within 105% of cap) lets the stream proceed but flags
  a warning email; hard mode (≥ 110%) blocks.
- **Free tier hard zero.** Any byte through relay for a free user
  is `402` immediately; the user can still read non-stream
  endpoints (`GET /api/libraries` for a one-shot library list)
  but only up to 100 MB / day total.

Mid-stream enforcement:

- We do **not** kill a running stream mid-byte just because the
  user crossed the cap. We let the open stream complete and refuse
  the *next* request. Reason: ripping the bytes mid-frame produces
  a worse UX than honoring an overage by a few hundred MB.
- A circuit breaker triggers at 200% of cap: at that point we
  forcibly close streams. Audit + abuse event recorded.

Tier source:

- The user's effective tier is loaded into the relay's per-pod LRU
  cache (key: `user_id`, TTL 60s, max 50k entries). Cache is
  invalidated by Postgres `LISTEN tier_changed` notifications
  pushed by the Stripe webhook handler (25.14). Cache miss falls
  through to Postgres `cloud_subscriptions WHERE user_id=...`.

## Acceptance criteria

- **Given** a Pro user with 1 active stream,
  **when** they open a 2nd,
  **then** the request succeeds (Pro = 2).
- **Given** the same user opens a 3rd,
  **when** the relay checks `cloud_streams_active`,
  **then** the response is `429 too_many_streams`.
- **Given** a Family user has streamed 99.5 GB this month,
  **when** they start a 1 GB stream,
  **then** the stream begins; an email warning fires when
  ≥ 100 GB and a stop fires when ≥ 110 GB.
- **Given** a free user issues a request for an HLS chunk,
  **when** the relay checks tier,
  **then** the response is `402 quota_exceeded` regardless
  of monthly counters.
- **Given** Stripe sends `subscription.canceled` for user U,
  **when** the webhook completes,
  **then** within 5s the per-pod LRU cache is invalidated and
  the next stream-open returns `402`.
- **Given** the cache is stale (TTL 60s),
  **when** the user upgraded to Family at the cloud,
  **then** within at most 60s the relay accepts 5 streams.
- **Given** a server has 2 active streams that count against
  user quota,
  **when** the user opens a 3rd through a different client
  device on the same server,
  **then** the limit is enforced *per user*, not per device.
- **Given** the stale-stream reaper closes a phantom row,
  **when** the user opens a new stream,
  **then** the count is correct (no stuck-at-limit).

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | tier=Pro, 0 streams | open | accept |
| T02 | unit        | tier=Pro, 2 streams | open | 429 |
| T03 | unit        | free user | open | 402 |
| T04 | integration | Pro user @ 99 GB | open 2 GB | accepted, warning email queued at 100 |
| T05 | integration | Pro user @ 110 GB | open | 402 |
| T06 | integration | webhook downgrades user mid-stream | observe | running stream completes, next 402 |
| T07 | regression  | LRU cache stale 90s | observe | invalidation < 60s in normal path; deferred up to 90s in pathological |
| T08 | unit        | yearly plan equals monthly cap × 12 | check | 100 GB/month = 1.2 TB/year cap |
| T09 | regression  | concurrent open by 2 devices for same user | observe | second device 429s if it overshoots limit |
| T10 | regression  | refund-induced reactivation | open | accepted within 5s of webhook |
| T11 | unit        | Family yearly | check tier | concurrent streams=5, cap=500 |

## Edge cases

- **Pause/resume.** A pause on the client is just an HTTP
  range request that closes mid-stream and resumes later;
  it counts as two streams temporally adjacent. We accept
  this — concurrent-stream limits are about *simultaneous*
  consumption, not session counts.
- **Buffering pre-roll.** A player may pre-buffer 30s
  before showing the user a "playing" UI; if the open
  fails, the user sees an error. We surface tier errors
  with a friendly message via the player's error channel.
- **Trial / promo codes.** Out for v1.
- **Family plan invitees.** Each invitee is a separate
  cloud user; the payer's `family_plan` membership is in
  `cloud_subscriptions.metadata` (Stripe metadata). All
  member users inherit Family caps. Implementation in
  25.13/25.14.
- **Server suspended.** Suspension at 110% of cap blocks
  the user across all their servers, not just the
  offending one.
- **Refund in middle of cycle.** Stripe refunds keep the
  subscription active until period_end; we honor the
  unrefunded period.
- **Stripe webhook delivery delay.** Up to 24h is possible
  in degraded modes. We gate enforcement on the *cloud's
  recorded* state, not on Stripe — webhooks are how we
  learn, but the source of truth is `cloud_subscriptions`.
- **Pricing change mid-cycle.** New rates apply only at
  next renewal; the user's current row is the contract.
- **Daily-rate-limit for free tier.** 100 MB / day total
  on `/api/*` (not media). Enforced by 25.24; this story
  references but does not implement.

## Files / packages

- `cloud/internal/billing/tier.go` — tier resolution and
  caching.
- `cloud/internal/relay/enforce.go` — pre-stream gate.
- `cloud/internal/billing/quota.go` — 105% / 110% / 200%
  enforcement levels.
- `cloud/internal/notify/email/quota_warn.go`.

## Open questions

- **Soft vs. hard cap defaults.** Currently 105% soft, 110%
  hard, 200% circuit breaker. Numbers picked as a starting
  point; revisit after 3 months of operation.
- **"Pause subscription" feature.** Stripe supports it;
  defer.
