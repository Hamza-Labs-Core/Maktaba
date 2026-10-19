# Story 34.6 — Channel switching (controls and speed)

> **Status:** the switching module is **implemented on main** as `@maktaba/tvplayer`
> (`web/packages/tvplayer`, PR #87, delivered with Epic 32.9) and already drives the Maktaba web
> live player on the LAN and remote paths. This story is what remains for the public site: wiring
> the module into `web/tv-site` with a public resolver. The controls, timings and behaviour below
> are the module's; they are not re-specified or re-implemented here.

## Description

*As a viewer of the public site, I want to flip channels like on a TV — up/down, a number, last
channel, a tap in the guide or a swipe on my phone — and see the new channel almost at once, never a
black screen, exactly as the Maktaba app already does.*

Design: [design.md](design.md) §8; README R6.

## What the module already provides (main)

Key map incl. TV remotes and RTL (`keymap.ts`), vertical swipe (`gestures.ts`), channel ring with
last channel (`ring.ts`), number entry, the `Switcher` (tuning card at once, 300 ms surf commit,
one tune in flight, make before break, return after an error), the two-element `VideoPipeline`,
the neighbour playlist `Prefetcher` (playlists only, never segments; paused in background),
`TuningCard` / `NumberOverlay` / `MiniGuide`, `SwitchEvent` telemetry and `useTvSwitcher`. Its tests
(`keymap.test.ts`, `swipe.test.ts`, `switcher.test.ts`, `pipeline.test.ts`, `prefetch.test.ts`) are
on main.

## Acceptance criteria (public site)

- **AC1 Public resolver.** `web/tv-site` supplies the module's `Resolver` over `POST /api/v1/play`
  (34.4): `Stream {url, release, path:"public"}`; `release` ends the session (`DELETE
  /api/v1/play/{session}`, `sendBeacon` on page hide); `202 tuning` → `ctx.status("starting")` and a
  retry after `retry_after`; `403/429/503` → a `SwitchError` whose code the card shows (off air,
  limit, budget).
- **AC2 Neighbour tokens.** The `neighbours[]` of the last `play`/heartbeat response (previous, next,
  last channel) are cached with their expiry; tuning a neighbour with a valid token makes no API
  call; the `Prefetcher` gets their playlist URLs (playlists only: Stream bills segments).
- **AC3 Token refresh.** Heartbeat URLs (half-life refresh, A/B rotation, R4.3) reach the playing
  stream through `Stream.onUrl` / `rewrite`, without a reload.
- **AC4 Warm state.** `isWarm` comes from the LiveHub live state (`on_air`); with the default
  `all_always_on` every public channel is warm: first frame < 2 s after the input (p75 broadband).
  Cold channels (`tiering = top_n` only): card at once, "Starting channel…", video < 20 s (34.3 spike).
- **AC5 Deep links.** Every switch updates the URL to `/<lang>/watch/<slug>` (history `replace` for
  rapid surfing, `push` when settled ≥ 5 s); a cold load of that URL plays the channel.
- **AC6 Telemetry.** The site's telemetry sink posts each `SwitchEvent` to the Worker, which writes it
  to Analytics Engine; the admin usage panel (34.9) shows p50/p75/p95.
- **AC7 Picture as encoded.** The player adds no logo or watermark overlay: watermark and channel strip
  are burned into the video; the tuning card and mini-guide use the published channel logos.

## Test cases

- **TC1** `publicResolver.test.ts`: play → Stream; neighbour hit without a request; 202 → status +
  retry; 403/429/503 → `SwitchError` codes; release sends DELETE.
- **TC2** `heartbeat.test.ts`: a new URL from a beat reaches `onUrl` / `rewrite`.
- **TC3** Playwright `switching.spec.ts` against `wrangler dev` and a local HLS fixture: warm switch
  < 2 s, no black frame, URL updated, no neighbour segment requested.

## Edge cases

- **EC1** Neighbour token expired: the resolver calls `play` before loading.
- **EC2** The target goes off air while tuning: the card shows the reason; the module returns to the
  previous channel after its error delay.
- **EC3** Embed (`/embed/<slug>`): one channel only, switching controls hidden.

## Dependencies

`@maktaba/tvplayer` (main, PR #87); 34.4 (play/beat with neighbours, LiveHub state), 34.5 (Watch page).
