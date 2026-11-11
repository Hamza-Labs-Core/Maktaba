# Story 57.04 — The device's signal: Declared Age Range and the Screen Time rating limit

> Epic 57 · Phase 2 (API), 3 (iOS) · Depends on: 57.01, 57.02, 57.03 · Needed by: 57.05, 57.11, 57.12

## Why

An account cannot know which device it is on. If a parent signs into their own account on a
child's iPad, or an older sibling's account is used there, the account's policy says nothing.
Apple knows: the guardian may share the child's age range (Declared Age Range, iOS 26+), and
Screen Time holds the movie and TV limit the parent set for that device (readable without any
authorization through `ManagedSettingsStore`). The Player should pass both to the home, and the
home should use them to tighten, never to loosen.

## Description

### 1. On the iPhone and iPad

`MaktabaKit/AgeSignal.swift` (new), behind a protocol `AgeSignalSource` so tests use a double:

- **Age range**: `@Environment(\.requestAgeRange)` with gates 9, 13, 18, asked once per sign-in
  (and at first launch where `isEligibleForAgeFeatures` is true). The answer is kept in memory and
  sent. A `.declinedSharing` or `notAvailable` sends no range. The app never asks again on its own
  for the same sign-in, unless the person opens Profile > Age range (a button that asks again).
- **Rating limit**: `ManagedSettingsStore().effectiveMaximumMovieRating` and
  `effectiveMaximumTVShowRating`, read at sign-in and on every return to the foreground
  (`scenePhase == .active`). Watching for changes needs the Family Controls entitlement, which
  only 57.16 asks for (README decision 6); this story does not depend on it.
- **Region**: `Locale.current.region` is sent so the home picks the right table for the 0–1000
  values; the US anchors apply when the region has no table.
- **Sending**: `POST /api/me/age-signal` whenever any value differs from what was last sent for
  this sign-in, then a token refresh so the next access token carries the new `ac` claim.
- **Entitlement**: `com.apple.developer.declared-age-range` in new `Player-iPhone.entitlements` and
  `Player-iPad.entitlements` (XcodeGen `project.yml`). It is a capability with no approval form.

### 2. On the home

`POST /api/me/age-signal` `{age_range?: {lower, upper, declaration}, movie_ceiling?, tv_ceiling?,
region}`:

1. Map each to a tier limit (rounded down, 57.01): the age range by README §6.3 (`upper` ≤ 9:
   0; ≤ 13: 9; ≤ 18: 13; null upper: none), each ceiling by `apple-movie` / `apple-tv` with the
   region's table.
2. `tier_limit` = the strictest of the three.
3. Store it on `session_age_signals` for the caller's **refresh-token family**:
   - first signal, or stricter than the stored one: applied at once;
   - looser than the stored one: kept in `pending_looser`, not applied, and a `signal` event is
     written for the child's guardians (57.08) if the account is restricted. For an unrestricted
     account, the admins see it.
4. Answer `{tier_limit, refresh: true}`. The refresh mints `ac = tier_limit` into the access token.

`POST /api/users/{id}/parental/signals/{family}/clear` (admin or guardian) applies
`pending_looser`. Signing in afresh with the account's password starts a new family with no
stored signal.

A sign-in that has never sent a signal has none (`ac` absent). The home does not require one: the
account policy is the floor (README decision 4).

### 3. What "tighten only" protects

- A modified app that sends "adult" or nothing gains nothing beyond its account's own policy.
- A child who turns age sharing off in Settings sends nothing new, and the stored limit stays.
- A child who reinstalls the app must sign in again, which needs the account's password or a
  guardian's approval (57.02 §4).

### 4. The real-device check (before the Phase 3 release tag)

Apple's documentation does not say whether `effectiveMaximum…Rating` reflects the parent's own
Content & Privacy Restrictions or only a Screen Time API app's settings. In a test Family Sharing
group with a child account on a real iPad:

1. Set Content & Privacy Restrictions > TV Shows to TV-PG (Ratings For: United States).
2. Open the Player's hidden diagnostics line (Profile > About, five taps) and read the values.
3. Record the result in the release's notes and in research.md (append a dated line).

If the values stay 1000, the Player keeps sending them (they are harmless) and Help (57.09) says
that the account's policy and the age range are what limit Maktaba.

## Acceptance criteria

- **Given** a test double sharing a guardian-declared range 9–12, **when** the Player signs in,
  **then** it posts the range, refreshes, and the home limits that sign-in to 9+ even on an
  unrestricted account.
- **Given** a TV limit of 400 (TV-PG) and region US, **then** the limit is 9+.
- **Given** a stored 9+ limit, **when** a later signal says 18+, **then** 9+ stays, `pending_looser`
  holds the new one, and a guardian's clear applies it.
- **Given** a restricted 13+ account and a device limit of 9+, **then** 9+; with a device limit of
  16+, **then** 13+.
- **Given** declined sharing and no Screen Time limit, **then** no `ac` claim, and the account's
  policy alone applies.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | MaktabaKit XCTest | double: each range and declaration | build signal | posted values |
| T02 | MaktabaKit XCTest | ceiling changes between foregrounds | `scenePhase` active | one post, then refresh |
| T03 | Go unit | ranges, ceilings, regions | map | README §6.3 and 57.01 limits |
| T04 | Go + Postgres | strictest of three | POST | stored limit; `ac` on refresh |
| T05 | Go unit | adult signal on a 9+ account; no signal | effective | 9+ both |
| T06 | Go + Postgres | looser after stricter | POST; clear by guardian; clear by child | pending; applied; 403 |
| T07 | Go + Postgres | new sign-in after reinstall | POST | new family, fresh signal |
| T08 | Player UI test (throwaway iPhone sim, en and ar) | double 9–12 | sign in to an unrestricted fixture account | 16+ fixture absent on Home |
| T09 | manual, real iPad in a test family | §4 | read values | recorded before the tag |

## Strings

| Key | English | Arabic |
|---|---|---|
| `ageSignal.profile.title` | Age range | الفئة العمرية |
| `ageSignal.profile.detail` | Shared by Apple. Maktaba uses it to hide content above this age on this device. | تشاركها Apple. يستخدمها Maktaba لإخفاء المحتوى الذي يتجاوز هذا العمر على هذا الجهاز. |
| `ageSignal.profile.notShared` | Not shared | لم تتم المشاركة |
| `ageSignal.profile.askAgain` | Share age range again | مشاركة الفئة العمرية مجددًا |
| `ageSignal.limit.device` | This device is limited to %@. | هذا الجهاز مقيَّد بـ %@. |
| `ageSignal.limit.pending` | A looser limit is waiting for a parent to approve it. | ينتظر حدٌّ أوسع موافقة أحد الوالدين. |

## Edge cases

- **The home is older than this story**: `POST /api/me/age-signal` answers 404; the Player stops
  sending for that home and shows nothing new.
- **Two homes**: signals are per home and per sign-in.
- **An Apple Account that is not in a family**: the range is self-declared or absent; a
  self-declared minor still tightens.
- **Region with no table** (UAE's catalogue lists only UR): the US anchors apply to the 0–1000
  values, as Apple's scale is defined by them.

## Build plan

| File | Change |
|---|---|
| `apps/apple/MaktabaKit/Sources/MaktabaKit/AgeSignal.swift` (new) | source protocol, Apple and double |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/Session.swift` | post on sign-in and foreground; refresh |
| `apps/apple/Player/Player-iPhone.entitlements`, `Player-iPad.entitlements` (new), `project.yml` | the capability |
| `apps/apple/Player/Profile/AgeRangeRow.swift` (new) + `Profile.xcstrings` | the Profile row |
| `api/internal/handlers/parental/signal.go` (new), tests | route, mapping, ratchet |
| `api/internal/auth/jwt/jwt.go`, `handlers/auth/auth.go` | `ac` claim at refresh |
