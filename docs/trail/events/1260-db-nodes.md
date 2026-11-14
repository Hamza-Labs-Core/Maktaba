---
schema: 1
kind: pr
pr: 1260
title: "The cloud database on its own nodes, behind PgBouncer (story 50.18)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1260 merged as 792229550. It adds three database nodes (Patroni with one synchronous
  replica, etcd and PgBouncer on each), the database's own Floating IP (`db.internal`), and a
  guarded move from the Phase 1 pair (`bin/migrate-db-nodes`, each step checks first and refuses
  with its reason). Nothing is deployed.
- The cloud now talks to the database through PgBouncer's transaction pool. relay.yml runs the
  integration suite a second time through it (`tools/ci/pgbouncer-ci.sh`); cloud-rehearsal runs
  `db-nodes-failover` and `db-nodes-migrate`.
- Measured in the rehearsals: 0 acknowledged commits lost, 0 relay requests failed when the
  primary was killed under load (new leader in 29–38 s, the Floating IP 6–7 s later). The whole
  move took 2 min 36 s, with writes held ≤ 2.5 s.

## What we learned
- lib/pq sends a query with arguments in two round trips, and a transaction pool can hand the
  second to another server connection ("bind message supplies 2 parameters"). Unnamed
  statements are not safe in transaction mode. `binary_parameters=yes` sends each query in one
  round trip; jsonb arguments must then be strings, since `[]byte` goes in binary.
- A session-level advisory lock is unsafe through a transaction pool; the migrator now holds
  `pg_advisory_xact_lock` in one open transaction.
- `patronictl switchover` reports Patroni's refusal with exit 0; parse its output.
- The pair's `restore-pitr` called `pg_switch_wal()` as the app role, failed unseen, and
  passed only on timing. Wait for the segment that holds the target instead.
- lint/repo caught two things a child missed: a new tracked script with no filter in
  `.github/ci-paths.yml` or entry in `NOT_IN_CI`, and early-exit readers (`| head -1`,
  `| grep -q`) in new scripts. Run `tools/ci/tests/test_no_head_pipes.py` and the ci-paths
  guard before pushing new shell scripts.

## Keep doing
- Run the integration suite through the real pooler, not just against Postgres: it found the
  two-round-trip bug that the spec had said could not happen.

## Open
- Owner actions: db-1/2/3 with `setup.sh --role db`, the PgBouncer certificate, the database
  Floating IP, firewall 6432, `db.internal` in /etc/hosts, the Prometheus targets, and
  `bin/migrate-db-nodes all --announced` after the status-page notice.
