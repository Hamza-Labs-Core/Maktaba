# Runbook: a disk is filling

> Alerts `disk_filling` (80 %), `disk_full` (90 %) (Epic 50 story 50.10).

`du -xh / | sort -h | tail -20`. The usual ones: Postgres's `pg_wal` growing because the WAL
archive fails (`docs/runbooks/cloud-backup-stale.md`), the logs (14 days kept, Epic 53.17), Docker
images (`docker image prune`). A node at 100 % stops Postgres: Patroni gives up the lease and the
other node leads; resize the volume in the Hetzner console before that.
