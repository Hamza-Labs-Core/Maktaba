---
schema: 1
kind: pr
pr: 1663
title: "A rollback's Down scripts never undo a schema an earlier rollback kept"
date: 2026-11-24
session: 1d933885-c614-466d-9dec-f738a7b6a527
agent: claude-code
prs: [1598, 1605]
---

## What happened
- **Data loss on the rollback path**, found in the review of #1605. `DownToFrom` (the rollback of a release that does not stay healthy) took the release before's newest migration as the target of the Down scripts.
  - On a database an earlier rollback had left on a newer schema (kept), that also undid the kept migrations and dropped what they held.
  - #1598 gave the failed-migrations path (`FailedBack`) the recorded slot (`schema_before`), but not this one.
- **The target is now the recorded slot**, never below it nor below the release before's newest. `Machine.migrateDown` reads it from the record (`layout.RecordSchemaBefore`).
- **An update an older release began** has no slot in its record: goose's own timestamps say what the database held when it started.
- **Rehearsal 03c and a real-Postgres test** start from a kept schema with rows written since.
- **What cannot be told runs no Down script:** a kept schema with no slot recorded, an unreadable record, a schema ahead that is not recorded as kept.
- **The schema the Down scripts leave stays kept** (`kept_for`) for the release before, in `MarkRecordDown` and in `backWithoutDump`. Both had dropped it, so the release before would have stopped with `db_ahead`.

## What we learned
- **Two paths decide the same thing.** A failed update (`FailedBack`) and a rolled back one (`DownToFrom`) each pick a target slot. A rule added to one needs a test on the other.
- **"The release before's schema" is not "the schema the release before ran on".** They differ after any kept rollback.
- **The right target hid a second fault:** with the old one the database went back to the release's own schema, so a lost `kept_for` never showed.

## Keep doing
- Test rollback decisions from a database already on a kept schema, not only from a release's own.

## Open
- A release between the kept one and the schema's release refuses a database whose record the Down scripts rewrote (`db_ahead`): the schema's own release is no longer named. The same holds after a restore.
