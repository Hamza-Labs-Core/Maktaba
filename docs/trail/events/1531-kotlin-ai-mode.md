---
schema: 1
kind: pr
pr: 1531
title: "Story 49.14 on the Kotlin clients: AI mode's tab on Android, Android TV and the desktop Player"
date: 2026-11-22
session: 47bb969d-440d-4135-8439-a753bb553b4a
agent: claude-code
prs: [1517, 1525]
---

## What happened
- Story 49.14's Kotlin part (#1531): Search in the Android Player, the Android TV app and the
  Player for Windows and Linux has two tabs, Results and AI mode, against 49.12's
  `POST /api/search/ai` (#1525).
- The stream is read once, in the portable client (`SearchAI.kt`): an SSE reader on bytes, the
  events read leniently, the refusals (`ai-unavailable`, `ai-off`, `ai-busy`, 404 for an older
  home) and `AiConversation`, the tab's state the three apps share.
- `ApiClient.send` reads a whole answer; a stream needed `ApiClient.opened`, which hands back
  the open answer (a 401 refreshed once) and its call, so cancelling can stop a read blocked on
  the socket.
- The first cancel test failed: cancelling the call made the reader's blocked read throw an
  `IOException`, wrapped as `ApiException.Transport`, which reached the collector of a flow it
  had already left. The reader now calls `ensureActive()` before wrapping the error.
- The Player's resources failed to compile ("Can not extract resource from ParsedResource"): an
  unescaped apostrophe (`home's`) in a new English string. Escaped as `\'`.
- The desktop's `formatDuration` wraps a time in bidi isolates (U+2066…U+2069), so its tests
  match a citation's spoken label with the isolate in it.
- The affected-tests gate blocked the first run: `SignedInApp.kt` changed (the TV's `onPlay` now
  takes a video id) with none of its tests changed. `SignedInAppTest` now checks that an AI mode
  citation makes a queue of the cited video (a1489f8c2).
- CI's Android TV emulator journey (`TvSearchJourneyTest.aiModeWithTheRemote`, Android 11)
  failed with `NoSuchMethodError: ByteArrayOutputStream.toString(Charset)`: a Java 10 method
  that Android has only from API 33. The JVM tests (Robolectric too) passed with it. The reader
  now decodes `String(bytes, UTF_8)` (d7970b386b19e3e49a297174076abf9b3d6e4c7a).
- Re-reading the conversation found Try again after a refused follow-up asking the first
  question instead of the follow-up; it asks the last question asked now, with a test.
- This machine had no Java on the PATH: Gradle ran with Homebrew's OpenJDK 21 as `JAVA_HOME`
  and `ANDROID_HOME=~/Library/Android/sdk`. The client, Player, core, TV and desktop unit tests
  ran locally; the TV emulator journey runs in CI only.
- A GLM-5.3 review found no major issue and three minor ones, fixed by the lead in
  38f418ddaacab5f0d2822c94515f8df19590a2ef:
  - A 409 or 429 for a question could land after a newer question was asked; it was then
    shown under the newer one, and stayed. Only the latest question's refusal is now
    shown, and the test holds the older refusal in `NonCancellable` to land it late.
  - The 429's place in line and `Retry-After` were read but never shown.
  - A `done` with `reason: timeout` before any sentence said the passages do not answer.
    It is now a stopped answer, with Try again.
- A desktop compose test timed out once at a load average of about 67, and passed alone.

## What we learned
- A blocking read on OkHttp's body is not cancellable by the coroutine: run it on IO in a
  `channelFlow` child, and cancel the `Call` in the parent's `finally` after `join()`; check
  `ensureActive()` before turning the resulting `IOException` into a failure.
- `ApiClient.opened` skips the relay's reconnect waits (`HomeReconnect`), which `send` does: a
  stream is asked again by the person, not retried.
- `mockwebserver3`'s `throttleBody(bytes, period)` lets a Robolectric test see what shows before
  the rest of a stream arrives (the passages before the sentences).
- A Gradle `--tests` filter from `apps/desktop-player` also applies to the included `:client`
  and fails it with no matching test: run `:test --tests …` there.

- The portable client compiles against the desktop JDK, not Android's API: a JDK method newer
  than Java 8 compiles and passes every JVM test, then fails on an older Android device. Only
  the emulator journey caught it.

## Keep doing
- Put the stream reader and the conversation's rules in the client, tested once; each app tests
  only its screen, with the shared `AiStreams` test fixture (a stream is not JSON, so it is not
  a file under `browse/`).

## Open
- The web, big screen, Apple and Roku parts of 49.14 are other PRs; the CHANGELOG and the spec's
  As built come in the story's closing PR.
