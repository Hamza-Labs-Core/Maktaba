---
schema: 1
kind: pr
pr: 759
title: Flows, the leaf rule and the cycle check, on the home and for the store (Epic 54, 54.39)
date: 2026-11-06
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
prs: [751, 733]
---

## What happened
- The manifest's graph fields in all four validators, `flow.toml`, and one flow checker in Go and TypeScript on a 23-case corpus that both answer byte for byte (findings, English and Arabic words, the normalised graph). The home runs it at install, update, enable and rollback; the store's C12 runs the TypeScript one, with a three-plugin cycle refused.

## What we learned
- The two checkers matched on the first run because the algorithm was written down to its tie-breaks first: sorted node ids, Tarjan, the component's smallest stage as the start, breadth-first search over sorted successors, Kahn's order with a min-heap for the longest path. A sort of the ready list on every step cost 86 ms for 2,000 stages against a 50 ms budget; a heap made it a few.
- The engine's own graph validation refuses request cycles through non-monotone stages with a GraphError that would stop every graph build, so the home's flow check has to refuse a self-requesting plugin before it is ever enabled.
- `propagate.finish` refuses any request outside the stage's `may_request`; cross-plugin events need their own allowance (54.40), never a `may_request` entry, or the leaf assertion would refuse them.
- A store test that imports the SDK's source needs smol-toml resolved from the store's node_modules (a vitest alias and a tsconfig path), or CI's store job, which installs only the store, cannot resolve it.

## Keep doing
- Generate a cross-language corpus with a script, write the goldens from one language, review them by hand, then hold the other language to them.

## Open
- Events and lineage (54.40), the chart (54.41) and joined machines (54.42) follow in order.
