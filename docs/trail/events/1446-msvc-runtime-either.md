---
schema: 1
kind: pr
pr: 1446
title: "The Windows payload takes either Visual C++ runtime of the runner images rolling out"
date: 2026-11-19
session: 753035df-3990-44b1-9dff-f305bbe9c3cd
agent: claude-code
prs: [1441]
---

## What happened
- #1446 merged as 36e035e11. windows-latest was rolling from an image with the Visual C++
  runtime 14.51.36247.0 to one with 14.52.37029.0, and no longer 14.51. Every Windows app build
  stopped at `payload.sh`'s runtime check on whichever image didn't hold the pin. #1441 met
  both images within an hour.
- `MSVC_RUNTIME_VERSION` names both, separated by a space. `payload.sh` takes the first found,
  that folder's three DLLs, each Microsoft-signed with one pinned FileVersion and all the same
  release. The Kimi K3.1 Pro review found no bugs. Its one gap, the 14.52 path not yet built,
  closed on #1441's next run.

## What we learned
- A single-version pin of a runner's own files fails both ways during an image rollout. Pin a
  set while the images roll, then narrow it once one remains.

## Open
- Narrow `MSVC_RUNTIME_VERSION` to 14.52.37029.0 once no runner has the older image.
