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
- **Sign-in stays where it is.** The UI's sign-in lives in the home's origin: the existing web
  cookies and CSRF (story 10.2), with no tokens handed through postMessage.

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
   - A home that answers is then reached by its HTTPS name from `urls` (story 50.15). If that name
     does not resolve (a router blocking DNS rebinding), it falls back to plain HTTP on the LAN.
   - It then shows the home's own code (`POST /api/pairing/device`) to approve on a phone.

A typed address (`Enter the home's address`) is the last option, under More ways.

**CORS for the launcher.**
- The launcher's own requests run from a `file://` (Tizen) or `file://`/`null` (webOS) origin.
  They are only `GET /api/discovery/home` on the home, and the cloud code calls (58.03).
- The home answers `/api/discovery/home` with `Access-Control-Allow-Origin: *` and no
  credentials, since that answer is public.
- The cloud answers `/v1/tv/*` the same way.
- Nothing else in the home's CORS policy (`httpsec/cors.go`, an allow-list) changes. The UI in the
  iframe is same-origin with the home, so it needs none.

**Compat check.** The launcher reads `api_level`, `api_level_oldest` and `client_floor` (58.01)
before loading the iframe. It shows Update the home or Update this app itself, so an old home
never loads a broken UI.

**The bridge.** It uses `postMessage` between the launcher and the iframe:
- **Checked origin.** Each message is checked against the home origin the launcher loaded.
  Anything else is dropped.
- **Versioned.** A `bridge_level` integer, starting at 1. The UI asks `hello` and gets
  `{bridge_level, platform, model, year, engine, device_caps, keys}`.
- **Messages:**

| UI → launcher | launcher → UI |
|---|---|
| `play {url, start_sec, subtitles[], audio, mode}` · `pause` · `seek {sec}` · `stop` · `audioTrack {i}` · `subtitle {id \| null}` | `state {position, duration, buffering, ended, error}` every 500 ms while playing |
| `exit` (after the UI's confirm) | `key {action}` for keys the iframe cannot receive (color keys, media keys registered by the launcher) |
| `setLanguage {lang}` (so launcher screens match) | `visibility {hidden}` (the TV's app switcher) |
| `openHomePicker` (Account → Switch home) | |

- **Owned by the launcher:** video playback, subtitles drawn over the video (Tizen's AVPlay hands
  text cues to the app; research.md §3.2), and the app's lifecycle. A message the launcher does
  not know gets `{error: "unknown"}`, so the UI can hide a feature. The UI never assumes a
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
  HTTP address, says so on the Account screen ("Connected without encryption on your network"),
  and never sends a password over it. Sign-in goes by code.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Vitest | the subnet probe with a fake fetch | scan | 32 at a time, 700 ms timeout, stops at the first home per IP, lists several homes |
| T02 | Vitest | the bridge with a fake iframe | messages from the home origin and another origin | handled / dropped |
| T03 | Vitest | the compat check with the 58.01 vectors | each case | the right screen |
| T04 | Playwright (Chromium, 1920×1080), the launcher with a fake `webapis`/webOS shim and a mock home and cloud | cloud code, find on network, typed address, iframe load, play via the bridge | D-pad only | screenshots dark, en and ar; launcher log and bridge trace in the CI report |
| T05 | Go unit | `/api/discovery/home` | preflight and GET with `Origin: file://` | `*`, no credentials; other routes unchanged |
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
| `web/src/bigscreen/bridge.ts` (new) | the UI side of the bridge |
| `api/internal/handlers/discovery/home.go` | the public CORS answer |
| `cloud/internal/handlers/tvcodes/` | the same on `/v1/tv/*` |
