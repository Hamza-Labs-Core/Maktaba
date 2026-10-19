# Plan 34.6 — Channel switching — implementation

> Implementation plan for [story-34-06](story-34-06-channel-switching.md). Design: [design.md](design.md) §8.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | One package `@maktaba/tvplayer` for the public site, Maktaba web (27.7) and remote (32.9). | Same controls and speed everywhere; one set of tests. |
| D2 | Two `<video>` elements, make before break. | No black frame; cut only when the new channel can play. |
| D3 | Prefetch playlists of neighbours, never segments. | Saves the manifest round trip; Stream bills segments (and preloaded segments), not playlists. |
| D4 | Host app supplies `resolve(channel)`. | Public site → `/api/v1/play`; LAN → local HLS; remote → `/api/tv/remote/tune`. |
| D5 | Always-on public channels → the warm path is the default. | Target < 2 s first frame. |

## 1. Files

**Create** (`web/packages/tvplayer/`)
- `src/keymap.ts` (keyboard, TV remotes, RTL), `src/gestures.ts` (vertical swipe, tap)
- `src/ring.ts` (channel order, wrap, last channel), `src/numberEntry.ts`
- `src/switcher.ts` (state machine: idle → carding → loading B → swap; debounce 300 ms; one in flight)
- `src/pipeline.ts` (two hls.js instances, lazy import, `startPosition: -1`, `canplay` + buffered check)
- `src/prefetch.ts` (playlist fetch of neighbours every 10 s, paused in background)
- `src/overlay/*` (tuning card, number overlay, mini-guide), `src/react.tsx` (binding)
- `src/telemetry.ts` (`switch` events to a host-provided sink)
- tests + Playwright fixture (local HLS generated with ffmpeg in CI)

**Modify**
- `web/src/pages/LiveTV/Player*` (Epic 27.7 player) — use the package with the LAN resolver
- Epic 32.9 remote player — use the package with the remote resolver
- `web/tv-site/src/pages/Watch.tsx` — use the package with the public resolver
- `web/package.json` / workspace — add `packages/*`

## 2. Steps

1. Key map, gestures, ring, number entry (unit tests first).
2. Switcher state machine with a fake pipeline; debounce; telemetry.
3. hls.js pipeline with two elements; make-before-break; error retry with the same URL.
4. Prefetch of neighbour playlists; verify no segment requests.
5. Overlays (card, mini-guide) with RTL; integrate into tv-site, then Maktaba web 27.7, then 32.9.
6. Playwright switching timing test (< 2 s warm on the fixture).

## 3. Tests

Story TCs.

## 4. Rollout

With Phase B. The Maktaba web player switches to the package behind a flag for one release, then the
flag is removed.
