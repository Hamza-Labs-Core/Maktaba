# Runbook: no synchronous replica

> Epic 50 story 50.18.

## When

`sync_replica_missing` (**SEV2**): for 2 minutes no replica is synchronous. Commits are on the
primary alone: if it is lost now, the last commits go with it (RPO is not 0). With three
database nodes this needs both replicas down or behind; with the Phase 1 pair, the standby.

## Check

1. `patronictl -c /etc/patroni/patroni.yml list` on any database node: each replica's state
   (`streaming`, `starting`, `stopped`) and its lag. Whether the apps saw the database move
   meanwhile: the service admin's cloud log view ([api, last 30 min](https://admin.maktaba.tv/logs?service=cloud.api&last=30m)).
2. A replica rebuilt from a backup joins as asynchronous until it has caught up: Patroni makes
   only a caught-up replica synchronous. Its lag falling means wait.
3. Planned work on a replica: the other should have become synchronous. Both down at once is
   what to avoid.

## Do

1. A replica stopped: start it (`sudo systemctl start patroni` on it) and watch it stream.
2. A replica that can't catch up (`wal_keep_size` passed, a broken slot):
   `patronictl -c /etc/patroni/patroni.yml reinit maktaba <member>` rebuilds it from the primary.
3. A node lost: create its replacement and run `setup.sh --role db` on it; it joins as a replica.

## Done when

`patronictl list` shows one `Sync Standby` streaming with no lag, and the alert is quiet.

## Tell users

Nothing to tell: users see nothing while the primary runs.
