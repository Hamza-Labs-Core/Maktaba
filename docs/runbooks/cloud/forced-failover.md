# Runbook: the leader is sick but alive (forced failover)

> Epic 50 story 50.12. Proven in CI first: the cloud rehearsal's `runbook-forced-failover`.

## When

The leader still holds its lease but can't serve: a stuck disk, an out-of-memory loop, the relay failing on it alone. Alerts: `canary_down` with a leader that answers Patroni, `disk_full` on the leader, `cloud_error_spike` from one node.

## Check

1. The cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines): what the leader says.
2. `patronictl -c /etc/patroni/patroni.yml list`: the standby streams (a failover to a lagging replica loses the commits it lacks: only with synchronous replication intact).
3. The standby is healthy: its own `curl -s http://127.0.0.1:8081/` (its Caddy) and disk.

## Do

1. Actions → **Cloud failover** → Run workflow. Tick `stop the old leader` when the node is too sick to demote cleanly (its Patroni is stopped, so it can't lead again until started by hand). It runs `deploy/cloud-ha/runbook/failover.sh`.
2. If the workflow says the old leader still leads: power it off in the Hetzner console.
3. Fix the old node, then `sudo systemctl start patroni` on it: it rejoins as a replica.

## Done when

The other node leads and takes writes, the canary answers, and the old node neither leads nor serves.

## Tell users

[canary-down.md](canary-down.md)'s, if users saw it. A postmortem within 5 days.
