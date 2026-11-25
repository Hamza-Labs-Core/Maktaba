---
schema: 1
kind: pr
pr: 1670
title: "A restore that cannot finish always has a safe way out"
date: 2026-11-25
session: 1d933885-c614-466d-9dec-f738a7b6a527
agent: claude-code
prs: [1605, 1698]
---

## What happened
- **Taken out of #1605 after its review (six majors), and designed again from one rule:** the record says what state the data is in (untouched, replaced, gone, not known), written before each step that changes it, and each way out (try again, another backup, give it up, a newer release) is allowed or refused by that alone.
- **"Untouched" is proven by the database's own identity** (the cluster, the database's oid, its public schema's oid): every release's restore makes another database or another public schema.
- **One lock** over asking, giving up and the start's own try; the record before the switch; one set aside for a restore; no backup removed until it is done (`backups/restore-hold`).
- **42 real-Postgres cases:** three kinds of Postgres, each stage a restore fails at, each way out.
- **The copy of what a restore replaces comes first, or the restore stops.** It goes on without one only when a person says so for that restore and that reason.
- **A restore that goes back to an older release** is refused up front unless that release's own copy fits with nothing removed. Only what the restore needs is linked out of its reach (`backups/being-restored/`).
- Four rounds of review: 6 majors in #1605's version, then 5, 3 and 3 here. The last three were all in going back to an older release.

## What we learned
- **A flag about the data is only as good as the oldest release that may touch it.** A record saying "untouched" is wrong as soon as an older release, which keeps no track, tries in between. Find something in the data itself that the action cannot leave unchanged, and compare that.
- **psql `--single-transaction` commits at the end of its input, wherever that is.** A server killed in the middle of a restore committed a part of the dump (shipped; proven by a test). The commit belongs in the script, as its last line.
- **`pg_restore` may end well on a truncated table** (PostgreSQL 18's). Its COPY's end is missing, and psql takes what follows for rows, the COMMIT too: the restore then said done with nothing committed.
- **A killed client is not a finished transaction.** Its server session, waiting for a lock, goes on and may commit later. End it and wait until it is gone before judging the data.
- **A shipped release cannot be taught a rule.** Each round added something for the older release to respect (a hold, a copy already there, a confirmation, links of every backup), and each found it does not. What works is to check, before handing over, that its own rules do no harm, and to refuse otherwise. That made the path smaller.
- **Links hold space.** Linking every backup kept them all, and left a home that could not free any by deleting backups. Link what the restore needs.
- **A restore's own copy can evict the backup it restores:** the room for a dump removes the oldest, and the newest of a kind stops being the newest once the new copy is written. Only a kind that copy is not of can be relied on.
- **A folder that is there is not a folder that is whole.** A release killed while it removed a backup leaves a part; "already there" then let the whole links be removed. Compare the files (`os.SameFile`), fill in what is missing, and leave one of another content alone.
- **A test rig that writes files in map order** makes "the newest" random: 4 failures in 20 runs.
- **Test the exits on the real thing.** The unit tests passed at once; the cut-rows case on 18, the hang and the kill each found a bug.

## Keep doing
- For every destructive step: what does the record say if the process dies right before, during and right after it?
- For a change an older release will act on: run that release's real code (a worktree at its tag) on the folder and the record.

## Stop doing
- Adding state for an older release to honour. Check its own rules up front, and refuse.

## Open
- The minors of the reviews: #1681. Nothing in a kept release says whether it knows the hold, so every release older than the one running is taken for one that does not.
- `--abandon` needs the running server.
- A restore on an external PostgreSQL 18 with pg_textsearch always fails (#1671).
