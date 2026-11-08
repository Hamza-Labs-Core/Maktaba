---
schema: 1
kind: pr
pr: 954
title: Spec 53.12 states the label caps as built
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
---

## What happened
- Story 53.12's text said label values per series are capped at 50 and `id` series are bounded by the catalog. Since #915 the home's `/metrics` carries every joined machine's component and host series. The spec now says what `logindex` does:
  - the allow-list lives at `shared/logkeeper/go/logindex/dashboard-series.json`;
  - label sets per series are capped at `label_cap` (50), and a sample past a cap is dropped and counted in `maktaba_metric_dropped_total`;
  - an `id` series is capped at `id_label_cap` (200) for each machine, for up to `label_cap` machines.

## What we learned
- The Arabic store keeps only a story's opening excerpt (about the first 600 characters), so editing a story deep in its text needs no new Arabic; `generate_wiki.py --check` confirms it (1431 of 1431).
