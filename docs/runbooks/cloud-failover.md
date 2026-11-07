# Runbook: the database leader changed, or the replica is gone

> Alerts `failover_happened`, `replica_missing`, `replication_lag` (Epic 50 stories 50.07, 50.10).

- **`failover_happened`**: confirm the new leader serves: `patronictl list` (a leader and, once
  the old node is back, a streaming replica), the canary answers, `fip_mismatch` is quiet. Then
  find out why the old leader stopped (its `journalctl -u patroni`, the Hetzner console).
- **`replica_missing`**: the leader takes writes alone (RPO is no longer 0). Start the replica's
  Patroni (`systemctl start patroni`); a node that cannot rejoin is rebuilt with
  `setup.sh --role a|b` after its data directory is moved away (Patroni re-clones it).
- **`replication_lag`**: usually a burst of writes or a slow disk on the replica; it catches up by
  itself. Lag that grows for minutes: the replica's disk or network.
- A node rebooting in a loop: set its `nofailover` tag (`patronictl edit-config`) while it is looked at.
