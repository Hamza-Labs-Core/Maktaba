---
schema: 1
kind: pr
pr: 963
title: The Mac page snapshots no longer compare the scroll bar
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [940, 959]
---

## What happened
- `PageSnapshots` compares each Mac page's accessibility elements, one line each, with a text reference. On the library pages the dump included the list's scroll bar and its value indicator. The value is the scroll position, and where SwiftUI lists the bar among the page's elements varies by run: `library-open-ar-ax3` failed with its first row listed before the bar on #940 and on #959's first attempt (and `library-open-ar` once on #960), passing on reruns.
- The text dump skips an element whose role is `ScrollBar`, with its children; the 8 library references lose those lines (18 of each, always paired). No other line or image reference changed.

## What we learned
- A flake seen twice is fixed in the test, not rerun a third time: compare only what the reference is about (the page's words), not layout state such as scroll position.
