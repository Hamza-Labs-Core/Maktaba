# Plan 33.6 — Staging preview — implementation

> Implementation plan for [story-33-06](story-33-06-staging-preview.md).
> Design: [design.md](design.md) §7. No migration.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Preview through the front door, not a direct slot URL. | Slots stay loopback-only; one auth point. |
| D2 | HMAC preview token `pt` (1 h) besides the admin session. | VLC/test Plex cannot carry a web session. |
| D3 | Optional second HDHomeRun device. | Lets the owner try a change in a test Plex; off by default to avoid confusing the real Plex. |

## 1. Files

**Create**
- `streaming/internal/frontdoor/preview.go` — `/stream/staging/…`, `/staging/lineup.m3u`, `/staging/xmltv.xml`
- `streaming/internal/hdhr/staging_device.go` — second device (reuses handlers with a release-bound repo)
- `api/internal/handlers/tvchange/preview.go` — token minting

**Modify**
- `cmd/maktaba-server/stream_gate.go` — allow `/stream/staging/channel/{uuid}/…` for admin sessions (`GET /api/auth/me` role check, cached)
- `streaming/internal/hdhr/guide.go` — render for a given generation/lineup

## 2. Steps

1. Token: `HMAC(cluster stream key, change_id|exp)`.
2. Front door preview routes → staging placement (warm if missing).
3. Gate rule for admin role.
4. Staging device behind `preview_hdhr`.

## 3. Tests

As in the story; gate test uses the existing `serve_proxy_test.go` patterns.

## 4. Rollout

Phase B.
