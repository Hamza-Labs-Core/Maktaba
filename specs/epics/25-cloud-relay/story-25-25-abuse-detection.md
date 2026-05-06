# Story 25.25 — Abuse detection & response

> Epic 25 · Cloud relay · Phase 5 (operations)

## Description

A small set of detectors that watch for behaviors hostile to other
users or to the platform. This story covers detection, scoring,
quarantine, and human-review queues. We are not building Cloudflare;
we are building the minimum that keeps a $9.99 service from being
abused as an HTTP proxy or a bandwidth piggyback.

Detectors:

- **`relay_host_abuse`.** A request through `{user}.maktaba.app`
  whose `Path` doesn't match Maktaba's known route shapes
  (`/api/...`, `/stream/...`, `/ws/...`, `/_health`, `/`,
  `/manifest.json`, `/favicon.ico`, `/robots.txt`,
  `/.well-known/...`). High false-positive risk; we only flag
  on > 100 unknown paths in 5 min from one server.
- **`bandwidth_anomaly`.** A server's hourly bandwidth
  > 10× its 7-day moving average AND > 5 GB. Often a HotLink
  scenario: someone shared a direct stream URL widely.
- **`port_scan_via_relay`.** Multiple distinct destination
  ports / paths from the same IP in < 60s. Defends against
  using the relay as an open-proxy probe (we never proxy
  arbitrary ports — only HTTP/S to the user's server, but
  a vector still exists at the path level).
- **`claim_token_brute`.** > 10 invalid claim tokens / minute
  / IP (already enforced in 25.6 / 25.24; this story adds
  scoring).
- **`oauth_state_mismatch`.** Reused / fabricated state
  cookies on OAuth callback (25.3 / 25.4).
- **`refresh_token_replay`.** Old refresh token used after
  rotation (25.2).
- **`signup_velocity`.** > 10 sign-ups from one /24 in 5 min;
  often bots.
- **`payment_chargeback`.** Stripe `charge.dispute.created`
  (25.14).
- **`tunnel_flap`.** > 100 tunnel reconnects/hour for one
  server (often a misconfigured server but sometimes a
  byzantine attempt to escape state).
- **`stream_path_oddity`.** Streaming URLs with abnormally
  long paths (> 1 KiB) or non-Maktaba path patterns.

Each detection writes a `cloud_abuse_events` row with
`kind`, `severity` (1-5), `user_id` (if attributable),
`server_id`, `payload_jsonb` (relevant request features —
sanitized to avoid storing the abuse content itself), and
`resolved_at`.

Scoring & escalation:

- A user/server's "abuse score" = sum(severity × decay) over
  90 days. Decay halves weekly.
- Score thresholds:
  - 10+ → admin notification (Slack #ops alert).
  - 25+ → automatic streaming-rate halving.
  - 50+ → automatic suspension; manual review unblocks.
- Manual override: admins can reset score to 0 with reason.

Response actions:

- **Suspension.** Sets `cloud_users.suspended_at`; tunnels
  closed; relay rejects with `503 server_suspended`. User
  receives an email with appeal link.
- **Rate halving.** Adds a `cloud_rate_limit_overrides` row
  with halved limits.
- **Hot-link mitigation.** If `bandwidth_anomaly` fires, the
  relay starts requiring a `Referer:` matching the
  `username.maktaba.app` (or an in-app token) for streaming
  endpoints — drops cold deep-link traffic. Affected users
  see the warning in their dashboard with a "click here to
  reset" button.

## Acceptance criteria

- **Given** a server emits 200 unknown-path requests in 5
  min,
  **when** the detector runs,
  **then** an `cloud_abuse_events kind=relay_host_abuse,
  severity=2` row is created.
- **Given** a server's hourly bandwidth jumps to 50 GB
  (vs. 5 GB average),
  **when** the detector runs,
  **then** `bandwidth_anomaly, severity=3` is recorded
  and hot-link mitigation activates within 5 min.
- **Given** a user accumulates 50 abuse points,
  **when** the scorer runs,
  **then** the user is suspended and an email is queued.
- **Given** the user disputes the suspension,
  **when** an admin clears it,
  **then** the score resets and the suspension lifts;
  audit row.
- **Given** a chargeback fires,
  **when** webhook 25.14 routes to abuse,
  **then** `kind=chargeback, severity=5` is recorded and
  the user is suspended immediately.
- **Given** a server reconnects 200 times/hour,
  **when** the detector observes,
  **then** `tunnel_flap, severity=2` is recorded; the user
  is emailed a "your server seems unstable" notice.
- **Given** abuse scores have decayed (90 days passed),
  **when** the scorer recomputes,
  **then** old events contribute negligibly.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | unknown path detector | feed 100 paths | flagged |
| T02 | unit        | path pattern allow-list | feed `/api/libraries` | not flagged |
| T03 | integration | 7-day MA computed | run | matches expected |
| T04 | integration | bandwidth anomaly | trigger 10x | hot-link mode on |
| T05 | regression  | refresh-token replay | fire | severity=4, abuse row |
| T06 | unit        | scorer decay | feed events at varying ages | weekly halving |
| T07 | regression  | suspension lift via admin | observe | tunnels reopen on next reconnect |
| T08 | unit        | chargeback severity = 5 | check | yes |
| T09 | a11y        | suspension email content | review | screen-reader friendly |
| T10 | integration | hot-link block | request without Referer | 451 with token-redirect link |

## Edge cases

- **False positives are inevitable.** Hot-link mitigation
  is reversible from the user's dashboard ("This was me —
  reset"). Every auto-action surfaces in the user's UI
  with explanation.
- **Privacy of abuse payload.** We never store URL paths
  containing query strings (which can carry tokens or
  filenames). Sanitize to `(method, path-shape, status)` only.
- **Cluster rebalancing risk.** Hot-link mode rejects
  legitimate deep-links from email; users can opt out
  in their privacy settings.
- **DDoS reflection.** A user could trick us into pushing
  a lot of pushes by sending bogus events. Push ingest
  (25.17) has its own limits.
- **Geographic anomalies.** Out for v1; we don't IP-geo
  log-in to flag "Lagos at 3am".
- **Account-share detection.** "Same account streaming
  from 5 cities" → tier enforcement (25.12) handles
  bandwidth; account-share isn't an abuse class.
- **Audit storage cost.** `cloud_abuse_events` rows
  retained 90 days for unresolved, 365 days for
  resolved. Older rows summary-rolled and dropped.
- **Admin self-abuse.** Limit on admin actions (25.20)
  prevents accidental fleet-wide suspensions; mass
  actions require a "I really mean it" type-the-count
  confirm.
- **Backpressure on hot-link mode.** Sometimes a user's
  legitimate "share with my mom" link is the one
  triggering the alert. We err on user-resettability
  (one click to undo).

## Files / packages

- `cloud/internal/abuse/detectors.go` — each detector.
- `cloud/internal/abuse/scorer.go` — scoring + decay.
- `cloud/internal/abuse/respond.go` — automated actions.
- `cloud/internal/relay/hotlink.go` — Referer gate
  middleware (off by default).
- `cloud/migrations/00080008_abuse_events.sql`.

## Open questions

- **Bot-signup defenses.** Cloudflare Turnstile or
  hCaptcha at sign-up? Defer; activate if velocity
  detector trips frequently.
- **Account warming.** Some legitimate users will be
  "new" on day 1 and look bot-like. Don't gate on age.
