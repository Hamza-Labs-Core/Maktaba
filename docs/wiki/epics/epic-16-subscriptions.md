# Epic 16 — Subscriptions & Monetization (Optional)

> **Status:** spec + plans complete. **Source:** `specs/epics/16-subscriptions/`.
> **Anchors:** [`architecture.md` §10.4](../../../specs/architecture.md), §11.5.

## Goal

Maktaba is fully usable for free as a self-hosted single-user product. Optional premium features (cloud relay quota, multi-user seats, cloud metadata backup, advanced analytics) are gated by a license key and validated locally; the license server is contacted only for refresh and revocation. **All paid features are server-side gates;** the client surface only enables UI. The system maintains full functionality even when the license server is unreachable, with a 30-day offline grace.

## Stories & Plans

| # | Story | Plan | Summary |
|---|-------|------|---------|
| 16.1 | [Free tier](../../../specs/epics/16-subscriptions/story-16-01-free-tier.md) | [plan-16-01](../../../specs/epics/16-subscriptions/plan-16-01-free-tier.md) | Canonical product: full features for single user, no nag screens, no license required. |
| 16.2 | [Premium features](../../../specs/epics/16-subscriptions/story-16-02-premium-features.md) | [plan-16-02](../../../specs/epics/16-subscriptions/plan-16-02-premium-features.md) | Tier matrix: free (1 seat) / home (4 seats, 200 GB relay, daily backup) / pro (unlimited seats, 1 TB relay, hourly backup, federation). |
| 16.3 | [Subscription management](../../../specs/epics/16-subscriptions/story-16-03-subscription-management.md) | [plan-16-03](../../../specs/epics/16-subscriptions/plan-16-03-subscription-management.md) | Settings UI deep-links to Stripe Customer Portal; webhook handler tracks state; daily reconciliation cron. |
| 16.4 | [License key validation](../../../specs/epics/16-subscriptions/story-16-04-license-validation.md) | [plan-16-04](../../../specs/epics/16-subscriptions/plan-16-04-license-validation.md) | Ed25519-signed license JSON; local signature check + daily server refresh; 30-day offline grace; revocation list fetch. |
| 16.5 | [Usage analytics (opt-in)](../../../specs/epics/16-subscriptions/story-16-05-telemetry-opt-in.md) | [plan-16-05](../../../specs/epics/16-subscriptions/plan-16-05-telemetry-opt-in.md) | First-launch consent dialog; client-side queue (cap 1000); device pseudonym; server kill-switch. |
| 16.6 | [Feature flags per tier](../../../specs/epics/16-subscriptions/story-16-06-feature-flags.md) | [plan-16-06](../../../specs/epics/16-subscriptions/plan-16-06-feature-flags.md) | Client-side resolution + 60 s TTL cache; Ed25519 signature verification. |
| 16.7 | [API: telemetry sink](../../../specs/epics/16-subscriptions/story-16-07-telemetry-api.md) | [plan-16-07](../../../specs/epics/16-subscriptions/plan-16-07-telemetry-api.md) | Allow-list filter; library-root redaction; rate-limited 1k events/min/IP; 90 d / 30 d retention. |
| 16.8 | [API: feature-flag resolution](../../../specs/epics/16-subscriptions/story-16-08-feature-flags-api.md) | [plan-16-08](../../../specs/epics/16-subscriptions/plan-16-08-feature-flags-api.md) | Layered resolver (defaults → tier → cohort → user); Ed25519 signing; 60 s LRU cache; LISTEN-based invalidation. |

## Key technical decisions

- **Ed25519 license keys.** License JSON is canonicalized (RFC 8785 JCS) and signed by the license server's Ed25519 key. Public key bundled at build time in `keys/license-server.pub.pem`. Distinct from Epic 10 Story 10.6 (RS256/JWKS for short-lived API JWTs).
- **Sentinel UUID for single-user mode:** `00000000-0000-0000-0000-000000000001`. Reuses Epic 10 Story 10.9 bootstrap-token infrastructure; no schema changes between single-user and multi-user.
- **Strictly opt-in telemetry.** No PII (filenames, transcripts, search queries) ever collected. Device pseudonym is per-device, never linked to `user_id`. Server-side kill-switch: `[telemetry] enabled = false` silently discards.
- **License keys never logged.** Stored encrypted-at-rest in `licenses.raw_sealed` (using Epic 10 Story 10.14 data key). Never returned in any API response; UI is write-only after submission.
- **Tier grace period (30 days)** when downgrading: premium-only features become read-only; mutations blocked with `403 tier-grace-readonly`. After grace, UI hides entirely.
- **Offline-tolerant license validation.** Refresher runs daily; 30-day offline grace before features lock. Local signature check (no network) means free tier operates indefinitely offline.
- **Feature-flag precedence** (later wins): Defaults < Tier overrides < Cohort overrides < User overrides.
- **Backup encryption.** AES-256-GCM, key derived from `HKDF(passphrase ‖ license_id ‖ "backup")`. Loss of either passphrase or license file = loss of backup.

## API endpoints

- `POST /api/billing/portal-session`, `POST /api/billing/webhook`, `POST /api/billing/cancel` (Story 16.3)
- `POST /api/admin/license`, `DELETE /api/admin/license`, `GET /api/admin/license` (Story 16.4)
- `POST /api/telemetry`, `POST /api/telemetry/web-vitals`, `DELETE /api/telemetry/devices/{pseudonym}` (Story 16.5/16.7)
- `GET /api/me/flags`, `POST /api/admin/flags/overrides` (+ PATCH/DELETE), `GET /api/admin/flags`, `POST /api/admin/cohorts/{cohort}/users`, `DELETE /api/admin/cohorts/{cohort}/users/{user_id}`, `POST /api/me/cohorts` (Story 16.6/16.8)

## Migrations claimed by this epic

| Slot | Plan | Tables / changes |
|------|------|------------------|
| `0060` | plan-16-02 | Tier matrix support; `users.tier` flag, seat-cap enforcement helpers. |
| `0061` | plan-16-02 | `tier_grace(started_at, prev_tier)` for downgrade grace tracking. |
| `0062` | plan-16-03 | `billing_customers`, `billing_subscriptions`, `billing_invoices`, `billing_webhook_events`. |
| `0063` | plan-16-04 | `licenses(raw_sealed, license_id, tier, seats, expires_at, version)`, `license_revocations(license_id, revoked_at)`. |
| `0064` | plan-16-07 | `telemetry_events`, `telemetry_web_vitals`. |
| `0065` | plan-16-08 | `feature_flag_overrides`, `beta_cohorts(user_id, cohort, joined_at)`. |

## Dependencies

- **Epic 10** Story 10.6 (signing keys; reused conceptually for license Ed25519), 10.9 (sentinel UUID bootstrap token), 10.14 (data encryption key for `licenses.raw_sealed`), 10.18 (Ed25519 long-term server identity).
- **Epic 21** Story 21.6 (audit log) — every subscription state change writes an audit row; Story 21.8 (privacy policy is canonical).
- **Epic 23** — payment endpoints rate-limited and audited.

## Related mockups

`web/mockups/admin/subscriptions.html`, `subscription-states.html` (commit `0253e38`).

## Open questions

1. **Subscription pricing.** Stories 16.x are tier-shaped but intentionally avoid prices.
2. **Cloud relay vendor.** Self-hosted (Cloudflare Tunnel-style), first-party, or hybrid? Affects Story 15.2 SLA and Story 16.2 quotas.

## Out of scope

- In-app purchases through Apple/Google billing (15-30 % cut); v1 routes through Stripe Customer Portal.
- Per-feature usage-based billing (e.g., "$X per transcribed hour"); v1 is flat-tier only.

## See also

- [Epic 15 — Discovery & Networking](epic-15-discovery.md) (relay & federation are tier-gated).
- [Epic 21 — Observability](epic-21-observability.md) (audit log, telemetry privacy).
- [Glossary](../glossary.md) — license key, tier, seat cap, offline grace, tier grace, device pseudonym, feature flag, cohort.
