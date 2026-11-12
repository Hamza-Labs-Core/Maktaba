# Parental controls: for client and API authors

A home can limit what a child's account sees and how long it watches. The home enforces every
rule; an app only shows what the home answers. This guide is for people who write a client of
the API, or add a route to it. It says what a client must do, which routes and answers exist,
and how the API keeps a new route from skipping the filter.

Every route here is described in the [API reference](https://maktaba.dev/api/), under
**Parental controls**, with its request and response in full. This page gives the rules that a
schema does not.

## The model in one minute

- A **restricted account** is a viewer with a parental policy: an age limit (`max_tier`: 0, 9,
  13, 16, 18, or -1 for nothing unless allowed), a rule for unrated videos, and allow and block
  rules. An admin is never restricted.
- A **guardian** is an admin, or a user an admin named for that child. A guardian changes the
  child's limit and rules. Only an admin lifts a restriction or names guardians.
- A **device signal** is what an Apple device says about who holds it. It can only tighten the
  limit of that sign-in.
- **Time rules** say how long, and in which hours, a restricted account may watch. The home
  counts the time.
- Parental controls are **off** in a home until the first account is made restricted or the
  first library gets a default age. An admin can switch them. While they are off, a device
  signal is recorded and limits nothing. A restricted account's own policy always applies.

## What a client asks first

`GET /api/capabilities` carries the caller's `parental` block:

```json
{ "parental": { "enabled": true, "restricted": true, "tier_limit": 9, "can_manage": false } }
```

| Field | Meaning |
|---|---|
| `enabled` | parental controls are turned on in this home |
| `restricted` | this account is restricted |
| `tier_limit` | the limit this sign-in is held to, by the account's policy or its device; `null` when none |
| `can_manage` | the caller is an admin, or a guardian of at least one account: show the family settings |

When the home cannot read the account's limits, the whole route answers **503** with `code`
`parental_unavailable` and `Retry-After`. It never answers a block that says nothing limits the
account. A client keeps what it had, or shows nothing, and asks again after the wait. It does
not treat the failure as "no limit".

`GET /api/me/parental` gives the limit in more detail (`restricted`, `max_tier`, `unrated`,
`allow_only`, `version`, and `device_limit` and `device_pending` when the device gave a signal).
When `version` or the limit changes, drop everything the app kept for the account: shelves,
pictures, the queue.

## A hidden item is a missing item

A restricted account is never told that something is hidden. A video, a series, a collection,
a channel or a library above the limit answers **404**, with the same status, headers and body
as an id that names nothing. The same holds for its poster, subtitles, chapters and thumbnail.
Lists, counts, search results and suggestions are computed under the filter, so they hold
nothing to hide.

For a client this means:

- Treat a 404 on an item as "not available". When `GET /api/me/parental` says the sign-in is
  limited, show a page that says so and offers Ask a parent. Do not show an error.
- Never show a title, a poster or a count the app kept from before the limit changed.
- Do not probe to tell hidden from missing. The home answers both the same on purpose.
- A route that is not for a restricted account at all (personal access tokens, YouTube search
  and import, the guide exports, admin routes) answers **403** with `code` `restricted`.

## Who gets which status

Every role rule is enforced by the API. Hiding a control in an app is not a rule.

| Caller | On the routes of a child's account |
|---|---|
| no sign-in | 401 |
| a viewer who looks after no one | 403 on a change; 404 on reading the policy, the rules or the activity |
| the restricted child | 403, `code` `restricted`, on a change; 404 on reading its own policy (its own state is `GET /api/me/parental`) |
| the child's guardian | allowed; lifting a restriction and naming guardians are 403 `admin_only` |
| **another child's guardian** | **404**, as if the account did not exist, on every route a guardian may use: the policy, the rules, the activity, the time rules, a request, a signal |
| an admin | allowed |
| a guardian signed in through Maktaba Cloud with a play-only sign-in | reads; a change is 403 (`cloud-session`) |

A route only an admin may use (naming a guardian, the home's settings, a tier) answers 403 to
everyone else. The child signed in through Maktaba Cloud gets exactly what the child gets.

## Problem types and codes

Every refusal is a problem (`application/problem+json`) with a `code`. Match on the status and
`code`.

| Status | `code` | Type slug | When |
|---|---|---|---|
| 403 | `restricted` | `restricted` | a restricted account asked for something it may not do. On a channel it carries `reason: "programme"` and `retry_at` |
| 403 | `admin_only` | `admin-only` | only an admin can do this |
| 403 | `not_restricted` | `not-restricted` | only a restricted account can ask a parent or unlock |
| 409 | `stale` | `parental-policy-stale` | the policy changed since it was read; read it again |
| 409 | `restricted_admin` | `restricted-admin` | a restricted account cannot be an admin |
| 409 | `restricted_guardian` | `restricted-guardian` | a restricted account cannot look after another |
| 422 | `no_library_access` | `no-library-access` | an allow rule on a library the account cannot open |
| 429 | `too_many_requests` | `parental-too-many-requests` | 20 requests of the child already wait |
| 409 | `request_decided` | `parental-request-decided` | the request was already answered |
| 410 | `request_expired` | `parental-request-expired` | the request expired, or its subject is gone |
| 403 | `pin_wrong` | `parental-pin-wrong` | a wrong PIN, a user who is not this child's guardian, or a guardian with no PIN |
| 429 | `pin_locked` | `parental-pin-locked` | too many wrong tries; `retry_after` (seconds) and the `Retry-After` header |
| 403 | `password_wrong` | `password-wrong` | the password typed to set a PIN is not right (`too_many_tries` with 429 after ten) |
| 409 | `no_sign_in` | `age-signal-no-sign-in` | an age signal from a credential that is no app sign-in |
| 403 | `device_limited` | `device-limited` | a device that is itself limited cannot clear a signal |
| 409 | `restrictions_exist` | `parental-restrictions-exist` | turning parental controls off while an account is restricted |
| 403 | `parental_time` | `parental-time` | the account's watching time is over (below) |
| 503 | `parental_unavailable` | — | the limits cannot be read (capabilities) |

The type slugs above are the ones the handlers name. The API sends only types that are entries
of the [problem registry](problem-types.md), and an unregistered one goes out as the general
type of its status. So match on `code`, which is always sent as listed, with `reason`,
`next_allowed_at`, `retry_at` and `retry_after` beside it.

## The routes

| Route | Who | What |
|---|---|---|
| `GET /api/parental/ratings` | any sign-in | the tier scale and the region tables |
| `PUT /api/{videos,libraries,series,collections,channels}/{id}/age-tier`, `PUT /api/libraries/{id}/youtube/sources/{sid}/age-tier` | admin | set or clear a tier (`{tier}`, `null` clears) |
| `GET /api/parental/children` | any sign-in | an admin: every restricted account; a guardian: their own; anyone else: none |
| `GET`, `PUT`, `DELETE /api/users/{id}/parental` | admin, guardian (delete: admin) | the policy, its guardians and the sign-ins' signals; `PUT` carries `version` |
| `POST /api/users/{id}/parental/guardians`, `DELETE …/{gid}` | admin | name or remove a guardian |
| `GET`, `POST /api/users/{id}/parental/rules`, `DELETE …/{rid}` | admin, guardian | allow and block rules |
| `GET /api/users/{id}/parental/activity` | admin, guardian | what was watched, blocked and asked for |
| `GET /api/me/parental` | any sign-in | the caller's own limit |
| `POST /api/me/age-signal` | any app sign-in | the device's age signal (below) |
| `POST /api/users/{id}/parental/signals/{family}/clear` | admin, guardian | apply a looser signal that waits |
| `POST /api/me/parental/requests` | restricted | ask a parent (below) |
| `GET /api/me/parental/requests` | restricted | what became of its own requests (below) |
| `GET /api/parental/requests`, `POST …/{id}/approve`, `POST …/{id}/deny` | admin, guardian | answer a request |
| `GET /api/me/parental/guardians` | restricted | who has a PIN to type |
| `POST /api/me/parental/unlock` | restricted | a guardian's PIN on the child's device |
| `GET`, `PUT /api/me/parental-pin` | admin, guardian | whether the caller's PIN is set; set it (six digits, with the password) |
| `GET`, `PUT /api/parental/users/{id}/time-rules`, `GET …/time-usage`, `POST …/time-grants` | admin, guardian | time rules, the count, more time for today |
| `GET /api/me/parental/time` | any sign-in | the caller's time now, for a countdown |
| `GET /api/parental/review`, `POST /api/parental/review/{video_id}` | admin (a guardian reads) | tiers detected from transcripts that wait for an admin |
| `GET`, `PUT /api/parental/settings`, `POST …/notice/dismiss`, `GET /api/parental/library-suggestions` | admin | the home's switch, the child-device notice, a first tier per library |
| `GET /api/channels/{id}/access`, `POST /api/channels/{id}/play` | any sign-in | may the caller watch the channel now; its signed stream URL |
| `GET`, `PUT /api/tv/hdhr/allowed` | admin | the addresses allowed to use the tuner |

## The age signal

An app on a device that knows something about who holds it posts it for its own sign-in:

```http
POST /api/me/age-signal
{ "age_range": { "lower": 9, "upper": 12, "declaration": "guardian_declared" },
  "movie_ceiling": 300, "tv_ceiling": 400, "region": "US" }
```

Every field is optional. `movie_ceiling` and `tv_ceiling` are on Apple's 0 to 1000 scale (1000:
no limit). The answer:

```json
{ "tier_limit": 9, "pending_looser": false, "refresh": true, "enabled": true }
```

- `tier_limit`: the device limit now in force on this sign-in (`null`: none).
- `pending_looser`: what was sent is looser than the limit in force. It is recorded and waits
  for an admin or a guardian.
- `refresh`: refresh the access token now, so the next one carries the limit.
- `enabled`: `false` while parental controls are off in this home. The signal was recorded and
  limits nothing, and `refresh` is `false` too. Keep sending signals as usual: they apply once
  an admin turns parental controls on.

Only an app's sign-in has a device to speak for. A web session or an API token answers 409
`no_sign_in`. A signal never loosens an account's own policy.

## Ask a parent

```http
POST /api/me/parental/requests
{ "subject_kind": "video", "subject_id": "…" }
```

`subject_kind` is one of `video`, `series`, `collection`, `library`, `channel`,
`youtube_source`, `plugin` or `time`. The answer is 201 with the new request, or 200 with the
one already waiting for the same thing.

**More time** is the kind `time`, with the minutes asked for as the text of `subject_id`: a
whole number from 1 to 480, written plainly (`"30"`).

A guardian approves with `POST /api/parental/requests/{id}/approve`. The body is optional:
`expires_at` ends the approval of an item; `minutes` (1 to 480) is for a `time` request only
and defaults to the minutes asked for. The answer is `{request, rule}` for an item and
`{request, grant}` for time.

**What became of a request.** `GET /api/me/parental/requests` answers the caller's own requests
of the last seven days, newest first, 50 at most:

```json
{ "requests": [ { "id": "…", "subject_kind": "video", "subject_id": "…", "status": "denied",
                  "created_at": "…", "expires_at": "…", "decided_at": "…" } ] }
```

`status` is `pending`, `approved`, `denied` or `expired`; a `time` request also has `minutes`.
There is no title and no guardian in it. The first entry for a subject is its latest request.
While a request is `pending`, read the list every 15 seconds while the app is in front, and
stop when nothing is pending. On `approved`, open the item (or, for `time`, read
`GET /api/me/parental/time` once and resume). On `denied`, say so and offer to ask again. On
`expired`, show the ask buttons again.

`POST /api/me/parental/unlock` takes `{guardian_id, pin, subject_kind, subject_id}` on the
child's device. A right PIN answers `{rule}` (the item is allowed until the day ends, 04:00
home time) or, for the kind `time`, `{grant}`. A wrong PIN is 403 `pin_wrong`. A lock is 429
`pin_locked` with `Retry-After`. Never keep, log or check a PIN on the device.

## Time: what a player must do

A restricted account with time rules is served a stream only while the home is counting it.
The stream session routes are:

| Route | For time |
|---|---|
| `POST /api/stream/sessions` | opens a session and answers its signed URLs. There is no renewal route: a player that needs fresh URLs (a limited sign-in's last at most 15 minutes) posts this again for the video. The home checks the filter and the time each time |
| `GET /api/stream/sessions/{id}` | reads it; 404 once its video is hidden |
| `POST /api/stream/sessions/{id}/progress` | the stream's **heartbeat**: it counts the time and keeps the stream running |
| `DELETE /api/stream/sessions/{id}` | closes it; the time is counted up to the close |
| `POST /api/stream/sessions/{id}/errors` | a player's report of a playback error |

A player must:

1. **Post progress at least every 60 seconds while it plays**, and at once on play and pause.
   The streaming service serves a kept account's session only for 60 seconds after the last
   counted call. A player that stops posting is stopped.
2. **Stop on a 403 `parental_time`**, from opening a session or from a progress post. Show
   the reason and `next_allowed_at`. The problem carries:

   | Field | Value |
   |---|---|
   | `reason` | `outside_slot` (not a watching hour), `limit_reached` (the day's time is used), or `no_heartbeat` (the home is not counting this session: no progress came for 60 seconds) |
   | `next_allowed_at` | when the rules allow watching again; absent when no moment in the next eight days does, and with `no_heartbeat` |

3. **Not retry by itself.** Asking again does not give time. Play again when the person asks,
   or when `GET /api/me/parental/time` says `allowed` (after a grant, or at `next_allowed_at`).
4. **Treat a 404 from a progress post as the stream ending.** The video was hidden from the
   account while it played. Stop, and show the blocked page, not an error.
5. **Wait on a 503.** When the home cannot read or count the time it answers 503 with
   `Retry-After`, never free time. Try again after the wait.

`GET /api/me/parental/time` (optionally `?library_id=`) answers `allowed`, `limited`, `reason`,
`remaining_seconds`, `slot_ends_at`, `next_allowed_at` and `now`. Use it for the countdown and
the warnings. It only reads: asking it counts nothing and keeps no stream running. Use the
home's `now`, not the device's clock. Keep no count in the app.

Every stream URL the home signs carries a `tk` claim (`1`: this account's time is kept; `0`:
it is not), and a kept account's URL also carries `tls`, the session whose lease it needs. A
client does not read or change them. It plays the URL it was given, and asks for a new one
through `POST /api/stream/sessions`.

## Live TV

Ask `GET /api/channels/{id}/access` before a channel starts, and again at `until`. While
`allowed` is false, show a "not available right now" screen; the home makes no slate video.
Play through `POST /api/channels/{id}/play`, which answers the signed URL, or 403 `restricted`
with `reason: "programme"` and `retry_at` while the programme on air is above the limit. A
channel above the limit is 404. In the guide, a programme above the limit comes with its time
and no title.

## Adding an API route: the registry

`parental.Surfaces` (`api/internal/parental/surfaces.go`) names **every** route of the API and
says what it is to a restricted account. A gate in the router acts on it for each request.

| Class | For a restricted account |
|---|---|
| `Open` | the handler runs: the route returns no content, or guards itself |
| `Filtered` | the handler runs and applies the filter itself (`parental.SQL` on its lists, `parental.Check` on an id in its body) |
| `ByID` | the gate checks the object named in the path first; a hidden one gets the handler's own answer for a missing id |
| `Refuse` | 403 `restricted` |
| `Closed` | the answer of a route that does not exist |

**A new route needs an entry.** `TestEveryRouteHasParentalSurface`
(`api/internal/router/parental_walk_test.go`) fails on a mounted route that no entry names, and
until then the gate treats the route as closed for a restricted account. To add one:

1. Decide the class. A route that returns content is `Filtered` or `ByID`, never `Open`.
2. Add the entry with its reason. Only `Refuse` and `Closed` may be given by prefix; a route
   that answers a restricted account is named exactly.
3. For `Filtered`, add `parental.SQL` next to the query's library scope, and compute counts
   under it. For a single object, use `parental.Check`, and answer a hidden one exactly as a
   missing one.
4. Give a `GET` route that returns content a probe, so the bypass rehearsal asks it.
5. If the route is one of the parental routes themselves, add its row to the role table.
6. Describe it in `shared/api/openapi.yaml`, with its Arabic.

## Running the proofs locally

Both need a Postgres to use, named by `MAKTABA_TEST_PG_URL`; each test makes and drops its own
scratch database. Run from `api/`:

```sh
# the registry walk
go test -run 'TestEveryRouteHasParentalSurface|TestParentalSurfaces' ./internal/router/

# the role table: every parental route as each of eight callers
go test -count=1 -run 'TestP57RoleTable' ./internal/router/

# the bypass rehearsal
go test -tags parental_bypass -count=1 -run 'TestParentalBypass' ./internal/router/
```

- **The role table** (`p57_roles_pg_test.go`) walks every parental route as no one, a viewer,
  the child, its guardian, another child's guardian, an admin, the admin token and the child
  signed in through Maktaba Cloud. A new parental route fails
  `TestP57RoleTableNamesEveryRoute` until it has a row.
- **The bypass rehearsal** (`p57_bypass_pg_test.go`, build tag `parental_bypass`) seeds a home
  and asks every probe of the registry, and the surfaces a `GET` cannot ask, as a caller held
  to 9+: the child, the child through Maktaba Cloud, that sign-in through the relay, and an
  unrestricted account whose device said 9+. An answer leaks when it names a hidden item, or
  when a hidden id is answered differently from an id that names nothing. It proves itself: an
  unrestricted viewer must find the hidden items, and with the filter taken off one surface it
  must fail. It runs on Postgres only.

In CI both run in the `parental-bypass` job whenever the API, the streaming service, the
migrations or the rating tables change. The job fails when a test was skipped for want of
Postgres, and keeps the home's log as the artifact `parental-bypass-log`.
