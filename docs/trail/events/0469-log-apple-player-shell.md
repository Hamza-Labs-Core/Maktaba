---
schema: 1
kind: pr
pr: 469
title: "48.13: the Apple Player shell and UI-test harness"
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
`Player/Shell/` holds `PlayerShell` (a `TabView` with `.sidebarAdaptable`, one `[PlayerRoute]` stack per section; choosing the chosen tab pops it), `PlayerSection` (`enabled` = `[.libraries]`), `PlayerRoute` (video, series, library, channel, watch) with one `playerDestinations`, and `Shell.xcstrings`.
Placeholder files sit in each screen's folder (`Home/`, `Libraries/`, `Video/`, `Search/`, `Live/`, `Profile/`, `Watch/`); each screen story owns its file. `LibraryBrowser.swift` moved to `Libraries/`.
CI: a new `player-ui-tests` job in `_apple-apps.yml` runs every `UITests/Player*Tests.swift` class on throwaway iPhone and iPad simulators (deleted after) and uploads the screenshots.

## What we learned
- Decided: with one section enabled, the shell shows that screen alone (no one-tab bar), so TestFlight builds look as before until a second section merges.
- Debug-only launch arguments (`-maktaba-sections all`, `-maktaba-section`, `-maktaba-open video/v1`) let the tests reach screens that are not turned on yet; Release builds ignore them.
- MockHome serves `UITests/Fixtures/*.json` (shapes taken from the Go handlers, with a `{{now±N}}` time token) through `routes.json`. It adds `stub`, `delay` and `requests` (with query and body).
- The string guard now checks every table of a target against the `<table>.xcstrings` in its folders, so a screen's new table needs no guard edit.
- Failed, then fixed: relaunching without `-maktaba-session-reset` went back to sign-in, because an unsigned test build keeps no Keychain token. So every UI test signs in fresh.
- The Duo hinge split was only built with Xcode 27 (it is `LibraryBrowser`'s and unchanged); it was not exercised on a Duo simulator.
