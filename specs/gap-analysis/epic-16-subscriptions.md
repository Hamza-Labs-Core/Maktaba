# Epic 16 — Subscriptions & Monetization: Gap Analysis

**Verdict:** Epic 16 is ~10% implemented. A single in-memory entitlement
store + Ed25519 license verifier exists and is mounted, but the
`/api/admin/license` endpoint is **dead on arrival** (Verifier is nil at
wiring), no entitlement gate is enforced at a single call site, the
license table is never read/written (no persistence, no refresh, no
CRL, no grace), and Stories 16.3/16.5/16.6/16.7/16.8 have essentially
no code (no migrations, no endpoints, no client surface).

---

## Method

Every AC was traced to code. "Verified against code" means: handler is
mounted **and reachable with working deps**, behavior matches the AC,
and there is a live caller. Spec/audit self-claims were not trusted.

Key wiring facts established:

- `api/main.go:266` calls `router.MountP10(r, router.P10Deps{DB: appDB})`
  — passes **only `DB`**. `SubscriptionsStore` and
  `SubscriptionsVerifier` are left nil.
- `api/internal/router/p10.go:76-80`: nil store → fresh in-memory
  free-tier store; **nil Verifier** → `SetLicense` returns
  `httperror.Internal("verifier not configured")`
  (`api/internal/handlers/subscriptions/subscriptions.go:78-81`). So
  `POST /api/admin/license` is permanently a 500.
- `grep` for `FeatureCloudRelay|FeatureFederation|FeatureMultiUser|
  FeatureCloudBackup|FeatureAdvancedMetric|.Allows(|SubscriptionsStore`
  across `api/ streaming/ cloud/` (excl. the subscriptions package
  itself and `_test.go`): **zero** results. No gate is enforced
  anywhere.
- Migration `shared/db/migrations/0056_licenses.sql` exists but **no Go
  code references the `licenses` table** (only sbom.go's unrelated
  `licenses` local var). The `Store` is purely in-memory
  (`subscriptions.go:182-204`); a process restart silently reverts any
  applied license to free.
- No migrations for telemetry, feature flags, billing, tier_grace, or
  seats. Highest migration is `0057_integrity_checks`.
- No `/api/telemetry*`, `/api/me/flags`, `/api/me/cohorts`,
  `/api/admin/flags*`, `/api/billing/portal-session` routes anywhere.
- `cloud/internal/handlers/billing/billing.go` is a **different
  subsystem** (Epic 13 cloud relay: tiers free/pro/family, routes
  `/v1/billing/*`, table `subscriptions`) — does not satisfy Epic 16
  Story 16.3 (tiers free/home/pro, routes `/api/billing/*`, tables
  `billing_customers/_subscriptions/_invoices/_webhook_events`,
  reconcile cron, dispute handler, license rotation).
- No web/native client surface: `Subscription.tsx`, `useFlag`,
  `useTier`, telemetry client, flags client, entitlements consumer — all
  absent.

---

## AC status counts

| Status | Count |
|---|---|
| complete | 1 |
| partial | 6 |
| unwired | 5 |
| stub | 0 |
| missing | 56 |
| **Total ACs assessed** | **68** |

"unwired" = code exists and is behaviorally plausible but unreachable
on any live path (nil deps / no caller / no persistence).

---

## Story 16.1 — Free tier (local only, single user)

| AC | Status | Evidence |
|---|---|---|
| Epics 1–15 work without a license key | complete | No code gates any Epic 1–15 feature on entitlement (grep: zero gate call sites). Free tier is unimpaired *by default* — but this is by *absence* of gating, not by design. |
| "Get Premium" entry point in Settings, unobtrusive | missing | No `web/src/features/settings/Subscription.tsx`; no native equivalent. `grep -rln Subscription web/src apps/` → nothing. |
| Single-user sentinel UUID `…001`; bootstrap admin token | partial | Sentinel-UUID auth is an Epic 10 concern; not verified here as Epic 16 code. plan-16-01 §1.3 explicitly defers to Epic 10 Story 10.9. No Epic-16-owned code asserts `playback_state.user_id = sentinel`. |
| LAN-only: relay/multi-user/cloud-backup disabled | partial | True only because gates are never enforced (features simply don't exist as gated). Not an implemented "disable" — an implemented "never wired". |
| License-server unavailable: free tier unaffected | partial | True by absence (no refresher, no license-server client exists at all — `grep Refresher` → nothing). The resolver/grace logic of plan-16-01 §1.4 is unimplemented. |
| `Resolver.Current` returns real `free` Tier value | missing | No `api/internal/license/resolver.go`. `subscriptions.Store.Current()` returns `nil` for free (subscriptions.go:200), and `Allows` branches on `e == nil` (subscriptions.go:71) — the exact "branch on is-licensed?" anti-pattern plan-16-01 §1.1 forbids. |
| Flag declarations bake free-tier-positive defaults | missing | No `api/internal/flags/declarations.go`. |
| Premium-back-to-free preserves data read-only | missing | No tier-downgrade/grace logic. |

## Story 16.2 — Premium features

| AC | Status | Evidence |
|---|---|---|
| Flags `relay/multi_user/backup/analytics/federation` gated by tier (free/home/pro) | missing | Tiers in code are `free`/`premium` (subscriptions.go:31-35) — **not** the spec's `free`/`home`/`pro`. No per-tier flag matrix. Migration `0056_licenses.sql:10` CHECK is `('free','premium')`, contradicting plan-16-04 (`home`). |
| `home` tier: 200 GB relay / 4 seats / daily backup / basic analytics | missing | No tier `home`; no quota, seat, backup, analytics gating. |
| `pro` tier: 1 TB / unlimited seats / hourly backup / advanced / federation | missing | Same. |
| Server enforces gates; clients only render UI | missing | Zero server-side gate call sites; no client UI. |
| Downgrading: 30-day read-only grace then hidden | missing | No `api/internal/license/grace.go`, no `tier_grace` table/migration. |
| Seat enforcement on `POST /api/users` | missing | No `api/internal/auth/seats.go`; `grep -i seat` in handlers → nothing. No `current_seat_count`. User-create path has no cap check. |
| Backup engine produces `.maktaba-backup`, encrypted, cadence by tier | missing | No `api/internal/backup/` package at all. |
| EC: clock skew 72 h grace / revocation locks immediately | missing | No CRL, no clock defense, no grace. |

## Story 16.3 — Subscription management

| AC | Status | Evidence |
|---|---|---|
| Read-only by default; "Manage" deep-links to Stripe portal | missing | No `api/internal/billing/portal.go`, no `POST /api/billing/portal-session`. (cloud `/v1/billing/checkout` is Epic 13, wrong epic/tiers/routes.) |
| Cancellation confirm modal copy | missing | No UI; no `billing.cancel.*` i18n strings. |
| Upgrades immediate / downgrades at renewal | missing | No Epic-16 webhook handler updating a `licenses` row; no tier flip. |
| Receipts: invoice list + downloadable PDF | missing | No `billing_invoices` table/migration; no invoice-PDF proxy endpoint. |
| VAT/tax indication | missing | No invoice surface. |
| EC: webhook retry/reconcile cron; dispute → free; double-purchase dedupe | missing | No `api/internal/billing/{webhook,reconcile}.go`; no `billing_webhook_events` dedupe table; no `handleDispute`/`RotateForCustomer`. |
| Schema: `billing_customers/_subscriptions/_invoices/_webhook_events` | missing | No `0062_billing.sql`. |

## Story 16.4 — License key validation

| AC | Status | Evidence |
|---|---|---|
| Ed25519-signed JSON `{license_id,tier,seats,expires_at,signature}` | partial | `subscriptions.go:79-148` verifies Ed25519 but over a **non-canonical** `json.Marshal` (subscriptions.go:171-178) — NOT RFC 8785 JCS as plan-16-04 §0/§2 mandates. Wire shape is `{license:{…},signature}` (nested), not the spec's flat object; no `kid` field. |
| Server bundles license-server public key at build time | missing | No `api/internal/license/embedded_pubkey.go`, no `keys/license-server.pub.pem`. Verifier's `PublicKey` is injected and is **always nil** in production (P10Deps.SubscriptionsVerifier unset). |
| Validation: signature + expiry + seat-count | partial | Signature + expiry + `seats>=0` present (subscriptions.go:118-129). Unreachable: `SetLicense` 500s because Verifier is nil. |
| Daily refresh; 30-day offline grace | missing | No `api/internal/license/refresher.go`; `grep Refresher` → nothing. |
| Revocation list daily; locks immediately | missing | No CRL fetch/store; migration 0056 has a `revoked_at` column but no `license_revocations` table and no code path. |
| License keys never logged/returned; write-only | partial | `GetEntitlements`/`SetLicense` never echo the raw license (handlers/subscriptions.go:53-68,93). But there's no `/api/settings` integration and no persistence to redact in the first place. |
| Storage: `licenses` + `license_revocations`, singleton, sealed-at-rest | partial | `0056_licenses.sql` creates `licenses` (plaintext `raw_jwt`, **not** `raw_sealed BYTEA` per plan-16-04 §3; no `version`, `last_refreshed_at`, `last_status` columns; no `license_revocations` table). **No Go code reads/writes it** — Store is in-memory only. |
| Clock-manipulation defense (trust server Date / effectiveNow) | missing | No `parseDateHeader`/`effectiveNow`; `now` is plain `time.Now().UTC()` (handlers/subscriptions.go:108). |

## Story 16.5 — Usage analytics (opt-in, client surface)

| AC | Status | Evidence |
|---|---|---|
| First-launch opt-in dialog, 5 bullets, no telemetry until accepted | missing | No `web/src/lib/telemetry/`; no consent component. |
| Collected/never-collected scope; sanitizer strips paths | missing | No client; no sanitizer. |
| Pseudonym per device, rotates on opt-out/in | missing | No `pseudonym.ts`. |
| Endpoints `POST /api/telemetry`, `/web-vitals` | missing | No routes (grep). |
| Self-host opt-out `[telemetry] enabled=false` | missing | No telemetry config. |
| EC: queue cap 1000 oldest-dropped; backoff; never blocks UI | missing | No queue. |
| Forget-my-device button → DELETE | missing | No UI, no endpoint. |

## Story 16.6 — Feature flags per tier (client surface)

| AC | Status | Evidence |
|---|---|---|
| Server returns `GET /api/me/flags` (resolved set) | missing | No route. |
| Client hides gated UI; signed-bundle verify; 60 s cache; foreground refresh | missing | No `web/src/lib/flags/`; no native `Flags.swift`/`Flags.kt`. |
| Beta flags via Settings → Advanced → Experiments | missing | No UI; no cohort opt-in call. |
| EC: refresh-fail uses cache; signed flags re-checked on privileged action | missing | No client, no `assertOn`, no signature verifier. |

## Story 16.7 — API: telemetry sink

| AC | Status | Evidence |
|---|---|---|
| Tables `telemetry_events`, `telemetry_web_vitals` + indexes | missing | No `0064_telemetry.sql`. |
| `POST /api/telemetry` (≤100, 413 over), `/web-vitals` (≤50), `DELETE /devices/{ps}` | missing | No `api/internal/http/telemetry.go`; no routes. |
| Rate-limit 1k/min/IP | missing | No handler. |
| Redaction: kind allow-list, field allow-list, IP drop, library-root regexp_quote strip | missing | No `api/internal/telemetry/{allowlist,redact}.go`. |
| `[telemetry] enabled=false` → 204 no-write | missing | No config/handler. |
| Retention 90 d / 30 d nightly sweep | missing | No `retention.go`. |

## Story 16.8 — API: feature-flag resolution endpoint

| AC | Status | Evidence |
|---|---|---|
| Tables `feature_flag_overrides`, `beta_cohorts` + scope-value CHECK + LISTEN trigger | missing | No `0065_feature_flags.sql`. |
| Resolver: defaults → tier → cohort → user precedence | missing | No `api/internal/flags/resolver.go` or `declarations.go`. |
| `GET /api/me/flags` 200 signed Ed25519, `kid`, issued/expires | missing | No route; no signing key (depends on Epic 10 Story 10.18 Ed25519 long-term keys — plan-16-06/08 §0 mark this as a hard blocker). |
| Admin override CRUD; cohort batch add/remove; user opt-in cohort | missing | No `/api/admin/flags*`, `/api/me/cohorts` routes. |
| In-memory cache per `(user_id, license_state_version)`; LISTEN invalidation | missing | No resolver/cache. |
| Audit row `category='flags'` on admin write | missing | No admin writes exist. |

---

## Top gaps by impact

1. **The entire entitlement enforcement layer is non-functional.**
   `MountP10` is called with only `DB` (`api/main.go:266`), so the
   Verifier is nil — `POST /api/admin/license` always 500s
   (`handlers/subscriptions/subscriptions.go:78-81`). Even if it
   worked, **no code anywhere enforces a single premium gate** (grep of
   `FeatureCloudRelay|...|.Allows(` across api/streaming/cloud → zero
   non-test, non-self call sites). The "premium-gates exist" audit
   claim is false at the call-site level: the gate type exists; nothing
   calls it. Net effect: premium features (if they existed) would be
   free for everyone, and licenses cannot be applied at all.

2. **No license persistence — applied licenses vanish on restart.** The
   `licenses` table (`0056_licenses.sql`) is never read or written by
   Go (`subscriptions.Store` is `sync.RWMutex`-guarded in-memory only,
   subscriptions.go:182-204). No refresher, no CRL, no offline grace,
   no clock defense — Story 16.4's entire operational half is absent.
   The migration's schema also diverges from plan-16-04 (plaintext
   `raw_jwt` vs sealed `raw_sealed`; tier CHECK `('free','premium')`
   vs spec's `free/home/pro`; missing `version`/`last_refreshed_at`).

3. **Tier model contradicts the spec.** Code uses `free`/`premium`
   (subscriptions.go:31-35; migration CHECK); every Epic 16 story and
   plan specifies `free`/`home`/`pro` with distinct quotas. Any future
   billing/flag work built on the current model will need rework.

4. **Stories 16.3, 16.5, 16.6, 16.7, 16.8 are ~0% — no migrations, no
   endpoints, no client.** No billing portal/webhook/reconcile, no
   telemetry sink, no flag resolver/`GET /api/me/flags`, no web/native
   subscription/flags/telemetry surface. The cloud `/v1/billing/*`
   handler is a separate Epic-13 subsystem and does not satisfy 16.3.
   Stories 16.6/16.8 are additionally blocked on an unimplemented Epic
   10 Story 10.18 (Ed25519 long-term server keys) for flag signing.
