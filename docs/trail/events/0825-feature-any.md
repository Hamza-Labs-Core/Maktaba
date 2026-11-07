---
schema: 1
kind: pr
pr: 825
title: feature_any, a feature's groups where any member is enough (story 50.35)
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [808]
---

## What happened
- Story 50.35: the catalog's top-level `feature_any` names, per feature, groups of components that count as one (on when any member is on). Both loaders, Go and the pipeline's, read and check it from shared fixtures, and both dumps carry it. `features.go` applies it, and the plugin merge keeps it.
- It ships as `feature_any = {}`, so no capabilities answer changes. Story 50.15 part 2 adds `remote_access = [["cloud.relay", "cloud.direct"]]` with `cloud.direct`.

## What we learned
- 50.15 depends on 50.35 (the story says so), so 50.35 went first, out of the listed order.
- A catalog key goes in five places: `topKeys`, `rawCatalog`, the plugin merge's `Catalog{}` copy (`plugins.go`), the Python loader's `_TOP_KEYS` and its `with_entries`, and both dumps.
- `make catalog-check` compares the synced copies against git: commit before running it, or it reports the uncommitted change as drift.

## Open
- 50.15 part 2 turns the group on, gives `cloud.direct` `features = ["remote_access"]`, and documents remote access on maktaba.dev.
