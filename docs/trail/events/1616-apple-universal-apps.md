---
schema: 1
kind: pr
pr: 1616
title: "One universal Maktaba Player and one universal Maktaba Remote for iPhone and iPad"
date: 2026-11-24
session: 366f9ce3-e03b-40f4-944e-650e7dafc5b7
agent: claude-code
prs: [1616, 1574]
---

## What happened
- The Player and the Remote are each one universal app now (`TARGETED_DEVICE_FAMILY "1,2"`).
  - The iPhone bundle IDs are kept.
  - Removed: the `.tablet` targets, their UI-test bundles, their schemes and their
    `Info-iPad.plist` files.
- App Review 4.3(a) ("Don't create multiple Bundle IDs of the same app") put the separate iPad
  records at risk (`docs/apple-app-review.md`, #1574). Decision `apple-universal-apps` chose
  universal apps.
- `testflight.yml` now uploads four apps instead of six.
- The iPad UI-test shards run the universal app's `Maktaba Player UITests` on the iPad
  simulator. The iPad build leg is kept, building the same scheme, so check names stay stable
  for the tag guard and the merge gates.
- The store gallery has one listing per app (`apple-player`, `apple-remote`) carrying both the
  iPhone sets and the iPad set.
- #1574's drafts became `player/` and `remote/`.

## What we learned
- **`X-Maktaba-Client` took "ipados; tablet" from the `.tablet` bundle ID.** A universal app
  has to read the device instead.
  - It reads the model identifier: `SIMULATOR_MODEL_IDENTIFIER` on a simulator, `uname`
    `machine` on a device.
  - UIKit's idiom would tie the header's `static let` to the main actor under Swift 6.
- **The UI tests already named shots by `userInterfaceIdiom`, not by bundle ID.** So no test
  depended on the iPad target, and no test was lost when it went.
- **`render.mjs` re-renders the store gallery deterministically.** Only the four sheets whose
  titles changed differed.
- **Rewriting `stores.json` with `json.dumps` and then Prettier changed every inline object.**
  A targeted text edit kept the diff to the listings.
- **The decision was answered on the page (13:35Z) but had not synced to
  `~/.maktaba/decisions/answers/` yet.** Reading the page's `answers` collection confirmed it.

- **A new bundle ID is a new app to iOS.** The old iPad apps' saved homes, theme and playback
  settings cannot carry over, and both apps answer `maktaba://` until the old one is deleted.
  Review found neither was said; `docs/testflight.md` and the changelogs now do.
- **`ui-shard.py --measure` told iPad from iPhone by the test module's name,** which the two now
  share. It reads the job's name in the log, and refuses a log without one.

- **The Cast SDK (4.8.6) sends no Bonjour query until it has read the network's state, at
  background priority.** `-[GCKNNetworkReachability startMonitoring]` asks on the background
  queue, which gets no time on a runner with no CPU to spare. #1621's test gave the TV 60 s and
  failed on #1615, #1632, #1593 and main (#1704) while the SDK said it was searching. Measured on
  CI with the SDK's own log: the query goes out within 0.2 s of that answer, the TV is listed
  within about 2 s of the query, and the answer took 0 to 137 s. The test waits for the first
  query, and looks every 5 s rather than each second. 18 of 18 runs passed; 3 of 12 failed before.

## Keep doing
- Capture the library's own log on the machine that fails before choosing a fix. Two earlier
  guesses here (a slow resolve, another runner's beacon) were wrong.
- Read a decision's answer from the page when the local inbox says pending and the brief says
  it was answered.

## Stop doing
- Dropping a bundle ID without listing what its users lose.

## Open
- **Owner:** leave the "Maktaba Player for iPad" and "Maktaba Remote iPad" records unsubmitted,
  tell iPad testers to install the universal apps and delete the old ones, and remove the `.tablet` App IDs after their
  builds expire.
- **#1574 and #1616 both regenerate `project.pbxproj`.** Whichever merges second brings main in
  and runs xcodegen again.
