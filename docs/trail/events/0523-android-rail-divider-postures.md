---
schema: 1
kind: pr
pr: 523
title: Android Player: collapsible rail, draggable list-detail divider, half-fold layouts
date: 2026-11-02
session: unknown
source: claude-trail.md
---

## What happened
- Report: on a foldable open flat (sideways, 841 dp) and a big tablet, Libraries showed a permanent 360 dp drawer, the list (380 dp) and the videos squeezed to about 100 dp. Cause: `navigationFor` gave expanded windows `NavigationDrawer`, and the list had a fixed 380 dp.
- Decided: expanded windows and a book-held foldable get a collapsible rail (`shell/CollapsibleRail.kt`, material3 1.4.0's `WideNavigationRail`, no upgrade needed): 96 dp of icons that expands over the content (modal, with a scrim, chosen over push so the content never reflows). The menu button or a mouse hover opens it; choosing, a tap outside, Back or the mouse leaving close it. Compact and tabletop keep the bar, medium keeps the rail.
- Decided: `core/layout/ListDetailSplit` is a draggable divider. List min 240, page min 320, default list 380 but at most 40 %. 48 dp handle, TalkBack widen/narrow actions and arrow keys, mirrored in RTL. Kept in saved state and in SharedPreferences `maktaba.splits`. Used by Libraries and the Remote.
- Decided (follow-up, half-fold postures): in book posture the rail and the list sit on the start page and the list ends at the hinge. The split is the hinge (no drag handle: any other split puts a pane under it), and the expanded rail is capped to the start page. In tabletop the page is on top and the list and bar on the bottom; non-splitting screens keep to the top half (`OffTheHinge` in PlayerShell). The shell passes its adaptive info down (`LocalAdaptiveInfo`), so tests fake hinges through `WindowAdaptiveInfo` (`harness.folded`).
- Failed first: (1) the rail's Back did not win over a section's Back: a BackHandler that is always composed keeps its old registration order; it is now composed only while open. (2) Swapping between NavigationSuiteScaffold and the rail frame recomposed the screens, and a live channel going full screen restarted; both now share one tree. (3) The rail's IconButton was 40 dp, and HomeScreenTest's accessibility audit caught it.
- CI failed the first push (run 41472993431): four LayoutScreenshotTest pictures had the library page scrolled down a row. Unsettled content, not fonts or a layout bug: the hub rows can land after the first videos and the grid keeps the first video in view (scroll anchoring), so the picture depended on answer order. The test then waited for idle and scrolled to top before each capture; goldens re-recorded, tolerance unchanged. The jump itself was in `LibraryPage` (fixed by #529).
- Merged at 245915cc.

## What we learned
- A BackHandler that is always composed keeps its old registration order, so it may not win over a later one. Compose it only while it should act.
- Swapping the outer scaffold recomposes the screens under it and restarts playback; keep one tree.
- `recordRoborazziDebug` rewrites every PNG, even unchanged ones (byte noise). Re-record only the tests that `verifyRoborazziDebug` fails.

## Open
- Not verified on a real foldable or tablet, and no emulator run.
- In book posture, Home and Search still spanned both pages (done in #536).
