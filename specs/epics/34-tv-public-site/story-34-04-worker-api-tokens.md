# Story 34.4 — Worker API, tokens and SiteControl

## Description

*As a visitor, I want to press play and get the channel from Cloudflare straight away; as the owner, I
want the site to decide who may watch without ever calling my home, and to stop everything with one
switch.*

Design: [design.md](design.md) §6; README R4.

## Acceptance criteria

- **AC1** The Worker serves the SPA (static assets), `/api/v1/*`, `/embed/*`, `sitemap.xml`,
  `robots.txt`, and `/_home/*` (HMAC over method, path, timestamp and body hash; 60 s window; nonce cache).
- **AC2** `POST /api/v1/play` follows design §6.3: access → off-air / home connected → channel on air
  (or wake with 202 in `top_n`) → budget → concurrency, per-IP and daily limits → session, token and
  neighbour tokens. Responses and error codes as in the README API block.
- **AC3** Tokens: RS256 with the site key from Worker secrets; `sub` = active input id; `exp` = now +
  `token_ttl_minutes`; `nbf` = now − 30; optional `ip.src` and country rules ending in `any: block`;
  verified in tests against the key's public JWK. Tokens are never logged.
- **AC4** Heartbeat every 60 s (15 s while the channel rotates) refreshes the token after half its life,
  returns the new URL after a rotation, and the session ends after 150 s of silence.
- **AC5** The home's control WebSocket (`/_home/ws`, hibernating, auto ping/pong) carries the messages of
  design §6.6; one home connection per environment; reconnect with backoff; the long-poll fallback works
  when WebSockets are blocked.
- **AC6** Home not connected for 60 s → every channel `off_air(home_offline)` on the site and no tokens
  minted; the guide is still served.
- **AC7** Panic off-air → all `play`/`beat` calls answer 503 within 5 s; the home rotates the site
  signing key (create new → set secret → delete old) so every outstanding token stops working; total < 30 s.
- **AC8** `SiteControl` state (snapshot, channels, sessions, counters, budget, flags) persists in DO
  storage and survives eviction or restart; no IP is stored (salted daily hash only).
- **AC9** `LiveHub` shards fan commits and state changes out to open pages; 1,000 simulated pages get an
  event in < 1 s.
- **AC10** HTML routes get title, description, OG, `hreflang`, canonical, JSON-LD and `lang`/`dir`
  injected with `HTMLRewriter`; responses carry the security headers of README R11.4.

## Test cases

- **TC1** `hmac.test.ts` (valid, skewed, replayed, wrong body).
- **TC2** `play.test.ts` (each gate in order; neighbours).
- **TC3** `tokens.test.ts` (claims, rules, signature checked with the JWK).
- **TC4** `sitecontrol.test.ts` (Miniflare: session expiry alarm, counters, persistence across restart).
- **TC5** `homews.test.ts` (hello/channels/rotate/off_air; replacement connection; fallback poll).
- **TC6** `livehub.test.ts` (sharding, broadcast, hibernation auto-response).
- **TC7** Go: `TestControlClientReconnect`, `TestSiteKeyRotation` (fake CF API).

## Edge cases

- **EC1** A second home connects (misconfiguration): rejected unless it presents the same instance id;
  the admin is alerted.
- **EC2** Clock skew on the home > 60 s: HMAC fails with `clock_skew`, shown on the admin page.
- **EC3** A viewer's IP changes mid-session (mobile): the heartbeat re-mints with the new IP rule; the
  session counter moves to the new hashed IP.

## Dependencies

34.1, 34.3 (input ids, live state); Epic 32.6 (Stream keys API client), 32.8 (token format).
