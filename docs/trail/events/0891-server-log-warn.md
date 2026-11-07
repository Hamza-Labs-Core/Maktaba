---
schema: 1
kind: pr
pr: 891
title: server.log keeps the roles' warnings and errors only (Epic 53 story 53.02's rollout)
date: 2026-11-07
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [673]
---

## What happened
- Story 53.02 shipped the log keeper (v0.3.83) with `server.log` still getting every line for one release. #891 makes the switch the story planned: `[logs] server_log` now defaults to `"warn"`, and `"all"` brings every line back.
- Only the supervised processes' lines go through the keeper's echo. The supervisor's, the launcher's and the desktop agent's own lines are written directly, and those are the only ones the desktop app's `failure_in_log` and the launcher's `cantstart` read. No CI job or rehearsal greps `server.log` for a child's info line.

## What we learned
- A planned "switch after one release" is easy to forget: the open item sat in #673's event for ten releases. Story rollouts with a second step belong in an event's Open list, and someone has to check them.

## Open
- None.
