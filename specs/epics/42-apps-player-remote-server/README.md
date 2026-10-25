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
| Maktaba service admin (the VPS, `cloud/`) | — | — | — (it runs the service itself: see Roles) |

   That is six mobile apps on each of Android and iOS (player and remote for phone, foldable and
   tablet), a desktop player beside the desktop server, the TV player, the home's own web UI, and
   the cloud web that is a player and a remote for a connected home.
3. **Macs are Apple silicon only** (no Intel builds since v0.3.15).
4. **The Maktaba service admin is its own role and surface**, not to be confused with a home's
   admins (see Roles).
5. **The apps are native** (2026-10-25): SwiftUI on Apple (iPhone with iPhone Duo, iPad, Mac,
   Apple TV), Jetpack Compose on Android (phone, foldable, tablet, Android TV), Compose for
   Desktop on Windows and Linux. The web SPA stays what the home serves and what the cloud web
   shows. The Desktop Server (Epics 36, 38, 39) stays the Tauri app that carries the server.

## Native toolkits

| Family | Toolkit | Covers | Shares with |
|---|---|---|---|
| Apple | SwiftUI + `MaktabaKit` (`apps/apple`) | Player: iPhone (iPhone Duo too), iPad, Mac. Remote: iPhone, iPad. Then Apple TV | the tvOS app, folded in (its client moves to `MaktabaKit`) |
| Android | Jetpack Compose | Player and Remote: phone, foldable, tablet; Android TV | the Android TV app |
| Windows, Linux | Compose for Desktop | Player | the Android code (Kotlin) |

Players come first. The Remote starts with the essentials: status, needs attention with
Retry and Retry all, processing, machines, updates, admin push alerts. Deeper admin pages open
the home's web UI until they are built natively.

**Form factors on Apple:** the iPhone Duo is an iPhone, and no device requirement in Apple's
documentation limits an app to it, so the iPhone app is the foldable app on Apple: it adapts
(Apple's iPhone Duo guide: `NavigationSplitView`, standard toolbars that move to the vertical
bar, `ArrangementView` for the watch screen, layout from the view's size). iPad is its own
target and listing. On Android the foldable can be its own listing.

## Roles

| Role | Who | Where they act | Rights |
|---|---|---|---|
| Viewer | a home's users | Player apps, TV, the home's web UI, the cloud web player | watch what their libraries allow |
| Home admin (local admin) | a home's users with `is_admin` | Remote apps, the home's web UI, the cloud web remote | run *their* home: processing, settings, users, Live TV, machines, updates |
| **Maktaba service admin** | the people who operate Maktaba Cloud (Hamza Labs) | the cloud's operator console (`cloud/internal/handlers/admin`, `/v1/admin/*`) on the VPS | run *the service*: cloud accounts, plans and billing, abuse, relay and subdomains, push, releases. **No** access to any home's libraries, videos or settings, and no home admin rights by being a service admin |

The two admin roles never mix: a home admin is never a service admin by that, and a service
admin reaches a home only as any other user would (an account that home made for them). The cloud
web's player and remote act with the signed-in user's rights on the connected home; the operator
console is a separate part of the cloud with its own sign-in and its own audit log. No app in this
epic offers service administration: the console stays a web surface of the cloud.

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

### 1. The web: one SPA, editions at build time (the home's web UI and the cloud web)

The installed apps are native (Native toolkits); the SPA is what a browser shows: the home's
own web UI, and the cloud web's player and remote for a connected home.
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
- **With a linked Maktaba Cloud account** (decided 2026-10-25): see Linked accounts.

### Linked accounts (a home account and a Maktaba Cloud account)

Two kinds of account, kept apart:

| | Home account | Maktaba Cloud account |
|---|---|---|
| Lives | the home's database (`users`; joined machines hold a read copy, never its tokens) | the cloud's database (`cloud/`) |
| Who | everyone who watches or runs that home | anyone (free); the owner's also owns the home's cloud registration (claim, subdomain, relay, plan) |
| Rights | everything inside the home: libraries, admin | none inside any home by itself |

**Linking:** a home user, signed in to the home, links their cloud account from Account →
Linked accounts (a cloud sign-in in a browser sheet; the home stores the cloud user's id against
the home user, `home_user_links`, with the link's scope: `play`, or `manage` for an admin who
asked for it on the home). One cloud account can be linked to users on several homes;
one home user to one cloud account. Unlinking (the user, or the home's admin) cuts it at once.

**Signing in with it:**

1. The app signs in to Maktaba Cloud (password, or Sign in with Apple / Google through the
   cloud's OAuth). The cloud lists the homes this cloud account is linked to (each home tells
   the cloud which cloud users it has linked, by id only).
2. The person picks a home. The app asks the cloud for a short-lived **home assertion**: signed
   by the cloud, naming the cloud user and that home, for one use, a minute.
3. The app gives the assertion to the home (`POST /api/auth/cloud`), through the relay or on the
   local network. The home verifies the cloud's signature (the cloud's public key, pinned at
   claim), finds the linked home user, and issues **its own** tokens, as a password sign-in
   would. From then on the app talks to the home with the home's tokens.

**Rules:**

- **The surface grants nothing.** Player or Remote, native or the cloud web, an app changed by
  its user: the home's API checks the home user's role on every route, from the token alone.
  A viewer through the cloud Remote is a viewer; a tampered app with a viewer's token is too.
- **Links have a scope, set on the home.** A link is `play` by default. `manage` (managing
  the home through the cloud) is set only on the home, by a home admin, for their own link.
  A token the home issues from an assertion carries the link's scope, and every admin route
  refuses a `play`-scoped token, even an admin's: a stolen cloud account can watch, not manage.
- The home decides everything: the rights are the linked home user's; a disabled home user or a
  removed link refuses the assertion; the home works without the cloud (local sign-in, local
  network).
- The cloud never holds a home's tokens, passwords or library data; an assertion names who, not
  what they may do.
- A Maktaba service admin is no one in a home unless a home user linked them (Roles).
- A home not claimed in the cloud has no linked accounts; its apps sign in to it directly.

**Away from home:** the app keeps both of a home's addresses (local, and the cloud's subdomain)
and uses whichever answers; the video then goes home → relay → app, metered by the owner's plan,
and the home's transcoding lowers it to fit the connection.

### 4. The apps

| App | Built from | Identity | Notes |
|---|---|---|---|
| Player, iPhone (iPhone Duo too) | `apps/apple`, SwiftUI | `com.hamzalabs.maktaba.player` | scheme "Maktaba Player" (started: PR on `feat/apple-native-apps`) |
| Player, iPad | `apps/apple` | `com.hamzalabs.maktaba.player.tablet` | its own listing |
| Player, Mac (Apple silicon) | `apps/apple` | `com.hamzalabs.maktaba.player.mac` | the desktop player on macOS |
| Remote, iPhone / iPad | `apps/apple` | `com.hamzalabs.maktaba.remote`, `.remote.tablet` | admin push notifications (failures, health) |
| Player and Remote, Android phone / foldable / tablet | Compose | `com.hamzalabs.maktaba.player`, `.player.foldable`, `.player.tablet`; `.remote…` | six listings with the remote ones |
| Player, Windows / Linux | Compose for Desktop | `com.hamzalabs.maktaba.player` | its own updater feed |
| Desktop Server | today's Tauri app (Epics 36, 38, 39), web edition `full` | `com.hamzalabs.maktaba` | unchanged |
| TV | `apps/tv` now, then the Apple and Android code bases | `com.hamzalabs.maktaba.tv` | gains the home picker and sign-in by code |
| Cloud web | `cloud/` serving the SPA, edition by URL (`/play`, `/manage`) | the cloud domain | talks to the connected home through the relay |

`apps/mobile` (the Capacitor wrapper) is retired once the native phone apps ship.

**Signing:** Apple apps sign with Xcode's automatic signing (team `G58UVTKQPB`); archives
and uploads from Xcode or `xcodebuild -allowProvisioningUpdates`.

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

1. **42.1 Apple Player and Remote:** `apps/apple` (SwiftUI, `MaktabaKit`): home by address,
   sign-in, libraries, a video, watching (stream sessions, progress), needs attention with
   Retry. iPhone (iPhone Duo), iPad, Mac; Xcode automatic signing. (Started.)
2. **42.2 Home picker:** mDNS (Bonjour on Apple, NSD on Android), by address, QR; several homes.
3. **42.3 Android Player and Remote:** Compose, phone / foldable / tablet, sharing with Android TV.
4. **42.4 Desktop Player on Windows and Linux:** Compose for Desktop, release legs,
   `latest-player.json`.
5. **42.5 Web editions:** `VITE_MAKTABA_EDITION`, lazy server routes, "Manage" in `full`.
6. **42.6 Sign-in by code:** endpoints, the approve screen, mobile, desktop and TV.
7. **42.7 Linked accounts and the cloud:** linking (home Account page, `home_user_links`), the
   cloud's home assertion, `POST /api/auth/cloud`, the app's cloud sign-in and home list, both
   addresses per home (local and relay).
8. **42.8 Tablet and foldable:** two-pane and fold-aware layouts, their own listings.
9. **42.9 Cloud web player and remote:** the cloud serves the SPA editions for a connected home.
10. **42.10 TV:** the home picker and sign-in by code on tvOS and Android TV.

## Open questions

- Store accounts and names: one developer account per store for all listings; app names in Arabic
  and English.
- Whether a remote app may also play (the table says remote only; watching from the remote could
  link to the player instead).
- Free and paid editions (billing, Epic 25) per app.
