---
schema: 1
kind: incident
title: "A home's claim raced its owner's plan change, and a suspended owner's home kept its tunnel: both from the review of #1627"
date: 2026-11-24
session: 72be8c47-3733-4379-9431-ce8fcdcdab2c
agent: claude-code
prs: [1627, 1661]
---

## What happened
- #1627 (a claimed home takes its owner's plan) copied `users.plan` and
  `subscriptions.extra_streams` without a lock. Its review found two things in it, and three older
  ones next to it:
  - **A claim during a plan change left the new home on the old plan or extra streams.** The
    writer's `UPDATE servers` could not see a home inserted after its snapshot, and the claim could
    not see an uncommitted plan. Fixed with one lock on the account's row (`stores.LockAccount`,
    `FOR NO KEY UPDATE`), taken first by the claim and by every plan writer.
  - **A suspended owner's claim code worked.** It is now refused as an invalid one is, unspent.
  - **A suspended owner's home kept its tunnel and could dial again with its secret**; only a resume
    ticket checked the owner. #1661 refuses the bearer and closes the live tunnel: the database
    already sent `server_changed` on a suspension, and nothing but the cache listened.
  - **A lost redeem answer spent the code.** #1661: the agent's retry key (HMAC of the code under its
    own key) redeems it again, until the home connects or the code expires.
  - **`MaxServers` is enforced nowhere.** The specs disagree on the number: decision
    `cloud-homes-per-account`.
- CI's affected-tests gate looks for a changed test in the changed file's own package. The races
  were tested through the real routes in `cmd/maktaba-cloud`, so it asked for one in
  `handlers/billing` and `handlers/servers`; each got a test of its own part.

## What we learned
- A copy of another row's value at insert needs the same lock as the writer that later fans it
  out. Under READ COMMITTED the values must be read in a statement after the lock's: the lock's own
  statement still sees the other tables as they were.
- A race test is deterministic when a third transaction's row lock stops one side in the middle
  (`pg_stat_activity` says when it waits). All 12 orders failed on the old code at once.
- The app's bundled PostgreSQL runs the cloud's integration tests locally without Docker:
  `initdb` in a scratch folder, `-c unix_socket_directories=''`, and `MAKTABA_CLOUD_DB_URL`.

## Keep doing
- Check a rule (here: the owner is active) at every door, and at the moment it changes for what is
  already inside.

## Open
- Decision `cloud-homes-per-account`.
