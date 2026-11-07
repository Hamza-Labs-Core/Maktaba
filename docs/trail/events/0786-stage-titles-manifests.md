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
- It now also reads every `[[stage]]` of `plugins/first-party/*/manifest.toml` with its label, and requires each `_fp()` stage to be declared in one. The floor stays over the whole graph.
- Two more web tests read the graph's source and would have broken at the next waves. `help.coverage.test.ts` took the shipped stages from `CORE_MODULES` alone: 36 on the wave 3 tree, under its floor of 40. `StatusOverview.components.test.tsx` read `model_rules="…"` from `graph.py` only. All three now share `web/src/test/firstPartyStages.ts`, which reads the manifests, and pass on main and on the wave 3 tree.

## What we learned
- A wave that moves stages out of `graph.py` breaks every test that reads the graph from source rather than from the assembled `GRAPH`. #760's CI did not run the web suite against the move, so this reached main.

## Keep doing
- Before each Phase 4 wave, `git grep "engine/graph.py"` outside the pipeline for readers of the graph's source, and point them at the manifests too.
