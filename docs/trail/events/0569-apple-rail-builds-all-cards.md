---
schema: 1
kind: pr
pr: 569
title: Apple Player Home rows could show a title with no cards
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 958813593. `testNotInterestedRemovesTheCard` failed once in 53 runs with For You empty.
- `Rail`'s cards were in a `LazyHStack` inside a sideways `ScrollView`, which is only as tall as the cards built so far; a row just scrolled in is 0 pt, and Home's last row could stay at 0 pt with no card ever built.
- Fix: `Rail` uses a plain `HStack` (every rail is a short list). Home tests check each row has cards as soon as it shows; `PlayerComponentsUITests.testRails` requires all 6 gallery cards (5 with the lazy rail).
- Never reproduced locally; the CI recording and screenshots gave the cause.

## What we learned
- A SwiftUI `LazyHStack` in a horizontal `ScrollView` is only as tall as the cards built so far and can stay at 0 pt. Short lists should use a plain `HStack`.
