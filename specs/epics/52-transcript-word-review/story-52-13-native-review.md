# Story 52.13 — Review in the native apps

> Epic 52 · Phase 4 · Depends on: 52.8, 52.9, 52.11 (its wording) · Needed by: —

## Why

Reviewing a word is a short task: listen, tap. It fits a phone or a tablet on the sofa better than
a desk. The Remote apps are the native apps for the people who manage the home (trail §2, "Product
shape"). They get the same queue, with the same rules, enforced by the same API.

## Description

### 1. Where

- **Apple:** the Remote app (`apps/apple/Remote`) on iPhone, iPad and Mac. It gets a **Word
  review** screen in its navigation, shown only when `GET /api/review/summary` is non-empty for
  the signed-in user.
- **Android:** the Remote app (`apps/android/remote`) on phone, foldable and tablet. Same entry,
  same rule.
- **Player builds never show it.** They are viewer-only (trail §2), and the API would refuse them
  anyway.
- TV apps get nothing.

### 2. Screens

- **List:** open flags by priority (the word, reason labels, video title), filters for library,
  language and reason, and pull to refresh.
- **Word:** the sentence with the word highlighted (bold and underlined, not colour alone), the
  sentence before and after, and the clip auto-playing.
  - Apple plays through `AVPlayer`, Android through Media3 `ExoPlayer`, on the signed URL from
    52.8.
  - Play/pause, 0.75×, replay. Candidate buttons 1-5, an edit field, and Accept, Correct, Name,
    Dictionary, Skip.
  - "In N other places" with Apply to all, and a confirm that shows the count.
- **Gestures:** swipe left/right for next/previous (mirrored in RTL), long-press a candidate to
  hear the clip again. After an action, a snackbar or toast with Undo shows for 6 s.
- **Hardware keyboard** (iPad, Mac, Android tablets with a keyboard): the same shortcuts as the web
  (52.11 §3), declared as `UIKeyCommand`s with discoverability titles on Apple, and handled with
  `onKeyEvent` on Android.

### 3. API client

- **Apple:** `apps/apple/MaktabaKit/Sources/MaktabaKit/Review.swift`, in the style of
  `Browse.swift`: async functions for each 52.8 route, `Codable` models, and reason and status as
  enums with an `unknown` case.
- **Android:** `apps/android/client/src/main/kotlin/.../core/api/Review.kt`, in the style of
  `Browse.kt`, with sealed reason and status classes with `Unknown`.
- Both prefetch the next 3 flags and their clip URLs.

### 4. Strings and direction

- Every string is in English and Arabic in the same PR:
  - Apple: `apps/apple/Remote/Localizable.xcstrings` (screens) and
    `apps/apple/MaktabaKit/Sources/MaktabaKit/Localizable.xcstrings` (reason and source names).
  - Android: `apps/android/remote/src/main/res/values/strings.xml` +
    `values-ar/strings.xml` (and `core` for shared ones).
  - The wording is the web's (`review.*`, 52.11).
- The chrome follows the app language. The sentence, candidates and field follow the transcript's
  direction (`direction` from the flag's language): `.environment(\.layoutDirection)` on Apple,
  `CompositionLocalProvider(LocalLayoutDirection)` on Android.
- Arabic plurals use the platforms' plural rules (String Catalog variations,
  `<plurals>` in Android).

## Acceptance criteria

1. **Given** a reviewer signed in to the Remote app, **then** Word review is in the navigation
   with the open count. **Given** a viewer, **then** it is not. **Given** a Player build, **then**
   it never is.
2. **Given** the word screen, **when** it opens, **then** the clip plays the word with 1.5 s either
   side, and the highlighted word is readable in both themes.
3. **Given** a candidate tapped, **then** the word is corrected, a toast with Undo shows, and the
   next word opens. **When** Undo is tapped, **then** the edit is undone.
4. **Given** the Arabic app language, **then** the screens are right to left, the swipe directions
   are mirrored, and every string is Arabic.
5. **Given** an English transcript in the Arabic app (or the reverse), **then** the sentence reads
   in the transcript's direction.
6. **Given** an iPad with a keyboard, **when** the reviewer holds ⌘, **then** the shortcut list
   shows, and `J`, `K`, `Space`, `1`-`5`, `N`, `D`, `S` and `U` work as on the web.
7. **Given** the API answers 403 (the user lost `write`), **then** the screen says so and returns
   to the list. A 409 (locked or stale) shows the reason and reloads the word.
8. **Given** a CI run, **then** the missing-Arabic tests on both platforms fail if a review string
   has no Arabic.

## Build plan

### Files

- Apple:
  - `apps/apple/Remote/Review/ReviewListScreen.swift`, `ReviewWordScreen.swift`,
    `ClipPlayer.swift`, `ReviewKeyCommands.swift`.
  - `apps/apple/MaktabaKit/Sources/MaktabaKit/Review.swift`.
  - Both `Localizable.xcstrings`.
  - `apps/apple/project.yml` (new files).
- Android:
  - `apps/android/remote/src/main/kotlin/com/hamzalabs/maktaba/remote/review/ReviewListScreen.kt`,
    `ReviewWordScreen.kt`, `ClipPlayer.kt`.
  - `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/api/Review.kt`.
  - `values/strings.xml`, `values-ar/strings.xml`.

### Tests

- Apple unit tests (MaktabaKit): decoding every route's JSON, unknown reasons, and prefetch.
- Apple UI tests (`apps/apple/UITests`): list → word → candidate → undo, in the Arabic language,
  on a **throwaway simulator** created and deleted per run. They run in CI only (no local Mac UI
  tests), against the branch stack with seeded flags.
- Android: unit tests for `Review.kt` decoding, and Robolectric/Compose UI tests for the screens
  in `en` and `ar` (RTL), with Robolectric logging to stdout kept in the CI report.
- Missing-Arabic checks: the existing String Catalog and `values-ar` tests cover the new keys.
- Screenshots in the dark theme in the CI report.

### Rollout

The next app release after phase 2 is live: TestFlight (every tag uploads the Remote) and Android
builds. Nothing on the server changes.

## Edge cases

- **The app goes to the background mid-clip:** the clip stops, and the lock expires by itself
  (52.8).
- **Offline:** the list shows its last copy, read-only, with "Offline: reconnect to review".
- **A cloud-linked sign-in:** play-scoped, so the API returns 403. The entry is hidden, because
  the summary is empty.
- **A small phone:** the context shrinks to the sentence, with the neighbours behind a "More"
  control.
