---
schema: 1
kind: pr
pr: 684
title: After the merge of #684 — reading start-failed.json on Windows, and a supervisor test's snapshot
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- On Windows CI, `TestCantStartStopsAndOffersSafeMode` read `start-failed.json` as soon as it existed and got nothing. The launcher writes it through `layout.WriteFileAtomic`, which uses `MoveFileEx(REPLACE_EXISTING | WRITE_THROUGH)`, so no reader ever sees half a file. But an open at the moment of the rename can fail (a sharing violation, a scan).
- Of the product readers, the launcher's page serves the record from memory, and its reader of the choice file already leaves an unparsable file for the next look. The desktop tray (`recovery::cant_start`) treated a failed read as "no record" and dropped its can't-start choices for one refresh. It now retries a few times, 20 ms apart, when the file is there but unreadable; a missing file answers at once (#690). The test waits until the record parses.
- `TestHaltedPrepareIsNotTriedAgain` (older than #684) read the halt's event from the snapshot that first showed the failed state. The supervisor sets the state under its lock and emits the event just after, so the test now waits for both (#691).

## What we learned
- An atomic replace on Windows guarantees whole contents, not a successful open. Every reader of a file another process replaces must treat a failed open as "try again", not as "absent".
- When a test reads one snapshot for two facts, check that both are published under the same lock.
