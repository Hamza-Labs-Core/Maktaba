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
