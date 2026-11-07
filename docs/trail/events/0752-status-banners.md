---
schema: 1
kind: pr
pr: 752
title: Maktaba Cloud's line in every app's status strip
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [739]
---

## What happened
- Story 50.11, part 2: web, cloud web, Apple and Android read `summary.json` only when they reach the home through Maktaba Cloud, and 53.06's strip draws the cloud's line: down before slow, then maintenance from 24 hours ahead, after the home's own lines, with Details to the status page. The home's CSP and the cloud editions' allow `https://status.maktaba.tv` in `connect-src`. maktaba.dev's Apps overview describes it.

## What we learned
- The home web app's CSP was `connect-src 'self'`, so a feed on another origin needs a CSP change. The cloud's player and remote editions copy the home's headers (`cloud/internal/handlers/relay/editions.go`), so change both.
- "Through the cloud" is the home's address under `relay.maktaba.tv` (plus the web's `IN_CLOUD` editions). Match the suffix with its dot: `evilrelay.maktaba.tv` must not pass.
- The Apple strip reads catalog keys through `LocalizedStringKey`. Words with values need `String(format: localized(key), args)`, with positional `%1$@` in both languages. A line's values belong in its close signature too, so a new maintenance window reopens a closed strip.
- Rewriting an `.xcstrings` file through a JSON round trip is lossless only without re-sorting: Python's sort is not Xcode's, and sorting rewrote 376 lines. Add the keys in place.
- Android's unit tests use OkHttp 5's `mockwebserver3` (`MockResponse.Builder()`, `server.close()`), not `okhttp3.mockwebserver`. Locally gradle needs `JAVA_HOME` (Homebrew's openjdk@17) and `ANDROID_HOME=~/Library/Android/sdk`.
- The web unit suite on a loaded machine still times out a few tests at 5 s, a different few each run (#746 is on it). Run the failing files alone before suspecting the change.

## Open
- Apple and Android read the feed at start and every 5 minutes, not at once after a failed request: neither reads the home's own status that way yet.
- The Android TV app has no status strip (53.06's TV part).
- 50.12 to 50.15, 50.19, 50.30 to 50.35, and 53.16 to 53.18 follow.
