# Story 50.27 — Guest viewers: the watch pages and the Maktaba apps

> Epic 50 · Phase B · Depends on: 50.26, Epic 34.5 (site UI), Epic 34.6 (channel switching) ·
> Needed by: the broadcasting gate

## Why

Guests watch on the web or on a TV. Epic 34 already has a site UI and `@maktaba/tvplayer` (on
main since PR #87) for one home's public site. This story makes the UI multi-tenant on
`watch.maktaba.tv`, and adds **Watch a broadcast** to every Maktaba Player and TV app. Every
string ships in English and Arabic together.

## Description

### 1. The watch pages (`watch.maktaba.tv`)

| Page | What |
|---|---|
| `/<lang>/<publisher>` | The publisher's channel grid: the channels the guest's pass or the open session allows, with now and next |
| `/<lang>/<publisher>/<channel>` | Player, now and next, today's guide, attribution for CC items (50.23), the Report button (50.24) |
| `/<lang>/<publisher>/guide` | The EPG grid for the publisher's channels |
| `/i/<invite>` | Redeem an invite (50.26), then go to the channel or the grid |
| `/pair` | Type a TV short code on a phone (§2) |
| `/<lang>/legal/*` | Copyright (50.24), terms, privacy |

- **Language:** Arabic (RTL) by default, and English. The `/ar/` and `/en/` prefixes decide;
  otherwise `Accept-Language` (Epic 34 R5.3).
- **Player:** `@maktaba/tvplayer`, with a resolver over `/api/v1/play` and neighbour tokens (Epic
  34 R6). It switches channels with up/down, swipe, number entry and the guide. The tuning card
  shows while an on-demand channel wakes (50.22).
- **The page shows the video as it is:** the watermark and strip are burned in, and no logo is
  overlaid.
- **"Free to watch"** in the footer (50.26). The publisher's display name is "{publisher} on
  Maktaba".
- **No directory and no cross-publisher links.** A page links only to the same publisher's
  channels. Maktaba never recommends or features channels (README §14.6).
- **Accessibility:** axe-clean, keyboard and screen reader, and 360 px wide (Epic 34 R5.2).
- **Branding per publisher:** a name and a logo the home publishes with the lineup. Colours
  stay Maktaba's in the first version.

### 2. The apps: Watch a broadcast

| App | Entry | How the guest gets in |
|---|---|---|
| Apple TV (tvOS) | Home → **Watch a broadcast** | type the short code with the remote, or show a QR code that opens `watch.maktaba.tv/pair?tv=<nonce>` on a phone, where the guest taps an invite or opens the channel; the TV receives the pass over a long-poll (`/api/v1/pair/<nonce>`, 10 min) |
| iOS, iPadOS | open an invite link (universal link) or scan a QR code; the menu → Watch a broadcast | the pass goes in the keychain |
| Android, Android TV | the same (app links, QR code, short code); Android TV uses the code and phone pairing flow | the pass goes in the keystore |
| Desktop player, web app | open the link | cookie or local store |

- The guest flow needs **no Maktaba or home account**. It is a separate space in the app: the
  guest sees only that publisher's channels and guide, and none of the app's home features.
- Several publishers can be saved. **Saved broadcasts** lists them, with a "Remove" button.
- **Native players** play the HLS from `live.maktaba.tv` with the token in the path, so no
  headers are needed. They heartbeat through the play API.
- The Report button is in the player's menu on every app.
- Every string ships in English and Arabic (Apple `Localizable.xcstrings`, Android `values`
  and `values-ar`, the web `en.json` and `ar.json`).

### 3. States the guest can see

`live`, `tuning`, `offline` (the home is offline: the slate plays and a note shows), `off_air`,
`full`, `geoBlocked`, `ended` (unpublished), `inviteUsed`, `inviteExpired`, `revoked`. Each has
an EN/AR string and a "try again" action where it helps.

## Acceptance criteria

- **Given** an invite link opened on a phone,
  **when** the page loads in Arabic,
  **then** it shows RTL, the channel plays within 3 s (p75), and swiping switches channels in
  under 2 s.
- **Given** an Apple TV with the app,
  **when** the guest types a valid short code,
  **then** the channel plays, and the broadcast is saved for next time.
- **Given** the QR code flow on Android TV,
  **when** the guest opens the invite on their phone,
  **then** the TV starts playing within 10 s.
- **Given** a CC BY programme,
  **when** it plays,
  **then** the page and the app show its attribution.
- **Given** the home goes offline,
  **when** the guest is watching,
  **then** the slate plays and the offline note shows, with no error dialog.
- **Given** a publisher page,
  **when** it renders,
  **then** it contains no link to any other publisher.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (web, vitest) | resolver | play, neighbours, a 429, a 451, an ended channel | the right state and string |
| T02 | e2e (Playwright, CI) | edge in Miniflare + fake home | invite → play → switch → report | works in `ar` and `en`; axe clean |
| T03 | unit (Swift, MaktabaKit) | guest pass store | save, list, remove, expire | keychain contents right |
| T04 | unit (Kotlin, Robolectric, logging=stdout) | guest pass store, the pairing poll | the same | the same |
| T05 | UI test (CI only, a throwaway tvOS simulator) | the app | type a short code; the QR pairing with a fake phone call | plays |
| T06 | UI test (CI only, Android TV emulator) | the app | short code | plays |
| T07 | unit | no-directory rule | render publisher pages | no foreign-publisher link |
| T08 | Arabic | every new key in every app | strings check | present |

## Edge cases

- **A guest pass expires while watching.** The heartbeat renews it if the invite is still valid.
  Otherwise the player shows `inviteExpired` at the next refresh.
- **A TV has no browser for the QR code.** The short code always works.
- **A deep link opens in a browser without the app.** The page plays in the browser and offers
  the app.
- **A slow network.** The player's ABR picks the lowest rung, and the master playlist lists only
  the rungs the mode allows (50.20).

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `guest.watchBroadcast` | Watch a broadcast | شاهد بثًا |
| `guest.enterCode` | Enter the code from the broadcast's page | أدخل الرمز الظاهر في صفحة البث |
| `guest.scanQr` | Or scan this code with your phone | أو امسح هذا الرمز بهاتفك |
| `guest.saved` | Saved broadcasts | البثوث المحفوظة |
| `guest.remove` | Remove | إزالة |
| `guest.onMaktaba` | {publisher} on Maktaba | {publisher} على مكتبة |
| `broadcast.homeOffline` | This channel's home is offline. It will be back when the home reconnects. | المنزل الذي يبث هذه القناة غير متصل. ستعود القناة عند عودة الاتصال. |
| `broadcast.ended` | This broadcast has ended. | انتهى هذا البث. |
| `broadcast.tuning` | Starting the channel… | جارٍ تشغيل القناة… |
| `guest.tryAgain` | Try again | حاول مرة أخرى |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `web/packages/broadcast-edge/site/` (new, based on Epic 34.5's site UI), `web/packages/tvplayer` (a host resolver only) | Multi-tenant pages, pairing, report. |
| `apps/` Apple (`MaktabaKit` guest module, tvOS and iOS screens, `Localizable.xcstrings`) | Watch a broadcast. |
| `apps/` Android and Android TV (guest module, `values/strings.xml`, `values-ar/strings.xml`) | Watch a broadcast. |
| the desktop player | Open link. |
| `docs/broadcasting.md` | The guest's side. |

### Tests

- T01, T07 and T08 in the web and app unit jobs. T02 in `_e2e.yml`, path-filtered.
- **CI only:** T05 (`_apple-apps.yml`, one throwaway simulator, one UI run at a time) and T06
  (`_android-tv.yml`). No local Mac UI tests.

### Rollout

Web first (with the canary and the owner's invite channel). Then all the apps in one release
train, with English and Arabic together.

### Rollback

The apps hide **Watch a broadcast** behind a remote flag (`guest.enabled`). The web pages can show
"temporarily unavailable" while the edge stays up.

### Dependencies

50.26 (access, tokens). Epic 34.5 and 34.6 (site UI, tvplayer).
