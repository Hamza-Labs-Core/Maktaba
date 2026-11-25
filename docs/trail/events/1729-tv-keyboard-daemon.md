---
schema: 1
kind: pr
pr: 1729
title: "TV testGuide's \"could not focus\" was the keyboard daemon starting 15 s late"
date: 2026-11-25
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
---

## What happened
- `TVDemoShotsUITests.testGuide` failed on #1616 and #1713 with "could not focus picker.address"
  (issue #1678 item 10, jobs 138956933820 and 139001674314), the job's first test both times.
- The focus moves. The keyboard opened and the app got every key on time, and waited for `kbd`,
  which a new simulator starts when a keyboard first opens. The simulator's new processes were all
  held then: `kbd` ran 15 and 27 s after it was asked for. The median start is 25 ms.
- `typeExactly` then looked for the focus of a field the keyboard covered, so the failure named
  the focus.
- #1729 starts `kbd` before the simulator settles, and on tvOS opens a field again only when it
  does not have the keyboard.

## What we learned
- The last step a test failed on is not always the one that broke: the recording showed the typed
  address come in, whole, as the test ended.
- `launchd_sim`'s "xpcproxy_sim spawned" and "service state: running" give each process's start
  time; list those over 1 s.

## Open
- What holds the simulator's new processes is on the runner and not traced: the bundle has only
  the simulator's log.
- Nothing shows yet that a held simulator passes with `kbd` there. The TV jobs to come do.
- apsd still runs in the TV and Remote simulators (#1710 turned it off in the Player's).
