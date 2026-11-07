# Runbook: a disk is filling

> Epic 50 stories 50.10, 50.12.

## When

- `disk_filling` (**SEV3**): 80 %.
- `disk_full` (**SEV2**): 90 %.

## Check

`du -xh / 2>/dev/null | sort -h | tail -20` on that node, and the cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines). The usual ones: Postgres's `pg_wal` growing because the WAL archive fails ([backup-stale.md](backup-stale.md)), the logs (14 days kept, Epic 53.17), Docker images.

## Do

1. The WAL archive: fix it ([backup-stale.md](backup-stale.md)); `pg_wal` shrinks as it catches up.
2. Docker images: `sudo docker image prune -f`.
3. Still filling: resize the volume in the Hetzner console. A node at 100 % stops Postgres: Patroni gives up the lease and the other node leads ([failover-check.md](failover-check.md)).

## Done when

The disk is under 70 % and not growing.

## Tell users

Nothing, unless it caused an outage.
