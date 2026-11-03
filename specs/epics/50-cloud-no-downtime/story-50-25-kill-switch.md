# Story 50.25 — Kill switch and the signed broadcast revocation list

> Epic 50 · Phase B · Depends on: 50.21, 50.22 · Needed by: 50.24, 50.29, the broadcasting gate

## Why

A host has to be able to stop a broadcast fast: after a valid notice on something airing now, an
urgent report, an abusive publisher, or an incident on our side. The stop has to reach two
places:

- **the edge**, so viewers stop within a minute;
- **the home**, so it stops uploading and tells the publisher why.

It has to keep working when the cloud API is down. Epic 54 §6.3 already has the pattern: an
Ed25519-signed list with a version that only goes up, sticky entries, and short-lived copies.

## Description

### 1. The list

`broadcast-revoked.json` is signed by the cloud's **broadcast signing key**: Ed25519, with a
`kid`, separate from the entitlement and plugin keys. The edge's `CLOUD_PUBKEYS` and every home
hold the public keys.

```json
{
  "schema": 1,
  "version": 1842,
  "issued_at": "2026-11-03T12:00:00Z",
  "expires_at": "2026-11-05T12:00:00Z",
  "entries": [
    {"id": "r-91", "kind": "channel",   "ref": "ch_7Q4F", "action": "off_air",   "reason": "notice", "since": "…"},
    {"id": "r-92", "kind": "publisher", "ref": "pub_42",  "action": "terminate", "reason": "repeat_infringer", "since": "…"},
    {"id": "r-93", "kind": "programme", "ref": "ch_7Q4F", "until": "2026-11-03T21:00:00Z", "action": "slate", "reason": "notice"},
    {"id": "r-94", "kind": "home",      "ref": "srv_9",   "action": "suspend",   "reason": "abuse"},
    {"id": "r-95", "kind": "global",    "action": "off_air", "reason": "incident"}
  ]
}
```

| Action | Viewers | The home |
|---|---|---|
| `slate` (programme) | the channel's playlists show the slate until `until`, or until removed | keeps uploading; the takedown arrives separately (50.24) |
| `off_air` | `503 off_air` on play; playing viewers get the off-air card at the next playlist | stops uploading that channel and shows why |
| `suspend` | as `off_air` for every channel of the home or publisher | stops all broadcasting; the admin sees why; reversible |
| `terminate` | as `suspend`, and the publisher can't publish again | as `suspend`; the Broadcast page shows `broadcast.terminated` |

- **Sticky.** Only a later list version without the entry lifts it. A service admin lifts it,
  and that is audited.
- **Version** only goes up. The edge and homes refuse a list older than the newest they have seen
  (anti-rollback).
- **Expiry** is 48 h, as in Epic 54. A home whose copy has expired keeps enforcing it (entries
  stay sticky), shows a warning, and refuses to **start** a new publish until it is refreshed.
  Channels already published keep running: a cloud outage must not stop every broadcast (Epic
  50's no-downtime rule; Epic 54 H10's reasoning).

### 2. Delivery

- **To the edge.** On every change the cloud API sends `POST
  https://ingest.maktaba.tv/_control/revocations` with the signed list. The `Revocations` DO
  checks the signature and the version, stores it, and fans it out to every `ChannelLive` and
  `PublisherControl` it names, plus a global flag. A pull every 60 s
  (`GET api.maktaba.tv/v1/broadcast/revocations`) backs up the push.
- **At the edge.**
  - `play` and `heartbeat` check the publisher's and the channel's state.
  - **Every playlist request** checks the channel's state, which `ChannelLive` answers along with
    the playlist.
  - So a playing viewer is stopped at the next playlist reload (≤ 4 s with 4 s segments), and a
    new viewer at once.
  - Ingest is refused for `off_air`, `suspend` and `terminate`.
- **To homes.** It rides the cloud link: a push within 1 minute and a poll every 10 minutes. The
  edge also sends `revoked` over the control WebSocket (50.22), so a home learns of it even when
  the cloud link is the thing that is down.
- **Timing target:** ≤ 60 s from the admin's action to viewers stopping (README §4.2).

### 3. Break-glass

- **When:** the cloud API is down and a stop is urgent.
- **Global:** the owner sets the Worker variable `BROADCAST_GLOBAL_OFF = 1` (Cloudflare
  dashboard or `wrangler secret put`), which acts as a global `off_air` at once.
- **Per channel:** `BROADCAST_OFF_CHANNELS = ch_a,ch_b` does the same per channel.
- Both are documented in 50.12's runbook `broadcast-break-glass`, and checked first on every
  request. They are cleared by hand when the API is back and its list carries the same entries.

### 4. The admin

The cloud admin (service admins only, enforced by the API's role checks) shows the current list,
with entries by kind, who added them, when and why, and a button to add or lift. Every change is
in `broadcast_revocation_audit`.

## Acceptance criteria

- **Given** a channel with 50 viewers,
  **when** a service admin adds `off_air` for it,
  **then** within 60 s new plays get `503 off_air`, playing viewers see the off-air card, and the
  home stops uploading that channel and shows the reason.
- **Given** a list with version 1842 at the edge,
  **when** version 1841 arrives,
  **then** it is refused and 1842 stays.
- **Given** a list with a bad signature,
  **when** it arrives,
  **then** it is refused and alerted (SEV2).
- **Given** a home whose copy expired 2 days ago,
  **when** it runs,
  **then** its published channels keep running, a warning shows, and a new publish is refused
  until the list is refreshed.
- **Given** the cloud API is down,
  **when** the owner sets `BROADCAST_GLOBAL_OFF = 1`,
  **then** every channel is off air within 60 s.
- **Given** a `programme` entry until 21:00,
  **when** it is 21:00,
  **then** the channel's playlists return to live without anyone acting.
- **Given** an account that isn't a service admin,
  **when** it calls the revocation API,
  **then** the API answers `403`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Go) | list signing | sign, verify, a bad key, a wrong `kid` | valid / refused / refused |
| T02 | unit (edge, vitest) | `Revocations` DO | newer, older, equal, bad signature | stored / refused / no-op / refused |
| T03 | unit (edge) | play, beat, playlist, ingest with each action | requests | the responses in the table; playlist stop within one reload |
| T04 | unit (home, Go) | expired copy | publish, keep running | refused / runs, with a warning |
| T05 | integration | cloud + edge (Miniflare) | push lost, pull at 60 s | applied ≤ 60 s |
| T06 | integration | break-glass vars | set and clear | off at once; back when cleared |
| T07 | unit | `programme` entry with `until` | the clock passes `until` | live again |
| T08 | role test | cloud API | non-service-admin | `403` |
| T09 | Arabic | off-air, suspended and terminated strings | strings check | Arabic present |
| T10 | rehearsal (CI, 50.29 `broadcast-kill`) | home + edge + cloud, 200 simulated viewers | off_air, then lift; the API down + break-glass | the ≤ 60 s target on both paths |

## Edge cases

- **The DO can't reach a `ChannelLive`** (a location problem). Each `ChannelLive` also checks the
  global and publisher state in `Revocations` at most every 10 s, so a missed fan-out is caught
  within 10 s.
- **A home ignores the list** (modified code). The edge refuses its ingest anyway, and viewers are
  stopped at the edge. The home's honouring of the list is for the publisher's information.
- **Key rotation.** A new `kid` is added to `CLOUD_PUBKEYS` and to homes in a release before it
  signs. The old key is removed one release later.
- **A `terminate` by mistake.** A service admin lifts it (audited), and the publisher can publish
  again. Strikes are separate (50.24).

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `broadcast.offAir` | This channel is off air. | هذه القناة متوقفة عن البث. |
| `broadcast.revoked.channel` | Maktaba took this channel off air: {reason}. See your notices on the Broadcast page. | أوقفت مكتبة بث هذه القناة: {reason}. راجع الإشعارات في صفحة البث. |
| `broadcast.revoked.suspended` | Broadcasting from this home is suspended: {reason}. | البث من هذا الجهاز موقوف: {reason}. |
| `broadcast.revocationsStale` | This home couldn't check Maktaba's broadcast list for {days} days. Published channels keep running; new ones wait until it's checked. | تعذّر على هذا الجهاز التحقق من قائمة البث لدى مكتبة منذ {days} أيام. تستمر القنوات المنشورة، وتنتظر الجديدة حتى يتم التحقق. |
| `broadcast.reason.notice` | a copyright notice | إشعار بشأن حقوق النشر |
| `broadcast.reason.report` | a report we reviewed | بلاغ راجعناه |
| `broadcast.reason.incident` | a service problem on our side | مشكلة في الخدمة من جهتنا |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/internal/broadcast/revocations.go` (new), tests | Signing, versions, audit, push and pull endpoints. |
| `cloud/internal/handlers/admin/broadcast_revocations.go` (new) | Admin API (service admin only). |
| `web/packages/broadcast-edge/src/revocations.ts`, `channel_live.ts`, `publisher.ts` | The DO, checks, break-glass. |
| `streaming/internal/broadcast/control.go`, `api/internal/broadcast/revocations.go` | The home honours, stores and warns. |
| `cloud/migrations/` (new) | `broadcast_revocations`, `broadcast_revocation_audit`. |
| `web/cloud/src/pages/admin/BroadcastRevocations.tsx`, i18n | Admin page. |
| `docs/runbooks/broadcast-break-glass.md` | Runbook (50.12's set). |

### Tests

- T01–T09 in the cloud, edge and home unit jobs.
- **CI only:** rehearsal `broadcast-kill` (50.29).

### Rollout

The list ships empty with 50.21. The home honours it from 50.22's first release. Break-glass is
documented and tried in staging before the gate.

### Rollback

A bad entry is lifted by a newer version. A bad edge release rolls back with Workers versions;
DO state stays.

### Dependencies

50.21 (the edge), 50.22 (the home's control socket).
