---
schema: 1
kind: pr
pr: 508
title: LibrariesSectionTest waits settle; tests keep the app's log; Player JVM tests on v2 compose rules
date: 2026-11-02
session: unknown
source: claude-trail.md
---

## What happened
- One CI run (41376606909) failed `aLibraryShowsItsHubRowsAndLeavesTheEmptyOnesOut`: it waited 10 s for the libraries list while the other 11 tests took about 0.5 s. Six local runs of the class passed. The report held no app log (Robolectric drops android.util.Log), so the first miss left no cause.
- First changes: a missed wait now says which paths the app asked the home and what the screen showed. Each FakeHome gets its own OkHttp connection pool (it shared defaultHttp's across tests).
- Both Android Gradle roots (apps/android, apps/tv/android) set `robolectric.logging=stdout` on every Test task, and LogsKeptTest fails if it goes. Reports now carry the app's log.
- Root cause, found with the new diagnostics: the miss is always the first wait. The report showed the app asked `/api/libraries/summary` while showing [libraries-list, tabs]: the answer came, no error was logged, the screen stayed on its spinner. Under Robolectric a bare `compose.waitUntil` only polls; an answer landing after the last idle is not applied until the main looper is idled. Reproduced with a 500 ms late answer: poll-only took 4.6 s of a 5 s budget, poll-with-idle 0.6 s.
- Fix: harness/Settle.kt `settledUntil` (waitForIdle in every poll) and `settled` (the same, plus the asked/shown message). All three Libraries test classes use them.
- Converted: every Player-test wait on a fake-home answer or what it causes (tags and text, request counts, HomeCache files, session phase, fake engine calls) is now `settledUntil`, in 16 Home/Video/Profile/Search/Shell/Start/Connect/Components/Live test files. HomeScreenTest's gated "not answered yet" wait dropped its inner waitForIdle (the gate is the test's own latch, so settling cannot let the answer in early). ConnectProgress's AwaitingApproval wait was converted too (its countdown is on a fixed clock).
- Left bare, each with a one-line comment: ChannelScreenTest's `controls()` (they hide after 4 s), WatchScreenTest's controls, seek-flash and next waits, and the `until` helpers in WatchScreenTest, WatchScreenshotTest and PipBackgroundCastTest (they step the players' clock themselves; settling would also advance Compose's).
- Settling alone did not end it. The PR's run on 8c6d44b1 missed again in facetsAndSortChangeTheRequestsQuery: answer in, no error logged (the kept log proved it), spinner for 10 s. Not reproducible locally even under full CPU load. The v1 compose rule (deprecated; the build warned) runs effects on an UnconfinedTestDispatcher, so the answer resumed the screen on OkHttp's thread and wrote Compose state from there. All 23 Player JVM test classes now use the v2 rules (`junit4.v2.createAndroidComposeRule` / `createComposeRule`), which queue that work for the main thread. Three full suite runs green, screenshots unchanged (verifyRoborazziDebug), tests about 3-5x faster. androidTest (on-device) files were still on v1.
- These commits landed in #508, whose title is about the Remote's name.

## What we learned
- Under Robolectric, a bare `compose.waitUntil` only polls. Work that lands after the last idle is not applied until the main looper idles, so a wait can time out with the answer already in.
- The v1 compose test rule runs effects unconfined: a network callback resumes the screen on OkHttp's thread. The v2 rules queue that work for the main thread.
- Settling advances the clock, so waits on things that hide by themselves (controls, seek flashes) must stay bare.
- Without the app's log in the report, a one-off miss leaves no cause.

## Keep doing
- Tests always keep the app's log, so none is dropped (`robolectric.logging=stdout` in both Android Gradle roots, guarded by LogsKeptTest).
- A missed wait should say what the app asked and what the screen showed.
