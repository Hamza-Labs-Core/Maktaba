---
schema: 1
kind: pr
pr: 786
title: Stage titles cover the stages first-party plugin manifests declare (main red since #760)
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [760]
---

## What happened
- After #760 merged, main's `web/src/lib/stageWords.test.ts` failed `expected 50 to be greater than 50`. Its `graphStages()` read only `engine/graph.py`, and wave 1 moved four stages into `tv.maktaba.classify`'s manifest (`_fp("…")` in the graph).
- It now also reads every `[[stage]]` of `plugins/first-party/*/manifest.toml` with its label, and requires each `_fp()` stage to be declared in one. The floor stays over the whole graph. It was checked on the wave 3 tree too.

## What we learned
- A wave that moves stages out of `graph.py` breaks every test that reads the graph from source rather than from the assembled `GRAPH`. #760's CI did not run the web suite against the move, so this reached main.

## Keep doing
- Before each Phase 4 wave, `git grep "engine/graph.py"` outside the pipeline for readers of the graph's source, and point them at the manifests too.
