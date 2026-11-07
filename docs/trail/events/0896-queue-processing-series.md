---
schema: 1
kind: pr
pr: 896
title: Epic 53 story 53.12 — the Queue and Processing boards get real series; /metrics serves the plugins' metrics
date: 2026-11-07
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [888]
---

## What happened
- The Queue and Processing dashboards were always empty. `dashboard-series.json` listed six series that nothing exported. The API now reads `processing_jobs` at most once a minute (`queues.QueueCollector`, on the home only) and exports four series: `maktaba_pipeline_queue_depth`, `maktaba_pipeline_jobs_last_hour`, `maktaba_pipeline_audio_seconds_last_hour` and `maktaba_pipeline_realtime_factor`. The allow-list and the boards now name these four.
- **Bug found on the way:** the API's `/metrics` served only `shared/metrics`' own registry. The plugins' metrics (the plugin API's calls, refusals and statements, and the health rows) register on `prometheus.DefaultRegisterer`, so they never showed. `metricsHandler` now also gathers the default registry's `maktaba_*` families that the process registry lacks.
- #888's Android job failed because the player's `CastTest` compared the cast URL exactly, and the playlist now carries `rid`. The tests now check the URL with `rid` taken out, and check that `rid` is a UUID.

## What we learned
- Before a dashboard lists a series, check that something actually exports it. A name in the allow-list proves nothing. Test the board from a scrape of the real exporter's names.
- In Go, two registries means two places a metric can hide. When a package registers on `prometheus.DefaultRegisterer`, check that the served gatherer includes it. A family in both registries fails the whole scrape, so gather the second registry without the families the first already has.
- A change to a URL that several clients build reaches every test that compares that URL exactly: the cast tests as well as the player tests. Search all of a module's tests for the old exact URL before pushing.
- The affected-tests gate blocks a change to `api/main.go` until a package-`main` test changes. Writing that test found the registry bug.

## Open
- None for 53.12.
