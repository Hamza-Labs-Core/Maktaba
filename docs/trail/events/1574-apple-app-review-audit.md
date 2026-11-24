---
schema: 1
kind: pr
pr: 1574
title: "App Store review audit: privacy manifests, local-network-only ATS, metadata drafts"
date: 2026-11-24
session: 366f9ce3-e03b-40f4-944e-650e7dafc5b7
agent: claude-code
---

## What happened
#1574 audited the six Apple App Store apps against the App Review Guidelines (last updated
2026-10-16) and App Store Connect for public release, and fixed what code can fix.
- **Privacy manifests.** No app had a `PrivacyInfo.xcprivacy`. Now one each, through the
  Player/, Remote/ and MaktabaTV/ source folders. Six Resources entries, one per app target.
- **ATS.** `NSAllowsArbitraryLoads` dropped. It had been set since the first commit
  (76b71cf99) with no stated reason.
- **Remote icon.** Its 1024 px icon lost an all-opaque alpha channel.
- **Privacy doc and policy.** `docs/app-privacy.md` gained the cloud sign-in and broadcast
  rows. The site's build ties maktaba.tv's privacy page to that doc (`apps-source` in
  `web/packages/website/scripts/legal.mjs`), so its `website` job failed until
  `copy/legal/privacy.{en,ar}.md` said the same. Both languages were updated in #1574 and
  wait for the owner's read.
- **New test and doc.** `tools/ci/tests/test_apple_app_review.py` checks all of the above.
  `docs/apple-app-review.md` holds the checklist, the ranked risks and the owner actions.
- **Metadata drafts.** English and Arabic drafts in `apps/apple/AppStore/`, in fastlane
  deliver's layout, never uploaded.

- **Decisions of 2026-11-24 recorded in the audit.** Cloud: launch before submitting. Universal
  apps (#1616). Cast on once its receiver is published (#1621). The drafts became one folder
  per universal app; their test skips the retired `.tablet` targets, so it passes before and
  after #1616.
- **Broadcasting is off for now (2026-11-24).** Product Interaction left the manifests, the label
  table and the privacy page (its `apps-source` recomputed), and the audit records "Leave
  channel" plus an invite-only review note for when it returns.

## What we learned
- **No multicast entitlement for Cast.** The first audit listed a multicast entitlement for
  Cast. Google's iOS sender page (SDK 4.8.x) needs none, and Apple grants none just for Cast.
- **ATS.** Apple's docs: iOS 10+ and macOS 10.12+ ignore `NSAllowsArbitraryLoads` when
  `NSAllowsLocalNetworking` is present. So the key never let plain http reach a non-local
  host, yet it still needed a review justification.
- **No live backend for reviewers.** The cloud's hosts (api, watch, live, status.maktaba.tv)
  and review.maktaba.tv did not resolve on 2026-11-23, and the review-home probe workflow is
  skipped on every run.
  - The apps still always offer "Through Maktaba Cloud" and "Watch a broadcast".
  - The review home's `reviewer` is a viewer, so it cannot get through the admin-only Remote.
  - With no live backend and no reviewer account for the Remote, guideline 2.1 is the first
    rejection to expect.
- **Report button.** Watch a broadcast has no in-app Report. `docs/broadcasting.md` lists it
  under "Not yet" (the cloud's report route needs Turnstile). Guideline 1.2 requires report and
  block for user-generated content.
- **App names.** Apple's marketing guidelines want "App for iPad" (referential). "Maktaba
  Remote iPad" is not that form. "Maktaba Player for iPad" and "for Mac" are.
- **Guideline 4.3(a)** ("Don't create multiple Bundle IDs of the same app") puts the separate
  iPhone and iPad records at risk.
- **Support URL.** maktaba.tv/help/ answers 200 but has no contact information. App Store
  Connect's support URL must show one.
- **Privacy policy.** It says the apps send nothing to Maktaba except problem reports. That
  misses cloud sign-in and broadcast metering.

## Keep doing
- Keep each app's manifest and `docs/app-privacy.md` in step. The test fails when a manifest
  names a data type the doc's table does not.
- Name secrets in review notes in double braces. `review_credentials.py` now scans
  `apps/apple/AppStore/` too.

## Stop doing
- Stop reading a TestFlight upload as App Store readiness. TestFlight took every build with
  no privacy manifest, an icon with alpha and a dead backend for reviewers.
- Stop adding Info.plist exception keys with no stated reason. Each key App Review asks about
  needs a reason written beside it in `project.yml`.

## Open
These wait on the owner (owner actions in `docs/apple-app-review.md`):
- deploy the review home;
- an admin review account for the Remote;
- launch the cloud, or hide its entries;
- an in-app Report for broadcasts;
- YouTube and Plex in the Apple apps;
- universal iPhone and iPad apps;
- Google Cast on or out (2.5.19, dormant features);
- privacy policy and support contact text;
- an in-app privacy link;
- EU trader status;
- the age-rating questions due 2027-01-12.
