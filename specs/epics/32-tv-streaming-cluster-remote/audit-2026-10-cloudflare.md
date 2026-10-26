# Audit — Maktaba TV through Cloudflare, end to end (2026-10-26)

Scope: every Epic 32 and Epic 34 story that touches Cloudflare (the integration, live input push,
signed playback, budget, observability, simulcast, failover of the push, the public site), checked
against the code on `main` at `4ab79c13`, then fixed on `feat/cloudflare-end-to-end`. Status after
this branch: **done**, **partial** (with what is left), **missing** (not built), or **fixed here**.
Nothing here called the live Cloudflare API.

Evidence runs: `go test ./...` in `api` and `streaming`; the Postgres and ffmpeg end-to-end tests
on a scratch Postgres 16 (`MAKTABA_TEST_PG_URL`, as in CI's integration tier):
`TestRemoteEndToEnd`, `TestSlotsFailoverEndToEnd`, `TestSwapRollbackEndToEnd`,
`TestPublicEndToEnd`, `TestPushCredentialsOnlyForHome`, `TestSecretsNotReadableByWorker`,
`TestSimulcastOnPostgres`; vitest for `web`.

## Story 32.6 — Cloudflare Stream integration

| AC / TC | Status | Evidence |
|---|---|---|
| AC1 `home_secrets`, AES-256-GCM, key file 0600 | done | `0120_home_secrets.sql` (revoked from `maktaba_worker`); `secrets/secrets.go`; `TestSecretsSealOpen`, `TestSecretsNotReadableByWorker` (PG) |
| AC2 admin entry, token stored masked | done | `handlers/queues/extensions.go` (`cloudflare_stream`: account id, `api_token` secret home-only → `cf_api_token`, customer code); Admin → Extensions card with Test connection (`Extensions.tsx`, `CloudflareRemote.tsx`) |
| AC3 Test connection lines | done | `tvremote/integration.go` `Test`: token, Stream, storage, analytics (optional), signing key created once, playback domain, push ability; `TestCFTestConnection*` |
| AC4 enable needs a green test < 24 h | done | `SetEnabled`; `TestRemoteEnableNeedsGreenTest` |
| AC5 remove: disable, stop, delete inputs + key + secrets | **fixed here** | recordings were left at Cloudflare and input/push rows stayed (counted against `max_channels` on re-add). `Remove` now deletes each input's recordings first, `Controller.Forget` closes pushes and drops the input rows; the site key goes too. `TestRemoveIntegration`, `TestSimulcastEdgeIntegrationRemoved`, `TestSiteSecrets` |
| AC6 errors shown, token never logged | done | `TestNoSecretsInLogs`; the token is only in the `Authorization` header (`cf/client.go`) |
| AC6a extension secrets moved to `home_secrets` | done | `secrets/migrate.go`; `TestExtensionSecretsMovedToHomeSecrets(PG)` |
| AC7 spike against a real account | **missing (needs the owner's account)** | tool ready: `maktaba-api cloudflare spike`, `tools/tv/cf-spike.md`. Open questions (a)–(g), notably whether a token with `sub` = live input id plays |
| EC2 account change refused while in use | **fixed here** | was refused only when a signing key existed; now also while input rows exist. `TestAccountChangeRefusedWhileInUse` |
| EC3 `secrets.key` lost | done | `TestCFTestConnection/lost_secrets.key` |

## Story 32.7 — live input pool and push (with 33.8 continuous push)

| AC / TC | Status | Evidence |
|---|---|---|
| AC1 slot 0125, node roles read no secret | done | `0125_tv_remote.sql`, `tv_push_credentials` home-only; `TestPushCredentialsOnlyForHome` (PG) |
| AC2 inputs on demand ≤ `max_channels`, signed URLs, 30 s timeout, keys in `home_secrets` | done | `controller.go` `assignInput`; `TestPoolCreatesLazilyUpToMax`, `TestPoolShrinkDeletesIdle` |
| AC3 request → relay starts ≤ 2 s | done | `startOn` NOTIFY `tv_push`; `frontdoor/relay/ctl.go` (NOTIFY + 2 s poll) |
| AC4 one ffmpeg, video stream-copied, AC-3 → AAC, RTMPS/SRT | done | `relay/args.go`, `TestRelayArgs`; `TestRemoteEndToEnd` checks `-c:v copy` and the key not in the log. SRT is not exercised locally (this machine's ffmpeg has no libsrt) |
| AC5 live from lifecycle; one retry then failed | done | `TestConnectTimeoutRetriesOnce`, `TestPushStateMachine` |
| AC6 one push per channel | done | partial unique index; `TestOnePushPerChannel` |
| AC7 pushed channel never reaped | done | `relaysource.go` `AttachPush` |
| AC8 idle stop, recording deleted, startup sweep | done | `TestRecordingDeletedAfterStop`, `TestStartupSweep` |
| AC9 rotation at `max_push_hours` | partial (by design) | `TestRotateAtMaxPushHours`. Rotation reuses the same input, so Cloudflare needs the old broadcast to end before a new video starts: a short gap every 8 h. Make-before-break would need a second input (Epic 34's A/B pair) |
| AC10 failover/swap keeps the same RTMPS session and video | **fixed here** | a clean failure already worked (`TestRemoteEndToEnd`: one ingest session across a node kill). A node that loses power holds its TCP connection open, and the relay blocked on it past Cloudflare's 30 s `timeoutSeconds`, so a new video started. The relay now drops a source silent for 5 s (20 s before its first byte) and resolves the channel again on the same session. `TestRelayDropsSilentSource`, `TestRelayDropsSourceWithoutFirstByte`; swap/failover E2E still one session (`TestSwapRollbackEndToEnd`: largest gap 204 ms) |
| AC11 `ERR_*` → actions | done | `errors.go`; `TestErrorsMapToActions` |
| AC12 credentials only on the home, masked | done | `Mask`/`MaskedArgs`; `TestRedactKeys` |
| AC13 webhook | **fixed here** | secret header `cf-webhook-auth` compared in constant time, allowlisted; it read the error code from `data.error_code`, but Cloudflare sends `data.live_input_errored.error.code` (developers.cloudflare.com/stream/stream-live/webhooks). Test fixture is now Cloudflare's documented payload. (`Webhook-Signature` HMAC belongs to the separate video webhook, not to Notifications) |
| EC1 uplink drops: reconnect with backoff | **fixed here** | restarts were a fixed 500 ms; now doubling to 5 s while ffmpeg keeps ending quickly. `TestRelayRestartBacksOff` |
| EC2 uplink too slow | done | `TestRelayFailsWhenUplinkTooSlow` |

## Story 32.8 — signed remote playback

| AC | Status | Evidence |
|---|---|---|
| AC1–AC8 | done | `sessions.go`, `cf/keys.go` `SignToken`; `TestTuneGatesOrder`, `TestTokenClaims`, `TestHeartbeatRefreshesToken`, `TestRotateSigningKey`; `TestRemoteEndToEnd` verifies the token and plays it at the fake |
| AC9 audit | **fixed here** | a heartbeat that ended a session for lost access wrote no audit row; now `tv_remote.end` reason `access_lost`. `TestHeartbeatAfterAccessLost` |

## Stories 32.9 (web part), 32.10, 32.11, 32.5 (remote parts)

| Item | Status | Evidence |
|---|---|---|
| 32.9 web player tunes through Cloudflare when away, token swap without restart | done | `Player.tsx`, `lib/remoteTv.ts`, `packages/tvplayer/src/live.ts`; `remoteTv.test.ts`, `Player.test.tsx`. TC3 (Playwright) missing |
| 32.10 budget, storage guard, 402 | done | `budget.go`, `storage_guard.go`; `TestBudgetThresholds`, `TestStorageGuard`; E2E 402 and 503 |
| 32.11 metrics, health samples, events | done; **fixed here**: simulcast events now reach the panel (`events.go`) | `TestMetricsRegistered`, `TestHealthSamplesWritten` |
| 32.11 AC3 no secrets in logs | done; **added**: simulcast stream keys | `TestSimulcastKeyNotInLogs` |
| 32.5 AC1/AC3 On air: push state, remote viewers | **fixed here** (was always `null` / 0) | `handlers/tvcluster` `remote()`; `TestClusterOnAirRemote` |

## Story 32.13 — simulcast

| AC | Status | Evidence |
|---|---|---|
| AC1–AC8, AC10, AC11 | done | Cloudflare live outputs on the push's input (`cf/outputs.go`, `simulcast.go`); keys in `home_secrets`, masked; `TestSimulcast*`, `TestSimulcastReachesRTMPDestination`, `TestOutputsSurviveFailoverAndRotation` |
| AC9 cap | **fixed here** | a manual start on a scheduled attachment with no window ahead had no end and ignored the cap; any running output without an end now stops at the cap. `TestSimulcastCapManualStartWithoutWindow` |
| admin only | done; **added**: every route in the role test | `TestSimulcastRoutes` (9 routes) |

## Epic 34 — the public TV site

Before this branch only 34.6's shared player module existed (`web/packages/tvplayer`). This branch
adds a first slice so the site plays the Cloudflare HLS of a public channel:

| Story | Status after this branch | Evidence |
|---|---|---|
| 34.1 public flag + rights confirmation | **done (core)**: slot 0154 `tv_public_channels`, admin-only API (422 without the confirmation, asked again each time, kept when private), slug; Admin → Live TV → Public site panel. Exclusions, "new series" review, panic off-air: missing | `TestPublicRequiresRights`, `TestPublicSlug`, `TestPublicRoutes`, `PublicPanel.test.tsx`, `test_tv_public_migration.py` |
| 34.2 publisher | **partial**: the lineup and live state, posted outward every 10 s, HMAC-signed. Guide blobs, R2, deltas: missing | `TestPublisherPostsSignedState`, `TestHomeSignatureParity` |
| 34.3 public feed push | **partial**: public channels pushed always on through Epic 32's push and relay (hold `public`). Dedicated A/B inputs, slates, lower rendition, tiering: missing (a public channel uses the Epic 32 pool and its rotation) | `TestPublicPushAlwaysOn`, `TestPublicEndToEnd` |
| 34.4 Worker, tokens, home channel | **partial**: Worker with `SiteControl` Durable Object, `/_home/state` (HMAC, ±60 s, replay guard), `/api/v1/lineup` (no video ids), `/api/v1/play` (RS256 token with the site's own key, off air after 60 s of silence). WebSocket control, LiveHub, sessions and limits: missing | `worker.test.ts`, `sign.test.ts` (token byte-identical to `cf.SignToken`), `homeauth.test.ts` |
| 34.5 site UI | **partial**: one page, channel grid, hls.js player, token renewed without a restart, Arabic/English (RTL). EPG, deep links beyond `/watch/<slug>`, SEO: missing | `TestPublicSiteDemo` (manual): played in a browser through the real Worker |
| 34.6 channel switching | partial (module only, as before) | `web/packages/tvplayer` |
| 34.7–34.11 access modes, branding/domain, budget/abuse, deployment pipeline, docs | missing, except docs: `docs/tv-cluster-and-remote.md` §4.1 (manual `wrangler deploy`) | — |

## Not verified here

- Anything against the real Cloudflare account (the 32.6 spike, real ingest, real Stream tokens,
  Notifications delivery, `wrangler deploy`). All Cloudflare calls ran against `cftest`, whose
  request/response shapes follow Cloudflare's API documentation.
- SRT ingest (no libsrt in the local ffmpeg).
- The admin pages in a browser against a live API (covered by component tests only).
- `TestStagingVerifyEndToEnd` (Epic 33 staging, not Cloudflare) fails on this Mac at its
  broken-release step, also before these changes; it passes in CI on `main`.
