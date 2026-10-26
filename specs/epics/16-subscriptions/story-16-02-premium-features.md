# Story 16.2 — Premium features (remote access, multi-user, cloud backup)

Premium adds remote-access relay quota, multi-user seats, scheduled
metadata backup, and advanced analytics dashboards.

**Anchors:** [`architecture.md` §10.4](../../architecture.md). Depends
on [Story 16.4](story-16-04-license-validation.md) (license validation),
[Story 16.6](story-16-06-feature-flags.md) (gating).

## AC

- Feature flags: `relay`, `multi_user`, `backup`, `analytics`,
  `federation`. Each gated by the license tier (`free`, `home`, `pro`).
- `home` tier: relay 200 GB / mo, 4 user seats, daily backup, basic
  analytics.
- `pro` tier: relay 1 TB / mo, unlimited seats, hourly backup, advanced
  analytics, federation.
- Server enforces gates; clients only render the UI conditionally.
- Downgrading: features remain visible read-only for 30 d, then hidden.

## TC

- Apply a `home` license: 4 seats become creatable; the 5th refuses with
  a clear quota message.
- Backup runs on schedule and produces a `.maktaba-backup` archive in
  the configured destination.
- Downgrade `pro` → `home`: federation pairings remain visible but new
  ones blocked.

## EC

- License clock skew (server time vs. license expiry): grace period of
  72 h before features lock.
- License revoked due to fraud: server receives revocation list; locks
  immediately with a clear admin message.
- A user is mid-backup when license expires: that backup completes; the
  next is blocked.

## Implementation (2026-10-26)

**Status: done** for the gates; see [`docs/licensing.md`](../../../docs/licensing.md).

- Tiers free/home/pro with the spec's seats (1 / 4 / unlimited) and features
  (`cloud_relay`, `multi_user`, `cloud_metadata_backup`, `advanced_analytics`,
  `federation`); one table (`subscriptions.tierFeatures`) feeds every gate and the
  feature flags (story 16.8).
- Server-side gates: seats (admin create and open registration), the cloud relay,
  analytics beyond 30 days / per-user / export. Refusals are `403` with
  `feature`, `tier`, `required_tier` and a plain `detail`.
- Clock skew: an expired license keeps working 72 h (`SkewGrace`).
- Revocation: the signed revocation list (daily) and the cloud's `license_revoked`
  lock at once; Settings shows _revoked by Maktaba_.
- **Differences:** cloud backup (story 24.5) and federation (story 15.3) have no
  routes yet; they mount behind `RequireFeature` when they ship, so the backup
  schedule and the "federation pairings stay visible" TCs wait for them. Relay
  quotas (200 GB / 1 TB) are the cloud's bandwidth meter's (Epic 25), by cloud
  plan. The "visible read-only for 30 d after a downgrade" AC is not built: on a
  downgrade existing users keep working and paid routes refuse with the reason.
