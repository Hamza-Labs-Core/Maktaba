---
schema: 1
kind: pr
pr: 1324
title: "The database-node move waits for each node to serve after a change"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1324 merged as 27e693678. The `db-nodes-migrate` rehearsal failed on #1266 (which doesn't
  touch it) with `db-2 neither leads nor streams`: after the switchover db-2 was still
  restarting onto the new leader's timeline, the etcd change came straight after, and
  `retire` checked each node's role once.
- `deploy/cloud-ha/bin/migrate-db-nodes` gains `wait_until` (asks for up to WAIT seconds, then
  returns false, so each step keeps its own refusal text). `retire` waits for each database
  node to lead or stream with no lag; `backup` waits for a database node to lead after
  `retire` stops Patroni on the pair.
- `scripts-test.sh`: four cases (a replica that streams late or never; a leader that appears
  late or never), all failing on the old script. Local rehearsal: the move took 2 min 31 s,
  writes held ≤ 2.5 s, 0 of 2,415 requests failed, 0 of 1,534 commits lost.

## What we learned
- Right after a switchover a Patroni replica shows `running` on the old timeline until it
  restarts to follow the new leader; the cluster's state settles over seconds, not at once.
- Any check that follows a change to the cluster asks repeatedly with a bound, never once. A
  single-shot check there passes on fast runs and would stop the owner's real move half-way.
- A rehearsal that fails rarely on unrelated PRs is a product race until shown otherwise.

## Keep doing
- Stub the clock or count questions in script tests to make "later" deterministic.

## Open
- Nothing.
