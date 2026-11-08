---
schema: 1
kind: pr
pr: 961
title: 53.16: Android R8 mappings, desktop stacks, and rehearsal T07
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [957]
---

## What happened
- Android: the Player, Remote and TV release builds are R8-obfuscated. A parser reads each release's `mapping.txt` into a table keyed by the app and version a report names (an Android release has no binary UUID), uploaded on the same route with the same token. The console restores classes, methods, lines and the exception type; inlined calls show as extra frames marked inlined. The Play bundles workflow uploads each app's mapping with the same no-secret behaviour.
- Two Android changes were needed for line numbers: the crash event now keeps each frame's file and line, and release builds keep `SourceFile,LineNumberTable` (the APKs grow slightly).
- Desktop: the installers are built with `packageMsi`/`packageDeb`/`packageRpm`, which run no ProGuard, so its stacks already read. The console labels them not obfuscated, and a CI test fails if the desktop release ever starts obfuscating.
- The console says per crash how its frames were read (dSYM, R8 mapping, not obfuscated) and names a missing mapping, in English and Arabic.
- T07: a cloud rehearsal scenario, `report-symbolicated`, runs the api role with a job-made token. The canary home sends an Apple and an Android report through its relay agent; they arrive raw before the upload, a wrong token gets 401, and both come back symbolicated after.

## What we learned
- A forwarded report goes from the home to its relay agent (`maktaba-cloudlink`), which posts it to the cloud API with the home's secret; it does not travel through the relay tunnel.
- The rehearsal cluster had no api role before; T07 adds one under a compose profile.
- The console listener refuses a sign-in without both the console's `Origin` and `X-Maktaba-Staff: 1`.
- The desktop build file said its installers used `packageRelease*`; the workflow uses `packageMsi`/`packageDeb`/`packageRpm` (the comment is fixed).

## Keep doing
- When a fix adds a matrix job, wait for that job by name (`need-check.sh`) before the merge watcher, or the watcher can merge before the job appears.
