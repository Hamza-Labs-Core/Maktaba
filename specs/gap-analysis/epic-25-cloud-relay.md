# Epic 25 — Cloud Relay: Spec-vs-Implementation Gap Analysis

**Verdict (one line):** Cloud-side server is broadly built and reachable for identity/billing/push/proxy, but **the entire on-prem→cloud "cloudlink" client does not exist anywhere in the repo**, so no self-hosted Maktaba server can claim, tunnel, refresh entitlements, or relay push to the cloud — Epic 25's core value proposition (remote reachability) is non-functional end-to-end, and several cloud-side stories (DNS provisioning, TLS/ACME, entitlement endpoints, direct fallback, GDPR export, worker fanout) are stubs or unwired.

**Scope reviewed:** `specs/epics/25-cloud-relay/README.md`, 36 `story-*.md`, `specs/PLAN_REVIEW_25.md`; code in `cloud/` and `api/`. Method: each AC traced to code; verified existence + reachability (router wiring) + behavior. Self-claims in `FULL_IMPLEMENTATION_AUDIT.md` were NOT trusted.

**AC status counts (≈190 ACs across 36 stories):**

| Status | Count (approx) | Meaning |
|--------|----------------|---------|
| complete | ~58 | Code exists, wired, behaviorally satisfies AC |
| partial | ~46 | Exists & wired but diverges from AC (protocol/shape/missing branch) |
| missing | ~52 | No code implements the AC |
| unwired | ~18 | Code exists but not mounted/called from any role |
| stub | ~16 | Placeholder shape, explicit "real impl in follow-up" |

---

## Critical: the on-prem cloudlink client gap (PLAN_REVIEW_25 §1.3)

This is the single worst gap and it invalidates the epic's premise.

**What the spec requires.** Story 25.6 §"Files/packages" requires `internal/cloudlink/claim.go`; Story 25.7 §"Files/packages" requires `internal/cloudlink/{conn,frame,multiplex,proxy,health}.go` plus a separate process `cmd/maktaba-cloudlink/main.go`. Story 25.7 ACs require the on-prem box to: open a WSS to `wss://relay.maktaba.app/tunnel/v1/connect` within 5s of start, send `Authorization: Bearer <server_token>`, frame-multiplex requests to its loopback API, PING/PONG every 25s/10s, exponential-backoff reconnect, handle `0x20 REVOKE`, refresh entitlements on `0x21 ENT_REFRESH`, and surface state at local `GET /admin/cloud-link`. Story 25.6 requires the server to POST `/api/servers/claim/init` with `{token_hash, server_pubkey, server_version}` and persist `{server_id, server_token}` encrypted-at-rest.

**What exists.** Nothing. Verified:

- `find . -type d -name cmd` (excluding worktrees): only `cloud/cmd`, `api/cmd`(absent — `api` has root `api/main.go` only), `streaming/cmd`, `tools/*`. **No `cmd/maktaba-cloudlink`.**
- `grep -rl cloudlink --include='*.go' .` → **zero files** anywhere in the repo.
- No top-level `go.mod` (`ls go.mod` → absent); `api/go.mod` is module `github.com/Hamza-Labs-Core/Maktaba/api`. PLAN_REVIEW_25 §1.3 flagged the module-placement question; it was never resolved and no code was written under any placement.
- `api/internal/` has no cloud-tunnel/claim package. `api/internal/discovery/discovery.go:1-18` is LAN mDNS only. `api/internal/handlers/ws/ws.go:1-20` is a *local* SSE/WS fan-out hub (Story 7.16), not an outbound cloud dialer. `grep websocket.Dial|gorilla/websocket api/` → only the local `ws` handler, no client dialer.
- `api/internal/subscriptions/subscriptions.go:43` defines a `FeatureCloudRelay` enum constant locally but nothing fetches or verifies a signed entitlement from the cloud.
- The Docker image (`cloud/installers/docker/Dockerfile:18-22`) builds **only** `api/maktaba-server`; `maktaba-cloudlink` is never compiled, contradicting plan-25-30 §1. So even the container path ships no cloud connectivity.

**Consequence.** The cloud server is a building with no doors: `cloud/internal/relay/ws.go:81 ServeWS` will accept a tunnel and `cloud/internal/handlers/servers/servers.go:81 RedeemClaim` will mint a `server_secret`, but **no software on a user's box ever calls them**. End-to-end claim → tunnel → relay is impossible. Every Story 25.7 AC and the "Persist (server side)" half of Story 25.6 are `missing`. Stories that depend on the tunnel existing at runtime (25.9 proxy reachability, 25.11 metering of real traffic, 25.12 stream gating, 25.16 server status, 25.17 push ingest over relay) are untestable end-to-end.

**Compounding (PLAN_REVIEW_25 §1.2).** Story 25.6 AC and 25.7 depend on an Ed25519 long-term server identity ("Epic 10 Story 10.18"). Story 10.18 does not exist; `grep -rn ed25519 api/internal/` finds only Epic 16 license keys. The shipped cloud `RedeemClaim` accepts a `public_key_pem` field but never verifies it against an `init` step (there is no `/claim/init` endpoint at all), so the spec's anti-replay pubkey-pinning (25.6 AC "claim_pubkey_mismatch") cannot function even if a client existed.

---

## Per-story AC tables

Legend: ✅ complete · 🟡 partial · ❌ missing · 🔌 unwired · 🧱 stub

### Phase 1 — Identity (25.1–25.5)

| Story | AC summary | Status | Evidence |
|-------|------------|--------|----------|
| 25.1 Bootstrap | Single binary, 3 roles | ✅ | `cloud/cmd/maktaba-cloud/{main,role_api,role_relay,role_worker}.go` |
| 25.1 | `/healthz`,`/readyz` (db+migrations) | ✅ | `cloud/internal/server/router.go:41-78` |
| 25.1 | Structured logs, request-id, CORS | ✅ | `router.go:36-39` |
| 25.1 | Cloudflare-in-front / TLS posture | 🧱 | No TLS termination code; assumes external LB (`role_relay.go:22-24`) |
| 25.2 Email reg | argon2id hashing | ✅ | `cloud/internal/auth/argon2id/argon2id.go`, `auth/password/password.go` |
| 25.2 | register/login/refresh/logout | ✅ | `cloud/internal/handlers/auth/auth.go`, mounted `role_api.go:98` |
| 25.2 | email verification token | 🟡 | `email_verifications` table exists (mig 00020001); send/verify handler present but no real mailer (no SMTP/provider code) |
| 25.2 | account lockout / brute-force | 🟡 | Per-IP limiter on `/v1/auth/login` only (`role_api.go:90-96`); no per-account lockout counter |
| 25.2 | password reset flow | 🟡 | Token table exists; reset endpoint present; mailer absent |
| 25.3 Google OAuth | OIDC code + PKCE | ✅ | `cloud/internal/auth/oauth/google.go`, wired `role_api.go:62-69` |
| 25.3 | account-link on email collision | 🟡 | `oauth_links` table + `handlers/auth/oauth.go`; explicit merge-prompt flow not evident (auto-handling only) |
| 25.4 Apple OAuth | Apple OIDC + JWS client secret | ✅ | `cloud/internal/auth/oauth/apple.go`, `oauth/jws.go`, wired `role_api.go:70-79` |
| 25.4 | private-relay email handling | 🟡 | Apple flow present; relay-alias dedup logic not clearly implemented |
| 25.5 Profile | GET/PATCH profile | ✅ | `cloud/internal/handlers/account/account.go:51,81` |
| 25.5 | change email / password | ✅ | `account.go:104,145` |
| 25.5 | GDPR delete (30-day hold) | 🟡 | `account.go:154 RequestDeletion` inserts `account_deletions`; purge in `role_worker.go purgeDeletedAccounts`. No PII-redaction / 9-table traversal / audit retention as README threat-model requires |
| 25.5 | **`GET /api/me/export` data export ZIP** | ❌ | No export handler in `account.go`; not mounted anywhere |

### Phase 2 — Linking & Relay (25.6–25.10)

| Story | AC summary | Status | Evidence |
|-------|------------|--------|----------|
| 25.6 | Mint claim code, 10-min TTL | 🟡 | `servers.go:48 MintClaim`; TTL 10m (`:59`). Spec wants `K3F9-MZ7P` base32 8-char w/ hyphen group + QR; impl format unverified, no QR |
| 25.6 | **`/claim/init` (server posts token_hash, pubkey)** | ❌ | No init endpoint exists; only one-step `/v1/servers/claims/redeem` (`servers.go:34`) |
| 25.6 | Redeem → cloud_servers + token in 1 txn | 🟡 | `RedeemClaim` (`servers.go:81`) creates server + argon2 secret; not bcrypt as spec; single-step (no init pairing) |
| 25.6 | 410 claim_expired + audit row | ❌ | Returns generic 404 `claim_invalid` (`servers.go:99`); no 410; no audit (no `audit_events` table) |
| 25.6 | pubkey mismatch → 400 | ❌ | No init→redeem pubkey comparison; `public_key_pem` stored unverified (`servers.go:116`) |
| 25.6 | claim brute-force → 429 + abuse row | ❌ | No per-IP limiter on redeem; `abuse` detector instantiated then discarded `_ = abuse.New` (`role_api.go:46`) |
| 25.6 | response includes cloud_endpoint + entitlement | ❌ | `redeemResp` is `{server_id,server_secret,slug}` only (`servers.go:70-74`); no endpoint, no entitlement |
| 25.6 | TLS-pin to Cloudflare cert (server side) | ❌ | Server side absent (no cloudlink) |
| 25.7 (server side) | **ALL 7 ACs** (WSS connect, frame mux, backoff, PING/PONG, REVOKE, concurrency, RSS bound) | ❌ | **No cloudlink client exists** — see Critical section |
| 25.8 (cloud side) | Accept tunnel, register, demux | 🟡 | `cloud/internal/relay/ws.go:81 ServeWS`, `relay/registry.go`, `relay/tunnel.go`; wired `role_relay.go:34` at `/v1/relay/ws` |
| 25.8 | Auth = `Authorization: Bearer server_token` | ❌ | Impl uses an AUTH **frame** with `{server_id,secret}` argon2 (`ws.go:99-136`), not the spec's Bearer header / bcrypt |
| 25.8 | Endpoint path `tunnel/v1/connect` | 🟡 | Impl path is `/v1/relay/ws` (`role_relay.go:34`); spec says `wss://relay.maktaba.app/tunnel/v1/connect` |
| 25.8 | Last-write-wins on duplicate session | 🟡 | `registry.go` Register/Unregister present; explicit older-session-close on collision not verified |
| 25.8 | PING/PONG, REVOKE, ENT_REFRESH frames | 🟡 | `protocol.go` has frame kinds; `tunnel.go:81` handles KindPing only; REVOKE/ENT_REFRESH dispatch absent |
| 25.9 HTTP proxy | Host→slug→tunnel proxy | ✅ | `cloud/internal/handlers/relay/proxy.go:31-83`, wired `role_relay.go:39-45` |
| 25.9 | hop-by-hop strip, XFF rewrite | ✅ | `proxy.go:120-135,61-63` |
| 25.9 | offline server → 502 | ✅ | `proxy.go:43-47` |
| 25.9 | streaming/no full-buffer (1GB) | 🟡 | `io.Copy` streams response, but `tunnel.go:137` copies whole body frames; backpressure/window not enforced (25.7 §backpressure) |
| 25.9 | 404 non-Maktaba host (threat model) | ✅ | `proxy.go:33-36` slug miss → 404 |
| 25.10 Direct fallback | **ALL ACs** (LAN probe, server_endpoints, candidates_sealed) | ❌ | No `server_endpoints` table (migrations grep), no probe handler, no client (no cloudlink). Entirely missing |

### Phase 3 — Metering & Billing (25.11–25.16)

| Story | AC summary | Status | Evidence |
|-------|------------|--------|----------|
| 25.11 Bandwidth | Per-server byte counts, 5-min samples | 🟡 | `cloud/internal/billing/meter.go`; `bandwidth_samples/_monthly` tables (mig 00050001); `proxy.go:79-81` records. Roll-up worker job NOT implemented (`role_worker.go` only cleanup+purge) |
| 25.11 | Cloud-side counting only (not client) | ✅ | Metered at edge in `proxy.go` |
| 25.12 Tier enforce | Free=0 relay GB hard cap | 🟡 | `meter.Allow` (`proxy.go:49-53`) returns 402; thresholds/Family-500GB/5-stream gauge not all verified; concurrent-stream gauge absent |
| 25.13 Stripe checkout | `POST /billing/checkout` → URL | ✅ | `cloud/internal/handlers/billing/billing.go:108`, `billing/stripe.go CreateCheckoutSession`, wired `role_api.go:115` |
| 25.13 | `POST /billing/portal` | ❌ | No portal handler in `billing.go` (only checkout+webhook+plans+me) |
| 25.14 Stripe webhook | Signature verify | ✅ | `billing.go:158 VerifyWebhookSignature`, mounted unauthenticated `role_api.go:129` |
| 25.14 | Idempotent via `stripe_events` PK | ✅ | `billing.go:171-186` INSERT dedupe |
| 25.14 | `customer.subscription.*` → entitlement | 🟡 | `billing.go:190 applyEvent` upserts `subscriptions`; does NOT issue/sign an entitlement blob (25.26 unwired) |
| 25.15 Plan UI | Marketing/upgrade page | n/a-web | `web/` not in scope of this cloud audit; `GET /v1/billing/plans` exists `billing.go:44` |
| 25.16 Server status | Online/offline/last-seen/version | 🟡 | `servers.go:165 Heartbeat` + `server_health` table; but heartbeat is POST-from-server which requires cloudlink (absent). "updates available" field not implemented |

### Phase 4 — Push (25.17–25.19)

| Story | AC summary | Status | Evidence |
|-------|------------|--------|----------|
| 25.17 Push ingest | `POST /push/dispatch` server-auth | 🟡 | `cloud/internal/handlers/push/push.go:90 Dispatch`, wired `role_api.go:122`. But mounted inside `RequireUser` group (`role_api.go:110-122`) — spec says X-Server-Token server auth, not user bearer; mismatch |
| 25.17 | tokens never returned in API | ✅ | `push.go` register/delete never echo token |
| 25.17 | tokens sealed at rest (AES-GCM/KMS) | ❌ | `push.go:60` inserts raw `token` into `push_devices`; no encryption (README threat model requires AES-GCM) |
| 25.17 | no-devices handling | ❌ bug | `push.go:107 errors.Is(err, errors.New("..."))` always false — sentinel comparison bug; branch dead |
| 25.18 APNs | team key + JWT, BadDeviceToken cleanup | 🟡 | `cloud/internal/push/apns.go` (125 lines), `push/jws.go`; driver only built if config present (`role_api.go:51`). BadDeviceToken row cleanup not clearly implemented |
| 25.19 FCM | Firebase Admin, per-device | 🟡 | `cloud/internal/push/fcm.go` (146 lines); built if config present (`role_api.go:55`) |

### Phase 5 — Admin, Subdomain, TLS, Security, Entitlement (25.20–25.26)

| Story | AC summary | Status | Evidence |
|-------|------------|--------|----------|
| 25.20 Admin fleet | Search users / servers / last-seen | 🟡 | `cloud/internal/handlers/admin/admin.go:61 Fleet`, wired `role_api.go:123` |
| 25.20 | Admin SSO `@hamzalabs.com` + audit on every action | 🟡 | `admin.go:33 RequireAdmin` checks email domain; **no fresh re-auth ≤5min** (threat model); **no `audit_events` table** so no admin-action audit |
| 25.21 Admin revenue | MRR/ARR/churn/LTV from Stripe | 🟡 | `admin.go:76 Revenue` sums `users.plan` locally — spec requires *pulled from Stripe*; no churn/LTV/ARR |
| 25.22 Subdomain | Username uniqueness + reserved list | 🟡 | `cloud/internal/handlers/servers/subdomain.go:42 Check/76 Claim`, `reserved_slugs` table |
| 25.22 | **DNS via Cloudflare API** | ❌ | `subdomain.go:20-22` comment "DNS provisioning … happens out-of-band in a worker"; **no Cloudflare API code anywhere** (`grep cloudflare cloud/internal` → none) |
| 25.22 | handler reachable | 🔌 | `MountSubdomains` defined (`subdomain.go:23`) but **never called** in any role (`grep MountSubdomains cmd/` → none) — dead code |
| 25.22 | 90-day rename limit / 301 old subdomain | ❌ | Not implemented |
| 25.23 TLS edge | Wildcard ACME `*.maktaba.app` DNS-01 | 🧱 | `cloud/internal/tls/acme.go:9-14` explicitly: "do not import x/crypto/acme … this file establishes the shape." Only a HostPolicy stub; no issuance, no rotation, not wired to relay listener |
| 25.24 Rate limiting | Per-IP/user/server sliding window, 429+Retry-After | 🟡 | `cloud/internal/ratelimit/ratelimit.go` token-bucket; wired only on login/register (`role_api.go:90-96`). No redis, no per-user/per-server scope, no `rate_overrides` table |
| 25.25 Abuse detection | Anomaly/port-scan/hotlink/bot signals + suspend | 🔌 | `cloud/internal/abuse/detector.go` exists but `_ = abuse.New(pool.DB) // wired … follow-up` (`role_api.go:46`) — instantiated and discarded; no signals emitted, `abuse_signals`/`blocklist` tables unused |
| 25.26 Entitlement signing | Ed25519-signed JSON, 24h, kid, server caches | 🔌 | `cloud/internal/entitlement/entitlement.go` Sign/Verify/Fingerprint correct & tested; but **no HTTP endpoint** (`GET /api/entitlements`, `/api/servers/{id}/entitlement` not mounted in any role), no issuance at claim/webhook, `entitlement_keys`/`entitlement_grants` tables never written. Server-side verify consumer absent (no cloudlink) |

### Phase 6 — Distribution (25.27–25.36)

All distribution stories ship **config/script artifacts, not built+signed binaries**, and every one references the non-existent `maktaba-cloudlink`.

| Story | Status | Evidence |
|-------|--------|----------|
| 25.27 macOS installer | 🧱 | `cloud/installers/macos/build.sh` script only; no notarized DMG, no Sparkle code; bundles `maktaba-cloudlink` (absent) |
| 25.28 Windows installer | 🧱 | `cloud/installers/windows/build.ps1` only; no EV-signed MSI |
| 25.29 Linux packages | 🟡 | `cloud/installers/linux/nfpm.yaml`, systemd unit, pre/post scripts present; no built repo, no `maktaba-cloudlink` unit |
| 25.30 Docker image | 🟡 | `cloud/installers/docker/Dockerfile` builds only `api/maktaba-server`; **`maktaba-cloudlink` never compiled** (contradicts plan-25-30 §1); multi-arch not in Dockerfile |
| 25.31 NAS support | 🧱 | `installers/nas/{synology/INFO,qnap/qpkg.cfg}` skeletons only |
| 25.32 Pi/ARM | 🧱 | `installers/rpi/README.md` only |
| 25.33 One-click VPS | 🟡 | `installers/cloud-vps/{hetzner.tf,cloud-init.yaml,digitalocean.yaml}` present |
| 25.34 Auto-update | 🧱 | `installers/auto-update.md` doc only; no signed-manifest verifier code |
| 25.35 First-run wizard | 🧱 | `installers/first-run-wizard.md` doc only; no wizard code; cloud-link step impossible (no cloudlink) |
| 25.36 Uninstaller | 🟡 | `installers/uninstaller.md` + `linux/scripts/preremove.sh,postremove.sh` |

---

## Top gaps by impact

1. **[BLOCKER] No on-prem cloudlink client (Story 25.6 server half + all of 25.7).** Zero code. The cloud cannot be reached by any self-hosted server. The epic's entire reason to exist (remote access without port-forwarding) is non-functional E2E. Cascades to 25.9/25.11/25.12/25.16/25.17 being runtime-untestable. *Evidence:* no `cmd/maktaba-cloudlink`, no `internal/cloudlink`, zero `cloudlink` refs repo-wide; Docker builds only `maktaba-server`.

2. **[BLOCKER] Claim protocol divergence (25.6).** Spec's two-step `/claim/init`→`/claim` with token_hash + Ed25519 pubkey pinning + entitlement-in-response is reduced to a single unverified `/v1/servers/claims/redeem` (`servers.go:81`). No init, no pubkey check, no 410/409 codes, no audit, no rate-limit, no entitlement returned. Defeats the threat-model anti-replay defenses.

3. **[BLOCKER] Tunnel auth & path mismatch (25.8).** Cloud accepts an AUTH-frame `{server_id,secret}` (argon2) at `/v1/relay/ws`; spec mandates `Authorization: Bearer server_token` (bcrypt) at `/tunnel/v1/connect`. Even a future client built to spec would not interoperate.

4. **[HIGH] Entitlement system unwired (25.26 + 25.14).** Crypto is correct & tested but no endpoint serves it, claim/webhook never issues/signs grants, `entitlement_keys`/`entitlement_grants` never written. Paid features cannot be gated on any server.

5. **[HIGH] DNS provisioning absent (25.22).** No Cloudflare API integration anywhere; `MountSubdomains` is dead code (never mounted). `username.maktaba.app` is never actually created in DNS.

6. **[HIGH] TLS edge is a stub (25.23).** `acme.go` explicitly ships only a HostPolicy shape; no ACME issuance/rotation, not wired. No valid cert for any subdomain.

7. **[HIGH] Abuse detection unwired (25.25); rate-limit narrow (25.24).** `abuse.New` result discarded; limiter only on login/register, no per-user/server scope, no redis, no `rate_overrides`.

8. **[MED] GDPR export missing, delete shallow (25.5).** No `/api/me/export`; deletion is a single `account_deletions` insert + blunt `DELETE FROM users` — no PII redaction/audit retention.

9. **[MED] Push token not encrypted at rest + dead no-device branch (25.17).** Raw token stored (`push.go:60`); `errors.Is(err, errors.New(...))` (`push.go:107`) is a永-false sentinel bug.

10. **[MED] Admin: revenue not Stripe-sourced; no fresh re-auth; no audit table (25.20/25.21).** Revenue sums local `users.plan`; `audit_events` table absent so no admin action is audited (also PLAN_REVIEW_25 §1.5 shape-drift).

11. **[MED] Worker role is hollow (25.11/25.14/25.22).** Only token cleanup + account purge; no bandwidth roll-up, no Stripe reconciliation, no DNS worker, no push fanout job — contradicting its own doc comment.

12. **[LOW/CONFIRMED-RESOLVED] Migration slot collision (PLAN_REVIEW_25 §1.1).** The *plans* collided, but shipped `cloud/migrations/*.sql` are collision-free and match README slots. However README-promised tables `audit_events`, `server_endpoints`, `rate_overrides` were never created (consistent with stories 25.20/25.10/25.24 being unimplemented).

## Notes on audit-claim verification

`FULL_IMPLEMENTATION_AUDIT.md`'s "cloud-side largely complete" is **directionally true for the cloud server's identity/billing/proxy surface but materially overstates readiness**: it lists entitlement-signing, subdomain, TLS/ACME, abuse, installers, auto-update, first-run as done — code inspection shows these are unwired, stubbed, or doc-only. Its "~6 gaps" undercounts; the cloudlink-client absence alone makes the epic non-shippable, and 6 cloud-side stories (25.10, 25.22-DNS, 25.23, 25.25, 25.26-wiring, worker jobs) are missing/stub beyond that.
