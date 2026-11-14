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
  answered only for a call that carries a bearer token (or its preflight), and before a token
  for `/api/auth/me`, `/api/auth/refresh` and `/api/pairing/device/token`: a web page can
  claim `null` too, and must read nothing that needs no sign-in. Starting a code
  (`POST /api/pairing/device`) is not answered, so the `--bundle-ui` copy signs in by the
  cloud code (a grant it redeems at the token route).

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
