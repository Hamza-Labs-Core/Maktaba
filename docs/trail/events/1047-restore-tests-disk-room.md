---
schema: 1
kind: pr
pr: 1047
title: The restore's tests no longer depend on the disk's free space
date: 2026-11-08
session: b7095d77-a4dd-4b22-9468-dcb6f082ae2b
---

## What happened
- `TestRestoreAskedPutsTheBackupBackBeforeAnyRole` failed on every run on the Mac home and passed on Linux CI. A restore asked by hand first sets aside the database it replaces (`setAsideReplaced`); that dump makes room through `pgbundle.MakeRoomWith`, which reads the real disk through `statfs` and wants 5% of it free (46 GB on a 926 GB Mac). On a fuller disk it returned a `NoRoomError`, the best-effort set aside was skipped, and the test's `asides` came back empty. CI runners always had 5% of their small disk free.
- Proven by patching `diskSpaceOf` to report 10 GB free of 926 GB: the target test failed the same way on any machine, and so did five `pgbundle` dump tests (`TestBackupKeepsSeven`, `TestBackupFailureLeavesNoPartial`, `TestDumpBeforeUpdate`, `TestPgDump*`), the failures #707 had recorded on that Mac.
- `makeRoom` is now a seam of the server's database step; the package's `TestMain` gives every test a disk with room, as it already fakes `portProbe`. `pgbundle`'s tests run on a fake disk with 1 TiB free unless they fake their own; reading the real disk keeps one test of its own.
- `TestRestoreAskedGoesOnWhenTheReplacedDatabaseHasNoRoom` pins the no-room case: the set aside is skipped with a warning (the log line is now `level=warn`) and the backup is put back all the same.
- The `launcher (macos-latest)` leg now runs the restore's tests and `pgbundle`'s room and dump tests; no job ran them on macOS before. Merged as 38ade4ba7 on head 0dc9ef933.

## What we learned
- The code was right; the tests assumed the host had room on its disk. A test that passes on CI and fails on one machine is first a question of what that machine has that the runner lacks (disk, ports, clock), before it is a flake.
- A best-effort step that skips itself quietly hides a test's real precondition: the test saw empty `asides`, not "no room".

## Keep doing
- Fake host resources (disk room, free ports) in `TestMain` for every test, and give the real read one test of its own.
- Reproduce a host-only failure on any machine by patching the one host reading, then prove the fix by removing it again.
