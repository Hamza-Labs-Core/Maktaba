---
schema: 1
kind: pr
pr: 1310
title: VoiceOver reaches a Home card's menu items as custom actions
date: 2026-11-14
session: 4d001160-585c-4d8f-b3ae-f6fe833c328d
agent: claude-code
---

## What happened
- The Apple Player's Home cards offer their context menu's items (Resume, Not interested) as
  VoiceOver custom actions (`.accessibilityActions`). The menu and the actions are built from
  one list, `HomeCardAction.of(card)` in `Player/Home/HomeRows.swift`, and both call
  `HomeCardAction.run(on:_:)` with the screen's `Handlers` (open a route, dismiss, say that
  dismissing failed), so they cannot drift apart. The names reuse the menu's existing strings
  (`Resume` in Components, `Not interested` in Home), which already had Arabic.
- The Remote has no Home card menu (its only context menu is Revoke on the Machines list), so
  nothing changed there.
- Unit tests: `PlayerMacTests/HomeScreenTests.swift` (suite `HomeCardActions`).

## What we learned
- The Mac Player's app-hosted unit tests run from an explicit `-only-testing` list in
  `.github/workflows/_apple-apps.yml`. A new suite in `PlayerMacTests/` compiles and passes CI
  without ever running until its suite name is added there. Check the job log for the suite's
  name, not just a green job.
- The affected-tests gate maps `HomeScreen.swift` to the iPhone/iPad UI tests that use its
  identifiers. A change no UI test can observe (custom actions: XCUITest has no public API to
  list or trigger them) passes with a `No-Test-Change:` trailer on a commit in the range.
- Keep the Resume path synchronous (`run` returns nil): wrapping every action in a `Task`
  would delay the push the menu UI tests wait for. Only Not interested returns a task, which
  the unit tests await.
