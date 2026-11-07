# Maktaba Cloud sizing

> Epic 50 story 50.14. What one relay node carries, **measured** on the owner's staging pair
> (`staging.relay.maktaba.dev`) by the load test (Actions → **Load test**,
> `cloud/cmd/maktaba-loadtest`): simulated homes holding their tunnels and players at a bitrate,
> ramped until a stop rule holds for 2 minutes. The last step before it is the node's capacity.

## The table

Each staging run's artifact has this file with its row; a pull request brings it here. Shared
vCPU plans vary from run to run, so a plan is run three times and the worst run kept.

| Plan | vCPU / RAM | Idle tunnels at stop | Relayed Mbit/s at stop | Concurrent 5 Mbit/s streams | CPU per 100 Mbit/s | Date, sha |
|---|---|---|---|---|---|---|

No plan has been measured yet: the staging pair doesn't exist. Until it does, the planning
figures are **300 Mbit/s** and **5,000 idle tunnels** per node (the epic README §4.2, §5). They are
what the witness's Phase 2 rules use (`RELAY_MEASURED_MBIT`, `RELAY_MEASURED_TUNNELS` in
`cloud-ha.env`); after a run, set them from the pair's plan's row
([docs/runbooks/cloud/capacity.md](runbooks/cloud/capacity.md)).

## Stop rules

A step fails, and the previous step is the capacity, when any of these holds for 2 minutes:
the relay's CPU over **70 %** of the machine; p95 added latency (time to first byte through
the relay minus straight to the simulated home) over **20 ms**; stalls (a segment arriving after
the next was due) over **0.5 %** of segments; any tunnel dropped that wasn't drained; memory
over **75 %**.

## In CI

The cloud rehearsal's `load-small` (1,000 homes, 50 players at 5 Mbit/s for 5 minutes, one node
on the runner) can't measure a Hetzner machine, so it checks regressions: the relay's
CPU-seconds per Mbit relayed and its memory per tunnel, against main's last run. More than 20 %
worse fails the pull request.

## The Phase 2 trigger

`relay_capacity_used` (relayed Mbit/s ÷ the measured figure) and `relay_tunnels_used` (open
tunnels ÷ the measured figure), on the witness. Above **60 %** at the daily peak for 7 days in a
row: start Phase 2 (story 50.16). Above **80 %** for an hour: a page, resize the pair now.

## What the owner sets up for a staging run

The staging pair (two nodes and the witness's role, the same plans as production, set up with
`deploy/cloud-ha/setup.sh` under `staging.relay.maktaba.dev`), deleted after the run (about
€2–5 at hourly prices). A `staging` environment with the secrets `STAGING_DB_URL` (the staging
cloud's database, reachable from the runner for seeding the simulated homes; through an SSH
tunnel or a firewall rule for the run), `STAGING_NODE_EXPORTER_URL` and
`STAGING_RELAY_METRICS_URL`, and optionally the variable `STAGING_RELAY_URL`.
