# Story 16.6 — Feature flags per tier (client surface)

A feature-flag layer that enables / disables UI elements based on the
license tier and per-user roles. The server-side flag-resolution
endpoint is owned by
[Story 16.8](story-16-08-feature-flags-api.md); this story owns the
client consumption surface.

**Anchors:** [`architecture.md` §10.4](../../architecture.md). Depends
on [Story 16.8](story-16-08-feature-flags-api.md).

## AC

- Server returns `GET /api/me/flags` (owned by Story 16.8) with the
  resolved flag set for the current user (tier, role, beta-cohorts).
- Client respects flags: gated UI is hidden, not disabled with a
  paywall, except for clearly opt-in upgrade affordances.
- Flags are cached for 60 s; refreshed on app foreground.
- Beta flags: opt-in via Settings → Advanced → Experiments; documented
  as unstable.

## TC

- Tier flips premium → free mid-session: gated UI disappears on next
  flag refresh.
- A beta flag is rolled back server-side: client picks it up within 60 s.
- A user with role "admin" sees admin-only flags; a regular user does
  not.

## EC

- Flag refresh fails: use the cached set; never enable a flag that
  failed validation.
- Conflicting flags (`relay = true`, `quota = 0`): UI shows feature but
  uses 0 quota; server rejects use.
- Tampering with the local cache: flags are signed by the server and
  re-checked on every privileged action.
