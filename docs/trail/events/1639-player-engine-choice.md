---
schema: 1
kind: pr
pr: 1639
title: "Story 13.21: the Windows and Linux Player's video engine as a setting in Profile"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
Story 13.21, after 13.18 (#1591): Profile → Playback gets **Video engine**, with **Automatic** (libmpv, and JavaFX only when libmpv cannot load) and **Older engine** (JavaFX and `desktop-player`).
- **Where the choice lives:** the Player's own settings file (`player.engine`), not the shared `PlaybackValues`.
- **When it is read:** at each video's open, by `Engines.players()`. `MAKTABA_PLAYER_ENGINE=javafx` comes first, then the setting, then whether libmpv loaded. Playback picks its players in `play()`, so the video playing and its reopens keep their engine.
- **The row locks on Older engine** when the variable is set or libmpv cannot load, with a line that says why.
- **Learn more** opens the home's help at `#video-engine` with `?lang=` in the Player's language; with no home signed in, maktaba.dev's Player page.
- **The libmpv notice** shows once per Player version and reason, with Learn more.
- **Report a problem's log** has a line per video opened, naming its engine and why.
- **Docs:** the help page's "The video engine" section in English and Arabic, and a maktaba.dev screenshot of Profile → Playback on test data, from an off-screen Compose capture.
- **An independent review** found two problems, both fixed in 8ca2af3c4:
  - T02 flipped a test variable that nothing read, so it could not fail. It now goes through the real `Engines` and `AppPrefs`, and checks that the playing video's player and session are untouched, and that a quality reopen of it stays on libmpv.
  - The help page's `?lang=` called `setLocale`, which saves `mkt:locale`, so the Player's Learn more switched the browser's web-app language for the home for good. It now applies to that visit only (`setLocale(l, { keep: false })`).
- Built while #1591 was unmerged, from its branch. Main was merged in once #1591 had landed.

## What we learned
- **A test of "not reopened" must watch the thing that would reopen.** Here that is the playing video's player and its session, plus a reopen that would pick the engine again. A variable the code never reads proves nothing.
- **The web app's `setLocale` persists to `localStorage`.** A link that asks for a language must not change what the person chose for every later visit.
- **The home's help page had no URL-driven language** before this. Any app that wants help in its own language now passes `?lang=en|ar`.
- **Choosing an engine per video, not per process,** turns `Engine` from a lazy singleton into `Engines` with injected env, load and players. That made the four states (variable, setting, loaded, missing) unit-testable without a library.
- **The probe (13.19) and the engine setting meet in `Engine.kt`**: the probe runs only while videos play with libmpv, and the problem report lists it only once it has started.
- **A status line must not load what its setting avoids**: naming the engine loaded libmpv for the very person who chose the older one to stay clear of it (fixed in #1636).

## Keep doing
- Taking maktaba.dev screenshots from an uncommitted off-screen Compose test with invented data, as the devportal README says.
- Asking the affected-tests gate before pushing, and giving every flagged source file a test that would fail without the change.

## Stop doing
- Writing a test whose assertion depends only on a variable the test itself sets.
- Nesting a blocking call to the test's main dispatcher inside the `until` helper (`onMain` within `onMain`): it deadlocked one run of the 13.20 tests in this session.

## Open
- **13.17's reference images** for Profile → Playback (this story's T06) wait for 13.17.
- **13.22** removes JavaFX: the row, its strings and the variable go, and a stored `javafx` reads as Automatic (`EngineChoice.of(…, olderAvailable = false)` is ready).
- **The packaged Player's notice and Learn more** have only been exercised in unit and Compose tests, never on a real installed Player.
