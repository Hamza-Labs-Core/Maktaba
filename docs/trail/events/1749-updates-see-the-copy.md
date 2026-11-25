---
schema: 1
kind: pr
pr: 1749
title: "An update sees a machine's running copy, and waits for it before its restart"
date: 2026-11-25
session: af4db1a0-1123-496c-869f-9a21bd3c8aad
agent: claude-code
prs: [1609, 1745]
---

## What happened
- **A correction of #1609's and #1745's events.** Both say an automatic update is held while a machine copies, and that the update waits for a copy before its migrations. Neither was so: they looked for an active slot of a table sync, and that slot is `active = f` during the whole `COPY` (it is active only while it is made and while the sync catches up). The wait also came after the restart of the bundled Postgres, which had ended the copy. #1745's review reproduced it on 16 and 18. No data was at risk: the machine copies that table again. #1667's first description of a hold without end (a stuck walsender's slot stays active) was wrong for the same reason.
- **The copy itself is looked for now:** `pg_stat_progress_copy` of a session of a machine's replication role. The wait is in the home's driver, before it applies the release; the new release's database step only pauses the copies.
- **A hold that works needs an end.** The blocker had never held anything, so its missing bound had never mattered. It is gone past after 20 hours.

## What we learned
- **A test that makes the state by hand proves the query, not the state.** #1609's tests made a slot and kept it active by holding a transaction open; a real subscription in its `COPY` has no active slot. A real subscription between two databases of one server, the subscriber's side held by a trigger on an advisory lock, shows it in five seconds.
- **A wait belongs where what it waits for is still alive.** Ask what ended it before the wait was reached: here the restart.
- **Disabling a subscription does not end a table sync in its `COPY`.** Its worker goes on; end the worker to model a machine that went away.
- **`pg_stat_progress_copy`'s pid and `pg_stat_activity`'s pid, database and user are readable by any role,** so the app role needs no `pg_monitor` for this.

## Keep doing
- Break the query on purpose and see the real test fail, before believing it.

## Open
- #1667: copies back up to 30 minutes late after an update, and keepalives on the machines' connections.
