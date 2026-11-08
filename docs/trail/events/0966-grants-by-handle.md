---
schema: 1
kind: pr
pr: 966
title: A Windows runner's grant or revoke lands only on the folder that was checked (story 54.23)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [956]
---

## What happened
- #956 left a window between a runner's last path check and its grant: the grant was applied by name (`SetNamedSecurityInfo`), so a folder swapped for a junction in between would have taken the grant onto the junction's target.
- The launcher now opens each folder by handle (`FILE_FLAG_OPEN_REPARSE_POINT`), refuses a reparse point, checks that `GetFinalPathNameByHandle` names the checked path, and sets the DACL on that handle (`SetKernelObjectSecurity`). Revokes go the same way.
- It propagates inheritable entries to existing children itself: each child is opened relative to its parent's handle, without following reparse points, and the walk stops at any reparse point or protected DACL. A NULL DACL, or none, stops the grant.
- Windows CI swaps a checked folder (and an ancestor) for a junction to a decoy between check and grant through a test hook: the decoy gets no entry and the start fails closed.

## What we learned
- Read a descriptor as stored (`GetKernelObjectSecurity`), not through `GetSecurityInfo`: the latter infers inheritance on a DACL without `SE_DACL_AUTO_INHERITED` (every file in CI's temp folders, control 0x8004) and reported a child as protected once its parent had gained an entry.
- Go's `os.WriteFile` in CI's temp folders left files with explicit entries only; a fixture that tests inheritance creates its files with `CreateFile` and no security attributes.
- Asking Windows to keep `SE_DACL_AUTO_INHERITED` on a set rebuilds the inherited entries from the old DACL and drops the new one: the walk leaves each child's control bits as they were.
- Three rounds of DEBUG commits were needed before the walk's logging reached the job log: log from the test (`t.Log`), or return what was skipped, rather than writing from inside the code under test.

## Open
- Nothing for the grant window.
