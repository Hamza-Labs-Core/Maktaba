---
schema: 1
kind: pr
pr: 522
title: Sign-in forms: Return goes to the next field, the field stays above the keyboard
date: 2026-11-02
session: unknown
source: claude-trail.md
---

## What happened
- Requirement: on iOS and Android, sign-in fields get a Next key, and the keyboard never covers the field.
- Apple (MaktabaKit `StartViews.swift`, `CloudViews.swift`): an `@FocusState` field chain. Username Next -> password Go; cloud email Next -> password (Next -> address when Advanced shows it, else Go) -> address Go; picker address Go. `keepsFocusInView` (`KeyboardFocus.swift`) scrolls the focused row to the middle on each focus change and again 350 ms later, once the keyboard is up. No new visible text, so no new strings.
- Android (core `StartScreens.kt`, `CloudScreens.kt`): a FocusRequester chain in the same order; the last field takes Go. `rememberKeptAboveKeyboard()` (`KeyboardFields.kt`) brings the focused field into view on focus and on every IME height change. The activities were already edge-to-edge with adjustResize, and `StartFrame` already pads for the IME.
- Tests: `SignInKeyboardTest` (player, Robolectric, v2 compose rule) for home, cloud and cloud+Advanced. `PlayerSignInKeyboardUITests` (iPhone/iPad CI): Return moves keyboard focus to the password, the password is above the keyboard, and Return sends the login. Text goes in through `typeExactly` (#510); only Return uses `typeText("\n")`.
- The first CI run (before main was merged in) failed for reasons outside this change: Mac PlayerMacMenuUITests:155 and Android LibrariesSectionTest (fixed by #510 and #508), and the iPhone/iPad accessibility audits (Libraries, Profile, Search). On the iPad run, `PlayerSignInKeyboardUITests` passed. A local `LibrariesScreenshotTest.tabletEnglish` timed out once under load from a parallel Xcode build and passed on re-run (FakeHome settling, unrelated).
- Merged as 0664eb2c.

## What we learned
- At merge the keyboard part was not really checked: CI and local simulators were believed to use a hardware keyboard, so "field above the keyboard" ran with no on-screen keyboard, and the Android scroll-into-view was checked only for focus and submit. #555 made both tests real.
