---
schema: 1
kind: pr
pr: 801
title: Plugin job metrics, and problem reports keep plugin logs out (Epic 54, 54.09 §3, §4)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
---

## What happened
- The home's API exports `maktaba_plugin_jobs_total`, `maktaba_plugin_job_seconds` (p50, p95 of the last 20 jobs) and `maktaba_plugin_memory_peak_bytes` from `plugin_health` at each scrape; a problem report drops a plugin's `plugin.<id>` lines unless an admin ticked **Include its logs in problem reports** on its page (`PUT /api/plugins/{id}/report-logs`). Closes #711.

## What we learned
- The pipeline has no Prometheus endpoint, but the breaker already writes every plugin job's outcome to `plugin_health`: a collector that reads those rows at the scrape gives the metrics without a second path. Its job time is a gauge per quantile over the row's window, not a histogram: the rows keep 20 outcomes, not buckets.
- Only the home registers the collector; every joined machine reads the same rows and would export them again.
- The plugins' admin routes have a test that counts them (`TestEveryPluginRouteIsAdminOnly`): a new route goes into its list.
