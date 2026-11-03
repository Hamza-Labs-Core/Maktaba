# Story 50.26 — Viewer access: invite and open; limits and tokens

> Epic 50 · Phase B · Depends on: 50.20, 50.21 · Needed by: 50.27, 50.29

## Why

Viewers aren't users of the home and **never pay** (decided 2026-11-03). Access is one of
two modes:

- `invite` (the default): the publisher decides who;
- `open`: anyone with the link.

Both modes need the same protections against scraping and cost abuse that Epic 34 specced for its
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

### 2. Open

- Anyone with the link can watch, after one **Turnstile** check per browser session (server-side
  siteverify in the Worker, as Epic 34 R7.1). The check gives a 12 h session cookie.
- **Native apps:** they get an app attestation (App Attest or Play Integrity) from the app and
  exchange it for the same 12 h session. Turnstile can't run in a TV app.
- `open` needs a **verified publisher** (README §14.6: a paid plan in good standing, a confirmed
  email, 7 days since the first publish) and `broadcast.open_access.enabled` (the broadcasting
  gate).
- The page is `noindex` by default. The publisher can allow indexing per channel (Epic 34 R5.5).

### 3. Play, tokens and heartbeats

- `POST watch.maktaba.tv/api/v1/play {publisher, channel}` with the guest pass or the open
  session:
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
| Daily minutes per guest pass or open session | 0 = none (publisher's setting) | `PublisherControl` |

At a limit, new plays get `429` with `broadcast.full` or `broadcast.ipLimit`. Playing sessions
aren't stopped.

### 5. Privacy

- No viewer account and no viewer email.
- An IP is used only as a salted hash, in memory and DO storage, for at most 24 h.
- Guest passes hold a random id and the invite id; the publisher sees labels and use counts, not
  identities.
- One functional cookie on `watch.maktaba.tv`. The privacy page lists Cloudflare (Workers,
  Turnstile, Analytics Engine) as processors.

## Acceptance criteria

- **Given** an invite with 2 uses left,
  **when** three devices open it,
  **then** two get guest passes and the third sees `broadcast.invite.used`.
- **Given** a revoked invite,
  **when** a device holding its pass heartbeats,
  **then** the heartbeat fails, and the player shows the ended card within 60 s.
- **Given** an `open` channel,
  **when** a browser plays without passing Turnstile,
  **then** play answers `403 turnstile_required`.
- **Given** an unverified publisher,
  **when** they set a channel to `open`,
  **then** the API refuses with `broadcast.openNeedsVerification`.
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
| T03 | unit | Turnstile | fake siteverify ok / fail / replay | session / 403 / 403 |
| T04 | unit | app attestation exchange | a fake attestation verifier | session / 403 |
| T05 | unit | play + beat | the token refresh at half-life, 150 s silence | refreshed; ended |
| T06 | unit | limits | per publisher, channel, IP, rate | 429s; playing sessions untouched |
| T07 | unit | routes | list every route | no payment route on `watch.` or `live.` |
| T08 | integration (Miniflare) | 1,000 sessions | heartbeats for 10 min | exact counts; viewer-minutes = beats |
| T09 | Arabic | strings | check | present |
| T10 | rehearsal (CI, 50.29 `broadcast-access`) | home + edge | invite flow, open flow, limits | as above |

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
| `broadcast.access.open` | Anyone with the link | أي شخص لديه الرابط |
| `broadcast.invite.create` | Create invite link | أنشئ رابط دعوة |
| `broadcast.invite.uses` | {used} of {max} devices | {used} من {max} أجهزة |
| `broadcast.invite.used` | This invite has already been used on the most devices it allows. Ask for a new one. | استُخدمت هذه الدعوة على الحد الأقصى من الأجهزة. اطلب دعوة جديدة. |
| `broadcast.invite.expired` | This invite has expired. | انتهت صلاحية هذه الدعوة. |
| `broadcast.openNeedsVerification` | Open channels are available once your account is verified: a paid plan, a confirmed email, and 7 days since your first broadcast. | تتاح القنوات المفتوحة بعد توثيق حسابك: خطة مدفوعة، وبريد مؤكد، ومرور 7 أيام على أول بث. |
| `broadcast.ipLimit` | Too many devices on this network are watching. Try again later. | عدد كبير من الأجهزة على هذه الشبكة يشاهد الآن. حاول لاحقًا. |
| `broadcast.free` | Free to watch | المشاهدة مجانية |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `web/packages/broadcast-edge/src/access.ts`, `invites.ts`, `turnstile.ts`, `attest.ts`, `play.ts`, `limits.ts`, tests | Access, tokens, limits. |
| `cloud/internal/broadcast/invites.go` (new) | Invite signing (the home's and the cloud web app's requests). |
| `cloud/internal/handlers/broadcast/invites.go` | `POST/GET/DELETE /v1/broadcast/invites`. |
| `api/internal/broadcast/` | The home's Broadcast page calls the cloud for invites. |
| `web/src/pages/LiveTv/Broadcast*.tsx`, `web/cloud/src/pages/Broadcast*.tsx`, i18n | Invites UI. |

### Tests

- T01–T09 in the edge and cloud unit jobs.
- **CI only:** rehearsal `broadcast-access` (50.29). Turnstile and app attestation are only ever
  fakes in CI.

### Rollout

`invite` first, with the canary and the owner. `open` at the broadcasting gate, behind
`broadcast.open_access.enabled`.

### Rollback

`broadcast.open_access.enabled = false` turns every `open` channel into invite-only for new
sessions. Playing sessions finish.

### Dependencies

50.20 (modes and limits), 50.21 (the edge, viewer tokens).
