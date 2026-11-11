# Story 57.09 — Screen Time, Family Sharing and the App Store

> Epic 57 · Phase 3 · Depends on: 57.04 · Needed by: —

## Why

Time limits, downtime, bedtime, app limits and Always Allowed are Screen Time's, set by a parent
in Settings for a Family Sharing child. Maktaba does not rebuild them. The home keeps its own
time rules for every device (57.14), and 57.16 carries those to Screen Time where Apple allows
(README decision 1). This story's job is to behave well when Screen Time stops it, to be honest in its App Store age rating so Apple's
age gate works, and to tell parents in Help how to use Apple's controls with Maktaba.

## Description

### 1. Under a shield

When downtime starts or an app limit is reached, iOS shields the Player. The Player must:

- save the playback position at once. The Player posts progress every 10 s and on pause; it also
  posts on `scenePhase` leaving `.active` and on `AVPlayer` interruption (audit and fix any path
  that skips it);
- pause playback, and end Picture in Picture and background audio when the scene goes to the
  background because of a shield. iOS ends them for a shielded app; the Player must not restart
  them on its own (no auto-resume on `.active` without a tap);
- on return, show the video page with Resume at the saved position.

AirPlay is the one residual (README §7 A21): an Apple TV receiving an AirPlay stream keeps playing
the URL it was given until it expires (≤ 15 minutes for a restricted sign-in, 57.03). Help says to
set Screen Time on the Apple TV too.

### 2. Nothing Maktaba asks of Screen Time

- No Family Controls authorization, DeviceActivity or shields in this story. Story 57.16 asks
  Apple for `com.apple.developer.family-controls` and uses them for the home's time rules only
  (README decision 6). Each extension needs its own approval.
- No StoreKit and nothing for sale in the iOS app, so Ask to Buy applies only to installing the
  free app. If a later epic sells anything in the app, it must handle StoreKit's
  `Product.PurchaseResult.pending` (an Ask to Buy waiting for a parent) and approvals arriving
  on `Transaction.updates`.

### 3. The App Store age rating

App Store Connect is set by hand (`docs/testflight.md`). With this epic's release, the owner's
release checklist in `docs/testflight.md` gains:

- keep the current age rating (Q5);
- in the questionnaire's In-App Controls, answer **Parental Controls: Yes** and **Age Assurance:
  Yes** (Declared Age Range);
- note that an age-rating change counts as a significant change under the Texas law's tools
  (`AppStore.ageRatingCode`, PermissionKit `SignificantAppUpdateTopic`), so the rating should not
  change with this release.

### 4. Help, in English and Arabic

A new section in `web/src/help/content/en/apple-apps.md` and its Arabic twin
(`web/src/help/content/ar/apple-apps.md`), also shown in the Player's Help: **Parental controls
on iPhone and iPad**:

1. Make your child's account restricted in Maktaba (Profile > Family), or ask an admin.
2. In Settings > Family (or Screen Time on the child's device): set Downtime and App Limits for
   Maktaba; set Content & Privacy Restrictions > TV Shows and Movies for your region; choose
   whether to share your child's age range with apps.
3. Maktaba hides content above the lower of the account's limit and what the device says.
4. Set Screen Time on your Apple TV too, if your child uses one.

## Acceptance criteria

- **Given** playback at 12:00, **when** the scene leaves `.active` (the UI test's stand-in for a
  shield), **then** progress at 12:00 ±1 s is posted, playback pauses, PiP ends, and nothing
  resumes until a tap.
- **Given** the release checklist, **then** it lists the questionnaire answers and the age-rating
  rule.
- **Given** Help in Arabic, **then** the section exists, mirrored, with the same steps.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | MaktabaKit XCTest | player model playing | scene → background | progress posted; paused |
| T02 | Player UI test (iPhone sim) | play, then `XCUIDevice.shared.press(.home)` | reopen | Resume at the saved position; not auto-playing |
| T03 | Help test (web's help content check) | en and ar `apple-apps.md` | build | section present in both |
| T04 | manual, real iPad in a test family (with 57.04 T09) | Downtime in 1 minute | watch | shield; position saved; resume works |

## Strings

| Key | English | Arabic |
|---|---|---|
| `help.parental.title` | Parental controls on iPhone and iPad | الرقابة الأبوية على iPhone وiPad |
| `help.parental.screenTime` | Screen Time sets how long and when. Maktaba sets what. | تحدد «مدة استخدام الجهاز» المدة والوقت، ويحدد Maktaba المحتوى. |
| `watch.resume.afterPause` | Resume from %@ | استئناف من %@ |

## Edge cases

- **Always Allowed** set for Maktaba by a parent: Screen Time does not stop it; Maktaba's own
  limits still apply.
- **Communication limits** (`activeParentalControls`): Maktaba has no messaging, so nothing to do.

## Build plan

| File | Change |
|---|---|
| `apps/apple/Player/Watch/` (player model, PiP) | save and stop on background; no auto-resume |
| `docs/testflight.md` | the checklist lines |
| `web/src/help/content/{en,ar}/apple-apps.md` | the Help section |
| `apps/apple/Player/Watch/…xcstrings` | strings |
