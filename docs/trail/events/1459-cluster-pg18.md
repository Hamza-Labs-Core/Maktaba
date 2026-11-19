---
schema: 1
kind: pr
pr: 1459
title: "Machines follow a home on PostgreSQL 18, and move their copy to 18 with their own update"
date: 2026-11-19
session: 753035df-3990-44b1-9dff-f305bbe9c3cd
agent: claude-code
prs: [1445]
---

## What happened
- #1459 merged as 2c87ae8f9. Epic 49 story 49.3.
  - A machine's copy runs on a Postgres it has of either major the payloads carry, 16 or 18
    (`serve_copy.go`). It is kept where a home of that major keeps its cluster
    (`pgbundle.DataDirFor`).
  - A cluster in the other major's directory is set aside (`<dir>.pg<major>-<time>`), never
    deleted.
- Proof, in the replica job:
  - `TestTwoMachinesOnePC` with the home on 18, the machine on 16 and moving to 18 within the
    run: its 16 copy is set aside, the fresh 18 copy follows, and the home-gone checks run on it;
  - the machine on 18 from the start;
  - a home on 16 with its machine on 18;
  - `TestSwitchTheHome` with every machine on 18.
- GLM-5.3's first review:
  - B1 (blocker): the copy always lived in `<state>/postgres`, so a machine whose copy ran on
    18 and was made the home looked for its cluster in `postgres-18` and restart-looped. This
    PR caused it: before it, machines always ran a fetched 16 copy. Fixed.
  - M1 (major): the upgrade was only modelled across two runs. Fixed.
  - Four minors, all fixed.
- The second review found no blockers or majors; its minors are issue #1473.

## What we learned
- A machine's directories are a home's directories in waiting. Anything a switch may promote
  must sit where that home will look for it, and nothing stale may sit where it might look.
- A switchover e2e whose machines share one `MAKTABA_TEST_PG_BIN` covers each major only
  when run on each.
- The e2e's `browser.do` fails the test on a connection error. Poll with `http.Get` across
  a restart, and start a machine meant to outlive a subtest on the parent test's `t`: a
  subtest's cleanup stops it.
- `pull_request` workflows don't run while a PR conflicts with main. On a busy day the
  epic-status totals line conflicts after nearly every merge.

## Open
- Issue #1473: set-aside clusters and storage moves; an ex-home's 16 cluster; the
  backups-folder check.
