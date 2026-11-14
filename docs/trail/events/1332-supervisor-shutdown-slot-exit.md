---
schema: 1
kind: pr
pr: 1332
title: "The supervisor's own shutdown no longer fails the TV over"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1332 merged as c5c15c123. `SlotExitReporter` (supervisor `slots.go`) no longer reports a TV
  slot's exit whose reason is the supervisor's own shutdown. The update rehearsal's 16.7 reads
  the live slot just before the swap, since older releases still have the race.
- Found on #1263's update rehearsal (story 50.22 branch), the first run updating from 0.3.142.

## What we learned
- During a supervisor-wide stop the live slot (blue) exited ~12 ms before the front door; the
  report reached the front door first, it failed over to green and saved those roles in the old
  release, and after the update the swap went green → amber, not blue → previous.
- Main's passing update rehearsals all updated from 0.3.141; a new "from" release can expose a
  race that was always there. Check a rehearsal failure against which release it updated from.

## Keep doing
- A fix found on a story branch that main also needs goes in its own PR off main at once.

## Open
- Releases before this one keep the race on their side of an update; 16.7 tolerates it.
