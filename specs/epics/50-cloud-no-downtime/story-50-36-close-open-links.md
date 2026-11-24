# Story 50.36 — Invite-only broadcasting: close the open links

> Epic 50 · Phase B · Depends on: 50.26, 50.27 · Needed by: the broadcasting gate

## Why

Broadcasting exists so people can subscribe to friends' channels, so access is by invitation
only (README §14, decided 2026-11-24). A channel is visible only to the people its publisher
invited: no open links, no public watch pages, no directory.

50.26 and 50.27 first specified a second mode, `open` (anyone with the link, after a Turnstile
check in a browser or an app attestation in an app), and it was built. This story removes it from
every layer, moves the data that may hold it, and proves that nothing plays or shows without an
invite. It changes no invite behaviour: invite links, guest passes, short codes that map to an
invite, and TV pairing that hands over an invite's pass all stay.

## Description

### 1. What exists today (the inventory, on main at 2026-11-24)

Line numbers are a guide only; the symbols are what to look for.

**Data and stored state**

| Where | What allows open access |
|---|---|
| `cloud/migrations/00500017_broadcast_plans.sql` | `broadcast_channels.access CHECK (access IN ('invite','open'))` |
| `cloud/migrations/00500021_broadcast_viewer_access.sql` | `broadcast_channels.indexable`; `servers.broadcast_max_sessions_per_ip` (limits open sessions only) |
| `shared/db/migrations/0227_broadcast.sql`, `0227_broadcast.sqlite.sql` | the home's `broadcast_channels_access_valid` check allows `'open'` |
| the edge's `PublisherControl` Durable Object | stored `AccessSettings` may hold `access: "open"` and `open_allowed: true`; sessions may hold `open` and `ip` fields |
| browsers and apps in the field | the `mko` cookie (an `mkbo1.` open-session token, 12 h); `GuestPass` of kind `open` in the Keychain or keystore |

The `'open'` status of a notice or report in `00500025_broadcast_moderation.sql` is a queue
state, not access, and stays.

**Cloud API (Go)**

- `cloud/internal/billing/broadcast_catalog.go`: `AccessOpen`, and the Broadcaster tier's
  `Access: {AccessInvite, AccessOpen}`; it reaches homes and clients through
  `billing/broadcast_usage.go` (`PublisherState.Access`), `handlers/billing/broadcast.go`
  (`broadcastTierView.Access`) and `broadcast/testdata/vectors.json`.
- `billing/broadcast_usage.go`: `Downgrade` and `EndGuestSharing` turn open channels back into
  invite ones, which becomes a no-op.
- `cloud/internal/broadcast/invites.go`: `AccessChannel.Access` (`invite` or `open`) and
  `Indexable`; `AccessState.OpenAllowed` and `MaxSessionsPerIP` in the signed post to the edge;
  `testdata/invite_vectors.json` signs a post with all of them.
- `cloud/internal/handlers/broadcast/broadcast.go`: `Deps.OpenAccess` and the open branch of
  channel publish (`openRefusal`).
- `cloud/internal/handlers/broadcast/invites.go`: `VerifyDays`, `Verification`,
  `openRefusal`, `writeOpenRefusal` (`broadcast.openNeedsVerification`,
  `broadcast.openNotEnabled`); `channelView.Indexable`; `accessView.OpenAccessEnabled`,
  `OpenInPlan`, `Verification`, `MaxSessionsPerIP`; the `invite|open` check and `indexable` in
  `SetChannelAccess`; the open branch of `accessRefused`; `max_sessions_per_ip` in
  `SetViewerLimits`.
- `cloud/internal/handlers/broadcast/edgepush.go`: sets `OpenAllowed` and `Indexable`.
- `cloud/internal/config/config.go`, `validate.go`, `cloud/configs/cloud.example.toml`:
  `broadcast.open_access.enabled`; `cloud/cmd/maktaba-cloud/role_api.go` and `role_worker.go`
  pass it in.
- `cloud/internal/broadcast/ae.go` and `testdata/events_schema.json`: an `access` analytics
  column that can now only be `invite`.

**Broadcast edge (`web/packages/broadcast-edge`)**

- `src/access.ts`: `AccessChannel.access` and `indexable`, `open_allowed`, the `open`
  credential, `openChannels()`, the open filter in `channelsFor`, `play()` answering
  `turnstile_required` for an open channel, the per-IP open limit, and `validSettings` accepting
  `open`.
- `src/site/routes.ts`: **`lineup` and `apiGuide` fall back to `openChannels()` when the visitor
  has no pass**, so a publisher's grid, guide and channel pages render for anyone while an open
  channel exists; the channel page gets the Turnstile key for the open check and `indexable`;
  `pairDeliver` can hand a TV a token of the phone's open session.
- `src/site/pages.ts`: `indexable` drops the `noindex` meta; the pair page's `sendOpen` button.
- `src/site/client.ts`: `verify()` posting `/api/v1/open/turnstile` and the `verify` state;
  `src/site/states.ts` (`turnstile_required`); `src/strings.ts` (`site.verify`,
  `site.pair.sendOpen`, and the privacy text naming Turnstile for viewing).
- `src/play.ts`: the routes `/api/v1/open/turnstile`, `/api/v1/open/challenge`,
  `/api/v1/open/attest`; the `mko` cookie and `openSession`, `openTurnstile`, `openAttest`; the
  `mkbo1.` branch of `credOf`; the `X-Maktaba-Indexable` opt-out of `noindex`; `ipHash` for open
  sessions.
- `src/invites.ts`: `OPEN_PREFIX` (`mkbo1.`), `OPEN_DOMAIN`, `OPEN_TTL_S`, `OpenClaims`,
  `mintOpen`, `readOpen`. `src/pairing.ts`: a handover of kind `open`.
- `src/attest.ts` (App Attest and Play Integrity exchange, and its `attest-spent-N` Durable
  Objects) and `src/turnstile.ts` (viewer siteverify): whole files.
- `src/limits.ts` (`DEFAULT_SESSIONS_PER_IP`), `src/env.ts` and `wrangler.toml`
  (`TURNSTILE_SECRET`, `TURNSTILE_API`, `ATTEST_API`); `src/tokens.ts`, `src/live.ts`,
  `src/events.ts` (an `acc` of `open`, never set).

**Home API (Go)**

- `api/internal/broadcast/service.go`: accepts `access = "open"` and `openIsAlwaysOn`;
  `checks.go` (`CodePlanAccess` and its comments); `store.go` stores the value.

**Web**

- `web/src/pages/Admin/LiveTV/BroadcastPanel.tsx`: the access choice (`invite`, `open`) and the
  on-demand rule tied to it; `web/src/lib/tvBroadcastApi.ts`: the access type.
- `web/src/pages/Admin/BroadcastAccess.tsx` and `web/cloud/src/pages/Broadcast.tsx`: the open
  option, its verification list and reasons, and the per-network device limit.
- `web/src/i18n/{en,ar}.json` (`broadcast.access.open`, `broadcast.openIsAlwaysOn`,
  `broadcastAccess.open*`, `verify*`, `perIP*`) and `web/cloud/src/i18n/{en,ar}.json`
  (`broadcast.access.open*`, `broadcast.openNeedsVerification`, `broadcast.verify.*`,
  `broadcast.limits.perIP*`).
- Help: `web/src/help/content/{en,ar}/broadcast-access.md`.

**Apps**

- Apple: `apps/apple/MaktabaKit/Sources/MaktabaKit/Guest.swift` (`GuestPass.Kind.open`, the
  `mkbo1.` prefix, the `verify` state for `turnstile_required`, the pairing fallback to `open`);
  `Localizable.xcstrings` (`site.verify`). The tvOS and iOS guest screens have no open branch,
  and no App Attest client exists.
- Android: `apps/android/client/.../core/guest/Guest.kt` (`Kind.Open`, `turnstile_required`),
  `GuestClient.kt` (`open` and the `mkbo1.` fallback), `apps/android/core/.../guest/Guests.kt`,
  `res/values{,-ar}/strings.xml` (`guest_verify`). Android TV uses the shared client; no Play
  Integrity client exists.

**Rehearsals, tools and docs**

- `tools/broadcast-viewers/tokens.go` (`Indexable`, `OpenAllowed`, `MaxSessionsPerIP`);
  `tools/broadcast-viewers/rehearsal/scenarios_cloud_test.go` (the `access` scenario's open
  channel, Turnstile and per-network limits); `tools/rehearsal-cloud/broadcast/edge/server.mjs`
  (the fake Turnstile siteverify for viewers); `cloud/rehearsal/broadcast_test.go`.
- `docs/broadcasting.md`, `docs/broadcast-edge.md`, `docs/cloud-rehearsal.md`,
  `docs/developers/contributing/broadcasting.md`, `cloud/docs/accounts-billing-admin.md`.
- maktaba.dev: `web/packages/devportal/scripts/apps.mjs`, the **Broadcast access** entry
  ("Anyone with the link"), in English and Arabic.

**Tests that assert open behaviour** (rewrite them to assert the refusal): cloud
`viewer_access_integration_test.go` (`TestViewerAccessOpenNeedsVerification`),
`broadcast_integration_test.go`, `handlers/broadcast/invites_integration_test.go`
(`TestOpenAccessRacingAPlanChange`), `billing/broadcast_test.go`, `broadcast/invites_test.go`,
`broadcast/tokens_test.go`, `cloudlink/broadcast_access_test.go`, `config/config_test.go`
(`TestBroadcastOpenAccessConfig`); edge `access.test.ts` (T03 Turnstile, T04 attestation),
`site.test.ts` (indexable, open channels with no pass, a TV given an open session),
`events.test.ts`; home `checks_test.go`, `service_pg_test.go`; web `BroadcastAccess.test.tsx`,
`web/cloud/src/pages/broadcast.test.tsx`, `BroadcastPanel.test.tsx`; Apple `GuestTests.swift`;
Android `GuestTest.kt`.

**Not open access, and staying:** the copyright notice form on `maktaba.tv/legal/copyright`
(`POST /v1/moderation/notices`), the cloud's sign-in Turnstile (50.30), and the Report form on a
channel page (`POST /v1/moderation/reports`), which only an invited guest now reaches. The
copyright work (50.24) is deferred with broadcasting (2026-11-24); this story doesn't change it.

**Already done while broadcasting is off (2026-11-24):** the edge no longer falls back to
`openChannels()` for a visitor with no pass (`lineup` and `apiGuide` return nothing, and `play`
answers `invite_required`), every watch page is `noindex`, and `BROADCAST_ENABLED` (off) makes
watch and live answer 404 to everything. The rest of this story still stands.

### 2. The work

1. **Data first, in one migration on each side.**
   - Cloud: set `access = 'invite'` where it is `'open'`, tighten the check to `'invite'` only,
     drop `indexable` and `servers.broadcast_max_sessions_per_ip`, and bump
     `servers.broadcast_access_version` for every home it touched so the edge receives the new
     settings at once. Its Down restores the columns and the wider check (values stay `invite`).
   - Home (Postgres and SQLite): the same rewrite and tighter check. Channels that were open
     keep their `always_on` mode.
2. **Cloud API.** Remove `AccessOpen` from the catalog and every view; refuse any access but
   `invite` with `400 broadcast.accessInvalid`; remove verification, `openRefusal`, the open
   branch of publish, `indexable`, `max_sessions_per_ip`, `OpenAllowed`, and
   `broadcast.open_access.enabled`. A config file that still sets the key is refused at start,
   naming the key, like every other unknown key. Regenerate the signed test vectors.
3. **Edge.** Only a guest pass is a credential. With no pass, every viewer route answers
   `401 broadcast.invite.required` and `lineup` and `apiGuide` return nothing; every page sends
   `noindex`; delete `attest.ts`, `turnstile.ts`, the `/api/v1/open/*` routes, the `mko` cookie,
   `mkbo1.` tokens, `openChannels`, the open pairing branch and the per-IP open limit;
   `validSettings` refuses `open`. `TURNSTILE_SITE_KEY` stays only if the Report form keeps it.
4. **Home API.** Accept `invite` only (`badAccess` otherwise); drop `openIsAlwaysOn`.
5. **Web.** Remove the access choice from the home's Broadcast panel, the open option and its
   verification and per-network fields from both Broadcast access pages, and their strings in
   English and Arabic. The help page says channels are invite-only.
6. **Apps.** Remove the `open` pass kind and the `verify` state; on start, delete any saved
   `open` pass (it no longer works).
7. **Rehearsal.** Rewrite the `broadcast-access` scenario as invite only, adding a viewer with no
   pass on every route; remove the fake viewer Turnstile.
8. **Docs and maktaba.dev.** Update the docs listed in §1 and the portal's **Broadcast access**
   entry in English and Arabic, in the same pull request.

## Acceptance criteria

- **Given** a cloud database with a channel whose access is `open`,
  **when** the migration runs,
  **then** the channel is `invite`, its mode is unchanged, the check refuses `open`, and the edge
  holds settings with no open channel within 60 s.
- **Given** a live channel and a browser with no guest pass,
  **when** it opens the publisher's grid, the channel page or the guide, or calls play or a
  playlist,
  **then** each answers `401 broadcast.invite.required`, and no channel, programme or guide data
  is in the response.
- **Given** a request to any `/api/v1/open/*` route,
  **when** it reaches the edge,
  **then** it answers `404`.
- **Given** a publisher on Broadcaster,
  **when** they set a channel's access to `open` through the cloud API or the home,
  **then** it is refused with `400 broadcast.accessInvalid`.
- **Given** an app holding a saved `open` pass,
  **when** it starts,
  **then** the pass is removed, and the guest's invite passes are untouched.
- **Given** any watch page,
  **when** it is fetched,
  **then** it carries `noindex`, whatever the channel's former settings.
- **Given** an invite, its short code, or a TV paired from a phone,
  **when** a guest redeems it,
  **then** it plays exactly as before.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | migration (Go, cloud) | rows with `open`, `indexable`, a per-IP limit | Up, then Down | `invite`; columns gone; version bumped / columns back, values `invite` |
| T02 | migration (Go, home; Postgres and SQLite) | an `open` channel | Up | `invite`, mode kept; `open` refused |
| T03 | unit (Go, cloud) | access values, config | `open` on every route; `broadcast.open_access` in a config | 400; refused at start naming the key |
| T04 | unit (edge, vitest) | no pass | every viewer route, playlist and `/api/v1/open/*` | 401 with no channel data; 404 |
| T05 | unit (edge, vitest) | settings with `open` | push them | refused |
| T06 | unit (web, vitest) | both Broadcast access pages and the panel | render | no access choice, no verification or per-network field |
| T07 | unit (Swift, Kotlin) | a saved `open` pass and an invite pass | start | the open pass removed; the invite pass kept |
| T08 | rehearsal (CI, 50.29 `broadcast-access`) | home + edge + cloud | invite flow; a viewer with no pass | invite plays; every route refuses the viewer with no pass |
| T09 | Arabic | changed strings and help | check | present |

## Edge cases

- **A guest is watching an open channel at deploy time.** Their session holds no pass; it ends at
  the next heartbeat (≤ 60 s) and shows `broadcast.invite.required`. The publisher was told in
  the release notes to invite them.
- **An old app version** still offers the verify step. The edge answers `404` on
  `/api/v1/open/*`, and the app shows its generic error; nothing plays without a pass.
- **The edge and the cloud deploy at different times.** The edge is deployed first, refusing
  open credentials; the cloud's later push simply no longer carries open fields.

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `broadcast.invite.required` | This channel is for invited guests. Ask the person who shares it for an invite. | هذه القناة للضيوف المدعوين. اطلب دعوة من الشخص الذي يشاركها. |
| `broadcast.accessInvalid` | Channels can only be shared by invitation. | لا يمكن مشاركة القنوات إلا بالدعوة. |

## Build plan

### Files to touch

The files in §1, plus one new migration in `cloud/migrations/` and one pair in
`shared/db/migrations/`.

### Tests

- T01–T07 and T09 in the cloud, API, edge, web, Apple and Android unit jobs.
- **CI only:** T08, rehearsal `broadcast-access` (50.29).

### Rollout

1. The edge first (refuses open credentials and serves nothing without a pass).
2. The cloud and home migrations and API with the next release.
3. The apps in the next release train.

### Rollback

The Down migrations restore the columns; no data is restored to `open`. Code rolls back by
release; the edge's refusal is safe to keep under any older cloud.

### Dependencies

50.26 and 50.27 (the invite flow that stays).
