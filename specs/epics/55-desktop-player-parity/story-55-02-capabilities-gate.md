# Story 55.02 — The capabilities gate and its build switch (#638)

> Epic 55 · Phase 1 · Depends on: 55.01 · Needed by: 55.03, 55.04, 55.06, 55.07, 55.08

## Why

Story 51.11 made every app read `GET /api/capabilities`: a feature that is off is hidden from a
viewer and explained to an admin with a way to turn it on. The Android Player can switch the
gate off with a build property (`-Pmaktaba.capabilitiesGate=off`, kept in
`BuildConfig.CAPABILITIES_GATE`). The desktop Player makes its `AppSession` with the gate on,
never follows capabilities and has no switch: issue #638. This story gives it the same switch,
follows capabilities while signed in, and gives every area one way to hide or explain a feature.

## Description

### 1. The build switch

- `apps/desktop-player/build.gradle.kts`: the property `maktaba.capabilitiesGate` (letters and
  digits only, else the build fails) is written into the generated `BuildInfo` as
  `CAPABILITIES_GATE`, next to `VERSION` and `UPDATER_PUBKEY`, and is an input of
  `writeBuildInfo` so a change rebuilds it.
- `buildCapabilitiesGate(setting = BuildInfo.CAPABILITIES_GATE)` calls `:client`'s
  `capabilitiesGate()`: "off", "no", "false" or "0" turn it off; anything else, or nothing,
  leaves it on.
- `playerSession(settings, scope, tokens)` makes the `AppSession` with
  `capabilitiesGate = buildCapabilitiesGate()`. `main()` uses it.
- Release builds pass nothing, so the gate is on; `desktop-release.yml` does not change.

### 2. Following capabilities

While a home is signed in, the window runs `session.followCapabilities()` (asked at sign-in,
then every five minutes with the ETag), as Android does from `ON_START` to `ON_STOP`. The
desktop window has no background state, so it follows for as long as the window is open and
signed in. A failed answer keeps the last good one; before the first answer every feature is on.

### 3. Hiding and explaining

- `Section.shown(features)`: Live TV is left off the rail when `features.hides("live_tv")`.
  `Navigator.follow(features)` moves a viewer showing Live TV to Home when it goes off.
- `FeatureOffNote(feature, home.webUrl)`: the sentence and a "Turn it on in Components" button
  that opens `<home>/settings/components` in the system browser. Areas show it where an off
  feature would be, for an admin only.
- `Texts.featureOffSentence(f)` and `featureUnavailableSentence(f)`: the feature's own
  sentence, else the general one with its id.
- Which places each story gates: Home's recommendations (55.03), search by meaning and
  transcripts (55.04), chapters (55.06), Live TV (55.07), subtitles (55.08).

### 4. Words

| Key | English | Arabic |
|---|---|---|
| `feature.offAdmin` | The {0} feature is off on this home. | ميزة {0} متوقفة على الجهاز الرئيسي. |
| `feature.unavailable` | The {0} feature isn't available on this home. | ميزة {0} غير متاحة على الجهاز الرئيسي. |
| `feature.turnOn` | Turn it on in Components | شغّل من صفحة المكوّنات |
| `feature.off.live_tv` | Live TV is off on this home. | البث المباشر متوقف على الجهاز الرئيسي. |
| `feature.off.transcripts` | Transcripts are off on this home. | نصوص الفيديو متوقفة على الجهاز الرئيسي. |
| `feature.off.subtitles` | Subtitles are off on this home. | الترجمة متوقفة على الجهاز الرئيسي. |
| `feature.off.semantic_search` | Search by meaning is off on this home. | البحث بالمعنى متوقف على الجهاز الرئيسي. |
| `feature.off.chapters` | Chapters are off on this home. | الفصول متوقفة على الجهاز الرئيسي. |
| `feature.off.recommendations` | Recommendations are off on this home. | الاقتراحات متوقفة على الجهاز الرئيسي. |
| `feature.unavailable.live_tv` | Live TV isn't available on this home. | البث المباشر غير متاح على الجهاز الرئيسي. |

…and the same `feature.off.*` and `feature.unavailable.*` pair for `youtube`, `plex` and
`remote_access`, with the Android core's wording (`apps/android/core/src/main/res/values{,-ar}/strings.xml`,
`feature_off_*`, `feature_unavailable_*`).

## Acceptance criteria

- **Given** a build made without the property, **when** the session is made, **then** its gate
  is on.
- **Given** `-Pmaktaba.capabilitiesGate=off`, **when** the Player signs in to a home with Live
  TV off, **then** the Live TV section shows and its calls get the home's `409 component_off`.
- **Given** the gate on, Live TV off and a viewer, **when** the window shows, **then** the rail
  has no Live TV.
- **Given** the same home and an admin, **when** they open Live TV, **then** they read "Live TV
  is off on this home." and a button that opens the Components page in the browser.
- **Given** a viewer on Live TV, **when** an admin turns it off, **then** within five minutes
  the rail drops it and the window shows Home.
- **Given** the home does not answer `GET /api/capabilities`, **when** the Player starts,
  **then** every section shows as before.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `CapabilitiesGateTest` | `playerSession(…)` | `session.capabilitiesGate == buildCapabilitiesGate(BuildInfo.CAPABILITIES_GATE)`; unset is on (pins the default, #638) |
| T02 | unit | `buildCapabilitiesGate` | "", "on", "off", "false" | on, on, off, off |
| T03 | unit | `Section.shown` | `live_tv` off, viewer / admin | hidden / shown |
| T04 | unit | `Navigator.follow` | viewer on Live TV, then off | Home |
| T05 | screen | `FeatureOffNote`, `en` and `ar` | compose | sentence and button in each language |
| T06 | unit | `StringsTest` | every known feature, both languages | an off and an unavailable sentence, never the key |
| T07 | build | `./gradlew :test -Pmaktaba.capabilitiesGate=off` (CI step) | T01 | the session's gate is off |

## Edge cases

- **A cloud-linked play sign-in** on an admin's account gets no `admin` block, so it is a
  viewer: features are hidden, not explained.
- **A feature this app does not know** (a newer home) is ignored; one the answer does not name
  (an older home) is on.
- **Switching home** forgets the capabilities (`AppSession.forgetCapabilities`) and asks the
  new home.

## Build plan

| File | Change |
|---|---|
| `apps/desktop-player/build.gradle.kts` | `maktaba.capabilitiesGate` → `BuildInfo.CAPABILITIES_GATE`. |
| `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/Main.kt` | `buildCapabilitiesGate`, `playerSession`, `followCapabilities` while signed in. |
| `.../desktop/shell/Navigator.kt` | `Section.shown`, `Navigator.follow`. |
| `.../desktop/shell/FeatureOff.kt` (new) | `FeatureOffNote`, the sentences, `openInBrowser`. |
| `.../desktop/shell/ShellStrings.kt` | §4 words. |
| `apps/desktop-player/src/test/.../CapabilitiesGateTest.kt` (new), `shell/FeatureOffNoteTest.kt` (new), `shell/NavigatorTest.kt`, `StringsTest.kt` | T01–T06. |
| `.github/workflows/_desktop-player.yml` | T07: one extra `:test --tests '*CapabilitiesGateTest*' -Pmaktaba.capabilitiesGate=off` on Linux. |
| `apps/desktop-player/README.md` | The switch, as the Android README says it. |
