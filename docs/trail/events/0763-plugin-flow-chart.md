---
schema: 1
kind: pr
pr: 763
title: The flow chart, on the plugin page, in the SDK and on maktaba.dev (Epic 54, 54.41)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
prs: [759, 762]
---

## What happened
- One chart layout in the plugin SDK, drawn by the web's plugin page (the plugin's flow and the home's whole graph), by maktaba.dev at build time and as text and Mermaid by `mkp flow`; `mkp test --with` runs a two-plugin chain in the harness.

## What we learned
- `@maktaba/flowchart` is the SDK's source file reached by a tsconfig and Vite alias (`../tools/plugin-sdk/js/src/flowchart.ts`), so every build that copies `web/` alone needs that file too: `web/Dockerfile` (now under `/src/web`), `cloud/Dockerfile`'s web stage and the dev compose's mounts. The full-workspace typecheck passes without them; only the e2e job's image build shows it.
- The web draws the chart from a geometry model with React's own SVG elements, so no plugin-named string is ever set as markup (and CodeQL has no innerHTML to flag); the same model writes the escaped SVG string for the portal.
- A label clipped to its box width cut the state off a long plugin id ("dev.example.writer · not instal…"): the state goes first.
- Node 22 imports a TypeScript file with `--experimental-strip-types` when it uses only erasable syntax, so the portal's build can draw with the SDK's own source instead of a copy or a committed picture.

## Open
- Joined machines (54.42).
