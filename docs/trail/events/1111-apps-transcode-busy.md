---
schema: 1
kind: pr
pr: 1111
title: "The native players wait for a busy home's transcode slot (story 59.05)"
date: 2026-11-10
session: bfbe1fc1-fe5c-4d94-b0ba-d4194c63c967
agent: claude-code
---

## What happened
- #1111 (merged at dd5dbe9e8) answers the home's 503 `transcode_busy` (#1110) in the native apps as the web player does. Android has `ErrorKind.TranscodeBusy`, and the player's Playback loop waits and asks again after the `Retry-After`, with the sentence on the watch screen in English and Arabic. Apple has `.transcodeBusy(retryAfter:)`: `Playback.openWhenFree` asks again while `waiting` is set, and WatchView shows the sentence over the spinner instead of the error screen. The desktop player has the `error.transcodeBusy` sentence in both languages.
- Connecting maps `TranscodeBusy` to the same busy reason as `Busy` (`AppSession.reasonFor`).

## What we learned
- The desktop player's `StringsTest.everyErrorKindHasASentence` fails as soon as a new `ErrorKind` lands without its sentence. The new kind went in through the shared client, and the desktop strings were missed until CI.
- Two Mac Player UI test failures, each of its own kind. First, the test video's JSON lacked `library_id` and `filename`, so `Video` failed to decode. Second, `aBusyHomeIsAskedAgainAfterItsRetryAfterThenPlays` waited 20 s for a player that never came: its `TokenStore` was empty, so once MockHome freed the slot, `playbackHeaders()` threw `signedOut` and set `failure`. The test now signs in through a stubbed `/api/auth/login` with `MockHome.accessToken`, and its wait ends on either a player or a failure, so it reports the failure instead of timing out.
- Unrelated flakes on the way: Android TV `TvConnectTest.theTvShowsTheCodeAndANewOneWhenItRunsOut` (a ConcurrentModificationException in the test's route harness) and iPad `PlayerMockHomeTests.testADelayKeepsTheQuerysAnswer` (1.50 s against a 1.4 s bound). Android TV passed on the next push's run, the iPad shard on a re-run of the failed jobs.
- Main moved twice while CI ran. Both times, `tools/dev/merge-main.sh` merged it cleanly, keeping both sides of the CHANGELOGs.

## Keep doing
- A player test that reaches AVPlayer signs in first: `playbackHeaders` needs an access token whose `exp` it can read.
- A test's wait for an outcome ends on the failure too, so the run names the cause instead of timing out.
- Add each new `ErrorKind` with its sentence in every app's strings (Android, Apple, desktop player) in the same commit.

## Open
- The Android TV app shows the busy sentence on its error screen but does not ask again by itself.
- `TvConnectTest`'s route harness and the MockHome delay test are timing-sensitive and fail now and then on CI runners.
