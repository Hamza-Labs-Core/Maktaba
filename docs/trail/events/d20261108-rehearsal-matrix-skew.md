---
schema: 1
kind: incident
title: A rehearsal of an older commit ran a scenario that commit does not have
date: 2026-11-08
session: a8e7d385-3ec1-4530-992e-133658e7a493
prs: [831, 963]
---

## What happened
- The release session found no tag due: the newest green commit on main was baef1a513, already v0.3.102. CI on 876fc8612 (#963) passed, and its update rehearsal (run 42447898488, a `workflow_run` started by that CI) failed.
- The failing row was 03b, `TestUpdateRehearsal03bRollbackDownScripts`, with `testing: warning: no tests to run`. The test was added by #831, merged as 1bb339e27 after 876fc8612. A `workflow_run` run uses the workflow file from main's head, so its matrix had the 03b row, while each job checks out the rehearsed commit (`ref: github.event.workflow_run.head_sha`), which has no such test. The step requires `--- PASS: <test>`, so the row failed and the verdict counted it.
- No release was lost to a real fault: 876fc8612 stays untagged, and the next commit whose CI passes (1bb339e27 or later) has the test and is rehearsed in full.

## What we learned
- A rehearsal row added to `update-rehearsal.yml` fails, for every commit older than the one that added it, any rehearsal whose CI finished after the row reached main. The failure says `no tests to run`, not a regression.
- Read a failed rehearsal row's test output before treating it as a fault: `no tests to run` means the matrix and the checked-out code disagree.

## Open
- The rehearsal could skip, as not applicable, a row whose test does not exist at the rehearsed commit (for example, `go test -list` before running it), so that a new row does not fail older commits.
