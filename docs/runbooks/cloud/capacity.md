# Runbook: the relay is filling its node

> Epic 50 stories 50.14, 50.16. The measured capacity is in [docs/cloud-sizing.md](../../cloud-sizing.md).

## When

- `relay_start_phase2` (**SEV3**): the daily peak passed 60 % of one node's measured capacity
  (relayed Mbit/s or open tunnels) for 7 days in a row.
- `relay_resize_now` (**SEV2**): above 80 % for an hour.

## Check

1. The cloud's dashboards in the service admin view (Epic 53.17) and the witness's Grafana:
   `relay:mbit_per_second`, `relay_capacity_used`, `relay_tunnels_used` over the last 30 days.
2. The measured figures the rules use: `relay_measured_mbit_per_second` and
   `relay_measured_tunnels` on the witness (`/var/lib/node_exporter/textfile/relay_capacity.prom`).
   The planning figures (300 Mbit/s, 5,000 tunnels) are only placeholders until a staging run;
   if the sizing table has a row for the pair's plan, use it.
3. Is the growth real (more paying homes), or one home (a viral link: [abuse-ddos.md](abuse-ddos.md))?

## Do

1. **`relay_resize_now`**: resize the pair to the next plan during a planned switchover
   ([switchover.md](switchover.md)): post the maintenance, switch over, rescale the standby in the
   Hetzner console, switch back, rescale the other. Then run the load test against staging on the
   new plan (Actions → **Load test**) and update `RELAY_MEASURED_*` in the witness's `cloud-ha.env`.
2. **`relay_start_phase2`**: start Phase 2 (story 50.16, a pool of relays): it takes weeks, which
   is why it starts at 60 %.
3. After a staging run, update the witness: `RELAY_MEASURED_MBIT` and `RELAY_MEASURED_TUNNELS` in
   `/etc/maktaba/cloud-ha.env`, then `sudo deploy/cloud-ha/setup.sh --role witness` (it rewrites
   the textfile).

## Done when

The pair is on the larger plan and `relay_capacity_used` is under 50 % at the daily peak, or
Phase 2 is planned and scheduled.

## Tell users

Only the maintenance notice for the resize ([templates/maintenance.md](templates/maintenance.md)).
