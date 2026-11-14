# Casting: the cast grant

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
  "grant": "cast_…",
  "video_id": "…", "queue_id": "…", "start_sec": 754,
  "lang": "ar", "api_level": 1
}
```

- **`home`** is an HTTPS address the TV can reach: the home's per-home HTTPS name on the
  LAN, or the relay. A receiver page is HTTPS, and a browser blocks plain-HTTP media from
  it. Never `localhost`, a `.local` name or a bare IP over HTTPS (its certificate check
  fails).
- **`lang`** is the sender's language (`en` or `ar`): the receiver's screens follow it.
- **`api_level`** is the home's level from `GET /api/discovery/home`. The receiver page is
  served per level at `https://cast.maktaba.tv/r/<api_level>/`.

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
