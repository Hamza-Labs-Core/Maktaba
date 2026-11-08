---
schema: 1
kind: pr
pr: 929
title: The Apple page snapshots check the page's words too
date: 2026-11-07
session: 29e8f107-ee7f-4db2-a128-c02a2839240a
agent: claude-code
prs: [893, 910]
---

## What happened
- #910's event left an open item: the page snapshots guard layout, not wording. A short phrase in the wrong language is a few hundred pixels, under the 0.2 % tolerance, and #910's first run passed against Arabic references that still said "3 episodes · 2024".
- #929 adds a text reference beside each image, `Snapshots/<name>.txt`. It holds the page's accessibility elements in order, each with its role and words (label, value, title), and must match exactly. It is attached and copied with `tools/apple/snapshots.py`, like the images.
- SwiftUI on macOS builds no accessibility tree in-process until an assistive client asks: a hosting view's children came back empty. Setting `AXEnhancedUserInterface` on the app (what VoiceOver sets) builds it; `AXManualAccessibility` does not. The test sets it after the image is drawn and clears it after reading, and the images matched their references unchanged.
- SwiftUI's elements are not `NSAccessibilityElement`s, and Swift sees the accessibility getters as both properties and methods (ambiguous), so they are read by selector.
- Proof: run 42425364483 ran on a commit that undid #910's series fix and failed exactly the four Arabic series pages on line 2 of their text references, while their images passed. The other 20 matched references drawn by an earlier run (42424767530), so the words are stable from run to run. The undo was reverted before the merge.

## What we learned
- Pixel references with a tolerance cannot guard wording; an exact text reference beside them can, and it shows the failing line in the log.
- The text references read as the page's script: what English remains on the Arabic pages is the fixtures' and the panel's data, which is easy to check by eye.
- To prove a new check, run it against the code it should catch (a temporary undo on the PR, reverted before the merge) and record the failing run.

## Keep doing
- Read each recorded text reference, as each image is read, before committing it.

## Open
- iPhone- and iPad-only layouts still have no references.
