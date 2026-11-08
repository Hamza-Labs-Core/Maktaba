# Epic 58: Apps for the TVs Maktaba does not reach yet: Fire TV, Google Cast, Samsung, LG and Roku

> **Status:** spec (2026-11-08). **Platforms:**
> - Amazon Fire TV on Fire OS;
> - Google Cast (a Maktaba receiver, with senders in the Android Player, the Apple Player and the
>   web);
> - Samsung Tizen and LG webOS, on one web-TV codebase;
> - Roku;
> - Amazon Vega OS, when its share warrants it;
> - the home's API, streaming service and Maktaba Cloud, for what they need.
>
> **Revisits:** [Epic 14](../14-tv-apps/README.md), which left Tizen and webOS out with a DLNA
> fallback.
>
> **Builds on:**
> - [Epic 15](../15-discovery/README.md) (discovery, pairing by code);
> - [Epic 25](../25-cloud-relay/README.md) and [Epic 50](../50-cloud-no-downtime/README.md) (the
>   relay, the connection list of story 50.13, per-home certificates of story 50.15);
> - [Epic 44](../44-viewing-analytics/README.md) (`X-Maktaba-Client`, story 44.2);
> - [Epic 46](../46-big-screen/README.md) (the web's 10-foot view);
> - [Epic 48](../48-native-players/README.md) (the native Players, signed stream URLs, subtitle
>   renditions);
> - [Epic 51](../51-configurable-components/README.md) (`GET /api/capabilities`);
> - [Epic 56](../56-tv-library-view/README.md) (the TV library layout);
> - [Epic 57](../57-parental-controls/README.md) (the parental filter).
>
> **Research:** [research.md](research.md). **Wiki:**
> [epic-58-smart-tv-platforms.md](../../../docs/wiki/epics/epic-58-smart-tv-platforms.md).

## 1. Why

Requirement: a household should be able to watch its Maktaba home on the TV it already owns.
Today that means an Apple TV, an Android or Google TV, or a phone with AirPlay. The TVs most
families own are another matter:
- a Samsung (about 17% of TVs worldwide, and 23% of US connected-TV platforms);
- an LG (about 7–12%);
- a Roku (28% in the US);
- an Amazon Fire TV stick (research.md §7);
- and in the Middle East, mostly Samsung, LG and Android TV.

Epic 14 pointed Samsung and LG owners to DLNA. DLNA is LAN-only, has no HTTPS or signed URLs and
weak subtitles, and behaves differently per model, so it is not a Maktaba experience.

Each platform is different in what it runs, what it plays, how it is tested and how its store
works. The research for each is in [research.md](research.md). This epic decides, platform by
platform, what to build and in what order, and specs the home-side pieces they all need.

## 2. What exists, and the gaps

Checked on `main` at 0770a41db (2026-11-08). Details and file paths are in research.md §1.

| Area | Today |
|---|---|
| TV apps | Apple TV (`apps/tv/tvos`), Android TV (`apps/tv/android`, minSdk 28, no Google Play Services) |
| Cast | Android Player only, to Google's Default Media Receiver, LAN only; no Apple or web sender |
| 10-foot web UI | `/big` (Epic 46), built for `es2022`; no Epic 56 library tabs yet |
| Reaching a home | mDNS `_maktaba._tcp`; `GET /api/discovery/home`; per-home HTTPS names (50.15); the cloud's connection list (50.13); pairing by a code the **home** makes |
| Sign-in without mDNS | none: no cloud device-code flow |
| Playback | named client profiles; unknown names fall back to 720p H.264; HLS is MPEG-TS only; no per-device capability report |
| Compatibility | apps read the home's `version`; no rule on which app builds a home serves |
| CORS | allow-list, empty by default; nothing answers a packaged TV app's `file://` origin |
| Parental | the home filters every client (Epic 57); TVs have no blocked page beyond the Apple TV's (57.11) |

### Gap list

- **G1** No app on Fire TV, Samsung, LG or Roku, and no Maktaba Cast receiver.
- **G2** Samsung, LG and Roku apps cannot use mDNS, and a TV away from the home's LAN cannot find
  it at all.
- **G3** Every new platform would fall back to 720p transcodes, and HEVC cannot go to Cast in TS.
- **G4** No way to keep five store-updated apps working against homes that update on their own
  clock.
- **G5** Store reviewers have no home to test with.
- **G6** Roku has no right-to-left text.

## 3. Goals

1. **Reach the TVs families own:** Fire TV, every Cast-capable TV, Samsung, LG and Roku, each with
   the Apple TV and Android TV apps' layout (Epic 56), D-pad first.
2. **Same server API, same roles.** No TV gets a private API. Every role rule is enforced by the
   server, with role tests (58.17).
3. **Parental controls apply by themselves** (Epic 57's filter), with a blocked page and Ask a
   parent on each TV (58.14).
4. **Direct play wherever the TV can.** Each TV reports what it decodes, and the home picks direct
   play, remux or transcode within the platform's limits (58.02).
5. **Any home, from anywhere.** On the LAN, through the relay, or at a relative's house, signed in
   by a code approved on a phone (58.03, 58.09).
6. **Apps and homes stay compatible** across independent updates, with clear Update screens
   (58.01).
7. **English and Arabic** for every string, mirrored, on every TV, Roku included (58.15).
8. **Tested in CI** with emulators and simulators where they exist, with honest real-device
   checklists where they do not, and with app logs in every report (58.16).

## 4. Non-goals

- **Xbox.** A UWP or WebView2 shell is possible: WebView2 exists on Xbox for media apps, and the
  individual Partner Center fee is waived. But reported HEVC direct-play problems, a 1 GB memory
  cap and Xbox's small share of living-room viewing put it last. It is out of this epic, and
  revisited as a WebView2 shell around `/tv/` if asked (owner question Q11).
- **Hisense VIDAA and Vizio's own app platforms.** Both need a partner agreement, with no path for
  a small developer (research.md §8). Vizio TVs and Hisense Google TV models are reached through
  Cast (58.06–58.07), and many VIDAA and Vizio TVs through AirPlay from the Apple Player.
- **TVs before 2020.** Tizen 5.0 (M63) and webOS 4.x (M53) are told they are too old and pointed to
  Cast and AirPlay.
- **DRM.** A home's media is its own. Nothing is encrypted beyond HTTPS.
- **Selling anything on a TV.** No in-app purchase, no plan prices, no purchase links or QR codes
  to buy. Maktaba Cloud plans are bought on maktaba.tv in a browser. No store payment system
  (Roku Pay, Amazon IAP, Samsung or LG billing) is integrated. **No Stripe or Stripe-owned
  service (Lemon Squeezy), ever**, here or anywhere else.
- **Store search and voice integrations that publish titles off the home:** Alexa video skills,
  Roku's search feed, Google TV's catalog. A self-hosted home's titles stay in the home.
- **DLNA.** Kept as the TVs' own last fallback; nothing is built for it.
- **A TV-side parent PIN or device age signal.** No TV platform here offers an app an age signal.
  A PIN on a remote is slow, and a guardian answers from the Player (58.14).

## 5. Platform by platform: the decisions

| Platform | Build | Why this way | Order |
|---|---|---|---|
| **Fire TV (Fire OS)** | **Native: the Android TV app**, an `amazon` flavor (58.05) | Fire OS is Android (API 28–36) and the TV app uses no Google Play Services. Days of work for about 4–6% of TVs and the US's second streaming stick | 1 |
| **Google Cast** | **A Maktaba custom web receiver** at `cast.maktaba.tv` (58.07) + **senders** in the Android Player, the Apple Player and Chrome (58.06) | One small web page reaches every Chromecast, Google TV Streamer and Google TV set (300M+ active devices) and Vizio's Cast-built-in TVs. It replaces the Default Media Receiver the Android Player uses today | 2 |
| **Samsung Tizen** | **Shared web-TV codebase:** a packaged launcher (58.09) hosting the home's `/tv/` UI (58.08), with AVPlay (58.10) | Tizen is Chromium. AVPlay is needed for 4K and HLS (MSE stops at FHD). The largest TV brand worldwide and in the Gulf | 3 (with LG) |
| **LG webOS** | **Shared web-TV codebase**, with the native `<video>` HLS player (58.11) | Same engines as Samsung by year. Individual Seller accounts and worldwide listing; jellyfin-webos proves a hosted UI passes LG's QA | 3 (with Samsung) |
| **Roku** | **Native: BrightScript/SceneGraph** (58.12, 58.13) | Roku runs neither web nor Android apps. 28% of the US, small elsewhere, **no right-to-left text**, so Arabic needs shaping of its own (58.15) | 4 |
| **Vega OS** | **Native: React Native for Vega**, sharing the web-TV TypeScript modules (58.18) | Android apps do not run on Vega. Only new sticks since 2025 run it. Built when Vega is a quarter of Fire TV use, or Amazon stops Fire OS sticks | 5 (gated) |

**Why this order:**
- **Cost first, where reach is close.** Fire TV is nearly free. Cast is one small page, and it
  reaches more screens than any single platform.
- **Then the most TVs per line of code.** One web codebase covers Samsung and LG, about 25–30% of
  TVs worldwide and most of the Gulf's.
- **Roku is the largest US platform.** It needs a whole new codebase plus Arabic shaping, and it
  is barely sold where Arabic is spoken, so it comes after the platforms that serve both
  languages' audiences.
- **Vega waits** for its share.

**Why one web codebase for Samsung and LG, and native elsewhere:**
- **Samsung and LG** run Chromium of the same version per model year, both play HLS natively
  (AVPlay, `<video>`), and their differences fit in a player adapter, a key map and two package
  formats.
- **Fire TV** already has a better native app.
- **Roku and Vega** cannot run web apps well: Roku not at all; Vega WebView exists, but React
  Native is Amazon's main path, with a better media stack.
- **The Cast receiver** is a web page by nature. It shares the web's fonts, tokens and i18n, but
  it is its own small app.

## 6. Design

### 6.1 The shape of it

```
                         ┌──────────────── the home ─────────────────┐
                         │ API: /api/discovery/home (api_level…)     │
 Fire TV (Android TV app)│ /api/stream/sessions (+device_caps)       │
 Roku channel ───────────┤ /api/pairing/*, /api/cast/grants          │
 Vega app (later)        │ parental filter (Epic 57) on every route  │
                         │ /tv/  ← the TV build of /big (58.08)      │
 Samsung/LG launcher ────┤   loaded in the launcher's iframe (58.09) │
   (AVPlay / <video>)    │ streaming: profiles + reports, HLS fMP4   │
                         └──────────▲──────────────────▲─────────────┘
 Cast receiver ──────────────────────┘ LAN HTTPS name   │ relay (maktaba.tv)
 (cast.maktaba.tv, grant from a sender)                 │
                         Maktaba Cloud: /v1/tv/codes (58.03), /v1/homes connections
```

### 6.2 Reaching a home and signing in (58.03, 58.09, 58.12)

| Way | Fire TV | Samsung, LG | Roku | Vega | Cast receiver |
|---|---|---|---|---|---|
| Saved home | yes | yes | yes | yes | n/a |
| **Cloud code** (any network; a phone approves) | yes (shared core) | **default** | **default** | **default** | n/a |
| mDNS | yes | no API | no API | — | n/a |
| Subnet probe of the TV's /24, then the home's own code | — | yes | yes | yes | n/a |
| Typed address | yes | yes | yes | yes | n/a |
| The sender's address and a cast grant | n/a | n/a | n/a | n/a | yes |

- **The cloud code.** The TV gets a code from Maktaba Cloud (`/v1/tv/codes`). A phone signed in to
  the home approves it in the Player (Profile → Approve a TV). The home then hands the cloud its
  connection list and a single-use pairing grant, and the TV redeems the grant on the home by
  the best connection: LAN first, relay last. The approver needs no cloud account, only a home
  with remote access.
- **HTTPS everywhere it can be.** The per-home certificates of story 50.15 give LAN addresses valid
  HTTPS, which web TVs and Cast receivers need (no certificate pinning in a web runtime, and no
  mixed content). Where a router blocks those names, web TVs and Roku fall back to plain HTTP on
  the LAN, say so on the Account screen, and sign in only by code there, never by password.

### 6.3 Playback (58.02)

- **Each TV reports `device_caps`.** The home merges the report with the platform's profile: the
  report narrows freely and widens only up to the platform's ceiling.
- **New profiles:** `firetv`, `tizen`, `webos`, `roku`, `cast` and `vega`.
- **HLS gains fMP4 segments** for HEVC and AV1 (Cast requires it; webOS, AVPlay and Roku play it).
  TS stays the default for everyone else.
- **Dolby Vision** profile 8 goes to TVs without DV as its HDR10 or HLG base layer, by remux.
  Samsung never gets DV. Roku gets DV only as profile 5.
- **TrueHD and DTS** go as EAC3 where a TV lacks them.
- **Media URLs.** Every media URL a TV player fetches is signed (`?sig=`), since AVPlay, webOS's
  `<video>` and Cast receivers cannot be relied on to send headers.

### 6.4 Updates and compatibility (58.01)

| Piece | How it updates |
|---|---|
| The home | itself (Epic 28) |
| Samsung/LG **UI** (`/tv/`) | **with the home**: the launcher loads the home's own build |
| Samsung/LG **launcher** | the store, after review; changes rarely (the bridge is versioned) |
| Fire TV, Vega | Amazon Appstore, automatically |
| Roku | Roku, automatically |
| Cast receiver | `cast.maktaba.tv/r/<api_level>/`, picked by the sender's level; one console registration forever |

- **The API level.** The home publishes `api_level`, `api_level_oldest` and `client_floor` on
  `GET /api/discovery/home`. Each app carries `min_api_level` and `built_for_api_level`.
- **The window.** A home serves every level from the last 12 months, so a TV app a year behind
  still works.
- **Mismatches.** Outside that window the app shows **Update the home** or **Update this app**, in
  the platform's words, rather than failing.
- **Missing features.** The home's features come from `GET /api/capabilities`, and missing ones
  are hidden, never shown broken.
- **The CI check.** An OpenAPI diff fails CI when a response changes incompatibly without raising
  the level.

### 6.5 The 10-foot UI

Every TV has Epic 56's layout:
- the library tabs (Recommended, Library, Collections, Playlists, Categories), with Play and
  Shuffle;
- the series page with season pills;
- collections and playlists;
- the letter rail (A…Z, ا…ي);
- Continue and Up next.

The remote rules follow Epic 56 §6.5, mapped to each platform's keys:
- **Back:** three steps, then leave with one confirm.
- **Play/Pause** on a focused card plays it.
- **Actions:** a long OK, or the menu or `*` key, opens a card's actions.
- **Focus memory** per tab.

The styling is dark Ink & Brass (Epic 48's tokens), with a 1.08 focus scale and the brass ring.

- **Samsung and LG** get this from the web's `bigscreen` code, which gains the Epic 56 screens for
  `/big` too.
- **Roku** builds it in SceneGraph.
- **Fire TV** already has it from the Android TV app.

### 6.6 Strings and Arabic (58.15)

- **Catalogs.** Every catalog has English and Arabic; one check fails CI on a missing key or a
  placeholder mismatch.
- **Web TVs and the receiver** mirror with `dir="rtl"` and logical CSS, and bundle IBM Plex Sans
  Arabic and Amiri.
- **Roku** gets visual text: Arabic shaped into joined forms and put in display order. Static
  strings are shaped at build time and the home's titles on the device, both checked against a
  shared vector file. Its layouts are mirrored by a layout helper.
- **Subtitles.** Arabic subtitles are drawn by Maktaba's own layer on Samsung, LG and Roku,
  because the platforms' renderers reverse them.

### 6.7 Parental controls (58.14)

- **No TV work needed for the filter.** Epic 57's filter is on the home, so every TV here is
  covered the moment it signs in.
- **What each TV adds:** the blocked page with Ask a parent, and the "Managed by" line.
- **New paths, closed and tested in the bypass rehearsal (A23–A27):**
  - the cast grant;
  - the cloud code;
  - deep links;
  - the `/tv/` UI.

### 6.8 Store reviewers (58.04)

- **The review home.** `review.maktaba.tv` is a home run by the project, with public-domain and
  openly licensed content, a viewer account and the full cloud path.
- **Every store's submission notes** point to it. Its password lives in the release secrets, never
  in the repo.

### 6.9 Payments

- **Nothing is sold on any TV app.** None of the stores' payment systems is used, so none of their
  payment rules apply.
- **Plans.** Maktaba Cloud plans are bought on maktaba.tv. The TV apps do not link or point to
  buying.
- **No Stripe or Stripe-owned service** (Lemon Squeezy) is used for any payment, in any part of
  this epic.

## 7. Phases

| Phase | Stories | Parallel? |
|---|---|---|
| 1. Groundwork | 58.01 compat, 58.02 playback, 58.03 cloud code, 58.04 review home, 58.15 catalog check, 58.16 jobs and logs | yes, all four home/cloud stories in parallel |
| 2. Fire TV and Cast | 58.05 Fire TV; 58.07 receiver, then 58.06 senders | Fire TV beside Cast |
| 3. Samsung and LG | 58.08 `/tv/`, 58.09 launcher, then 58.10 Tizen and 58.11 webOS | 58.10 and 58.11 in parallel; LG submits first (no partner contract, worldwide) |
| 4. Roku | 58.15 Roku shaping, 58.12 channel, 58.13 player and certification | 58.15's shaping first |
| 5. Parental and roles | 58.14, 58.17 (each route's role rows land with its story; the rehearsal last) | — |
| 6. Vega | 58.18, when its start condition is met | — |

## 8. Stories

| # | Story | Platforms |
|---|---|---|
| 58.01 | [The compatibility contract: API levels, oldest supported apps and the update prompt](story-58-01-compatibility-contract.md) | API, every app |
| 58.02 | [Playback that fits each TV: device-reported capabilities, new profiles and HLS in fMP4](story-58-02-device-playback-caps.md) | API, streaming |
| 58.03 | [Sign a TV in from anywhere: a code through Maktaba Cloud](story-58-03-cloud-tv-code.md) | cloud, API, Players, web |
| 58.04 | [A home for store reviewers](story-58-04-store-review-home.md) | cloud, release |
| 58.05 | [Fire TV on Fire OS: an Amazon build of the Android TV app](story-58-05-fire-tv.md) | Fire TV |
| 58.06 | [Google Cast senders in the Android Player, the Apple Player and the web](story-58-06-cast-senders.md) | Android, iOS, web, API |
| 58.07 | [Maktaba's Cast receiver at cast.maktaba.tv](story-58-07-cast-receiver.md) | Cast |
| 58.08 | [The TV build of the big-screen view, served by the home at /tv/](story-58-08-tv-web-ui.md) | web, Samsung, LG |
| 58.09 | [The web-TV launcher: finding the home, signing in, and the bridge to the TV](story-58-09-tv-web-launcher.md) | Samsung, LG |
| 58.10 | [Samsung Tizen: AVPlay, the package and the Samsung store](story-58-10-tizen.md) | Samsung |
| 58.11 | [LG webOS: the native player, the package and the LG Content Store](story-58-11-webos.md) | LG |
| 58.12 | [Roku: a SceneGraph channel with the TV layout](story-58-12-roku-channel.md) | Roku |
| 58.13 | [Roku: the player, certification and release](story-58-13-roku-player-certification.md) | Roku, streaming |
| 58.14 | [Parental controls on the new TVs](story-58-14-parental-controls.md) | every TV, API |
| 58.15 | [English and Arabic on every TV: catalogs, mirroring, fonts and Roku's visual text](story-58-15-strings-arabic-rtl.md) | every TV |
| 58.16 | [CI for each TV: emulators where they exist, real-device checklists where they don't, logs kept](story-58-16-ci-per-platform.md) | CI |
| 58.17 | [OpenAPI, role tests and the new-clients rehearsal](story-58-17-openapi-roles-rehearsal.md) | API, cloud, CI |
| 58.18 | [Amazon Vega OS: a React Native app, built when Vega devices are a real share](story-58-18-vega-os.md) | Vega |

## 9. Test and rehearsal plan

Everything automated runs in CI on jobs scoped to their code (CI runs only related jobs), and
nothing runs against the live home. Story 58.16 has the full table. In short:

| Platform | Automated in CI | Real devices only (written checklist before each submission) |
|---|---|---|
| Home, cloud | Go unit and Postgres tests; role tables; the `tv-clients` and parental-bypass rehearsals; the cloud rehearsal | — |
| Fire TV | Robolectric screenshots; Google's Android TV emulator without Google APIs; no-Play-Services check | codecs, HDR, Dolby, Amazon's remote |
| Cast | receiver in Playwright with a CAF stub; senders with SDK fakes | everything involving Google's SDK on a device; LAN fetch rules |
| Samsung, LG | Vitest; `build:tv` M68 checks; Playwright with platform shims; emulator and simulator smoke runs (not required) | AVPlay and native HLS playback, HDR, keys, performance |
| Roku | brs-engine logic, focus and layout tests; BrighterScript build and lint; shaping vectors | the Video node, rendering, fonts, timing, certification |
| Vega | Jest; the Vega Virtual Device where KVM exists | playback |

**Logs and screenshots in every run:**
- Every run keeps the app's log in its CI report: the Playwright console, network and trace;
  logcat; brs-engine stdout; emulator and simulator logs; the launcher's buffer.
- UI screenshots are dark, in English and Arabic.
- Device checklists attach the device's log to their result.

## 10. Acceptance (the epic)

- A Fire TV Stick 4K Max signs in by a cloud code and plays a 4K HEVC HDR10 MKV in direct play.
- An iPhone casts a video to a Google TV Streamer with Maktaba's receiver, in Arabic, and the
  home's Continue Watching follows the TV after the phone is locked.
- A Samsung TV (2022) and an LG TV (webOS 6) at a relative's house sign in by a code approved on
  a phone at home, show the Epic 56 library layout, and play through the relay.
- A Roku shows every screen in Arabic with correct reading order and plays Arabic subtitles in
  order.
- A restricted child signed in on each TV sees only allowed items, and a blocked item shows Ask a
  parent.
- An app a year older than the home still works. An app older than that shows Update this app,
  and a home older than the app shows Update the home.
- No TV app sells anything, and no Stripe or Stripe-owned service appears anywhere.
- Every string exists in English and Arabic, every TV screen is checked mirrored, and every CI
  run keeps the app's log.

## 11. Decisions (2026-11-08)

1. **Order: Fire TV, Cast, Samsung and LG, Roku, then Vega.** Cheapest and widest first; Roku
   after the platforms that serve Arabic-speaking homes. Rejected: Roku first for its US share,
   which would put the hardest Arabic work and the least Middle East reach at the front.
2. **Fire TV is a flavor of the Android TV app**, with stock Media3 and a CI check that no Google
   Play code enters it. Rejected: a separate app, and Amazon's Media3 port by default (one player
   for both flavors until a device test shows the port fixes something).
3. **Samsung and LG share one web codebase:** a packaged launcher with a versioned postMessage
   bridge, hosting the home's own `/tv/` UI in an iframe. The UI therefore updates with the home,
   not through weeks of store review. Rejected: the UI bundled in each package (every change a
   store review, and the UI drifts from the home), kept as a build flag in case a store refuses
   remote UI (Q3). Also rejected: a hosted app whose start page is the home (the store cannot
   know each home's URL, and a Samsung forum answer says hosted apps need a partnership).
4. **`/tv/` is the web's `bigscreen` code in a second build**, not a new UI. It gains the Epic 56
   screens for `/big` too. Rejected: a new TV framework (Lightning, Enact), which would fork the
   10-foot UI the web already has.
5. **AVPlay on Samsung, native `<video>` HLS on LG**, with Maktaba drawing subtitles on both.
   Rejected: hls.js/MSE everywhere, which stops at FHD on Samsung and loses the TVs' HDR and
   passthrough paths.
6. **A Maktaba Cast receiver, not the Default Media Receiver**, with a cast grant scoped to the
   items cast, so the TV reports progress and renews URLs itself. Rejected: handing the
   receiver the person's token, and the Default Media Receiver (no Arabic, no queue, no
   progress).
7. **The cloud code for TVs that cannot find the home**, approved from a phone signed in to the
   home, with no cloud account needed. Rejected: typing a home address and password on a remote
   as the main path, and requiring a Maktaba Cloud account.
8. **Each TV reports what it decodes, clamped to its platform's ceiling.** Rejected: one fixed
   profile per platform (wrong for most models), and trusting the report without a ceiling.
9. **HLS fMP4 only where needed.** TS stays the default, so no existing client changes. Rejected:
   moving every session to fMP4 at once.
10. **Roku gets full Arabic by shaping text itself.** Rejected: an English-only Roku, which breaks
    the English-and-Arabic rule.
11. **A 12-month API level window** and a `client_floor` for security fixes. Rejected: requiring
    the latest app (stores take weeks), and no contract at all.
12. **Nothing is sold on TVs.** Rejected: store billing for Cloud plans, which would bring store
    rules and fees into every TV app.
13. **The review home is the project's own**, with open content. Rejected: giving reviewers a
    customer's home, or a home with no cloud path (the cloud code would go untested).
14. **Vega waits for a start condition.** Rejected: building it now, for sticks first sold in
    late 2025, ahead of Samsung, LG and Roku.

## 12. Questions only the owner can answer

None blocks the epic. Each has a default the stories use.

| # | Question | Default used |
|---|---|---|
| Q1 | Is the platform order right? | Fire TV, Cast, Samsung and LG together, Roku, then Vega when its start condition is met (§5, decision 1) |
| Q2 | Should the project sign Samsung's Partner Seller contract, which any country outside the US (including the Gulf) needs? | yes: launch in the US as a Public Seller and apply for Partner status at the same time (58.10) |
| Q3 | If a store refuses an app that loads its UI from the home, ship the bundled-UI build there? | yes, per store, as decided at its first submission; home-served stays the default (58.09) |
| Q4 | Which countries should each store listing cover? | every country each store offers: LG worldwide; Samsung the US until Q2 is done; Amazon and Roku wherever they sell |
| Q5 | Roku in Arabic: is build-time and on-device shaping acceptable, given Roku has no RTL? | yes, full Arabic (58.15); Roku waits until it passes the shaping vectors |
| Q6 | Set up a device lab (a self-hosted runner with a Fire TV, a Samsung, an LG, a Roku and a Google TV Streamer on the home network) for scheduled real-device runs? | no lab; written checklists run by hand before each submission (58.16) |
| Q7 | Run a store-review home at `review.maktaba.tv` with public-domain content? | yes (58.04) |
| Q8 | Start condition for Vega OS | Vega at 25% of Fire TV use, or Amazon stops selling Fire OS sticks in a listed market (58.18) |
| Q9 | Host the Cast receiver at `cast.maktaba.tv` and register it in Google's console ($5, once) under the project's Google account? | yes (58.07) |
| Q10 | Oldest TVs supported | Samsung and LG from 2020 (Tizen 5.5, webOS 5); older ones are pointed to Cast and AirPlay |
| Q11 | Xbox | out of this epic; a WebView2 shell around `/tv/` is the path if wanted later (§4) |
| Q12 | How long must a home keep serving old app versions? | 12 months of API levels; `client_floor` only for security fixes (58.01) |
