---
schema: 1
kind: pr
pr: 939
title: The Floating IP retry stops when the node no longer leads; a test leaked it for a day
date: 2026-11-08
session: c0d50e69-00c4-47e2-9b33-1ac69218ae34
prs: [928, 907]
---

## What happened
- **The leak.** Ten `bash …/t3-d76f2b8c/deploy/cloud-ha/bin/on-role-change on_role_change primary maktaba` processes had been running on the owner's Mac for 23–24 h, all orphans (parent launchd). Each was `on-role-change`'s background Floating IP retry, leaked by the `refused` case of `deploy/cloud-ha/tests/scripts-test.sh`, one per run in a rehearsal worktree.
  - The test's stub `sleep` returns at once, so during the test the loop spun hot.
  - The test's trap then deleted its stubs, and the loop ran on with the real `curl` and `sleep`, against `http://fake/v1` with a deleted token file.
  - They touched nothing live: they never reached systemctl or the Floating IP. They were killed, and that was confirmed.
- **The production bug behind it.** The loop stops only on a successful move. A node demoted after the retry window would keep trying, and could take the IP back to a replica. Each background try now asks Patroni's `/primary` (`PATRONI_URL`) and stops on a definite non-200 answer. With no answer (Patroni restarting), the retry goes on.
- The `refused` case now checks the retry's three states (leading, unanswered, demoted), and its trap kills any retry a failing run leaves.

## What we learned
- A shell test that runs a script which backgrounds a loop must stop that loop itself. A trap that only deletes the temp dir leaves the loop running against the real tools that the stubs were shadowing.
- Checking the new test against the old script leaked one more loop: the old script writes no pidfile for the trap to find. Kill what a red-before-green check starts, too.
- An env var longer than 128 KB fails on Linux (`MAX_ARG_STRLEN`) but not on macOS. A stub default (`CODE_AFTER`) beats a huge generated list.

## Keep doing
- After any local run of `deploy/cloud-ha/tests` or a rehearsal, check `pgrep -fl on-role-change` and kill leftovers.

## Open
- A scratch Postgres from the same rehearsal worktree's earlier session (port 55432, data in that session's scratchpad) is still running, with no clients. It is not the live install. It is that session's to stop.
