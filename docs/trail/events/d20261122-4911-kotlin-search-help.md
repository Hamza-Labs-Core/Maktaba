---
schema: 1
kind: pr
title: "Story 49.11 on the Kotlin clients: search help on Android, Android TV and the desktop Player"
date: 2026-11-22
session: 110a659c-8eac-4e3b-a02d-0ae3582e6546
prs: [1517]
---

## What happened
- Story 49.11's Kotlin part (#1517): the Android Player, the Android TV app and the Player for
  Windows and Linux show story 49.8's suggestions with their kinds, did-you-mean, facets with
  counts and the sort, matching the web's 49.10 rules.
- The request rules live once in the portable client (`SearchHelp.kt`). The Player's
  `SearchModel` moved to the Android core, so the TV app uses the same model instead of a
  third copy.
- Android TV's Search moved from the video list's title match to `POST /api/search`. Results no
  longer follow the typing: each search becomes a recent search on the home, so only the
  Search key or a suggestion runs one.
- The independent review (GLM-5.3) found three majors the green tests had passed: the shared
  model kept an older word's late suggestions, the TV's Back-focus effect re-fired on every new
  search id and took the focus off a pressed chip (the fixtures always answered `s1`), and the
  desktop set a title suggestion's video with no page to show it. Each fix has a test, seen
  failing with the fix reverted.
- The local disk filled up mid-session from outside the worktree. Gradle's test results and the
  Player's build directory were left corrupt ("Unknown type of test metadata"), and 113 TV tests
  failed with "No space left on device", not from the code. Deleting the module's `build`
  directory cleared it.

## What we learned
- A full disk leaves Gradle's binary test results half written; later runs fail before any
  test starts. Read `df -h /` before blaming the code, and delete the module's `build`
  directory (not only `test-results`) to recover.
- Robolectric's lazy lists compose only what is on screen: a TV test must scroll a row into
  view (`performScrollToNode`) before asserting it.
- tv-material's `MediaCard` takes focus and clicks on `cardModifier`; a test tag on `modifier`
  marks a node without the click action.

- A fixture that answers the same id to every request hides any effect keyed on that id:
  give each answer a fresh id, as the home does.

## Keep doing
- Put a cross-platform rule (the request body a facet choice sends) in the shared client and
  test it there once; each app tests only its screen.

## Open
- 49.11 is Partial: the Apple apps (iOS, macOS, tvOS), the web big screen and Roku.
