---
schema: 1
kind: incident
title: "The live home's 0.3.157 restore was stopped before it could drop the real database, and v0.3.161 was installed through the app's own order: schema 236 in 17 s, no dump put back"
date: 2026-11-24
session: 56b0186a-7446-4a5f-9c22-07bd7eb25454
agent: claude-code
prs: [1538, 1592]
---

## What happened
- **The state at 12:30Z.** v0.3.157's database step (run f18be9ee) had failed 0235 on the
  `CREATE INDEX CONCURRENTLY` lock timeout at 08:20Z, and was putting the 34 GB dump back into
  `maktaba_restoring` (14 workers).
  - From 08:32Z the semantic search's HNSW index was built alone (`maintenance_work_mem`
    32 GB, 7 workers). After 4 h it was at 87% of 3.76 M blocks, moving about 1.3 blocks/s
    (IO bound, `DataFileRead`).
  - The API had been down since 08:12Z.
  - At its end the restore's `swapIn` renames `maktaba` to `maktaba_replaced` and drops it.
    That would have lost 0231–0234 and the LAN machine's writes since the 08:11Z dump.
- **Why a plain stop would not have stopped it.**
  - The record (`database-pg18.json`) said `restoring`. Every release's database step
    (0.3.153, 0.3.157, 0.3.161) finishes such a record before anything else ("a putting back
    cut short: finish it first"), so the next start would restore again from the beginning.
  - The launcher's guard (`pending.json`, naming 0.3.157) falls back to 0.3.153 when its
    deadline lapses, and marks the record `restoring`.
  - There is no command to cancel a restore. The record is "put right by hand".
- **What was done, 12:46–12:52Z.** The plan and every command were written first into the
  home's `updates/manual-fix-2026-11-24.md`, and every state file was copied aside.
  1. **Staged.** A hand-written order in `updates/app-install.json` (state `ordered`; feed = the
     release's own `latest.json`), in the format the home's updater writes, made the app stage
     v0.3.161 with its own minisign check. The same bytes were verified independently: key
     FE28E0D64F73E05E as stamped into the installed app, Developer ID codesign, stapled
     notarization.
  2. **Guarded.** `ALTER DATABASE maktaba_restoring WITH IS_TEMPLATE true`. A stopped restore's
     cleanup drops `maktaba_restoring`, and a new restore drops it first (`makeRoomBeside`).
     PostgreSQL refuses to drop a template database before it ends any session, so neither
     could run, and no restore could reach `maktaba`.
  3. **Record reset.** The record went back to the update's own `migrating` record (version
     v0.3.157, from v0.3.153, the pre-update dump). `pending.json` was moved aside.
  4. **Installed.** `CHECKPOINT`, then the order's state set to `go`.
     - The app stopped the server (`launchctl kill TERM`). The restore's cleanup logged
       `cannot drop a template database`, and the whole stack was down in 10 s.
     - The app swapped in v0.3.161, started the agent and relaunched.
  5. **v0.3.161 started.**
     - Its updater ended run f18be9ee `apply_failed / replaced_by_newer`.
     - The database step went on from `migrating` with no new dump and ended 19 sessions.
     - `migrate up` dropped 0235's two INVALID leftovers, then ran 0235 (0.9 s) and 0236.
       Schema 236 at 12:49:19.9Z, 17 s after the stop.
     - The launcher cleared the 0.3.153 payload override by itself.
- **Verified:**
  - `/api/system/health` answers 200; `/api/system/version` says v0.3.161, schema 236.
  - No INVALID index remains. 65 libraries and 148,592 videos are intact.
  - Real clients' session refresh, home, posters, channels and play queue answer 2xx.
  - `IS_TEMPLATE` was set back to false. `maktaba_restoring` (82 GB) and the dumps are left for
    the owner to decide on.
- **The LAN machine's copy.** All three of its slots were `lost` (`max_slot_wal_keep_size`
  20 GB, overrun by the restore's WAL). v0.3.161 logged `cluster_copy_lost` and dropped them.
  The machine copies anew when it next asks.

## What we learned
- **Stopping the server does not stop a restore in progress.** A record that says `restoring`
  makes every release's next start restore again from the beginning. Put the record right
  before any start.
- **Kill the server, never `pg_restore` or the build's backend.** 0.3.157's restore retries a
  failed run with one worker (a fresh `maktaba_restoring`). A failed vector index build is also
  tried twice more (0 workers, then the server's settings). Only a cancelled context, a stop of
  the server, ends them without starting over.
- **This home's HNSW index can't be rebuilt in hours.** Once the graph outgrew 32 GB of
  `maintenance_work_mem`, the build crawled at about 1 block/s. At that rate its last 13%
  would have taken about four more days.
- **`IS_TEMPLATE true` is a reversible guard** that makes every `DROP DATABASE` of that
  database fail before it ends anything.
- **The app's order file is the supported way to install a named release** on an app-bundle
  home whose updater is stuck in a run. The app checks the feed and the signature itself, and
  the new release's `Resume` closes the stuck run as `replaced_by_newer`.
- **A hand install leaves the live TV slots on the kept payload.** It has no TV change, so the
  slots run the kept payload's streaming binary (0.3.153) until the next update's TV change.

## Keep doing
- Before stopping anything on the live home, read both the running release's code and the
  target's for every state file it touches: record, guard, journal, order.
- Write the plan and the commands into the home's updates folder first.
- Verify a release's bytes independently even when the app verifies them too.

## Stop doing
- Treating "the next start finishes it" as harmless during an incident: it restarts the very
  thing being stopped.

## Open
- v0.3.161 cannot see new releases (`larger than 8388608 bytes`). #1592 is merged but in no
  release yet.
- The Down-first way back (#1598) is still open.
- `second_listen` has failed since 2026-11-20 on its `transcript_segments` update: "Replica
  identity must not contain unpublished generated columns", while the copy's publication
  holds that table. No issue filed.
- The LAN machine had not started its new copy by 12:53Z.
- Whether to delete `maktaba_restoring` and the pre-update dumps is the owner's decision.
