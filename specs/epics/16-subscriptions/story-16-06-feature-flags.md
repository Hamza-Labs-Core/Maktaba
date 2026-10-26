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

## Implementation

Web only (`web/src/lib/flags.tsx`): `FlagsClient` + `FlagsProvider`
(inside `AuthProvider` in `App.tsx`), `useFlag`, `useFlags`,
`useFlagsClient` and `<Flag name>` (renders nothing while off: gated UI
is hidden, not disabled). The set is fetched from `GET /api/me/flags`
when a user signs in, kept 60 s, refreshed every 60 s and when the
window comes back to the front (focus / visibilitychange, at most every
5 s); a failed or malformed/unsigned answer keeps the last good set, and
the client starts all-off. Unknown flags and non-boolean values are
ignored. Sign-out (or another user) clears it.

Settings → Advanced → Experiments (`ExperimentsSection`) lists the
opt-in cohorts (`GET /api/me/cohorts`), says experiments are unstable,
and joins/leaves (`POST /api/me/cohorts`, `DELETE /api/me/cohorts/{c}`)
with an immediate flag refresh. Strings in `en.json` and `ar.json`.

Differences from the plan:

- **No client-side signature check on the web.** The web keeps the set
  in memory only (no disk cache to tamper with), and the key it would
  verify against comes from the same server. The bundle is still signed
  (Story 16.8, `kid` + `/api/me/flags/keys`) for clients that persist
  it; the tamper EC is met by the server: every privileged action is
  gated server-side (`Entitlements.Allows`, admin checks), whatever the
  client's flags say. `flags.Verify` (Go) is the reference check.
- No tvOS / Android clients in this story; no Zustand or zod (plain
  `useSyncExternalStore` and a small validator).
- Existing UI was not re-gated here: the subscription/billing surfaces
  are owned by a parallel story; `<Flag>` / `useFlag` are ready for them.
- An admin sees admin-only flags (`flags_admin`) because the server only
  includes them in an admin's bundle.
