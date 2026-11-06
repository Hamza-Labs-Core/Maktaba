---
schema: 1
kind: pr
pr: 684
title: Epic 53 stories 53.04 and 53.05 — recovery page, safe mode, cut-offs
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- `/recovery`, served by the supervisor in English and Arabic. An admin gets in with a recovery pass or a tray code, and from there can see the status board and recent lines, restart a part, start in safe mode or roll back.
- Safe mode runs the database, the API, updates and the log index, for one start.
- A part that fails 3 times in a row is cut off. The launcher's can't-start cut-off adds `start-failed.json`, a loopback page and the desktop tray's choice.

## What we learned
- The recovery pass must be verified without the API: the supervisor reads `jwt-public.pem` and a revocation file that the API writes on logout, demotion and deletion.
- `takeChoice` removed the choice file on any read, so it could lose a choice it caught half written (an empty file). Under `-race` that made `TestCantStartRollbackByTheApp` flaky. A reader of a hand-off file must leave a file it cannot parse for the writer.
- Stacked branches rebuilt from wip commits: applying `git diff <old base> <old tip>` onto the new base with `git apply -3` gives far fewer conflicts than `git merge --squash` of a branch that carries older copies of the base.

## Keep doing
- Pipe test output through `tail` only with `set -o pipefail`, or check `$?` first. A `go test ... | tail` hid a failure before a push.

## Open
- The native Remote apps' recovery entry points (they open the web page for now).
