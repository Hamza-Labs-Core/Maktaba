---
schema: 1
kind: pr
pr: 555
title: Sign-in keyboard tests measure the real on-screen keyboard and IME
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [522]
---

## What happened
- Merge 4a785bd1c. The sign-in keyboard tests from #522 now prove the keyboard part.
- Apple: PlayerSignInKeyboardUITests fails unless `app.keyboards` shows a full keyboard (at least 150 pt) and the focused password is wholly above its frame; a new AX3 case needs `keepsFocusInView` to scroll. CI iPhone/iPad simulators are set to ConnectHardwareKeyboard=false and AutomaticMinimizationEnabled=false. Measured on CI: iPhone keyboard y=583 (233 pt), iPad y=925 (282 pt), the password above it in every case.
- Android: SignInKeyboardTest dispatches an IME inset in a 360x640 dp window; a negative check (StartFrame without IME padding) fails 3 tests. `rememberKeptAboveKeyboard()` turned out redundant (Compose's scroll container keeps the focused field in view), left as is.
- Merged with two failures outside the change: the Mac View-menu test (fixed on main by #559) and iPhone 3/4 `PlayerComponentsUITests.testHero` "Audit failed to complete in time" on the shard's first cold launch (addressed by #566).

## What we learned
- A probe showed headless xcodebuild simulators already show the software keyboard. #522's keyboard-less shot came from `app.screenshot()`, which leaves out the keyboard window; the test's shots are now whole-screen (`shot(wholeScreen:)`).
