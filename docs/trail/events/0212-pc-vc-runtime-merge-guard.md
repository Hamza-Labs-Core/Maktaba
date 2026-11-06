---
schema: 1
kind: pr
pr: 212
title: Visual C++ runtime pin; merged while a check was still running
date: 2026-10-21
session: 295d4839
source: claude-trail.md
---

## What happened
#212 pinned the Visual C++ runtime for the Windows installer (#198 follow-ups). A merge guard
let #212 merge with a check still running; the guard was fixed in its own release (session
f43fad60).

## What we learned
- The VC++ runtime pin must match the runner's Visual Studio: 14.51 on windows-2025-vs2026, not
  VS 2022's 14.44.
- A merge guard must wait for every check, including ones still running.
