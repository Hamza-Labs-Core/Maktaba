# Story 58.03 — Sign a TV in from anywhere: a code through Maktaba Cloud

> Epic 58 · Phase 1 · Depends on: — · Needed by: 58.04, 58.09, 58.12, 58.14, 58.17, 58.18

## Why

A TV signs in today in one of two ways:
- **By a code the home makes.** It shows `ABCD-1234` (`POST /api/pairing/device`), a phone
  approves it, and the TV redeems the code. This needs the TV to know the home first: Android
  TV and Apple TV find it with mDNS (`_maktaba._tcp`).
- **By typing a username and password** on an on-screen keyboard.

The new platforms cannot always find the home:
- Samsung and LG web apps have no mDNS or SSDP API (research.md §3.1, §4.1).
- Roku has no mDNS API either.
- A TV at a relative's house is not on the home's network at all.

The cloud already knows how to reach each registered home. `GET /v1/homes` lists a home's
`connections` (`local`, `local_ip`, `direct`, `relay_hint`, `relay`; story 50.13). What is
missing is a way to hand that list, and a sign-in, to a TV that has no account on anything yet.

## Description

**The flow** (RFC 8628 device authorization, adapted):

```
TV                        Maktaba Cloud                    Phone (Player, signed in to the home)
│ POST /v1/tv/codes ─────▶ code ABCD-1234, poll secret
│ shows code + QR (maktaba.tv/tv?c=ABCD1234)
│                                                 Profile → Approve a TV → types or scans code
│                                                 POST /api/pairing/cloud-approve {code} ─▶ home
│                         ◀── home (cloudlink): code ABCD-1234 is mine; grant G, connections
│ POST /v1/tv/codes/poll ─▶ {connections, home_id, home_name, grant G}
│ tries connections in the 50.13 order
│ POST /api/pairing/device/token {device_code: G} ─────────────────────▶ home: signed in as the approver
```

- **The TV asks the cloud for a code.** It sends `POST /v1/tv/codes {platform, app_version}`
  with no sign-in. The answer is `{code, poll_secret, expires_in: 600, interval: 5}`. The code is
  8 characters from the pairing alphabet of story 15.06 (Crockford's, with no `I L O U`) without
  `0` and `1`, shown as `ABCD-1234`. The
  QR holds `https://maktaba.tv/tv?c=ABCD1234`.
- **A person approves it on a device already signed in to the home.** There are two ways:
  - **The Player.** In the Android or Apple Player (and the web's `/pair`), Profile → **Approve a
    TV** (the existing `approve_tv` entry) takes either code shape. A home pairing code is
    approved on the home as today. A cloud code is read first (`GET /api/pairing/cloud-code/{code}`: the TV's platform and
    country) and then goes to `POST /api/pairing/cloud-approve {code}`.
    The home then creates a pairing code for the approver (`device_kind: tv`), approves it at
    once, and tells the cloud over its link (`cloudlink`): "code X is mine, here are my
    connections and grant G" (G is that pairing code's redeem secret). The approver needs no
    cloud account. The home only needs to be registered with the cloud, which any home with
    remote access already is.
  - **maktaba.tv/tv in a browser**, signed in to a Maktaba Cloud account that is linked to a user
    of the home (Epic 50). The person picks the home, and the cloud asks that home over its link
    to approve the code as the linked user. Linked sign-ins stay play-scoped (`ApplyCloudScope`),
    so the TV is play-scoped too, as they already are.
- **The TV polls the cloud.** `POST /v1/tv/codes/poll {code, poll_secret}` answers `pending`
  until the code is approved, then the payload once, then `410`. The TV tries the connections in
  the order of story 50.13 and redeems G on the first home address that answers
  (`POST /api/pairing/device/token`, which exists). The home's own sign-in rules apply from there,
  including `client_floor` (58.01) and parental controls (58.14).

**Limits and safety:**
- A code lasts 10 minutes and is used once. A poll faster than `interval` gets `slow_down`.
- A cloud IP may hold at most 10 open codes, using the cloud's existing rate limiter on
  `/v1/auth/*`, extended to `/v1/tv/*`.
- G is the home's own single-use pairing secret, valid 10 minutes. The cloud stores it sealed
  (the cloud's existing secret box) and deletes it on the first poll that returns it.
- The approver's list of app sign-ins (Profile, `GET /api/me/app-sign-ins`) shows the new
  sign-in as "<platform> TV, via Maktaba Cloud code", where it can be signed out.
- **Threat:** someone who learns a code before it is approved can only get a person to approve
  *their* TV. So the approve screen shows the TV's platform and its public IP's country (from the
  cloud) and asks "Is this your TV?" before approving.

**Without Maktaba Cloud** (a home never registered), the LAN probe of story 58.09 and the home's
own code are the way. Typing the home's address and a password stays as the last fallback.

**The cloud's data** (slot `0058`, `cloud/migrations/00580001_tv_codes.sql`):

| Column | |
|---|---|
| `code` text pk | the 8 characters |
| `poll_secret_hash` bytea | sha-256 |
| `platform`, `app_version` text | shown to the approver |
| `client_ip_country` text | shown to the approver |
| `server_id` uuid null | the home that claimed it |
| `payload_sealed` bytea null | connections, home id and name, G |
| `created_at`, `expires_at`, `claimed_at`, `delivered_at` timestamptz | |
| `last_poll_at` timestamptz null | the last poll, for `slow_down` |

Rows are swept an hour after expiry.

## Acceptance criteria

- **Given** a Samsung TV at a relative's house and an iPhone signed in to the home, **when** the
  code is approved in the Player, **then** the TV is signed in as the approver through the
  relay within one poll interval.
- **Given** a TV on the home's LAN, **when** it receives the payload, **then** it uses the `local`
  or `local_ip` connection, not the relay.
- **Given** a code approved and already delivered, **when** it is polled again, **then** it
  answers `410` and holds no secret.
- **Given** a viewer approving a code, **then** the TV signs in as that viewer, with that
  viewer's libraries and parental policy. An admin approving gives a sign-in that never acts as
  an admin (it reads every library, named one by one, and stays so when it refreshes): a cloud
  code can be read out to anyone anywhere, so approving one hands over watching, never running
  the home.
- **Given** a home not registered with the cloud, **then** Approve a TV says a cloud code needs
  remote access turned on and offers the home's own code instead.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit (cloud) | handler | create, poll pending, slow_down, expire | states and status codes |
| T02 | Go Postgres (cloud) | migration 00580001 | up/down; sweep | table, index on `expires_at`; rows gone after expiry + 1 h |
| T03 | Go Postgres (home) | `cloud-approve` as viewer, admin, a restricted child, no one | approve | the grant names that user; no one → 401; a restricted child → allowed, TV restricted (role test) |
| T04 | Cloud rehearsal (`cloud/rehearsal`) | a home linked through the tunnel | full flow, TV client from the rehearsal harness | signed in through the relay; payload delivered once |
| T05 | Go unit | 11th code from one IP | create | 429 |
| T06 | Kotlin/Swift/web unit | Approve a TV with a cloud-shaped code | submit | calls `cloud-approve`, shows platform and country, asks to confirm |

## Strings

Approve a TV in the Players and the web gains:

| Key | English | Arabic |
|---|---|---|
| `approveTv.cloud.confirm` | Is this your TV? %1$s, in %2$s. | هل هذا تلفازك؟ %1$s، في %2$s. |
| `approveTv.cloud.needsRemote` | A code from Maktaba Cloud needs remote access turned on for this home. Use the code from the home instead. | يحتاج الرمز القادم من Maktaba Cloud إلى تفعيل الوصول عن بُعد لهذا المنزل. استخدم رمز المنزل بدلًا منه. |
| `approveTv.cloud.done` | Approved. The TV is signing in. | تمت الموافقة. التلفاز يسجّل الدخول الآن. |
| `devices.viaCloudCode` | %1$s TV, via Maktaba Cloud code | تلفاز %1$s، عبر رمز Maktaba Cloud |

The TV side's strings are in 58.09 and 58.12.

## Edge cases

- **The cloud is down** (story 50.13's outage): the TV falls back to the LAN probe and the home's
  own code, and says the cloud code is unavailable.
- **The home is offline when the code is approved from maktaba.tv**: the cloud answers that
  the home is not reachable. Nothing is approved.
- **Two TVs show codes at once**: each code is separate. A person approving one never approves
  the other.

## Build plan

| File | Change |
|---|---|
| `cloud/internal/handlers/tvcodes/` (new, beside `homes/`) | create, poll, approve-from-web |
| `cloud/migrations/00580001_tv_codes.sql` | table |
| `cloud/internal/cloudlink/` | the home → cloud "claim code" message |
| `api/internal/handlers/discovery/device.go` (the pairing routes) | `POST /api/pairing/cloud-approve` |
| `apps/android/player/…/ApproveTv*.kt`, `apps/apple/MaktabaKit/…/ApproveTV*.swift`, `web/src/pages/Pair.tsx` | cloud-shaped codes |
| `web/packages/cloud-web/` | `maktaba.tv/tv` page |
