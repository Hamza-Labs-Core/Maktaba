# Runbook: check an automatic failover

> Epic 50 stories 50.07, 50.12.

## When

- `failover_happened` (**SEV2**): Patroni moved the leader by itself.
- `replica_missing` (**SEV2**): the leader takes writes alone (no failover is possible, RPO no longer 0).
- `replication_lag` (**SEV3**): the replica is behind.

## Check

1. The cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines): the old leader's last lines (Patroni, the relay, the kernel).
2. `patronictl -c /etc/patroni/patroni.yml list`: a leader, and once the old node is back, a streaming replica.
3. The canary answers (the status page, or `curl -fsS https://canary.relay.maktaba.tv/api/health`); `fip_mismatch` is quiet.
4. The old leader is fenced: it doesn't lead (`curl -s http://<its private address>:8008/primary` answers 503) and its relay is stopped (the leader guard).

## Do

1. **The old node is back**: Patroni rejoins it as a replica by itself (pg_rewind). Nothing to do.
2. **It can't rejoin**: move its data directory away and start Patroni again (`sudo systemctl start patroni`): it re-clones from the leader. A node that won't come up is rebuilt with `setup.sh --role a|b`.
3. **It reboots in a loop**: set its `nofailover` tag (`patronictl edit-config`) while it is looked at.
4. **Replication lag that grows for minutes**: the replica's disk or network ([disk.md](disk.md)).

## Done when

`patronictl list` shows a leader and a streaming sync standby with zero lag, and `replica_missing` is quiet.

## Tell users

Nothing if the probe saw no outage. If it did, [canary-down.md](canary-down.md)'s. Every automatic failover gets a postmortem within 5 days ([postmortem-template.md](postmortem-template.md)).
