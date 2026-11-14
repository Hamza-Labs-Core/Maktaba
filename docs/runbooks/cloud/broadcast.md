# Runbook: broadcasting is down or degraded

> Epic 50 stories 50.21, 50.28. The broadcast availability SLO is 99.9 % (README §4.2); the
> canary broadcast measures it. The edge is on Cloudflare, so this is rarely Hetzner.

## When

- `broadcast_canary_down` (**SEV1**): the canary broadcast fails from both networks twice in a row (60 s apart).
- `broadcast_slo_burn_fast` (**SEV1**), `broadcast_slo_burn_slow` (**SEV2**): broadcasting's downtime budget burns too fast.
- `broadcast_probe_silent` (**SEV2**): the broadcast probe has pushed nothing for 5 minutes (no measurement, not a success).
- `broadcast_ingest_lag_high` (**SEV2**): segments take over 3 s (p95) from upload to the playlist, for 10 minutes.
- `broadcast_revocation_list_stale` (**SEV2**): the edge's revocation list is over 10 minutes old, so a takedown may not reach it.
- `broadcast_glass_to_glass_high` (**SEV3**): the canary's glass-to-glass p95 over a day is over 20 s.
- `broadcast_cost_80pct` (**SEV3**): this month's edge estimate passed 80 % of `broadcast.monthly_budget_usd`.
- `broadcast_metering_mismatch` (**SEV3**): for a publisher's day, analytics and metering differ by over 2 %.

## Check

1. The cloud's broadcast lines in the service admin's cloud log view ([broadcast errors, last 30 min](https://admin.maktaba.tv/logs?q=broadcast&level=error&last=30m); the daily check's line: [metering mismatches, last 2 days](https://admin.maktaba.tv/logs?q=broadcast_metering_mismatch&last=48h)). The edge's own lines (`service = broadcast-edge`) arrive through Logpush on the witness.
2. **Both networks, or one?** `broadcast_probe_success` by `instance` on the witness's Prometheus. One network alone is that network; both is the edge or the canary home.
3. **Is the canary home publishing?** On W: `systemctl status maktaba-canary` and its broadcast log. A home that stopped uploading makes the edge show the offline slate: the probe then fails on a stale segment, not on HTTP.
4. **Is the edge answering?** `curl -sI https://live.maktaba.tv/` answers 404 from the Worker in milliseconds. A 5xx or a timeout: Cloudflare's status page and the Worker's dashboard (errors, CPU time).
5. **Ingest lag:** `maktaba_broadcast_ingest_lag_seconds` on the api's `/metrics`; slow R2 writes show as `storage_unavailable` errors in the edge's lines.
6. **The revocation list:** `maktaba_broadcast_revocation_list_age_seconds`; the cloud pushes the list (50.25) and records each accepted push.
7. **Cost:** `maktaba_broadcast_edge_cost_estimate_usd` against the Cloudflare bill (the model is README §14.4; the estimate counts viewer-hours and live channel-hours).

## Do

1. The edge deployed within the last hour: **Broadcast edge deploy** with `rollback: true` (docs/broadcast-edge.md, Rollback).
2. R2 delivery failing: switch to the Stream fallback (`BROADCAST_DELIVERY = stream`, or a plan push per publisher).
3. Abuse or a legal order needing everything off: `BROADCAST_GLOBAL_OFF = 1` (the break-glass variable).
4. The canary home down for maintenance: silence the broadcast alerts for the window (50.10's planned-work silence), as for the canary home.
5. The revocation list stale: push it again from the console; until it is fresh, a takedown also goes through `BROADCAST_GLOBAL_OFF` for that publisher's channels if it is urgent.
6. A metering mismatch: compare the day's `broadcast_usage_hourly` with `broadcast_usage_windows` for that publisher. Metering's truth is the edge's PublisherControl; analytics are a cross-check and are never billed from. A steady gap points at Analytics Engine sampling or a dropped hour (the rollup re-runs an hour on its next tick).
7. Cost past 80 %: check for a runaway channel (the rollup's segments and bytes per channel), then raise the budget or move delivery to a cheaper path.

## Done when

The canary broadcast plays from both networks for 10 minutes, the status page shows Broadcasting working, and the alert that fired is quiet.

## Tell users

Broadcasting down over 5 minutes: post [templates/investigating.md](templates/investigating.md) on the status page within 15 minutes ([status-page.md](status-page.md)), naming Broadcasting, then update every 30 minutes; [templates/resolved.md](templates/resolved.md) when done. Lag, cost and metering alerts are internal: nothing to post.
