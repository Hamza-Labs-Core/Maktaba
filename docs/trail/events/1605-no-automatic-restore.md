---
schema: 1
kind: pr
pr: 1605
title: "A failed update never puts the backup back by itself: it stops and asks (held)"
date: 2026-11-24
session: 1d933885-c614-466d-9dec-f738a7b6a527
agent: claude-code
prs: [1598, 1601]
---

## What happened
- **Research item 1** (`docs/research/update-process.md`, #1601), built on #1598's branch. The PR targets main and carries #1598's commits, so its CI runs.
- **The paths that put the dump back by itself:**
  - the database step's three cases that #1598 left automatic (`db_down_failed`, lossy, unknown);
  - `updater.Machine.rollback` (`MarkRestore` then `RestoreBack`);
  - `cameBackOld`;
  - the launcher's `markRecord` (P3).
- **All of them now mark the record `held` instead:**
  - The database stays as it is.
  - The previous release is switched back in.
  - Every start stops with `db_held` until a person puts the backup back (`/recovery` or `maktaba-server restore`) or installs a newer release, which goes on from the held database.
  - A `restoring` mark with no `by`, written by an older release or its launcher, is held too (`db_held_unasked`).
- **Words:** `db_held` has words on web, Apple and Android. The recovery page shows a held banner with a button for the named backup.
- **Update rehearsal 04–06:** they now expect the hold and then play the person's restore.

## What we learned
- **Why `held` keeps the failed release's version:** every release from before the change refuses the database as older (`db_refused_older`) instead of restoring it. The new state stops even code that was never taught it.
- **A restore asked by a person** (`restorer.mark`) builds a fresh record, so the dump's kept-schema marker (`dump_kept_for`) was lost on that path. It must be carried over from the held record, or the release kept for the schema halts with `db_ahead` after the restore.
- **Layout installs (deb/rpm, self-managed) had no Down-script path at all.** `LayoutInstaller` implemented neither `KeepsSchema` nor `DownTo`, so `migrateDown` returned false silently and the whole dump was their only way back. The auto-update e2e's 0.9.2 (a lossless migration) stopped and asked once restoring was gone. It now reads the release before's migrations from `bin/releases/<from>/`.
- **A newer release goes on from a held update.** So a synthetic restore mark for a release *older* than the one starting (rehearsal 07's `0.0.1`) no longer halts anything. The rehearsal now names a newer failed release.
- **While the library is halted,** the api (and so the Updates page) is not running. Only the supervisor's `/recovery` page and the start page reach the person, so the choices belong there.

## Keep doing
- When removing an automatic action, check what the *older* releases do with the new record. The only code guaranteed to read it is the newest.

## Open
- Release-before-the-fix paths are unchanged:
  - Layout installs keep their old launcher, which still writes `restoring`. Only a release with this change holds it.
  - An update *to* a release from before this change runs that release's own database step.
- Merge order: #1598 first, then #1605.
