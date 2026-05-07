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
