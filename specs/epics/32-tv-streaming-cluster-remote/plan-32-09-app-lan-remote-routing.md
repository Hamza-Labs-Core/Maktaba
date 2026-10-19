# Plan 32.9 — LAN or remote in the apps — implementation

> Implementation plan for [story-32-09](story-32-09-app-lan-remote-routing.md).
> No migration. Design: [design.md](design.md) §8.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | The server tells the client whether it came through the relay (`remote.via_relay`). | The web app cannot know reliably; the relay agent can mark its loopback calls. |
| D2 | hls.js for both paths in browsers without native HLS. | Fixes the audit's P1-3 and is needed for Cloudflare HLS in Chrome/Firefox anyway. |
| D3 | Token swap by rewriting the token path segment in an hls.js loader. | No media reload, no visible glitch on refresh. |
| D4 | Native apps re-tune at `refresh_after` and swap at a segment boundary. | AVPlayer/ExoPlayer URL rewriting is costly; a short re-buffer every hour is acceptable. |
| D5 | Web first; native in phase C. | The web app is what is used today; native TV apps have no live channels yet (audit 27.9). |

## 1. Files

**Server**
- `api/internal/handlers/tv/tv.go` — `Info.Remote`; `viaRelay(r)`: header `X-Maktaba-Via: relay` from loopback, or a public client IP when `remote_detection = relay_or_public_ip`
- `api/internal/middleware` — strip `X-Maktaba-Via` unless `RemoteAddr` is loopback
- relay agent (Epic 25, `cloud/internal/cloudlink/proxy.go` `parseRequest`) — set `X-Maktaba-Via: relay` on loopback requests, dropping any client copy (coordinated with the Epic 25 work that wires the agent into `maktaba-server`)

**Web**
- `web/package.json` — `hls.js` (exact version pinned), dynamic `import()`
- `web/src/lib/liveHls.ts` — attach/detach, the token-rewriting loader, stall rejoin (audit P1-3 "no rejoin after a stall")
- `web/src/lib/remoteTv.ts` — tune / heartbeat / end; 202 retry; error mapping
- `web/src/pages/LiveTV/Player.tsx` — path choice, states and messages, surf handling (end old session first)
- `web/src/i18n/en.json`, `ar.json` — `live.remote.*`
- tests: `liveHls.test.ts`, `remoteTv.test.ts`, `Player.test.tsx`, Playwright `live-remote.e2e.spec.ts` (fake API + fake HLS origin)

**Native (phase C)**
- `apps/tv/android/.../data/api` — remote tune client; player swap at `refresh_after`
- iOS/tvOS (when the live player exists): the same through the 25.10 resolver
- `apps/mobile` (Capacitor) inherits the web player

## 2. Loader

```ts
class TokenLoader extends Hls.DefaultConfig.loader {
  load(ctx, cfg, cbs) {
    ctx.url = ctx.url.replace(/(cloudflarestream\.com\/)[^/]+(\/)/, `$1${currentToken()}$2`);
    super.load(ctx, cfg, cbs);
  }
}
```

`currentToken()` is updated by the heartbeat; the regex touches only the first path segment on
the Cloudflare host.

## 3. Tests

Unit: path choice table (`via_relay`, `available`), loader rewrite (only the token segment, only on
the Cloudflare host), heartbeat timer and `sendBeacon`, 202 retry with the same session, error
messages en/ar. E2E: remote tune plays; token swap mid-play → no `emptied`; 402 banner.

## 4. Rollout

Web behind `tv_remote.enabled`; LAN behaviour unchanged except hls.js (a fix). Native after the
web has run for a few weeks.
