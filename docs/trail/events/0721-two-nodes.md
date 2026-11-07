---
schema: 1
kind: pr
pr: 721
title: Two nodes, one address: Patroni, etcd and the Floating IP, rehearsed with kill and partition
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [720, 715, 714]
---

## What happened
- Story 50.07: `deploy/cloud-ha` (Patroni, three-member etcd with TLS, the role-change callback moving the Hetzner Floating IP, a leader guard, the switchover, setup.sh) and the rehearsal's failover scenarios on three containers built from those files. Kill: B led in 38 s, play back in 47 s, 0 commits lost. Partition: never two writers, relay off in 20 s. Switchover: 2 s pause.

## What we learned
- A partitioned primary demotes only after its WAL senders give up: with the default `wal_sender_timeout` (60 s) the demotion took 43 s, and Patroni's `on_role_change` fires after it, so the cloud kept running on the cut-off node. `wal_sender_timeout: 10s` and a guard that watches `/primary` stop it in 20 s.
- Postgres reports a commit done when a client cancels it while it waits for the synchronous standby ("canceling wait for synchronous replication"): only the primary has it, and a failover loses it. A rehearsal writer with a 2 s deadline "lost" nine acknowledged commits that way; RPO 0 holds only for commits that were not cancelled.
- Patroni 4's `patronictl list -f json` reports `Receive Lag` and `Replay Lag`, not `Lag in MB`; its REST API answers `/leader`, `/sync` and `/primary` with 200 or 503, the cheapest role check from outside.
- Moving a Docker network alias means disconnecting and reconnecting a container: done on the private network it breaks etcd and triggers failovers. The Floating IP gets its own network.
- A stale debug cluster kept its networks' subnets, and the next project's network creation failed ("Pool overlaps"): `docker compose down` needs the same required variables as `up`.
