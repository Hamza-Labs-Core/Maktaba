# Epic 16 — Subscriptions & Monetization (Optional)

**Goal.** Maktaba is fully usable for free as a self-hosted single-user
product. Optional premium features (cloud relay quota, multi-user
seats, cloud metadata backup, advanced analytics) are gated by a
license key and validated against a license server. All paid features
are server-side gates; the client surface only enables UI.

**Anchors:** [`architecture.md` §10.4](../../architecture.md) (cost
control), §11.5 (secrets).

---

## Stories

| # | Story | Status |
|---|-------|--------|
| 16.1 | [Free tier (local only, single user)](story-16-01-free-tier.md) | spec |
| 16.2 | [Premium features](story-16-02-premium-features.md) | spec |
| 16.3 | [Subscription management](story-16-03-subscription-management.md) | spec |
| 16.4 | [License key validation](story-16-04-license-validation.md) | spec |
| 16.5 | [Usage analytics (opt-in)](story-16-05-telemetry-opt-in.md) | spec |
| 16.6 | [Feature flags per tier](story-16-06-feature-flags.md) | spec |
| 16.7 | [API: telemetry sink](story-16-07-telemetry-api.md) | spec (added per REVIEW §3.2) |
| 16.8 | [API: feature-flag resolution](story-16-08-feature-flags-api.md) | spec (added per REVIEW §3.2) |

---

## Dependencies

- **Epic 10** (Auth) Story 10.6 (signing keys; license keys reuse the
  Ed25519 infrastructure).
- **Epic 23** (Security hardening) — payment-related endpoints are
  rate-limited and audited.
- **Epic 21** (Observability) Story 21.6 (audit_log) — every
  subscription state change writes an audit row.

## Cross-cutting checklist

- **Optionality:** every story in this epic is optional from the
  product's point of view; the free tier
  ([Story 16.1](story-16-01-free-tier.md)) must remain unimpaired even
  if 16.2–16.8 are not deployed.
- **License keys** are never logged, never returned in API responses,
  and live in the same redaction set as PATs (Epic 21 Story 21.1).
- **Telemetry:** strictly opt-in; no PII (file names, transcripts,
  search queries) ever leaves the server.

## Open questions

1. **Subscription pricing.** Stories 16.x are tier-shaped but
   intentionally avoid prices; a product decision is required before
   the billing portal goes live.
2. **Cloud relay vendor.** Self-hosted (Cloudflare Tunnel-style),
   first-party, or hybrid? Affects
   [Story 15.2](../15-discovery/story-15-02-cloud-relay.md) SLA and
   [Story 16.2](story-16-02-premium-features.md) quotas.

## Out of scope

- In-app purchases through Apple / Google billing (they take 15-30%);
  v1 routes through Stripe Customer Portal.
- Per-feature usage-based billing (e.g., "$X per transcribed hour");
  v1 is flat-tier only.
