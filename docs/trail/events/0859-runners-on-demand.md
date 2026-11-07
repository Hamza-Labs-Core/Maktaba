---
schema: 1
kind: pr
pr: 859
title: Maktaba's built-in plugin runners start only when they have work (the Core budget)
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [833, 844]
---

## What happened
- After #833 made the runners start, an idle Core install ran its shared runner groups as idle Python processes. Main went red on `mac-app / Bundle size (Core payload)`: 6 processes and 731 MB against 4 and 700. #844 (waves 5–8) showed 9 and 909 MB.
- The shared groups now start on demand. The worker runs a job itself while its runner is down and asks the supervisor for it (`POST /runners/{group}/start`). The runner exits 0 after ten idle minutes, and the supervisor doesn't restart it (`Process.IdleOnClean`) until it's asked again.

## What we learned
- A process that crashed at start hid a budget breach: the fix for the crash (#833) is what exposed it. When fixing a start-up crash, check the steady-state budgets it was hiding.
- "Runs while its component is on" is the wrong lifetime for a runner whose components are on in every home. Tie it to work instead. The worker's in-process fallback already covers the moment before the runner is up.
- The supervisor's health check must not count as activity, or an idle timeout never fires.

## Open
- None.
