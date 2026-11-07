---
schema: 1
kind: decision
pr: 751
title: Epic 54 spec, flows, cross-plugin events, loop checks and joined machines
date: 2026-11-06
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
prs: [683, 693, 707, 717, 723]
---

## What happened
- Epic 54's spec gained §4.12 (flows: a signed `flow.toml` per set of plugins is the only wiring; leaves after core; published events between plugins; the cycle check at the store's verification and on the home; run-time lineage with a 16-hop cap and the cause `plugin_loop`; the chart) and §4.13 (third-party plugins on joined machines), H12 rewritten, H24 to H26, F25 to F31, and stories 54.39 to 54.42; 54.16 gained the store's flow check C12 with a three-plugin cycle test.

## What we learned
- The wiki's Arabic store translates only each spec file's title and its first 600 characters (`EXCERPT`), so edits deep in a README need no new Arabic; a changed story header line ("Depends on") does, and `--ar-todo --epic 54` lists exactly what.
- Adding a case to the WIT `call-error` variant would break 1.0 guests' import types, so a loop refusal reuses `scope` with the API code `plugin_loop`.

## Keep doing
- Write a cross-language check once as an algorithm with a deterministic output (the cycle path: the component with the smallest node id, shortest cycle by sorted breadth-first search), so Go and TypeScript can be compared byte for byte on one corpus.

## Open
- The build PRs: the manifest graph and checks, events and lineage, the chart, joined machines; the store's C12 is the Phase 2 build's.
