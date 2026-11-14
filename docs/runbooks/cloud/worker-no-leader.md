# Runbook: no api node runs the worker's jobs

> Epic 50 story 50.17.

## When

`worker_no_leader` (**SEV2**): every api node's worker reports `worker_leader 0` for 2 minutes.
The expired-token cleanup, the account-deletion purges, the relayed streams' sweep, the
pay-as-you-go reports and the direct-access jobs wait until a node holds the lock again.
Nothing users see breaks at once: the jobs are catch-up work and each is idempotent.

## Check

1. The workers' lines in the service admin's cloud log view ([worker, last 30 min](https://admin.maktaba.tv/logs?service=cloud.worker&last=30m)): `leader_acquired`, `leader_released` (with its reason) and `leader_holder_ended`.
2. Who the database says holds the lock: `SELECT node, backend_pid, heartbeat_at FROM cloud_leaders WHERE lock_id = 8472613;` and whether that backend is alive (`SELECT pid, state, backend_start FROM pg_stat_activity WHERE pid = <backend_pid>;`).
3. Whether the workers reach the database at all (`database_down`, `api_down`, the Postgres runbooks).

## Do

1. The database is away: follow [failover-check.md](failover-check.md); the workers take the lock again by themselves within 15 s of its return.
2. A holder whose heartbeat stopped (a hung process) is ended by another node after 60 s. If it isn't: `SELECT pg_terminate_backend(<backend_pid>);` and restart that node's worker (`sudo systemctl restart maktaba-cloud-worker`).
3. Every worker stopped: `sudo systemctl start maktaba-cloud-worker` on each api node.

## Done when

`worker_no_leader` is quiet and one node's `/metrics` shows `worker_leader{node="…"} 1`.

## Tell users

Nothing to tell: no feature users see depends on the jobs from one minute to the next.
