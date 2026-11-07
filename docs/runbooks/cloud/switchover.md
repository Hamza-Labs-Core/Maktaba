# Runbook: planned node work (switchover)

> Epic 50 stories 50.07, 50.12. Proven in CI first: the cloud rehearsal's `runbook-switchover` runs the same script against the rehearsal pair.

## When

Work on the leading node: an OS update, a resize, a reboot. Never for an emergency without `announced` unticked.

## Check

1. Maintenance is posted at least 48 hours ahead ([templates/maintenance.md](templates/maintenance.md), [status-page.md](status-page.md)).
2. `patronictl -c /etc/patroni/patroni.yml list`: the standby streams with zero lag.
3. The cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines): no error spike on either node.

## Do

1. Actions → **Cloud switchover** → Run workflow (`announced` ticked). The production environment's reviewer approves. It runs `deploy/cloud-ha/runbook/switchover.sh`: `bin/switchover`'s checks, then Patroni's switchover; the Floating IP and the relay follow the new leader, and homes move their tunnels (DRAIN).
2. Work on the old leader, now the standby.
3. Switch back if wanted: run **Cloud switchover** again.

## Done when

The workflow ends with "done: <node> leads", the canary paused at most 15 s, and `patronictl list` shows the old node streaming again after the work.

## Tell users

The maintenance notice, posted 48 hours ahead; set it `completed` when done.
