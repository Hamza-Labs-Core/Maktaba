# Story 16.1 — Free tier (local only, single user)

The free tier is the canonical product: full library, full streaming,
full search, full clients, single user. No nag screens, no expiring
features.

**Anchors:** [`architecture.md` §10.4](../../architecture.md), §9.8.

## AC

- All Epics 1–15 features work without a license key.
- "Get Premium" entry point exists in Settings but is unobtrusive (no
  modal nags).
- Single-user mode: bootstrap admin token
  ([architecture.md §9.8](../../architecture.md)); no user
  table required. The synthetic admin's `user_id` equals the sentinel
  UUID `00000000-0000-0000-0000-000000000001` (resolves
  [REVIEW §2.4.b](../../REVIEW.md)).
- LAN-only: cloud relay disabled; multi-user disabled; cloud metadata
  backup disabled.
- License-server unavailable: free tier is unaffected.

## TC

- Fresh install with no license key: every documented feature works.
- Disconnect the license server: free tier features remain working.
- Open a UI element gated to premium: it's hidden, not just disabled.
- Single-user admin makes a watch-progress write: the
  `playback_state.user_id` is the sentinel UUID.

## EC

- A user accidentally entered then removed a premium key: free tier
  resumes; no data loss.
- Migrating from premium back to free: any premium-only data (analytics
  history beyond 30 d) is preserved server-side but read-only.

## Implementation (2026-10-26)

**Status: done.** See [`docs/licensing.md`](../../../docs/licensing.md).

- Free is the default with no license and no license server: every Epic 1–15
  feature works; a home that cannot reach the license server, or a build without
  the license key, stays on Free unaffected (`subscriptions.Store`, nil = Free).
- Single user: `POST /api/users` and open registration after the first user are
  refused on Free with a plain reason (`seat-limit`). Users that already exist
  keep working (homes whose users predate the gate, or that went back to Free).
- LAN-only: every request the cloud relay forwards is refused on Free
  (`subscriptions.RelayGate`, health probes excepted); cloud backup and federation
  are behind `subscriptions.RequireFeature`.
- "Get Premium": Settings → Subscription shows **Get a plan** (a link to the cloud
  billing page) and the plan's features; no modal, no nag.
- Removing a license returns to Free; nothing is deleted. Analytics history stays
  in the database; on Free the dashboard reads the last 30 days.
- Not changed here: the bootstrap-admin sentinel user id of the AC predates this
  work (Epic 10) and is unchanged.
