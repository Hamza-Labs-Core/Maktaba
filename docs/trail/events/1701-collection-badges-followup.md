---
schema: 1
kind: pr
pr: 1701
title: "#1615's review: a leftover Plex collection reads as curated everywhere"
date: 2026-11-24
session: 1b83b72b-70fd-40e3-a727-36b3e7966ef9
agent: claude-code
---

## What happened
PR #1701 takes the five minors of #1615's review (no blockers, no majors). The web reads an older home's `kind: "plex"` as `curated`: it dropped kinds it did not know, so a UI shipped apart from the server (the desktop frame, the Capacitor wrapper, the bundled TV build) hid such a collection on homes up to v0.3.162. Promoted rows and collection-mode cards no longer send `source_kind: "plex"`. The OpenAPI's `Collection` lost the value too. Epic 56's specs no longer ask for the removed badges and kind.

## What we learned
- Retiring an enum value has two directions. #1615 covered an old client on a new home, and missed a new client on an old home: a reader that drops unknown values hides the row. Check both.
- A value is sent from more than one query. `git grep source_kind -- api/internal/handlers` finds the list, the single read, the promoted rows and the grid's cards; #1615 changed only the first two.
- A test that a removed badge is absent must look for the element (`.mk-badge`, `.bs-tile__badge`), not its text: with the string gone from the catalog, the old code draws the raw key and a text check still passes. Put the old code back and see the test fail.
- In zsh, `git show $B:web/x` applies the `:w` modifier to `$B` and prints nothing useful; write `"${B}:web/x"`.

## Keep doing
- Leave a test-only Apple change to an issue when a pull request otherwise needs no Apple run.

## Open
- #1702: the Apple TV Collections test passes at 0 = 0 texts, and a comment in `LibraryView56.swift` still names `plex`.
