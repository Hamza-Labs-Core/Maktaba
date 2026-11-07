# Runbook: remote play is down (the canary or the API fails)

> Epic 50 stories 50.10, 50.12. Budget: 43.8 minutes a month (99.9 %).

## When

- `canary_down` (**SEV1**): the canary home fails from a network for 90 s.
- `slo_burn_fast` (**SEV1**), `slo_burn_slow` (**SEV2**): the month's downtime budget burns too fast.
- `api_down` (**SEV2**): sign-in is down while the relay may be up; with the Tunnel, see also [tunnel-down.md](tunnel-down.md).
- `held_requests_timing_out` (**SEV2**): homes are slow to come back after a move.

## Check

1. The cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines): the saved search `relay errors, last 30 min`.
2. **Is it us or the probe?** A real outage fails from both networks (the witness and GitHub's runners). One alone: look at that network first.
3. **Who leads?** `patronictl -c /etc/patroni/patroni.yml list` on A or B. A failover in progress takes up to 3 minutes by itself (`failover_happened` says when it started).
4. **Does the Floating IP point at the leader?** `fip_mismatch` says no: [hetzner-api-down.md](hetzner-api-down.md).
5. **Is the relay running on the leader?** `systemctl status 'maktaba-cloud-relay@*'`; `curl -s http://127.0.0.1:8443/readyz` (8453 for green). `draining`: a deploy is moving homes; `db_unreachable` after 5 minutes: the database is away too.
6. **Is the canary home itself up?** On W: `systemctl status maktaba-canary`.

## Do

1. A deploy within the last hour: [deploy-rollback.md](deploy-rollback.md).
2. The leader sick but alive (a stuck disk, an OOM loop): [forced-failover.md](forced-failover.md) (Actions → **Cloud failover**).
3. The Floating IP stuck: [hetzner-api-down.md](hetzner-api-down.md) (Actions → **Cloud DNS swap**).
4. Both nodes gone: [restore.md](restore.md).
5. The relay process down on a healthy leader: `sudo systemctl restart maktaba-cloud-relay@$(cat /opt/maktaba-cloud/colour)`.

## Done when

The canary answers from both networks for 10 minutes, the status page shows Remote access working, and `slo_burn_fast` is quiet.

## Tell users

Over 5 minutes: post [templates/investigating.md](templates/investigating.md) on the status page within 15 minutes ([status-page.md](status-page.md)), then update every 30 minutes; [templates/resolved.md](templates/resolved.md) when done. SEV1 longer than an hour: email paying users the same text. A postmortem within 5 days ([postmortem-template.md](postmortem-template.md)).
