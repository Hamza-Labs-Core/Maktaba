---
schema: 1
kind: pr
pr: 576
title: Live guide UI tests drag the guide, not flick it
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge a6d918a92. Live guide UI tests flaked (4 of about 200: time scrolled 116->116, channels scrolled up 427.5->427.5). Not a product bug: the XCUITest swipes were quick flicks (58 pt in 58 ms on an iPad row), a drag that finishes in under about 20 ms is never scrolled, and on busy runners the swipes moved nothing.
- A two-axis guide rework did not change the result and was dropped.
- `PlayerUITestCase.drag` presses, moves at 400 pt/s and holds before lifting; the guide tests use it, and the time test checks it returns to its start.

## Keep doing
- Scroll in tests with press-drag-hold, not `swipeUp()`/`swipeLeft()`.
