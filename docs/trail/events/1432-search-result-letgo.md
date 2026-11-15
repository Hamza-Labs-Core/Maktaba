---
schema: 1
kind: pr
pr: 1432
title: "A search result opens only after the search field lets go of the keyboard"
date: 2026-11-15
session: a3d43d04-c88d-4939-9b95-b1d413c3b5a5
agent: claude-code
prs: [1425, 1419]
---

## What happened
- #1432 merged as ad7ca742c. It closes the open item of the #1425 event. If someone went back
  to the search field to refine and then tapped a result, the page was pushed with the
  keyboard still up.
- Each result row (a title, a video of In the transcript, a passage) is now a `Button`, not a
  `NavigationLink(value:)`. It goes through `LetGoFirst<SearchChange>` (`.search(q)` or
  `.open(route)`): the field drops focus, and the push runs from `onChange(of: typing)`.
- New `PlayerPush` environment value (`PlayerShell.swift`): a stack that holds a path of
  `PlayerRoute`s pushes onto it from code. The shell sets it per section, outside the
  `NavigationStack`, so pushed pages have it too. Libraries' compact and wide stacks set it,
  and `LinkedRouteSheet` clears it. Where it is nil, `SearchScreen` falls back to its own
  `navigationDestination(item:)`, the pattern `HomeScreen` uses.
- Tests: `SearchLetsGoFirst` (Mac unit tests), and
  `PlayerSearchUITests.testATitleTappedWhileRefiningLetsTheKeyboardGoAndOpensItsPage`. That UI
  test submits, types in the field again with the keyboard up, and taps a title with
  `tapWhenIdle`. The page opens on the first tap and the keyboard goes. On a throwaway iPhone
  17 simulator (iOS 27.0), the tap took 1.1 s.
- `affected-tests` blocked `VideoLink.swift`, whose only change clears `PlayerPush`. An empty
  commit with a `No-Test-Change` trailer covered it. Final checks: 34 pass, 32 skipped.

## What we learned
- To run something before a push in a `NavigationStack(path:)`, push from code (a `Button`,
  then append to the path). Nothing runs before a `NavigationLink(value:)`'s own push, and a
  gesture next to the link can take its tap (#1425).
- Set the push action with `.environment` outside the `NavigationStack`, not on the root
  inside it. Then every pushed page has it, including the `navigationDestination(item:)`
  pages of `HomeScreen`.
- To keep a row from looking changed when a `NavigationLink` becomes a `Button`: add a
  trailing `chevron.forward` on iOS (it mirrors in Arabic). Use `.buttonStyle(.plain)` on the
  Mac, or the row gets a push-button bezel.
- Xcode's iOS 27.1 simulator runtime lists no supported device types, so `simctl create`
  fails with `Incompatible device`. Create the throwaway simulator on iOS 27.0.

## Keep doing
- Check whether `SearchScreen` (or any screen) is reachable from more than one stack before
  choosing how it pushes. Search opens as a tab, as a `.search(q)` route in the shell and
  Libraries stacks, from Home's item destinations, and in a linked page's sheet.
