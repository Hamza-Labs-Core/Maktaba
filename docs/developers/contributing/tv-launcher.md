# The Samsung and LG launcher

Samsung (Tizen) and LG (webOS) TVs run web apps. Maktaba's app for them is a small
**launcher** ([`apps/tv/web`](https://github.com/Hamza-Labs-Core/Maktaba/tree/main/apps/tv/web)):
it finds the home, then shows the home's own TV interface, `/tv/` (the big screen, built for
Chromium 68 by `pnpm build:tv`), full screen in an iframe. The interface comes from the home,
so a TV always runs the home's version of it; the launcher changes only when the stores
release it. The packages (`.wgt` for Samsung, `.ipk` for LG) and the TVs' own players are
stories 58.10 and 58.11.

## Finding the home

On first start the launcher offers, in order:

- **Find on this network:** it asks the TV for its address (`webapis.network.getIp()` on
  Samsung, the connection manager on LG) and tries `GET /api/discovery/home` on port 8088 of
  every address of that `/24`, 32 at a time, each for 0.7 seconds. Each home found appears once.
- **Enter the home's address:** an address, a name or a full URL; a bare host gets port 8088.
- **The home's code:** the sign-in inside `/tv/` (below).

When a home's discovery answer names an HTTPS address for the address it was found at (the
direct names of story 50.15, `1-2-3-4.<home id>.<domain>`), the launcher uses it; it takes a
name only when its first label is that address and its second the home's id, so a device on
the network cannot send the TV to another site. Otherwise it uses plain HTTP on the network,
and the sign-in and **Settings → Connection** say the connection is not encrypted.

Before showing anything, the launcher checks the [API levels](api-levels.md): **This home
needs an update** or **Update this app** (in the Samsung or LG store's words) when the two
cannot work together. The home is remembered, and opens at once the next time; **Settings →
Switch home** in `/tv/` brings the launcher back.

## Signing in

The launcher's page is a packaged `file://` page, and `/tv/` is in its iframe, so the browser
never sends `/tv/` the home's session cookie there. In the launcher, `/tv/` signs in by the
home's own code: it asks for one (`POST /api/pairing/device`), shows it large with a QR code of
the approve page, and polls `POST /api/pairing/device/token` until someone signed in on a phone
or a computer approves it. The tokens stay in the home's origin (`localStorage`), never in the
launcher's and never on the bridge; every call carries `Authorization: Bearer`, and an expired
access token is renewed with `POST /api/auth/refresh`. The TV is signed in as the person who
approved it, with that person's role, libraries and parental rules. No password is typed on
the TV.

## The bridge

The launcher and `/tv/` talk with `postMessage`, at **bridge level 1**. The contract is
[`apps/tv/web/bridge.schema.json`](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/apps/tv/web/bridge.schema.json),
and both sides' tests check every message against it.

| From | Message | Meaning |
|---|---|---|
| `/tv/` | `maktaba:ready` | `/tv/` is listening; the launcher shows the frame |
| `/tv/` | `maktaba:hello {id, bridge_level}` | what are you? |
| launcher | `maktaba:hello {re, platform, model, year, engine, version, device_caps, keys, messages}` | the TV, and the messages it takes |
| launcher | `maktaba:action {action}` | a key `/tv/` could not receive (a media or colour key), as a big-screen action |
| launcher | `maktaba:visibility {hidden}` | the TV's app switcher |
| `/tv/` | `maktaba:play {url, start_sec, subtitles, audio, mode}`, `pause`, `resume`, `seek {sec}`, `stop`, `audioTrack {i}`, `subtitle {id}` | the launcher's player (the TV's own, in 58.10 and 58.11) |
| launcher | `maktaba:state {position, duration, buffering, ended, error}` | the player, every 500 ms while it plays |
| `/tv/` | `maktaba:exit` | after **Leave Maktaba?** is confirmed |
| `/tv/` | `maktaba:setLanguage {lang}`, `maktaba:openHomePicker`, `maktaba:logs {id}` | the language, Switch home, the launcher's log |
| launcher | `maktaba:logs {re, lines}`, `maktaba:error {re_type, error}` | the answers |

The launcher takes messages only from its iframe's window at the home's origin; `/tv/` only
from its parent at a packaged app's origin (`null`, `file://`, `app://…`) or its own. Until a
launcher has answered `hello`, `/tv/` sends nothing but `ready` and `hello`.

## What the home sends

- `/tv/`'s page carries `Content-Security-Policy: frame-ancestors 'self' file: app:`: only a
  packaged app or the home itself may frame it.
- `GET /api/discovery/home` answers any origin (`Access-Control-Allow-Origin: *`): the launcher
  reads it before it has chosen a home.
- A packaged app's origin (`null`, `file://`, `app://…`) gets its own origin back and the
  headers a bearer call needs, never `Access-Control-Allow-Credentials`: no cookie is sent
  cross-site, and every rule of who may do what is the API's, as for any device. It is
  answered only once the API has verified the call's bearer token (a made-up token is no
  sign-in), on a 401 (the TV renews its token on it), for a preflight that asks to send
  `Authorization`, and before a token for `/api/auth/me`, `/api/auth/refresh` and
  `/api/pairing/device/token`: a web page can
  claim `null` too, and must read nothing that needs no sign-in. Starting a code
  (`POST /api/pairing/device`) is not answered, so the `--bundle-ui` copy signs in by the
  cloud code (a grant it redeems at the token route).

## Playing through the launcher

When `hello` lists `play`, `/tv/` does not play the video itself. The TV's own player plays it
in the launcher, under the transparent iframe (Samsung's AVPlay, LG's `<video>`). `/tv/`'s
`<video>` becomes a stand-in (`web/src/bigscreen/input/launcherMedia.ts`):

- `play()`, `pause()`, setting `currentTime`, enabling one of `audioTracks` and showing a text
  track become `maktaba:play`, `pause`, `resume`, `seek`, `audioTrack` and `subtitle`;
- each `maktaba:state` fires the element's own events (`loadedmetadata`, `timeupdate`, `pause`,
  `waiting`, `playing`, `seeked`, `ended`), so the player's progress posts, stall reports and
  controls are the same code as in a browser;
- `<html>` carries `tv-launcher-video` while it plays, and the page is transparent down to the
  video plane;
- URLs are made absolute (the launcher's page is `file://`), sidecar subtitles are
  `sub-<index>` in `play.subtitles`, and the size is the player's `--mkt-caption-scale`.

Stream sessions opened in a launcher send `client_profile` (`tizen` or `webos`) and the
launcher's `device_caps` (story 58.02). When the TV's player reports an error, the session is
opened again once with `force_transcode`, from where it stopped; a second error shows the error
screen. The state may list the stream's audio tracks (`audio_tracks`, added at
`bridge_level` 1). The launcher draws the subtitles itself, in the shared subtitle layer
(`apps/tv/web/src/subtitles/`): WebVTT read by the launcher, right to left for Arabic and any
other RTL language (from the track's language, else the text's first strong letter), at
`/tv/`'s sizes, and always as text, never markup.

## Building and testing

```sh
cd web
pnpm build:tv                 # /tv/ (dist/tv), checked for Chromium 68
pnpm build:launcher           # the launcher (dist-launcher), checked for Chromium 68
pnpm build:launcher:bundled   # --bundle-ui: a copy of /tv/ in dist-launcher/ui
pnpm exec vitest run ../apps/tv/web/src   # probe, bridge, compatibility vectors, schema
pnpm e2e:launcher             # after build:tv and build:launcher
```

The launcher's build is one classic script (no ES module: a `file://` page's origin is opaque,
and a module script there is a cross-origin fetch). With `--bundle-ui` the launcher opens its
own copy of `/tv/` with `?home=<address>` and reaches the home's API there; the home-served
`/tv/` stays the default, and ignores `?home=`.

`pnpm e2e:launcher` opens the launcher from `file://` with a Samsung and an LG shim, against a
mock home on the TV's `/24`, in English and Arabic, and keeps the launcher's log, the
screenshots and the traces.

## LG (webOS)

- **Player** (`platform/webos-player.ts`): the TV's own `<video>`, with the signed URL set
  directly on it. webOS 5 and later play HLS v7 (TS and fMP4 segments) and MP4 and MKV files
  natively, HDR included, so there is no hls.js and no MSE. The start position is set once the
  metadata is in. The rate is held at 1.0, since webOS plays no other speed. A sidecar file's
  cues are drawn by the clock, and a manifest rendition's text track is kept hidden while its
  active cues are drawn.
- **Device caps** (`platform/webos-caps.ts`): the API's `DeviceCaps` from the TV's description
  and `MediaSource.isTypeSupported`, tested against `shared/playback/caps-vectors.json`. Dolby
  Vision profile 8 is reported only for a model year in `DV_GOOD_YEARS`, which the real-device
  checklist fills; otherwise the home sends the HDR10 base layer.
- **Luna** (`platform/webos-luna.ts`): `webOS.service.request` and `webOS.deviceInfo` when LG's
  webOSTV.js is loaded, else the runtime's own `PalmServiceBridge`. Only documented services are
  called: `com.palm.connectionmanager/getStatus` (ACG `network.query`) and
  `com.webos.service.tv.systemproperty/getSystemInfo` (`systemconfig.query`). Without
  webOSTV.js, a UHD panel counts as HDR10 and HLG, and Dolby Vision is never claimed.
- **Keys:** Back is 461. On Home, after `/tv/`'s confirm, the launcher leaves through
  `webOS.platformBack()`, else `PalmSystem.platformBack()`. `appinfo.json`'s
  `disableBackHistoryAPI` hands Back to the app. Media keys (415, 19, 413, 417, 412) and the
  colour keys (403–406) arrive without registration. The Magic Remote's pointer reaches the
  iframe directly.
- **Package** (`apps/tv/web/webos/`): `appinfo.json` with its icons (cut by
  `tools/icons/make-icons.py`) and its Arabic title. `package.mjs` packs the launcher's build
  with `ares-package` (`@webos-tools/cli`, pinned), unsigned, because LG signs on submission.
  `lint-ipk.mjs` opens the `.ipk` and checks `appinfo.json`.

```sh
cd web && pnpm build:launcher && cd ..
node apps/tv/web/webos/package.mjs --version 1.4.2        # dist/maktaba-webos-1.4.2.ipk
node apps/tv/web/webos/lint-ipk.mjs dist/maktaba-webos-1.4.2.ipk --version 1.4.2
node --test apps/tv/web/webos/webos-package.test.mjs
ares-install --device <tv> dist/maktaba-webos-1.4.2.ipk  # a TV in Developer Mode
```

`web/e2e/launcher/webos.ui.spec.ts` plays a video from `/tv/` through the launcher's `<video>`
with a webOS shim. A `v*` tag whose launcher changed attaches the `.ipk` to the release
(`tv-web-release.yml`). The store steps and the real-device checklist are in
`docs/stores/lg.md`.

## Samsung (story 58.10)

The Samsung-only code is `apps/tv/web/src/platform/tizen.ts`, `src/platform/tizen/` and the
package folder `apps/tv/web/tizen/`.

- **The player is AVPlay** (`webapis.avplay`), Samsung's own: HTML5 media on Samsung TVs stops
  at 1080p, while AVPlay plays HLS (TS or fMP4) and MP4, MKV and TS files by URL in 4K and HDR,
  with Dolby passthrough. `play` opens the signed URL (AVPlay cannot add a header per segment),
  covers the screen under the transparent iframe, letterboxed, asks for 4K for a 2160p source
  (the optional `height` of `play`; `ADAPTIVE_INFO FIXED_MAX_RESOLUTION=3840X2160`, with
  `SET_MODE_4K` for an older firmware), seeks to `start_sec` and plays. `audioTrack {i}` is the
  i-th audio track of the stream, and `state` lists them (`audio_tracks`). AVPlay is suspended
  while the TV's app switcher hides the app, and restored after.
- **Subtitles are drawn by the launcher** (`src/subtitles/`, shared with LG), since the TV's
  renderer reverses Arabic: a sidecar WebVTT from `play`'s `subtitles` is fetched and timed by
  AVPlay's play time; an embedded track (`subtitle {id: "embedded:<n>"}`, AVPlay's text track n)
  comes as text from AVPlay. The size is the optional `subtitle_scale` of `play` and `subtitle`.
- **Errors:** AVPlay's error becomes `state.error` (`"avplay <reason>"`); `/tv/` retries once
  with `force_transcode`, and the launcher says **The TV couldn't play this. Trying a converted
  version…** over the video meanwhile.
- **What the TV plays** (`device_caps` in `hello`, the API's shape of story 58.02): the panel
  from `webapis.productinfo`, the codecs from the TV's decoder, HDR from `webapis.avinfo`, and
  passthrough from the audio output mode (`tizen.tvaudiocontrol`). It never lists Dolby Vision
  (Samsung TVs have none) or HDR10+, and the mapping is tested against the Samsung vectors of
  `shared/playback/caps-vectors.json`.
- **Keys:** the media and colour keys are registered with `tizen.tvinputdevice` at start (a
  Samsung TV sends an unregistered key to no page). Back is 10009; after `/tv/`'s **Leave
  Maktaba?**, the launcher leaves through `tizen.application`.

**The package** (`apps/tv/web/tizen/`): `config.xml` (the app ID, Tizen 5.5 and up, the
privileges, open access and navigation for the homes' per-home names, a CSP that allows frames
and requests over `https:` and the LAN's `http:` only), the icon, `package_wgt.py` (the stage
and an unsigned `.wgt`; `index.html` gains Samsung's `webapis.js`), `lint_wgt.py` (config.xml
exactly as listed) and `sign_wgt.sh` (Tizen Studio's CLI with the project's certificates, from
the release secrets only). `tv-web-release.yml` builds and signs it on a tag whose launcher
changed. The store, the checklist on real TVs and the signing certificates are in
[docs/stores/samsung.md](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/docs/stores/samsung.md).

```sh
cd web && pnpm build:launcher && cd ..
python3 apps/tv/web/tizen/package_wgt.py --dist web/dist-launcher --version 1.0.0 --out build/tizen
python3 apps/tv/web/tizen/lint_wgt.py build/tizen/maktaba-tizen-1.0.0-unsigned.wgt --version 1.0.0
python3 -m unittest discover -s apps/tv/web/tizen -p 'test_*.py'
```
