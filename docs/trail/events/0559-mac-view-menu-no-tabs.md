---
schema: 1
kind: pr
pr: 559
title: Mac View menu test root-fixed: no window tabs
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge c822bf1c8. `PlayerMacMenuUITests.testTheViewMenuChoosesEachSection` had two causes.
- App: automatic window tabbing put Show Tab Bar / Show All Tabs at the top of the View menu as it opened, then removed them while open, so items moved up two rows under the pointer (View > Live TV chose Profile; a person could mis-click too). The Player sets `NSWindow.allowsAutomaticWindowTabbing = false` on macOS (one window, no tabs); the test asserts View has no tab items.
- Test: the menu helper's straight pointer move from View to Home crossed Playback in the menu bar and opened its menu; it now clicks straight below the bar item.
- Diagnosed from the xcresult screen recording, UI hierarchy and synthesized-event coordinates.
