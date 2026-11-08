---
schema: 1
kind: incident
title: Old backups were pruned only at an update, and a lone failed dump stayed for good
date: 2026-11-08
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
---

## What happened
- The home's backups folder on Big held about 290 GB: seven 26 GB nightly dumps, three pre-update dumps, a 33 GB dump of the failed 0.3.36 update from October 27, and a one-off dump from before the app install.
- The nightly dumps were pruned to [backups] keep after each dump. The pre-update, failed and replaced dumps were pruned only when an update took its dump, and by count only, so the single failed dump would have stayed until two more updates failed.
- On request the old ones were deleted by hand, keeping the newest nightly and the newest pre-update dump: 51 GB left.

## What we learned
- A rule that runs only on a rare event (an update) is not a retention rule. Pruning belongs in the loop that already runs every 15 minutes.
- Count-only retention never empties a kind that rarely gets new members. Dumps that explain one event (a failed update, a replaced database) need an age limit; the newest pre-update dump is the way back and must not have one.

## Keep doing
- Check a folder's size against its rules when reporting disk space: `ls -l` shows a directory-format dump as a few KB, `du -sh` shows its real size.

## Open
- Nothing.
