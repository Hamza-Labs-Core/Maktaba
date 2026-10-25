# Epic 42 — The apps: Player, Remote and Server

> **Status:** spec. **Source:** `specs/epics/42-apps-player-remote-server/`.
> **Anchors:** `apps/desktop` (Tauri 2), `apps/mobile` (Capacitor), `apps/tv` (tvOS, Android TV),
> `web/` (the SPA every web-based app shows), `cloud/` (Epic 25: the hosted service and its relay),
> [Epic 36 — Maktaba.app](../36-mac-app/README.md), [Epic 38 — Windows app](../38-windows-app/README.md),
> [Epic 39 — Linux app](../39-linux-app/README.md).

## Goal

The owner's decisions (2026-10-25):

1. **Three kinds of app.**
   - **Player:** browse and watch, nothing else, like the TV apps.
   - **Remote:** manage a home server from elsewhere: processing, transcription, settings,
     users, Live TV, machines, updates, and the rest of the admin pages.
   - **Server:** runs Maktaba itself (the API, streaming, the pipeline, the database).
2. **Which apps exist:**

| Surface | Player | Remote | Server |
|---|---|---|---|
| Phone, Android and iOS | Maktaba Player | Maktaba Remote | — |
| Foldable, Android and iOS | Maktaba Player (foldable) | Maktaba Remote (foldable) | — |
| Tablet, Android and iOS | Maktaba Player (tablet) | Maktaba Remote (tablet) | — |
| Desktop, macOS (Apple silicon), Windows, Linux | Maktaba Player | — | Maktaba (the server app of Epics 36, 38, 39) |
| TV, tvOS and Android TV | Maktaba TV (today's apps) | — | — |
| Web served by the home | — | — | part of the server: its web UI is player and remote together |
| Cloud web (the VPS, `cloud/`) | yes, once a home is connected | yes, once a home is connected | — |

   That is six mobile apps on each of Android and iOS (player and remote for phone, foldable and
   tablet), a desktop player beside the desktop server, the TV player, the home's own web UI, and
   the cloud web that is a player and a remote for a connected home.
3. **Macs are Apple silicon only** (no Intel builds since v0.3.15).

## What exists (main, 2026-10-25)

| Piece | Today | Gap |
|---|---|---|
| Web SPA (`web/`) | One build: every route, eagerly imported (`web/src/App.tsx`, no `React.lazy`); Processing, Transcription and Settings in every user's nav, the Administration section for admins (`components/AppShell.tsx` `NAV`, `ADMIN`) | No edition: a player cannot leave the admin code out; viewers see server pages |
| Desktop (`apps/desktop`) | `mode.json`: Server or Client (a home's URL); client-only when no server payload is bundled (`src/mode.rs`, `agent.rs` `payload_present`); mDNS scan `discover_servers` exists (`src/lib.rs`) but nothing calls it; "Switch Server…" emits an event nothing listens for | No player identity, no server picker, one bundle id `com.hamzalabs.maktaba` and one updater feed for every desktop build |
| Phone (`apps/mobile`) | Capacitor wrapping `web/dist`, bundle id `com.hamzalabs.maktaba` | **It cannot reach a home:** the API base is build-time `VITE_API_BASE`, which the release never sets, so a released app calls `/api` on its own origin; no picker, no discovery; no tablet or foldable layouts or listings |
| TV (`apps/tv`) | Native players; the server URL typed in Settings; username and password sign-in | No discovery, no sign-in by code |
| Pairing (`api/internal/handlers/discovery/pairing.go`) | A signed-in device asks for a code; a phone redeems it (`maktaba://pair?code=…`) | No "show a code on the new app, approve it from a signed-in one" |
| Cloud (`cloud/`) | Hosted identity, a relay that reaches a home through its subdomain (`internal/handlers/relay`), server claim (`/v1/servers`) | No player or remote web UI of its own: the relay shows the home's own UI |
| Server-side rights | Admin pages and actions check `is_admin` on the server (queue actions `requireAdmin`, settings `IsAdmin`) | Some reads (the queue snapshot) are open to every user, scoped to their libraries; fine for the remote, not shown in the player |

## Shape

### 1. One SPA, editions at build time

`VITE_MAKTABA_EDITION` = `full` (the home's own web UI, the default), `player` or `remote`.

- **Routes:** viewer routes (library, series, video, watch, search, TV guide and live, history,
  account, help) are in every edition. Server routes (`/queue*`, `/transcription`, `/settings/*`,
  `/admin/*`) are in `full` and `remote` only, loaded with `React.lazy`, so a player bundle does not
  contain their code (a build check fails if it does).
- **Nav:** the player shows only viewer items. The remote opens on the home's status (health,
  processing, what needs attention) and keeps watching available. `full` shows both, with the
  server pages in one "Manage" section for admins only (Processing, Transcription and Settings move
  there from the main nav).
- **Rights stay on the server.** An edition only hides; the API keeps refusing non-admins.
- **Remote and non-admins:** a non-admin who signs in to a remote app is told it is for the
  home's admins, with a link to the player.

### 2. Connecting to a home (every app but the server)

A player or remote app is not tied to one origin. On first start, and from "Switch home", it shows
a **home picker**:

- **On this network:** the homes found by mDNS (`_maktaba._tcp`); the desktop app's
  `discover_servers` already scans; the mobile apps need a zeroconf plugin; the TV apps get the
  same scan later.
- **Through Maktaba Cloud:** sign in with the cloud account and pick a connected home; the app then
  talks to the home through the relay (its subdomain), from anywhere.
- **By address:** type the home's URL, or scan its QR code.

The chosen home's base URL is a **runtime** setting (replacing build-time `VITE_API_BASE`): the
SPA's `api.ts`, `ws.ts` and `clusterSocket.ts` take it from the shell (desktop `mode.json`,
mobile preferences). The app remembers several homes.

### 3. Signing in

- **Username and password**, as on the TV apps today.
- **By code** (new): the app shows a short code; a signed-in device (the home's web UI, or a remote
  app) approves it. New endpoints next to pairing (`/api/pairing/…`); the TV apps adopt it too.

### 4. The apps

| App | Built from | Identity | Notes |
|---|---|---|---|
| Desktop Player | `apps/desktop`, no server payload, edition `player`, mode fixed to Client | `com.hamzalabs.maktaba.player`, "Maktaba Player" | its own `mode.json` folder (from the identifier), its own updater feed |
| Desktop Server | today's app (Epics 36, 38, 39), edition `full` | `com.hamzalabs.maktaba` | unchanged |
| Mobile Player, phone / foldable / tablet | `apps/mobile`, edition `player`, layouts per form factor | `com.hamzalabs.maktaba.player`, `.player.foldable`, `.player.tablet` | six store listings per platform with the remote ones |
| Mobile Remote, phone / foldable / tablet | `apps/mobile`, edition `remote` | `com.hamzalabs.maktaba.remote`, `.remote.foldable`, `.remote.tablet` | admin push notifications (failures, health) |
| TV | `apps/tv` | `com.hamzalabs.maktaba.tv` | gains the home picker and sign-in by code |
| Cloud web | `cloud/` serving the SPA, edition by URL (`/play`, `/manage`) | the cloud domain | talks to the connected home through the relay |

**Form factors:** the foldable and tablet apps share the phone app's code. What differs is the
layout: two panes (library list and video, or queue and detail) on wide screens, and a foldable's
fold posture (the player on one half, controls on the other). Each is its own store listing, as
decided. A platform without foldables (iOS today) has no foldable listing until one exists.

### 5. Releases and updates

- `desktop-release.yml` gains player legs (macOS arm64, Windows, Linux) beside the server legs.
  Each family publishes its own feed: `latest.json` (server apps) and `latest-player.json`
  (players), with distinct platform keys, so a player never takes a server bundle.
  `updaterfeed.Required` covers each family.
- `mobile-release.yml` builds each edition and form factor with its own bundle id, signing and
  store metadata; sideloaded builds update through their own feed (`VITE_DISTRIBUTION`).
- Apple silicon only on macOS.

## Stories (in order)

1. **42.1 Editions:** `VITE_MAKTABA_EDITION`, lazy server routes, nav per edition, a bundle check
   that the player has no server code, "Manage" section in `full`.
2. **42.2 Runtime home:** the API base as a runtime setting from the shell; several homes
   remembered.
3. **42.3 Home picker:** mDNS (desktop now, mobile with a plugin), by address, QR.
4. **42.4 Desktop Player:** identity, mode fixed to Client, release legs, `latest-player.json`.
5. **42.5 Mobile Player and Remote (phone):** two editions, bundle ids, store builds.
6. **42.6 Sign-in by code:** endpoints, the approve screen, mobile, desktop and TV.
7. **42.7 Through the cloud:** pick a connected home via Maktaba Cloud, talk through the relay.
8. **42.8 Tablet and foldable:** two-pane and fold-aware layouts, their own listings.
9. **42.9 Cloud web player and remote:** the cloud serves the SPA editions for a connected home.
10. **42.10 TV:** the home picker and sign-in by code on tvOS and Android TV.

## Open questions

- Store accounts and names: one developer account per store for all listings; app names in Arabic
  and English.
- Whether a remote app may also play (the table says remote only; watching from the remote could
  link to the player instead).
- Free and paid editions (billing, Epic 25) per app.
