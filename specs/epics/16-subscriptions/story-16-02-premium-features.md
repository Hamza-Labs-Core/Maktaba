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
- Expiry or downgrade (owner's decision, 2026-11-19): when a license expires or the
  home downgrades (a lower plan, or no license), the paid features keep working
  **fully** for 30 days counted from the license's expiry, then lock. Not read-only:
  everything the plan allowed keeps working. A license replaced before its expiry
  counts from when it was replaced. The home's admin sees how many days are left.

## TC

- Apply a `home` license: 4 seats become creatable; the 5th refuses with
  a clear quota message.
- Backup runs on schedule and produces a `.maktaba-backup` archive in
  the configured destination.
- Downgrade `pro` → `home`: everything Pro allowed (federation, unlimited
  seats) keeps working for 30 days from the downgrade, then only Home's remains;
  Settings → Subscription shows the days left.
- A license expires: its plan keeps working fully for 30 days from `expires_at`,
  the days left shown; on day 30 the home is on Free.

## EC

- License clock skew (server time vs. license expiry): covered by the 30-day grace
  (it replaced the 72 h this EC first gave).
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
- Expiry and downgrade grace (2026-11-19, the owner's decision): see "Grace (as
  built)" below. It replaced the 72 h clock-skew grace (`SkewGrace`).
- Revocation: the signed revocation list (daily) and the cloud's `license_revoked`
  lock at once; Settings shows _revoked by Maktaba_.
- **Differences:** cloud backup (story 24.5) and federation (story 15.3) have no
  routes yet; they mount behind `RequireFeature` when they ship, so the backup
  schedule and the "federation pairings stay visible" TCs wait for them. Relay
  quotas (200 GB / 1 TB) are the cloud's bandwidth meter's (Epic 25), by cloud
  plan.

## Grace (as built, 2026-11-19)

The owner decided that an expiry or a downgrade keeps the paid features working
**fully** for 30 days counted from the license's expiry, then locks them, and that the
home's admin sees how many days are left. That replaces the AC's "read-only for 30 d".

- `subscriptions.Grace` (30 days, `api/internal/subscriptions/store.go`):
  - **Expiry.** An applied license that expires grants its whole plan until
    `expires_at + Grace` (status `grace`), then the home is on Free (`expired`).
  - **Downgrade or removal.** A license replaced by another (a lower plan, the cloud's
    `no_license`, an admin's removal) keeps granting its plan until its end + Grace.
    Its end is its `expires_at`, or the moment it was replaced if that is earlier.
  - **The union is granted.** While a replaced license is in its grace, the home gets
    every feature of both, the most seats and the higher tier. So a Pro → Home
    downgrade keeps federation and unlimited seats for 30 days.
  - **Refreshes don't reset it.** The cloud's daily refresh replaces the license each
    day, and every license replaced less than 30 days ago counts, so a refresh never
    ends an earlier downgrade's grace.
  - **Restarts don't lose it.** The ended licenses are the `licenses` rows with a
    `revoked_at` less than Grace ago (no new table). Each is re-verified against the
    build's key at start, as the active one is.
- **Not covered.** A revocation (the revocation list, or the cloud's
  `license_revoked`: fraud) locks at once, with nothing in grace.
- **The admin's view.** `GET /api/admin/license` adds `grace_until` and
  `grace_days_left` (whole days, rounded up), and its message says how long and until
  when. Settings → Subscription shows _Keep working fully for N more days, until
  <date>_, in English and Arabic (Arabic plurals).
- **Components.** The components' licence publish (`components.Licences`, story 51.19)
  takes each flag's end from `Store.Until`, so a component the grace allows keeps
  running until the grace's end.
- **Offline homes.** A plan license lasts 31 days and is renewed daily, so a home cut
  off from the cloud keeps its plan about 61 days after its last renewal (31 + 30),
  where story 16.4 had said 35.
- **Tests.**
  - `grace_test.go`: expiry, downgrade, refreshes after a downgrade, upgrade,
    revocation, restart.
  - `TestTheAdminSeesTheGraceDaysLeft` (handler).
  - `SubscriptionSettings.test.tsx` SUB-12 and SUB-12b.
  - The existing store and refresh tests, moved to the 30 days.
