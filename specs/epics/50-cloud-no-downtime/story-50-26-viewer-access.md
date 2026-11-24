# Story 50.26 — Viewer access by invitation; limits and tokens

> Epic 50 · Phase B · Depends on: 50.20, 50.21 · Needed by: 50.27, 50.29, 50.36

## Why

Viewers aren't users of the home and **never pay** (decided 2026-11-03). Broadcasting exists
so people can subscribe to friends' channels, so **access is by invitation only** (decided
2026-11-24, replacing the earlier choice of `invite` or `open`): the publisher decides who, and
nobody else can see the channel. There are no open links, no public watch pages and no
directory. The `open` mode this story first specified, and that was built, is removed by 50.36.

Invited access still needs the same protections against scraping and cost abuse that Epic 34 specced for its
site (34.4, 34.9): short-lived tokens refreshed by a heartbeat, concurrency limits, and per-IP
limits.

## Description

### 1. Invite

- The publisher creates **invite links** on the Broadcast page or in the cloud web app. Each
  invite is a cloud-signed token (Ed25519, broadcast key, `kid`) holding:
  - the publisher, and one or all of their channels;
  - an expiry (default 30 days, up to 1 year);
  - a use limit (default 10 devices, up to 500);
  - an optional label ("Family in Cairo").
- **Redeeming.** Opening `watch.maktaba.tv/i/<invite>` redeems it, with no account:
  1. `PublisherControl` checks the signature, the expiry, the uses left, and the revocation list.
  2. It issues a **guest pass** for that device: an HMAC with the edge's key, 30 days, renewed on
     use.
  3. The pass goes in an HttpOnly cookie on the web, or in the keychain or keystore in the apps
     (50.27).
  4. One use is counted per device.
- **Short codes for TVs:** `MK-` plus 4 characters, mapped in `PublisherControl` to an invite,
  valid as long as the invite, and rate-limited (10 tries a minute per IP). That gives about
  1.6 M codes per publisher, with brute force bounded by the limit.
- The publisher can see every invite's uses, revoke an invite (its passes stop at the next
  heartbeat), or revoke one device's pass.

### 2. No access without an invite

- **A guest pass is the only way in.** Every viewer route on `watch.maktaba.tv` (the publisher's
  grid, a channel page, the guide, play, beat, the guide blobs) and every playlist on
  `live.maktaba.tv` answers `401 broadcast.invite.required` without a valid guest pass and a
  viewer token minted from it. The answer says nothing about the channel: not its name, its
  guide or whether it exists.
- **No other mode.** A channel's access is always `invite`. The cloud API and the home refuse
  any other value (`400 broadcast.accessInvalid`). There is no Turnstile or app-attestation
  session for viewers, and no verified-publisher step.
- **Not indexed.** Every page sends `X-Robots-Tag: noindex, nofollow`, and `robots.txt`
  disallows everything; no setting changes that.
- **Guests without an account.** An invited friend needs no Maktaba or home account: the
  invite is the permission. A signed-in Maktaba user is treated the same way and still needs an
  invite.

### 3. Play, tokens and heartbeats

- `POST watch.maktaba.tv/api/v1/play {publisher, channel}` with the guest pass:
  1. It checks access, geo (50.23), revocations (50.25), the plan mode (50.20 §2) and the limits.
  2. It returns `{session_id, master_url, expires_at, refresh_after, state, neighbours[]}`. The
     URLs carry a **viewer token** (50.21 §4): an HMAC, 10 minutes, bound to the session and the
     mode.
- **Heartbeat** every 60 s: `POST /api/v1/play/{session}/beat` with the rung and the player's
  stats.
  - It refreshes the token after half its life.
  - It counts a viewer-minute (50.20 §3).
  - A session with no beat for 150 s ends, and no new token is minted for it (Epic 34 R4.4).
- **Neighbour tokens** for channel switching, as in Epic 34 R6.3: manifests only, no segment
  preloading.

### 4. Limits

| Limit | Default | Where |
|---|---|---|
| Concurrent viewers per publisher | from the plan (5 / 100, plus add-ons) | `PublisherControl`, exact |
| Concurrent viewers per channel | the publisher's setting, ≤ the publisher's limit | `PublisherControl` |
| Sessions per IP per publisher | 3 | `PublisherControl`, salted IP hash kept ≤ 24 h |
| Play requests per IP | 30 per 60 s | Workers Rate Limiting |
| Other API requests per IP | 120 per 60 s | Workers Rate Limiting |
| Daily minutes per guest pass | 0 = none (publisher's setting) | `PublisherControl` |

At a limit, new plays get `429` with `broadcast.full` or `broadcast.ipLimit`. Playing sessions
aren't stopped.

### 5. Privacy

- No viewer account and no viewer email.
- An IP is used only as a salted hash, in memory and DO storage, for at most 24 h.
- Guest passes hold a random id and the invite id; the publisher sees labels and use counts, not
  identities.
- One functional cookie on `watch.maktaba.tv`. The privacy page lists Cloudflare (Workers,
  Analytics Engine) as processors.

## Acceptance criteria

- **Given** an invite with 2 uses left,
  **when** three devices open it,
  **then** two get guest passes and the third sees `broadcast.invite.used`.
- **Given** a revoked invite,
  **when** a device holding its pass heartbeats,
  **then** the heartbeat fails, and the player shows the ended card within 60 s.
- **Given** a live channel and a browser or app with no guest pass,
  **when** it opens the channel's page, the publisher's grid or the guide, or calls play,
  **then** each answers `401 broadcast.invite.required`, and the response names no channel,
  programme or publisher detail.
- **Given** a publisher on any plan,
  **when** they set a channel's access to anything but `invite`,
  **then** the API refuses with `400 broadcast.accessInvalid`.
- **Given** a session that stops heartbeating,
  **when** 150 s pass,
  **then** no token is minted for it, and its segments stop working after the token's 10 minutes.
- **Given** 100 concurrent viewers on a Broadcaster publisher,
  **when** the 101st plays,
  **then** they get `429 broadcast_full`, and nobody playing is affected.
- **Given** any request,
  **when** a viewer pays,
  **then** that can't happen: no endpoint takes viewer payment (a test asserts there is no payment
  route on `watch.` or `live.`).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (edge, vitest) | invite verify | good / expired / bad signature / uses exhausted / revoked | pass / 410 / 401 / 410 / 410 |
| T02 | unit | short codes | redeem, a wrong code, 11 tries in a minute | pass / 404 / 429 |
| T03 | unit (edge, vitest) | no guest pass | every viewer route and playlist | `401 broadcast.invite.required`; no channel data in the body |
| T04 | unit (Go, cloud and home) | access values | `invite`; `open`; anything else | accepted / 400 / 400 |
| T05 | unit | play + beat | the token refresh at half-life, 150 s silence | refreshed; ended |
| T06 | unit | limits | per publisher, channel, IP, rate | 429s; playing sessions untouched |
| T07 | unit | routes | list every route | no payment route on `watch.` or `live.` |
| T08 | integration (Miniflare) | 1,000 sessions | heartbeats for 10 min | exact counts; viewer-minutes = beats |
| T09 | Arabic | strings | check | present |
| T10 | rehearsal (CI, 50.29 `broadcast-access`) | home + edge | invite flow, a viewer with no pass, limits | as above |

## Edge cases

- **CGNAT** (many viewers behind one IP, which is common on mobile networks in the region). The
  per-IP limit is per publisher and is a setting (`max_sessions_per_ip`, default 3, up to 50). An
  invite pass bypasses the IP limit, since the publisher chose who to invite.
- **A guest switches device.** That counts as a new use of the invite. The publisher can raise
  the limit.
- **An invite link is shared widely.** The use limit and expiry bound it, and the publisher can
  revoke it in one click.
- **The clock on a TV is wrong.** Tokens are checked against the edge's clock, and the app uses
  the server time from `X-Server-Time`.

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `broadcast.access.invite` | Invite only | بالدعوة فقط |
| `broadcast.invite.required` | This channel is for invited guests. Ask the person who shares it for an invite. | هذه القناة للضيوف المدعوين. اطلب دعوة من الشخص الذي يشاركها. |
| `broadcast.invite.create` | Create invite link | أنشئ رابط دعوة |
| `broadcast.invite.uses` | {used} of {max} devices | {used} من {max} أجهزة |
| `broadcast.invite.used` | This invite has already been used on the most devices it allows. Ask for a new one. | استُخدمت هذه الدعوة على الحد الأقصى من الأجهزة. اطلب دعوة جديدة. |
| `broadcast.invite.expired` | This invite has expired. | انتهت صلاحية هذه الدعوة. |
| `broadcast.ipLimit` | Too many devices on this network are watching. Try again later. | عدد كبير من الأجهزة على هذه الشبكة يشاهد الآن. حاول لاحقًا. |
| `broadcast.free` | Free to watch | المشاهدة مجانية |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `web/packages/broadcast-edge/src/access.ts`, `invites.ts`, `play.ts`, `limits.ts`, tests | Access by guest pass only, tokens, limits. |
| `cloud/internal/broadcast/invites.go` (new) | Invite signing (the home's and the cloud web app's requests). |
| `cloud/internal/handlers/broadcast/invites.go` | `POST/GET/DELETE /v1/broadcast/invites`. |
| `api/internal/broadcast/` | The home's Broadcast page calls the cloud for invites. |
| `web/src/pages/LiveTv/Broadcast*.tsx`, `web/cloud/src/pages/Broadcast*.tsx`, i18n | Invites UI. |

### Tests

- T01–T09 in the edge and cloud unit jobs.
- **CI only:** rehearsal `broadcast-access` (50.29).

### Rollout

Invites with the canary and the owner first, then every publisher at the broadcasting gate.
There is no later `open` step.

### Rollback

`broadcast.enabled = false` stops new plays; playing sessions finish. Revoking an invite stops
its passes at the next heartbeat.

### Dependencies

50.20 (modes and limits), 50.21 (the edge, viewer tokens).
