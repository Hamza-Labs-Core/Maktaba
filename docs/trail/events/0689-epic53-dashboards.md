---
schema: 1
kind: pr
pr: 689
title: Epic 53 story 53.12 — dashboards from this machine's metrics
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- The log index scrapes this machine's `/metrics` every 15 s and keeps an allow-list of series: by the minute for 7 days, by the hour for 30. It answers boards in one call.
- The API adds the Use board from `watch_sessions` (counts and titles only). The web draws each board in SVG over a shared range.

## What we learned
- The API's admin `/metrics` already merges the supervisor's, so one scrape covers the components and the API. Other roles' metrics are not on it yet.
- The Prometheus text format is simple enough to parse in ~60 lines, which kept `cmd/maktaba-server` free of the Prometheus modules. Labels are sorted into one key so a label set is one row.
- A board test that scraped over HTTP 720 times took 2.2 s under `-race`, over the 1.2 s cap. Scrape over HTTP once, then record the parsed samples directly.

## Open
- The pipeline's queue and processing series on `/metrics`, API latency histograms, per-machine host metrics, and the Remote apps' Overview and Storage.
