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
│  launcher: home picker · discovery · compat check · errors                     │
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

**Finding the home.** At start the last home used is opened at once. Otherwise the first screen
offers, in this order:
1. **The cloud code** (58.03), the default: it works on any network (below).
2. **Saved homes**, under the code (Settings → Switch home comes back to this screen).
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
   - Each home found is listed by name; choosing one connects to it.

A typed address (`Enter the home's address`) is the last option, under More ways.

**The cloud code** (`apps/tv/web/src/cloud.ts`):
- The launcher asks Maktaba Cloud for a code (`POST https://api.maktaba.tv/v1/tv/codes
  {platform, app_version}`, no sign-in) and shows it large with a QR of its `verify_url`
  (`maktaba.tv/tv?c=…`), how long it lasts, and where to approve it: the phone's camera, or
  Profile → Approve a TV or device in a Player signed in to the home.
- It polls `POST /v1/tv/codes/poll {code, poll_secret}` at the cloud's `interval` (5 s more on
  `slow_down`), asks for a new code on 410 or 404 or when the code runs out, and stops polling
  once the screen is left. With no answer from the cloud it says the code is unavailable, keeps
  the other ways, and asks again after 30 s.
- The approval names the home, its connections (story 50.13's order) and the grant. The launcher
  tries the connections in that order with `GET /api/discovery/home`, leaving out a raw address
  pinned to a certificate name (`local_ip` with `tls_name`: a web runtime cannot check a
  certificate against another name), and takes the first that answers as that home. The cloud
  names the home by its own server id, which the home's discovery answer does not give (it
  gives the home's instance id), so the home is known by address: the grant goes only to an
  https name the cloud gives for it (its relay, its direct name, a LAN name under its
  certificate), where the certificate proves the home. A plain http or raw IP address (the
  cloud also keeps the home's plain public address, for older apps) is never tried: anyone on
  that network or on the path could answer there as the home. The compat
  check runs as for any home, the home is saved by that address, and its `/tv/` is loaded with
  the grant waiting for it (the bridge's `signIn`). When none answers, Cannot reach <home>
  offers Try again (the same connections) and Back (a new code).
- The poll secret and the grant stay in the launcher's memory: never stored, never in a URL,
  never in its log.

**Signing in** is `/tv/`'s, inside the frame, in the home's origin, in one of two ways:
- **A cloud code's grant** (`web/src/tv/GrantSignIn.tsx`). After an approval the launcher's
  `hello` answer says `sign_in: true` and a `signIn {grant}` follows, once. `/tv/` redeems the
  grant at `POST /api/pairing/device/token {device_code: grant, device_label}` and keeps the
  tokens as below; a refused grant (used, expired) falls back to the home's own code with a
  line saying so. A cloud code's TV is restricted on the home whoever approved it
  (`principal.ApplyRestricted`): never an admin, no credential of its own, and unlinking an
  account that approved it from maktaba.tv ends its sign-in.
- **The home's own code** (`web/src/tv/CodeSignIn.tsx`), for a home found on the network or
  typed: `/tv/` asks the home for a code (`POST /api/pairing/device`), shows it large with a QR
  of the approve page and how long it lasts, and polls `POST /api/pairing/device/token` until a
  person signed in on a phone or computer approves it. A new code is fetched when one expires
  (410).

No password is typed on the TV.

**CORS for the launcher.**
- The launcher's own requests run from a `file://` (Tizen) or `file://`/`null` (webOS) origin.
  They are `GET /api/discovery/home` on the home and the cloud code's two calls on the cloud.
- The home answers `/api/discovery/home` with `Access-Control-Allow-Origin: *` and no
  credentials, since that answer is public.
- The cloud answers `POST /v1/tv/codes` and `POST /v1/tv/codes/poll` to a packaged app's
  origin (`null`, `file://`, `app://…`) only: that origin echoed with `Vary: Origin`, `POST`
  with `Content-Type` and `X-Maktaba-Client`, never `*` and never
  `Access-Control-Allow-Credentials` (`cloud/internal/middleware/tvcors.go`, wired by
  `server.WithTVOrigins(tvcodes.PublicPaths)` on the api's user listener). Any other origin
  gets nothing from it, and no other cloud route answers those origins (the web allow-list is
  unchanged). A page claiming `null` reads only what any client reads from these routes by
  itself: a fresh code and its own poll. Asked from a viewer's browser, such a code carries
  the viewer's own country to Is this your TV?, as a code asked through a proxy in that country
  would; a person still has to type or scan a code they were shown, and the TV it signs in is
  restricted.
- A packaged app's origin (`null`, `file://`, `app://…`) gets its own origin back and the headers
  a bearer call needs (the `--bundle-ui` copy of the UI calls the home cross-origin), never
  `Access-Control-Allow-Credentials`: no cookie rides cross-site, and every role rule stays the
  API's. It is answered only once the API has verified the call's bearer (a made-up token is no
  sign-in), on a 401 (the TV renews on it), for a preflight asking to send `Authorization`,
  and before a token for
  `/api/auth/me`, `/api/auth/refresh` and `/api/pairing/device/token`: a web page can claim
  `null` too. Starting a code is not answered (a fresh code read cross-origin could be phished),
  so the `--bundle-ui` copy signs in by the cloud code, redeeming its grant at the token route.
  With no grant it shows Sign in with the code on the start screen and a Get a code button,
  which opens the launcher's code screen (`openHomePicker`). The allow-list for web origins (`httpsec/cors.go`) is unchanged. The UI in the iframe
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
- **Versioned.** A `bridge_level` integer, starting at 1. The UI asks `hello {id, takes}` and
  gets `{re, bridge_level, platform, model, year, engine, version, device_caps, keys, messages,
  sign_in}`. The UI names the TV in its client header from it. `takes: ["signIn"]` says the UI
  redeems a grant; an older home's `/tv/` leaves it out, and the launcher then keeps the grant
  to itself (the home's own code signs in instead). A new message or field keeps
  `bridge_level`.
- **The grant.** `signIn {grant}` goes once, right after a `hello` answer with `sign_in: true`,
  only to the origin the launcher loaded (for the bundled copy's `file://` frame, `*`, as every
  message to it) and only to a UI that took it; a second `hello` gets none. The UI takes it only
  from the launcher that answered `hello`, only when that answer announced it, only once, and
  only in the grant's shape. The launcher logs that it sent `signIn`, never the grant.
- **Messages** (each `maktaba:`-prefixed; the contract is `apps/tv/web/bridge.schema.json`):

| UI → launcher | launcher → UI |
|---|---|
| `ready` (the UI listens) · `hello {id, bridge_level, takes}` | `hello {…}` (above) · `signIn {grant}` (once, after an approved cloud code) |
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
- **Given** a Samsung TV at a relative's house, **when** its cloud code is approved in the
  Player, **then** the launcher reaches the home through the relay and `/tv/` signs in with the
  grant, never as an admin, and no home code is asked for.
- **Given** a web page at any origin but a packaged app's, **then** the cloud's `/v1/tv/*`
  answers it no CORS headers; a packaged app's origin gets its own origin back and never
  credentials.
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
| T04 | Playwright (Chromium, 1920×1080, `pnpm e2e:launcher`), the launcher from `file://` with a Tizen and a webOS shim and a mock home on the TV's /24 | find on network, iframe load, the home's code and its approval, the big screen by bearer, play via the bridge, exit, the saved home after a reload, Switch home | D-pad only | screenshots dark, en and ar; launcher log, console, network and bearer log in the CI report |
| T05 | Go unit | `/api/discovery/home`, a packaged app's origin, `/tv/`'s page | preflight and GET with `Origin: file://` or `null`; a viewer's and an admin's bearer | `*` on discovery; the packaged origin echoed, never credentials; roles unchanged (401, 403, 200); `/tv/` framed only by `'self' file: app:` |
| T06 | Contract test | `bridge_level` 1 schema `apps/tv/web/bridge.schema.json` | UI and launcher messages | both sides validate against it |
| T07 | Vitest (`apps/tv/web/src/app.test.ts`, `bridge.test.ts`) | the launcher with a fake cloud and fake connections | no saved home; approve; none answers; cloud down; leave the screen | the code and its QR first; the first usable connection loaded with the grant handed once to the home's origin after a `hello` that takes it; Cannot reach; the other ways; polling stops; no grant or poll secret in a URL, storage or the log |
| T08 | Vitest (`web/src/tv/GrantSignIn.test.tsx`, `launcherBridge.test.ts`) | `/tv/` with a fake parent | `hello` with and without `sign_in`; `signIn` from other origins and windows, of a bad shape, twice | redeemed at the token route before any home code, the tokens kept; a refused grant falls back to the home's code with its line; the bundled copy redeems at its base and, with no grant, sends the person to the launcher's code |
| T09 | Go unit (`cloud/internal/server/tvcors_test.go`) | the api's router with `WithTVOrigins` | preflight and POST on `/v1/tv/codes` and `/poll` from `file://`, `null`, `app://…`, another site, a look-alike; a TV origin on other routes | the packaged origin echoed with `Vary: Origin`, never credentials, never `Authorization` allowed; nothing for any other origin or route; nothing without the option |
| T10 | Playwright (`pnpm e2e:launcher`, `pnpm e2e:tv`) | the launcher from `file://` with a mock cloud, a LAN name that does not answer and a relay that does; `/tv/` framed by a launcher page | the code, approval, sign-in | screenshots dark, en and ar, of the code screen and Home; the grant redeemed once; no home code; the grant and poll secret in no URL, console line or log; the cloud's calls carried the page's own origin |

## Strings

The launcher's catalogs are `apps/tv/web/i18n/{en,ar}.json`, plus 58.01's `compat.*`:

| Key | English | Arabic |
|---|---|---|
| `launcher.cloud.title` | Sign in with a code | سجّل الدخول برمز |
| `launcher.cloud.asking` | Getting a code… | جارٍ الحصول على رمز… |
| `launcher.cloud.where` | On your phone, scan the code or open %1$s, then approve this TV. | على هاتفك، امسح الرمز أو افتح %1$s، ثم وافق على هذا التلفاز. |
| `launcher.cloud.app` | Or in Maktaba on a phone signed in to your home: Profile → Approve a TV or device. | أو في مكتبة على هاتف مسجّل الدخول إلى جهازك الرئيسي: الملف الشخصي ← الموافقة على تلفاز أو جهاز. |
| `launcher.cloud.expires` | This code lasts %1$d minutes. | يبقى هذا الرمز صالحًا %1$d دقائق. |
| `launcher.cloud.waiting` | Waiting for approval… | في انتظار الموافقة… |
| `launcher.cloud.new` | Get a new code | احصل على رمز جديد |
| `launcher.cloud.down` | A code from Maktaba Cloud is unavailable right now. Find the home on this network, or enter its address. | رمز Maktaba Cloud غير متاح الآن. ابحث عن الجهاز الرئيسي في هذه الشبكة، أو أدخل عنوانه. |
| `launcher.cloud.unreachable` | The home approved this TV, but none of its addresses answered. Check that it is on and that its remote access is turned on. | وافق الجهاز الرئيسي على هذا التلفاز، لكن لم يستجب أيٌّ من عناوينه. تأكّد أنه يعمل وأن الوصول عن بُعد مفعّل فيه. |
| `launcher.cloud.qr` | QR code of the approve page | رمز QR لصفحة الموافقة |
| `launcher.other` | Or connect another way | أو اتصل بطريقة أخرى |
| `launcher.saved.title` | Your homes | أجهزتك الرئيسية |
| `launcher.find.title` | Find on this network | ابحث في هذه الشبكة |
| `launcher.find.searching` | Looking for Maktaba homes… | جارٍ البحث عن أجهزة مكتبة الرئيسية… |
| `launcher.find.none` | No home found on this network. | لم يُعثر على جهاز رئيسي في هذه الشبكة. |
| `launcher.typed.title` | Enter the home's address | أدخل عنوان الجهاز الرئيسي |
| `launcher.more` | More ways | طرق أخرى |
| `launcher.plainHttp` | Connected without encryption on your network | متصل دون تشفير على شبكتك |
| `launcher.plainHttpCode` | Sign in with a code: a password is never sent without encryption. | سجّل الدخول برمز: لا تُرسَل كلمة المرور أبدًا دون تشفير. |
| `launcher.tooOld` | This TV is too old for the Maktaba app. You can still cast to it from Maktaba on your phone if it supports Google Cast or AirPlay. | هذا التلفاز أقدم من أن يعمل عليه تطبيق مكتبة. ما زال بإمكانك البث إليه من مكتبة على هاتفك إن كان يدعم Google Cast أو AirPlay. |

The code screen is `/tv/`'s, so its keys are in `web/src/i18n`:

| Key | English | Arabic |
|---|---|---|
| `tv.code.title` | Sign in with a code | تسجيل الدخول برمز |
| `tv.code.where` | On a phone or computer signed in to Maktaba, open {url} or scan the code, then approve this TV. | على هاتف أو حاسوب سجّل الدخول إلى Maktaba، افتح {url} أو امسح الرمز، ثم وافق على هذا التلفاز. |
| `tv.code.expires` | This code lasts {n} minutes. | يبقى هذا الرمز صالحًا {n} دقائق. |
| `tv.code.new` | Get a new code | احصل على رمز جديد |
| `tv.code.onlyCode` | A password is never sent without encryption: sign in with a code. | لا تُرسَل كلمة المرور أبدًا دون تشفير: سجّل الدخول برمز. |
| `tv.code.signingIn` | Approved. Signing this TV in… | تمت الموافقة. جارٍ تسجيل دخول هذا التلفاز… |
| `tv.code.grantFailed` | That approval could not be used. Sign in with a new code. | تعذّر استخدام هذه الموافقة. سجّل الدخول برمز جديد. |
| `tv.code.bundled` | Sign in with the code on the start screen: approve it on a phone signed in to Maktaba. | سجّل الدخول بالرمز الظاهر في شاشة البداية: وافق عليه على هاتف سجّل الدخول إلى Maktaba. |
| `tv.code.bundledGo` | Get a code | احصل على رمز |

## Edge cases

- **A home with several addresses on different subnets** (a VLAN for the TV): the probe finds
  nothing; a typed address works, and so does the cloud code, because the cloud lists every
  connection.
- **The cloud is down** (story 50.13's outage): the code screen says the code is unavailable and
  asks again every 30 s; Find on this network and a typed address still work.
- **An older home's `/tv/`** (no `takes` in its `hello`): the grant is not sent and the home's
  own code signs in.
- **The TV sleeps mid-video**: on `visibility {hidden}` the UI reports progress and stops the
  session. On wake it offers Resume.
- **Two homes found**: the picker lists both by name, as the Android TV app does.

## Build plan

| File | Change |
|---|---|
| `apps/tv/web/` (new) | launcher (`src/app.ts`, `src/probe.ts`, `src/discovery.ts`, `src/compat.ts`), bridge, i18n, platform shims (`src/platform/tizen.ts`, `src/platform/webos.ts`, `src/platform/browser.ts` for tests) |
| `apps/tv/web/bridge.schema.json` | the bridge contract |
| `web/src/bigscreen/input/bridge.ts` | the UI side of the bridge |
| `web/src/tv/session.ts`, `web/src/tv/CodeSignIn.tsx`, `web/src/lib/api.ts` (new, changed) | the code sign-in and bearer tokens in the launcher |
| `api/internal/httpsec/cors.go` | the public CORS answer, and a packaged app's origin |
| `cmd/maktaba-server/internal/webui/serve.go` | `/tv/`'s `frame-ancestors` |
| `web/vite.launcher.config.ts`, `web/vite.config.ts` (`tv-bundled`) | the launcher build and `--bundle-ui` |
| `apps/tv/web/src/cloud.ts`, `apps/tv/web/src/app.ts`, `apps/tv/web/src/ui/dom.ts` | the cloud code screen (the code, its QR from `web/src/lib/qr.ts`, the poll) and the connection the approval names |
| `web/src/tv/GrantSignIn.tsx`, `web/src/tv/TvApp.tsx` | `/tv/` redeeming the launcher's grant; the bundled copy's way back to the code |
| `cloud/internal/middleware/tvcors.go`, `cloud/internal/server/router.go` (`WithTVOrigins`), `cloud/internal/handlers/tvcodes/handler.go` (`PublicPaths`) | the cloud's answer to a packaged app's origin on `/v1/tv/codes` and `/poll` |
