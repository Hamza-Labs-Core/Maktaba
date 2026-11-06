---
schema: 1
kind: pr
pr: 673
title: Epic 53 stories 53.02 and 53.03 — the log keeper and /status.json
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- The supervisor's log keeper (`internal/logkeeper`) follows each child's append spool into hourly segments per service, compresses closed hours with zstd, and keeps them within retention and the diagnostics cap. Disk floors drop debug first.
- `/status.json` on the web port, built from the Epic 51 catalog's status groups, with two-check hysteresis and crashes shown at once.

## What we learned
- Children write to spool files, not pipes, so lines survive a supervisor handover. The keeper's offsets must be saved on every Flush (and at stop), or a restart reads the last line again.
- The janitor raced background compression of the same hour. A `zipping` set keeps it off a segment that is still being compressed.
- Keeper tests with production-sized data went over the 1.2 s per-test cap under `-race`. A configurable segment `maxSize` and small data keep them fast.
- A worker crash first showed as `degraded` after the two-check delay. The board keeps a `crashed` map so a crash shows at once.

## Keep doing
- Rebuild a never-pushed stacked branch as one clean commit (`git merge --squash` onto the previous PR's tip) rather than pushing wip commits.

## Stop doing
- Adding a CHANGELOG entry at the top of "Added". Parallel branches all edit there; the end of the section conflicts less.

## Open
- `server.log` still gets every line. Stop that once the Logs page (53.11) reads segments.
