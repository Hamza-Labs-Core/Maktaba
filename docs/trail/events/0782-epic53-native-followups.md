---
schema: 1
kind: pr
pr: 782
title: Epic 53 native follow-ups — Report this in the desktop Player, the last-crash notice, the phone screenshots
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- The desktop Player's error boxes offer Report this with the failed call's reference. It says once whether its last crash went to the home or stayed on the computer.
- An Android Robolectric test shoots the crash question and Report a problem in English and Arabic, and checks the two buttons are the same size.
- maktaba.dev's Player page shows the question and the form on an iPhone and on an Android phone.

## What we learned
- Every PR of this series appended its changelog entry at the end of the Unreleased "Added" section, where other sessions append too. Each round of CI (about an hour for the Apple jobs) ended with a new conflict there, three times on #732. Putting the entry at the top of the section, where no appender writes, let it merge. While many sessions run, a slow PR's entry belongs at the top.
- A T3 server restart drops the session's tracking of its background watchers, but not the processes. One such orphan was found by its command line (`pr checks 774`) and stopped by its PID.

## Open
- 53.14 T09 (a Debug crash, relaunch, the home receives it): the simulator does not deliver MetricKit diagnostics on demand.
- 53.16's "Also send to Maktaba" box.
- Keeping the Apple dSYMs as a release asset (53.14 §2), a release-workflow change.
