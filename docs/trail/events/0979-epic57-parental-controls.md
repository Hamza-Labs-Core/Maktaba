---
schema: 1
kind: decision
pr: 979
title: Epic 57 spec: parental controls on iPhone and iPad, on Apple's Screen Time
date: 2026-11-08
session: 3a6c87d8-f5f2-4864-b70f-910a9fc90156
agent: claude-code
---

## What happened
- Merge 51e0bebc2. 13 stories, spec only. The scope moved mid-design from every app to the Apple Player on iPhone and iPad. Apple TV is in only where it falls out. Android, the web and the desktop Player are a later epic, though the home's filter covers them from the first build.
- **Device side.** Apple's Screen Time owns time limits, downtime, bedtime, app limits, the App Store age gate and Ask to Buy. Maktaba builds no timers and no app-leave PIN.
- **Content side.**
  - A child has their own restricted account, managed by admins and guardians an admin names.
  - Age tiers (All ages, 9+, 13+, 16+, 18+, unrated) come from admin tiers, containers, Plex, guide, YouTube `age_limit` and plugins.
  - One `parental` filter covers every content route, with a router walk and a bypass rehearsal. A blocked item answers 404, the same as a missing one.
- **Apple's signals.** The Player sends the Declared Age Range (gates 9, 13, 18) and the Screen Time movie and TV limit. They only tighten, and are stored per sign-in as a ratchet.
- **Ask a parent.** A child can ask, or a guardian can type a 6-digit PIN checked on the home, with lockouts. Activity is kept 90 days on the home.
- Unrated content is hidden from restricted accounts. Existing homes change nothing until an admin sets parental controls up.
- Migration slots 0216-0218 are planned and taken at merge time.
- Rejected:
  - profiles inside one account with a PIN switcher;
  - Maktaba's own time limits;
  - trusting a device's age to loosen limits;
  - the Family Controls entitlement.

## What we learned
- An app can read the device's effective movie and TV rating limit (`ManagedSettingsStore.effectiveMaximumMovieRating`, `effectiveMaximumTVShowRating`) without Family Controls authorization, on iOS 15+ and tvOS 26+. Watching for changes needs the entitlement, so re-reading on each foreground avoids it.
- `com.apple.developer.family-controls` needs Apple's approval for distribution, separately for each extension. `com.apple.developer.declared-age-range` is a plain capability.
- Declared Age Range is iOS, iPadOS and macOS 26+ only, with no tvOS. The Player already targets iOS 26.0, so it needs no availability checks.
- The home has no central access check: about 15 packages each scope by library. Live channel streams and the HDHomeRun routes are unsigned, and a channel with no library is visible to every signed-in user. The browse row cache assumes rows have no per-user part.

## Open
- A real-device check of whether the effective rating limit reflects the parent's own Content & Privacy Restrictions (story 57.04 §4), before the Phase 3 release tag.
- The build PRs, Phase 1 first: 57.01, 57.02, 57.03 and 57.10.
