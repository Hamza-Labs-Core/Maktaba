# Implementation Plan — Story 16.1 Free tier (local only, single user)

> Companion to [story-16-01-free-tier.md](story-16-01-free-tier.md).
> The story states *what* and *why*; this plan states *how*.
> The free tier is the canonical product. This plan is mostly about
> defaults and absence of dark patterns.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Tier resolver default | `api/internal/license/resolver.go` — when no license is loaded, returns tier `free`. |
| Single-user mode bootstrap | Reuses Epic 10 Story 10.9 sentinel UUID `00000000-0000-0000-0000-000000000001`; this plan defines how `playback_state.user_id` and other tables resolve in single-user mode. |
| Feature defaults | Embedded in the binary's flag declarations ([Story 16.6](story-16-06-feature-flags.md) / [Story 16.8](story-16-08-feature-flags-api.md)). |
| "Get Premium" entry point | `web/src/features/settings/Subscription.tsx` and the equivalent on each native client; never modal-blocks the user. |
| Out of scope | License validation ([Story 16.4](story-16-04-license-validation.md)); feature-flag mechanics ([Story 16.6](story-16-06-feature-flags.md), [Story 16.8](story-16-08-feature-flags-api.md)); subscription management ([Story 16.3](story-16-03-subscription-management.md)). |

## 1. Behaviour specification

### 1.1 No license loaded → free tier

```go
// api/internal/license/resolver.go
type Resolver struct { store Store; pubkey ed25519.PublicKey; offlineGrace time.Duration }

func (r *Resolver) Current(ctx context.Context) (Tier, error) {
    raw, err := r.store.LoadActive(ctx)
    if errors.Is(err, ErrNoLicense) {
        return Tier{Name: TierFree, ExpiresAt: time.Time{}, Reason: "no-license"}, nil
    }
    if err != nil { return Tier{}, err }
    // ... validate signature + expiry; this is Story 16.4's territory.
    return decodeTier(raw), nil
}
```

The `free` tier is a real `Tier` value — not a `nil`, not a `null`. Downstream code never branches on "is licensed?"; it branches on `tier.Name`.

### 1.2 Free-tier feature gate set

The flag declarations (Story 16.6 / 16.8) bake in defaults per tier:

```go
// api/internal/flags/declarations.go
var Declarations = []Declaration{
    {Key: "relay",         DefaultByTier: map[string]bool{"free": false, "home": true,  "pro": true}},
    {Key: "multi_user",    DefaultByTier: map[string]bool{"free": false, "home": true,  "pro": true}},
    {Key: "backup",        DefaultByTier: map[string]bool{"free": false, "home": true,  "pro": true}},
    {Key: "analytics",     DefaultByTier: map[string]bool{"free": false, "home": true,  "pro": true}},
    {Key: "federation",    DefaultByTier: map[string]bool{"free": false, "home": false, "pro": true}},
    // Free-tier-positive defaults:
    {Key: "library",       DefaultByTier: map[string]bool{"free": true,  "home": true,  "pro": true}},
    {Key: "streaming",     DefaultByTier: map[string]bool{"free": true,  "home": true,  "pro": true}},
    {Key: "search",        DefaultByTier: map[string]bool{"free": true,  "home": true,  "pro": true}},
    {Key: "transcribe",    DefaultByTier: map[string]bool{"free": true,  "home": true,  "pro": true}},
}
```

Free-tier users have all Epic 1–15 capability flags = true.

### 1.3 Single-user mode

The story AC says: "The synthetic admin's `user_id` equals the sentinel UUID `00000000-0000-0000-0000-000000000001`."

The bootstrap admin token (per Epic 10 Story 10.9) carries `sub = sentinel_uuid` claim. Any `playback_state.user_id`, `recommendation_runs.user_id`, etc., write made via the bootstrap token is keyed to that sentinel UUID. The sentinel row exists in `users` (per Epic 10 Story 10.1 migration) with `is_admin = true`, so all FK constraints resolve.

This means the difference between single-user mode and a multi-user install where a single user is logged in is **literally zero schema changes** — single-user mode is an authentication-side decision that picks the sentinel as the implicit user. That's the design.

### 1.4 License-server unavailable

When the license server is unreachable:

- The resolver returns the cached license tier if one exists and the cached license has not expired (the offline grace lives on Story 16.4).
- If no license has ever been entered, the resolver returns `free` immediately — no network dependency.
- If a license existed but the offline-grace period has passed, the resolver returns `free` plus a notification to the admin (Story 16.4 wiring).

So even with the license server permanently down, the free tier remains fully functional. The TC: "Disconnect the license server: free tier features remain working."

## 2. UI surfacing

The story AC: "'Get Premium' entry point exists in Settings but is unobtrusive (no modal nags)."

Design:

- Settings → Subscription page renders the current tier plus a Premium card with three lines and an "Upgrade" button.
- No periodic toast, no inline banner outside that single page, no "trial expiring" countdown.
- Gated UI is **hidden**, not greyed-out (i.e., a feature flag `false` removes the menu item entirely). EC: "A user accidentally entered then removed a premium key: free tier resumes" — UI state changes follow the same visibility rule.
- The exception is the Subscription page itself, which surfaces "Premium adds X, Y, Z" descriptively.

```tsx
// web/src/features/settings/Subscription.tsx
export function SubscriptionSettings() {
    const tier = useTier();   // from useFlags()'s tier metadata
    return (
        <Section title="Subscription">
            <Row label="Plan" value={tier.displayName} />
            <Row label="Status" value={tier.statusText} />
            {tier.name === 'free' && <UpgradeCard />}
            {tier.name !== 'free' && <ManageButton />}
        </Section>
    );
}
```

## 3. Migrating premium-back-to-free

The story EC: "Migrating from premium back to free: any premium-only
data (analytics history beyond 30 d) is preserved server-side but
read-only."

The implementation:

- Premium-tier-only tables (e.g., `analytics_events_history`) are not
  deleted on tier downgrade; they're frozen.
- The `analytics` flag flips to `false`; the UI hides the panel; the
  table data remains.
- Re-applying a premium license restores access without backfill.

This requires no migration; it's a UX rule the flag system enforces.

### 3.1 Multi-user excess on downgrade

A `home`-tier server with 4 active users that downgrades to `free`
(seat cap 1) does **not** delete the extra users. The seat-cap
enforcement in plan-16-02's `seats.go` runs only on **creation** of a
new user, not retroactively. The pre-existing extra users remain
authenticatable but enter a *read-only* state per plan-16-04's
"Seats=4 but 5 users exist" EC: writes (create-collection,
delete-video, etc.) return `403 seats-exceeded-readonly`. Any user
*can* delete themselves; doing so reduces the count and unblocks
writes for the remaining users (in user-id order — the lowest-id
remaining user becomes the writable one).

This reconciles the "free tier resumes" UX statement above with the
multi-user grace path: free tier resumes for the system, but excess
users keep their data and can read it; only writes are blocked until
the seat count returns to within the cap.

## 4. Test plan

### 4.1 Resolver

| Test | What it pins |
|---|---|
| `TestResolverNoLicenseReturnsFree` | Empty `licenses` table → `Tier{Name: "free"}`. |
| `TestResolverLicenseServerDownStillReturnsCached` | Valid cached license + license server unreachable → cached tier. |
| `TestResolverLicenseServerDownNoCacheReturnsFree` | No cached license + server unreachable → `free`. |
| `TestResolverPostExpiryReturnsFree` | License `expires_at < now()` past offline grace → `free`. |

### 4.2 Single-user mode

| Test | What it pins |
|---|---|
| `TestSingleUserPlaybackPersistsAsSentinel` | Bootstrap-token playback write → `playback_state.user_id = sentinel_uuid`. |
| `TestSingleUserRecommendationsRunOnSentinel` | Compose for sentinel returns rows; cache row keyed on sentinel UUID. |
| `TestSingleUserCannotPromoteSelfBeyondAdmin` | Bootstrap token already has `is_admin = true`; no escalation needed. |

### 4.3 UI

| Test | What it pins |
|---|---|
| `testNoModalNagOnAppLaunch` | Cold-start does not surface a modal; only the bottom-of-Settings card. |
| `testGatedUIHiddenNotDisabled` | With `relay = false`, the "Remote Access" toggle is absent (not visible-but-greyed). |
| `testReplacePremiumKeyRestoresUI` | Re-apply key → UI flag flip → menus appear. |

### 4.4 End-to-end

| Test | What it pins |
|---|---|
| `e2e_FreshInstallEveryFeatureWorks` | Spin up empty DB; sign in via bootstrap token; library scan, transcribe, stream, search all green. |
| `e2e_LicenseServerDownDoesNotImpair` | Network drop the license host; e2e_FreshInstall still succeeds. |

## 5. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| User accidentally pasted then removed a premium key | Free tier resumes; no data loss; UI flips. | `testReplacePremiumKeyRestoresUI` |
| Premium-only data after downgrade | Frozen, preserved, hidden from UI; restored on re-upgrade. | `testReplacePremiumRestoresData` |
| Bootstrap admin disabled (multi-user mode forced) | Sentinel UUID still present in users; no orphan rows. Documented. | `TestSentinelStableAcrossModeFlip` |
| License server intermittently up | Resolver caches success for 24 h; transient failures don't ruin the user's evening. | `TestResolverCachesSuccess` |
| Two premium keys consecutively (revoke + new one) | Resolver uses the latest with valid signature; older is ignored. | `TestResolverPicksLatestValid` |
| Client (mobile) sees a 404 on a premium-only API | UI fails gracefully; the flag should have hidden the entry point in the first place. | `testGatedAPISurfacesEmpty` |

## 6. Acceptance checklist

**Resolver**
- [ ] No license → `free` immediately, no network dependency.
- [ ] License server down → cached tier honored.

**Single-user**
- [ ] Sentinel UUID used end-to-end.
- [ ] Bootstrap-token writes persist with sentinel as `user_id`.

**UI**
- [ ] No modal nags.
- [ ] Gated UI hidden (not greyed).

**Tests**
- [ ] All §4 tests pass.

**Docs**
- [ ] `specs/epics/16-subscriptions/README.md` ticks story 16.1.
