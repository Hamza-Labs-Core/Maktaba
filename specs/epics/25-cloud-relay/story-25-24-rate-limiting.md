# Story 25.24 — Rate limiting & quota

> Epic 25 · Cloud relay · Phase 5 (operations)

## Description

Public-facing endpoints need limits to prevent (a) accidental DOS from
a misbehaving client and (b) deliberate abuse. This story defines
limits per-endpoint, per-actor, and the storage / response model
shared across the cloud.

Storage:

- **Redis** with the sliding-window-counters algorithm.
- Key shape: `rl:{scope}:{actor}:{bucket_minute}` →
  counter integer; expires after window.
- Redis is required (no degraded-mode "best effort" — when Redis
  is down, we fail-closed by returning `503 dependency_down` so
  attackers can't blast through during an outage).

Limits (default; can be raised in admin):

| Endpoint family                          | Per actor              | Limit / window     |
|------------------------------------------|------------------------|--------------------|
| `POST /api/auth/register`                | IP                     | 10 / hour          |
| `POST /api/auth/login`                   | IP + email             | 10 failures / 15 min |
| `POST /api/auth/forgot-password`         | IP + email             | 5 / hour           |
| `POST /api/auth/verify-email/resend`     | user                   | 5 / hour           |
| `POST /api/servers/claim`                | IP                     | 10 / minute        |
| `POST /api/billing/checkout`             | user                   | 30 / hour          |
| `POST /api/billing/webhook`              | (none — Stripe only)   | n/a                |
| Relay-via-`{user}.maktaba.app` (any)     | IP per subdomain       | 600 / minute       |
| Relay (free tier, total)                 | user                   | 100 / day (any)    |
| Relay streaming open                     | user                   | tier-specific (25.12) |
| `POST /api/push/devices`                 | user                   | 30 / hour          |
| `POST /api/push/dispatch`                | server                 | 1000 / hour (default), 10000 (trusted) |
| Admin endpoints                          | admin user             | 600 / minute       |

Algorithm:

- **Token bucket with sliding window.** Each call increments
  the actor's counter for the current minute and reads up to
  `N` minutes back depending on window size. We use a Lua
  script in Redis for atomicity (read-and-increment).
- **429 response** with headers:
  - `Retry-After: <seconds>` (always present)
  - `X-RateLimit-Limit: <N>`
  - `X-RateLimit-Remaining: <M>`
  - `X-RateLimit-Reset: <epoch>` (when `Remaining` rolls over)
- Body:
  ```json
  {"error":"rate_limit","retry_after":60,"limit":600,"window":"1m"}
  ```

Multi-tenant scoping:

- An IP scope is hashed and prefixed by `/24` (IPv4) or `/64`
  (IPv6) to spread across CGNAT pools fairly.
- A "user" scope is the JWT `sub`.
- A "server" scope is the server bearer token id.
- Combined scopes (`IP+email`) hash both fields together.

Operator overrides:

- Admin can raise limits per actor (`cloud_rate_limit_overrides`
  table) for known-trusted servers / users.
- Lowering (more restrictive) limits for known abusers also
  supported.

## Acceptance criteria

- **Given** an IP that has logged in 10 times in 15 min,
  **when** the 11th attempt arrives,
  **then** the response is `429` with `Retry-After: 900`.
- **Given** Redis is unreachable,
  **when** any limited endpoint is hit,
  **then** the response is `503 dependency_down` with
  `Retry-After: 30` and metric `rate_limit_dependency_failures` increments.
- **Given** a free-tier user has hit 100 relay requests today,
  **when** the 101st arrives,
  **then** the response is `429 daily_quota_exceeded` with
  `Retry-After` until midnight UTC.
- **Given** a server posts 1000 push events in an hour,
  **when** the 1001st arrives,
  **then** the response is `429`.
- **Given** an admin operator,
  **when** they hit 600 admin requests in a minute,
  **then** the response is `429`.
- **Given** an admin uplifts user U to a higher relay limit
  via `cloud_rate_limit_overrides`,
  **when** the override is in effect,
  **then** U's calls succeed up to the new ceiling.
- **Given** an IP makes 10 invalid claim attempts,
  **when** the 11th arrives,
  **then** the response is `429` and a
  `cloud_abuse_events kind=claim_token_brute` is recorded.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | Lua INCR atomicity | concurrent 1000 | exact count |
| T02 | integration | IPv4 /24 fairness | distribute across IPs | per-/24 limit honored |
| T03 | integration | IPv6 /64 fairness | distribute | per-/64 limit honored |
| T04 | regression  | Redis flap | observe | fail-closed 503 |
| T05 | unit        | sliding window crossing minute boundary | feed | smooth, no jump-back |
| T06 | integration | override raised limit | observe | new ceiling applied |
| T07 | regression  | clock skew between cloud pods and Redis | observe | bucket key uses Redis TIME |
| T08 | a11y        | rate-limited UI | render | shows friendly retry message |
| T09 | unit        | scope key collision (`user_X` vs `server_X`) | check | distinct keys via prefix |
| T10 | integration | DDoS simulator | observe | sustained 429s, no breach |

## Edge cases

- **CGNAT users sharing a /24.** Mobile carriers can put
  thousands of users behind one /24. Limits per IP are
  loose enough (10 logins / 15 min) that legitimate
  carrier traffic is fine; if not, we shift to per-IP-plus-email.
- **Behind Cloudflare.** The "true" client IP is in
  `Cf-Connecting-Ip`; we trust this header only when the
  request came from a Cloudflare edge IP (allowlist of CF
  ASNs). Otherwise the source IP is whatever the LB sees.
- **Bots learning thresholds.** Limits aren't secret; we
  publish them in API docs. Defense-in-depth via abuse
  signals (25.25), not via obscurity.
- **Stripe webhooks bypass limits.** Verified by signature;
  rate-limiting them would risk dropping legitimate
  events. They have an internal queue instead.
- **Sliding-window edge case.** Bucket-per-minute keys
  smooth at the minute boundary; users see a max
  ±20% variance from the configured limit.
- **Refund of "wasted" calls on client-side error.** Out
  of scope: a client that gets a 400 still consumed a
  request. Document.
- **Header/body symmetry.** Both the rate-limited 429
  body and the headers report the same numbers; no
  drift.
- **Distributed Redis cluster failover.** A primary
  swap (~5s) loses no buckets thanks to Redis Sentinel;
  Lua scripts are replicated.
- **Time of day bursts.** Free-tier 100/day is a flat
  quota; resets at midnight UTC. We don't track
  "rolling 24 hours".

## Files / packages

- `cloud/internal/ratelimit/redis.go` — Lua scripts +
  client.
- `cloud/internal/ratelimit/middleware.go`.
- `cloud/internal/ratelimit/policies.go` — endpoint→policy
  mapping.
- `cloud/migrations/00080008_rate_limit_overrides.sql`.

## Open questions

- **Per-region rate limiting.** Out for v1 (single region).
- **Captcha layer.** v2; we don't ship CAPTCHAs in v1
  (friction tax) — if abuse rises we layer Turnstile.
