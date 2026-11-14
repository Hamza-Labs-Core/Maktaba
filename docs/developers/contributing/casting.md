# Casting: the cast grant and the receiver

Maktaba casts to Google Cast devices (Chromecast, the Google TV Streamer, TVs with Google TV
or Cast built in) with a receiver of its own, a web page Google loads on the device. A
receiver page cannot sign in, and it must never hold the person's own token. So the sender
(the Android Player, the Apple Player, the web) mints a **cast grant** on the home and hands
it to the receiver. With the grant, the TV opens its own stream sessions, reports where it
is, renews its URLs and walks a play queue, so the phone can sleep or leave the house and
the home still knows where the TV stopped (Epic 58, stories 58.06 and 58.07).

This page is the contract for sender and receiver authors. The route is in the
[API reference](/api/) under Sessions.

## Minting a grant

```http
POST /api/cast/grants
Authorization: Bearer <the person's access token>
Content-Type: application/json

{"video_id": "8f0c…"}            // or {"queue_id": "…"}, never both
```

```json
{
  "grant": "cast_eyJhbGciOiJSUzI1NiIs…",
  "scope": "cast",
  "expires_at": "2026-11-14T22:41:07Z",
  "video_id": "8f0c…"
}
```

| Caller | Answer |
|---|---|
| no sign-in | 401 |
| a viewer with the video's library, an admin | 201 |
| a restricted account, for a video within its limit | 201; the grant carries its limit |
| a restricted account, for a video above its limit | 404, the same answer as a missing video |
| a viewer, for a video in a library they were not given | 403 `access-denied` |
| someone else's play queue, or no such queue | 404 |
| the admin token (no person) | 403 `cast_needs_person` |
| a sign-in through Maktaba Cloud | 403 `cast_via_cloud`: it may not mint a credential that outlives its link |
| a cast grant | 403 `cast_scope`: a grant mints no grant |
| neither or both of `video_id` and `queue_id` | 422 |

A home that answers 404 for the route itself is older than the cast grant. A sender then
casts to Google's Default Media Receiver with a plain signed URL, as the Android Player
always has.

## What a grant is

- An RS256 token of the home's own signing keys, written `cast_` + the token. Its audience
  is `cast`, so no route that takes the home's tokens (audience `api`) accepts it, and a
  grant never works as a sign-in.
- It names **one video or one play queue** of its user, and lasts **at most six hours**.
- It acts **as its user, read again on every request**: a deleted user's grant is no one
  (401), a library taken away is gone at once, and a restricted account's parental limit
  applies as it does to that account's own sign-in, with the device age limit the minting
  sign-in carried. A video hidden after the mint answers 404.
- It is **never an admin**, whoever minted it.

Treat it as a secret: send it only in `Authorization: Bearer <grant>`, never in a URL or a
log line, and never show it.

## What a grant may do

Only these routes, and only for the items the grant names. Anything else, any other video,
any other session, any other queue, is **403 `cast_scope`**.

| Route | For |
|---|---|
| `GET /api/discovery/home` | the home's identity and addresses (it needs no grant) |
| `POST /api/stream/sessions` | the grant's video, or an item of its queue |
| `GET`, `DELETE /api/stream/sessions/{id}` | a session of the grant's user for one of those items |
| `POST /api/stream/sessions/{id}/progress`, `…/errors` | the same sessions: progress lands on the user's own Continue Watching |
| `GET /api/videos/{id}`, `…/chapters`, `…/subtitles`, `…/poster` | the items |
| `GET /api/me/play-queues/{id}`, `PUT /api/me/play-queues/{id}/current` | the grant's queue: read it, move to the next item |

**Renewing URLs.** A session's signed URLs live for the session's lifetime or the video's
length plus an hour, whichever is longer, and at most 15 minutes for a restricted account. A receiver renews them by opening a session again for the
same item at its position (`POST /api/stream/sessions` with `start_sec`): the home hands back
the user's open session with freshly signed URLs.

**When the grant ends.** A receiver lets the item that is playing finish (the segment URLs
already signed play out), then shows that the cast has ended and asks for a new one from the
phone. It never stops a film mid-play for the grant's sake.

## What the sender hands the receiver

The grant goes in the Cast load request's `MediaInfo.customData`, never in headers:

```json
{
  "home": "https://192-168-1-20.<home-id>.<direct-domain>:8444",
  "urls": ["https://192-168-1-20.<home-id>.<direct-domain>:8444", "https://<home-id>.<relay-domain>"],
  "grant": "cast_…",
  "video_id": "…", "queue_id": "…", "queue_pos": 0, "start_sec": 754,
  "lang": "ar", "api_level": 1
}
```

- **`home`** is an HTTPS address the TV can reach: the home's per-home HTTPS name on the
  LAN, or the relay. A receiver page is HTTPS, and a browser blocks plain-HTTP media from
  it. Never `localhost`, a `.local` name or a bare IP over HTTPS (its certificate check
  fails).
- **`urls`** is the home's own list from `GET /api/discovery/home` (the LAN addresses first,
  the relay last), as the sender read it. The receiver asks `home` first and waits 3 seconds;
  then it uses the relay from this list. Without it, a TV that cannot reach `home` has nowhere
  else to go.
- **`video_id`** or **`queue_id`** names the item, as the grant does. With `queue_id`,
  `queue_pos` is where to start (default: the queue's `current_pos`).
- **`lang`** is the sender's language (`en` or `ar`): the receiver's screens follow it.
- **`api_level`** is the home's level from `GET /api/discovery/home`. The receiver page is
  served per level at `https://cast.maktaba.tv/r/<api_level>/`.

## The senders

Every sender follows the same rules, kept in `shared/cast/`:

- **`app.json`** holds the receiver's application ID from Google's Cast console
  (docs/stores/cast.md), its URL and the custom namespace. The ID is public, never a secret,
  and each build reads it: vite for the web (`MAKTABA_CAST_APP_ID` overrides it). **Empty, no
  sender shows Cast**: no button, no Cast SDK started.
- **`load-vectors.json`** holds the custom data above, case by case, with the sender's own
  address and the home's discovery list as input. Each sender runs every case in its unit
  tests. When no address qualifies (a home reached only at `http://` or by IP), the sender
  hides Cast for that home.

A sender mints the grant with the person's own sign-in when the cast starts (and for the
item that plays next when the person picks another), loads it, then follows the receiver's
media status: the item, the queue position, the time. While the receiver plays, the sender
posts no progress of its own: the receiver reports it with the grant, and Continue Watching
follows the TV. About 30 minutes before the grant ends, a sender that is still running mints a
fresh one and sends it as the namespace's `grant` message.

The grant's refusals have their problem pages: `cast_via_cloud`, `cast_needs_person`,
`cast_scope` (shared/api/problems.json).

**The web** (`web/src/lib/cast/`, the player's `CastControl`): Google's Cast Web Sender, loaded
only in Chrome on an HTTPS page with an ID set. A page reached at `http://`, another browser,
or a sign-in through Maktaba Cloud shows no button. Tests: Vitest against a fake of the SDK
(`web/src/test/fakeCast.ts`). The web is served by the home itself, so it never meets a home
older than the cast grant.

**The Apple Player** (iPhone and iPad; the Cast SDK has no tvOS or macOS):
`MaktabaKit/Cast.swift` builds the custom data and mints the grant; `Player/Cast/` puts
Google's Cast iOS SDK (`google-cast-ios-sdk`, pinned) behind a protocol, with AirPlay first
in the TV list. Before the first cast, Google's required local network screen explains the
prompt iOS shows next. The ID reaches the build through `apps/apple/Config/Cast.xcconfig`,
written from `app.json` by `tools/apple/cast-app-id.py` (the `apple-apps` job fails when the
two differ); it also sets the `NSBonjourServices` entries `_googlecast._tcp` and
`_<ID>._googlecast._tcp`. A home older than the cast grant (its route answers a plain 404)
gets the plain signed stream URL, which the receiver plays as it came. Tests: MaktabaKit unit
tests over the vectors, and UI tests against a fake Cast context.

## The receiver

`web/packages/castreceiver`, served at `https://cast.maktaba.tv/r/` (the one URL registered in
the Cast console, docs/stores/cast.md). Its loader picks the receiver build for the sender's
`api_level` from `/r/builds.json`: the newest build at or below it. A home older than every
build gets an "Update the home" screen, and the load fails with `customData.error` `update`.
A build stays while its level is in the 12-month window.

For each load it:

1. finds the home: `home` within 3 seconds, else the relay from `urls` (the Remote badge);
2. reads the item (`GET /api/videos/{id}`, or the queue and its item);
3. opens its own session: `POST /api/stream/sessions` with `client_profile: "cast"` and
   `device_caps` from the device's `canDisplayType` and its HDR, Dolby Vision and Atmos
   capabilities (playback.md). A home older than `device_caps` answers 400, and the receiver
   asks again without it;
4. plays it: HLS through Shaka, with `hlsSegmentFormat` fMP4 for HEVC and AV1 and TS otherwise;
   direct play with the sidecar WebVTT tracks signed with the session's `stream_sig`;
5. reports progress every 10 seconds, renews the URLs two minutes before they end, walks the
   queue (`PUT …/current`), and retries a stream the TV cannot play once with
   `force_transcode`.

The media status Google sends every connected sender carries the load's custom data without the
grant or the address: `video_id`, `queue_id`, `queue_pos`, `lang`.

### The custom namespace

`urn:x-cast:tv.maktaba.cast`, JSON messages.

| Direction | Message | Meaning |
|---|---|---|
| sender → receiver | `{"type": "grant", "grant": "cast_…"}` | a fresh grant for the cast that is playing (mint one before the first ends, for a cast longer than six hours) |
| receiver → senders | `{"type": "state", "state": "loading" \| "ready" \| "playing" \| "paused" \| "idle", …}` | `ready` adds `remote`, `video_id` and `queue_pos` |
| receiver → senders | `{"type": "error", "error": "home" \| "format" \| "grant" \| "update"}` | the screen the TV shows: the home out of reach, a format the TV cannot play after the transcode retry, the grant over, the home older than every build |

A failed load also answers the sender's load request with `LOAD_FAILED` and the same `error`
in its `customData`.

## Cross-origin calls

The receiver is served from `https://cast.maktaba.tv`. The home's API answers that origin
for bearer requests only: its answers carry `Access-Control-Allow-Origin` for it and never
`Access-Control-Allow-Credentials`, so no cookie is sent or read. Signed media URLs (`?sig=`)
on the home's web port answer any origin, since the signature is the credential.

## Tests

- `api/internal/castgrant`: minting and parsing, the credential middleware, the gate without
  a database.
- `api/internal/router/p58_cast_pg_test.go` (Postgres): who may mint, a grant for one video
  used for another (403), the queue walk, the user's limit, a deleted user, an expired grant.
- The parental bypass rehearsal probes `POST /api/cast/grants` with every hidden video.
- `web/packages/castreceiver` (`_cast-receiver.yml`): Vitest with a fake `cast.framework` (the
  load, `device_caps`, TS or fMP4, progress, renewal, the queue, the grant's end, the relay
  fallback, the loader's pick and window), and Playwright with a stub of Google's SDK and a mock
  home at 1920×1080, dark, in English and Arabic, against visual references. No emulator runs a
  Cast receiver: real devices are docs/stores/cast.md's checklist.
