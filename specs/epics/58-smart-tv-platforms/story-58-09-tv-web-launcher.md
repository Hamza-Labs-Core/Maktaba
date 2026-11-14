# Story 58.09 — The web-TV launcher: finding the home, signing in, and the bridge to the TV

> Epic 58 · Phase 3 · Depends on: 58.01, 58.03, 58.08 · Needed by: 58.10, 58.11, 58.14

## Why

The Samsung and LG apps are packages installed from each TV's store. The 10-foot UI is the
home's `/tv/` (58.08). Something packaged has to:
- find a home on a TV with no mDNS (research.md §3.1, §4.1);
- check that the home and the package fit (58.01);
- show the home's UI;
- give that UI what only the TV's packaged context has:
  - Samsung's AVPlay and its `tvinputdevice` key registration;
  - LG's luna services;
  - the TV's model and decoders;
  - leaving the app.

That is the launcher: one TypeScript codebase, `apps/tv/web`, packaged twice, as a `.wgt` for
Tizen (58.10) and an `.ipk` for webOS (58.11).

## Description

**Shape.**

```
┌ packaged app (file:// origin, has webapis / webOS APIs) ───────────────────────┐
│  launcher: home picker · discovery · cloud code · compat check · errors        │
│  ┌ full-screen iframe: https://<home>/tv/?bridge=1 (the home's own origin) ───┐ │
│  │  the 10-foot UI (58.08)  ◀── postMessage bridge ──▶  launcher              │ │
│  └────────────────────────────────────────────────────────────────────────────┘ │
│  video plane (AVPlay on Tizen / <video> on webOS) under the transparent iframe │
└────────────────────────────────────────────────────────────────────────────────┘
```

jellyfin-webos uses this same pattern and passes LG's QA (research.md §4.3).

Why an iframe over the home's origin, and not the UI inside the package:
- **The UI matches the home.** It always speaks the home's API at the home's version, so a store
  review (Samsung about 1–2 weeks a round, LG 2 to 12 weeks; research.md §3.4, §4.4) is needed
  only when the launcher or the bridge changes.
- **Sign-in stays in the home's origin.** The browser never sends the home's SameSite=Lax session
  cookie to an iframe inside a `file://` page (checked in Chromium), so in the launcher `/tv/`
  signs in by the home's own code (`POST /api/pairing/device`, then its token poll) and keeps
  the tokens it gets in the home's origin (`localStorage`), never in the launcher's and never
  through postMessage. Every call then carries `Authorization: Bearer`, and an expired access
  token is renewed with `POST /api/auth/refresh`. The TV is a device signed in as the person who
  approved it: the home's rules decide its role, libraries and parental policy.

**Fallback if a store refuses remotely loaded UI.** A Samsung forum answer says a hosted web
app needs a partnership (research.md §3.4). The launcher then has a build flag, `--bundle-ui`,
that puts a copy of `/tv/` inside the package and points it at the home's API with bearer
tokens. That uses the same code, a store update per UI change, and the compatibility contract
(58.01) to cover the gap. Which mode each store gets is decided at its first submission, and
owner question Q3 records the default.

**Finding the home.** The first screen offers three ways, in this order:
1. **Saved homes.** The last home used is opened at once.
2. **Sign in with a code** (default for a new TV), via Maktaba Cloud (58.03). The screen shows the
   code large, the QR, how long it lasts, and where to approve it: "In Maktaba on your phone:
   Profile → Approve a TV, or go to maktaba.tv/tv".
3. **Find on this network.** The launcher probes the TV's own /24 subnet:
   - The TV's address comes from `webapis.network.getIp()` on Tizen, or
     `luna://com.webos.service.connectionmanager` on webOS.
   - It sends `GET http://<ip>:8088/api/discovery/home` to each of the 254 addresses, 32 at a
     time, with a 700 ms timeout. That is about 6 seconds worst case.
   - A home that answers is then reached by its HTTPS name from `urls` (story 50.15). The
     launcher takes a name only when its first label is the address the home answered from
     (`1-2-3-4.`) and its second the home's id, so a device on the LAN cannot send the TV to
     another site. If that name does not answer as the same home (a router blocking DNS
     rebinding), it falls back to plain HTTP on the LAN; a plain fallback never replaces a
     saved HTTPS address.
   - It then shows the home's own code (`POST /api/pairing/device`) to approve on a phone.

A typed address (`Enter the home's address`) is the last option, under More ways.

**CORS for the launcher.**
- The launcher's own requests run from a `file://` (Tizen) or `file://`/`null` (webOS) origin.
  They are only `GET /api/discovery/home` on the home, and the cloud code calls (58.03).
- The home answers `/api/discovery/home` with `Access-Control-Allow-Origin: *` and no
  credentials, since that answer is public.
- The cloud answers `/v1/tv/*` the same way.
- A packaged app's origin (`null`, `file://`, `app://…`) gets its own origin back and the headers
  a bearer call needs (the `--bundle-ui` copy of the UI calls the home cross-origin), never
  `Access-Control-Allow-Credentials`: no cookie rides cross-site, and every role rule stays the
  API's. It is answered only for a bearer call (or its preflight), and before a token for
  `/api/auth/me`, `/api/auth/refresh` and `/api/pairing/device/token`: a web page can claim
  `null` too. Starting a code is not answered (a fresh code read cross-origin could be phished),
  so the `--bundle-ui` copy signs in by the cloud code, redeeming its grant at the token route. The allow-list for web origins (`httpsec/cors.go`) is unchanged. The UI in the iframe
  is same-origin with the home, so it needs none.
- `/tv/`'s page carries `Content-Security-Policy: frame-ancestors 'self' file: app:`: only a
  packaged app or the home itself may frame it.

**Compat check.** The launcher reads `api_level`, `api_level_oldest` and `client_floor` (58.01)
before loading the iframe. It shows Update the home or Update this app itself, so an old home
never loads a broken UI.

**The bridge.** It uses `postMessage` between the launcher and the iframe:
- **Checked origin.** The launcher takes a message only from its iframe's window at the home
  origin it loaded. The UI takes one only from its parent at a packaged app's origin (`null`,
  `file://`, `app://…`) or its own. Anything else is dropped.
- **Hello first.** Until a launcher has answered `hello`, the UI sends nothing but `ready` and
  `hello` (no play address, no command). A packaged app's origin is opaque, which postMessage
  cannot name, so the UI's messages go to `*`; a launcher at an http(s) origin is sent to by it.
- **Versioned.** A `bridge_level` integer, starting at 1. The UI asks `hello {id}` and gets
  `{re, bridge_level, platform, model, year, engine, version, device_caps, keys, messages}`. The
  UI names the TV in its client header from it.
- **Messages** (each `maktaba:`-prefixed; the contract is `apps/tv/web/bridge.schema.json`):

| UI → launcher | launcher → UI |
|---|---|
| `ready` (the UI listens) · `hello {id, bridge_level}` | `hello {…}` (above) |
| `play {url, start_sec, subtitles[], audio, mode}` · `pause` · `resume` · `seek {sec}` · `stop` · `audioTrack {i}` · `subtitle {id \| null}` | `state {position, duration, buffering, ended, error}` every 500 ms while playing |
| `exit` (after the UI's confirm) | `action {action}` for keys the iframe cannot receive (color keys, media keys registered by the launcher), as a big-screen action |
| `setLanguage {lang}` (so launcher screens match) | `visibility {hidden}` (the TV's app switcher) |
| `openHomePicker` (Settings → Switch home) | |
| `logs {id}` | `logs {re, lines}` · `error {re_type, error}` |

- **Owned by the launcher:** video playback, subtitles drawn over the video (Tizen's AVPlay hands
  text cues to the app; research.md §3.2), and the app's lifecycle. A message the launcher does
  not know gets `error {re_type, error: "unknown"}`, so the UI can hide a feature. The UI never assumes a
  message exists without `hello` listing it.

**Logs.** The launcher keeps the last 500 lines of its own log and of the bridge traffic. URLs
are redacted: no `sig`, no grant. Help → Send a report sends them with the app's report
(Epic 53's reports), and CI keeps them in each run's artifacts (58.16).

**Client header and profile.** `tv-web/<launcher version> (tizen|webos; tv)`. Stream sessions
from `/tv/` set `client_profile` to `tizen` or `webos`, with the launcher's `device_caps`.

## Acceptance criteria

- **Given** a new LG TV and a phone signed in to the home, **when** the person approves the TV's
  code, **then** the home's `/tv/` UI loads full screen within 10 seconds.
- **Given** a Samsung TV on the home's LAN and no cloud registration, **when** Find on this
  network runs, **then** the home is listed within 8 seconds and the home's own code signs in.
- **Given** a message posted from any origin other than the loaded home, **then** the launcher
  ignores it (no playback, no exit).
- **Given** a home at a level the launcher cannot use, **then** the launcher's own Update the home
  screen shows and the iframe is never loaded.
- **Given** a router blocking DNS rebinding, **then** the launcher falls back to the LAN's plain
  HTTP address, says so on the code sign-in and in Settings → Connection ("Connected without
  encryption on your network"), and never sends a password over it. Sign-in goes by code.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Vitest | the subnet probe with a fake fetch | scan | 32 at a time, 700 ms timeout, stops at the first home per IP, lists several homes |
| T02 | Vitest | the bridge with a fake iframe | messages from the home origin and another origin | handled / dropped |
| T03 | Vitest | the compat check with the 58.01 vectors | each case | the right screen |
| T04 | Playwright (Chromium, 1920×1080), the launcher with a fake `webapis`/webOS shim and a mock home and cloud | cloud code, find on network, typed address, iframe load, play via the bridge | D-pad only | screenshots dark, en and ar; launcher log and bridge trace in the CI report |
| T05 | Go unit | `/api/discovery/home`, a packaged app's origin, `/tv/`'s page | preflight and GET with `Origin: file://` or `null`; a viewer's and an admin's bearer | `*` on discovery; the packaged origin echoed, never credentials; roles unchanged (401, 403, 200); `/tv/` framed only by `'self' file: app:` |
| T06 | Contract test | `bridge_level` 1 schema `apps/tv/web/bridge.schema.json` | UI and launcher messages | both sides validate against it |

## Strings

The launcher's catalogs are `apps/tv/web/i18n/{en,ar}.json`, plus 58.01's `compat.*`:

| Key | English | Arabic |
|---|---|---|
| `launcher.code.title` | Sign in with a code | تسجيل الدخول برمز |
| `launcher.code.where` | In Maktaba on your phone: Profile → Approve a TV, or go to maktaba.tv/tv | في Maktaba على هاتفك: الملف الشخصي ← الموافقة على تلفاز، أو اذهب إلى maktaba.tv/tv |
| `launcher.code.expires` | This code lasts %1$d minutes. | يبقى هذا الرمز صالحًا %1$d دقائق. |
| `launcher.code.new` | Get a new code | احصل على رمز جديد |
| `launcher.find.title` | Find on this network | ابحث في هذه الشبكة |
| `launcher.find.searching` | Looking for Maktaba homes… | جارٍ البحث عن منازل Maktaba… |
| `launcher.find.none` | No home found on this network. Try a code instead. | لم يُعثر على منزل في هذه الشبكة. جرّب الرمز بدلًا من ذلك. |
| `launcher.typed.title` | Enter the home's address | أدخل عنوان المنزل |
| `launcher.more` | More ways | طرق أخرى |
| `launcher.plainHttp` | Connected without encryption on your network | متصل دون تشفير على شبكتك |
| `launcher.tooOld` | This TV is too old for the Maktaba app. You can still cast to it from Maktaba on your phone if it supports Google Cast or AirPlay. | هذا التلفاز أقدم من أن يعمل عليه تطبيق Maktaba. ما زال بإمكانك البث إليه من Maktaba على هاتفك إن كان يدعم Google Cast أو AirPlay. |

## Edge cases

- **A home with several addresses on different subnets** (a VLAN for the TV): the probe finds
  nothing, and the cloud code works because the cloud lists every connection.
- **The TV sleeps mid-video**: on `visibility {hidden}` the UI reports progress and stops the
  session. On wake it offers Resume.
- **Two homes found**: the picker lists both by name, as the Android TV app does.

## Build plan

| File | Change |
|---|---|
| `apps/tv/web/` (new) | launcher, bridge, i18n, platform shims (`platform/tizen.ts`, `platform/webos.ts`, `platform/browser.ts` for tests) |
| `apps/tv/web/bridge.schema.json` | the bridge contract |
| `web/src/bigscreen/input/bridge.ts` | the UI side of the bridge |
| `web/src/tv/session.ts`, `web/src/tv/CodeSignIn.tsx`, `web/src/lib/api.ts` (new, changed) | the code sign-in and bearer tokens in the launcher |
| `api/internal/httpsec/cors.go` | the public CORS answer, and a packaged app's origin |
| `cmd/maktaba-server/internal/webui/serve.go` | `/tv/`'s `frame-ancestors` |
| `web/vite.launcher.config.ts`, `web/vite.config.ts` (`tv-bundled`) | the launcher build and `--bundle-ui` |
| `cloud/internal/handlers/tvcodes/` | the same on `/v1/tv/*` |
